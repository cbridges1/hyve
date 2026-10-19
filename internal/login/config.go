// Package login is the client side of `hyve login`: signing in to a hyve
// host or to Rancher, keeping the session, and building kubeconfigs that
// use it (HYVE-PROPOSAL.md, "Signing in: one URL, a username and a
// password").
//
// hyve's host speaks the part of Rancher's /v1-public login API the CLI
// needs, so one client signs in to both. For Rancher it does what Rancher's
// own CLI does for each auth provider (rancher/cli cmd/kubectl_token.go):
// a username and password for local and LDAP users, the request-ID handoff
// for SAML, the device code for Azure AD. For providers Rancher's CLI
// can't sign in (GitHub, Google, OIDC), it uses the hyve extension's CLI
// sign-in page (browser.go).
package login

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// Kinds of server.
const (
	KindHyve    = "hyve"
	KindRancher = "rancher"
)

// Server is one place the CLI has signed in to.
type Server struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Kind     string `json:"kind"`
	Provider string `json:"provider,omitempty"`
	Username string `json:"username,omitempty"`
	// Token is the session. The file is written 0600, like a kubeconfig.
	Token     string    `json:"token,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	// CACertFile is a PEM file to trust in addition to the system roots.
	CACertFile         string `json:"caCertFile,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`

	// Environment is the namespace cluster commands use by default
	// (`hyve env use`), and TemplateNamespace the one template and shared
	// Resource commands use.
	Environment       string `json:"environment,omitempty"`
	TemplateNamespace string `json:"templateNamespace,omitempty"`
}

// SignedIn reports whether the session is present and unexpired.
func (s *Server) SignedIn(now time.Time) bool {
	return s.Token != "" && (s.ExpiresAt.IsZero() || now.Before(s.ExpiresAt))
}

// Config is every saved server, and which one commands use.
type Config struct {
	Current string   `json:"current,omitempty"`
	Servers []Server `json:"servers,omitempty"`

	path string
}

// Path is where the config lives under the hyve home directory.
func Path(home string) string {
	return filepath.Join(home, "logins.yaml")
}

// Load reads the config, returning an empty one if the file doesn't exist.
func Load(home string) (*Config, error) {
	p := Path(home)
	cfg := &Config{path: p}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	cfg.path = p
	return cfg, nil
}

// Save writes the config with owner-only permissions.
func (c *Config) Save() error {
	sort.Slice(c.Servers, func(i, j int) bool { return c.Servers[i].Name < c.Servers[j].Name })
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Get returns the named server, or the current one when name is empty.
func (c *Config) Get(name string) (*Server, error) {
	if name == "" {
		name = c.Current
	}
	if name == "" {
		return nil, errors.New("not signed in anywhere; run 'hyve login <url>'")
	}
	for i := range c.Servers {
		if c.Servers[i].Name == name {
			return &c.Servers[i], nil
		}
	}
	return nil, fmt.Errorf("no saved server %q; run 'hyve login <url>'", name)
}

// FindByURL returns the server saved for url, if any.
func (c *Config) FindByURL(u string) *Server {
	for i := range c.Servers {
		if c.Servers[i].URL == u {
			return &c.Servers[i]
		}
	}
	return nil
}

// Put adds or replaces s, by name.
func (c *Config) Put(s Server) {
	for i := range c.Servers {
		if c.Servers[i].Name == s.Name {
			c.Servers[i] = s
			return
		}
	}
	c.Servers = append(c.Servers, s)
}

// Remove deletes the named server.
func (c *Config) Remove(name string) bool {
	for i := range c.Servers {
		if c.Servers[i].Name == name {
			c.Servers = append(c.Servers[:i], c.Servers[i+1:]...)
			if c.Current == name {
				c.Current = ""
			}
			return true
		}
	}
	return false
}

// UniqueName derives a name for a new server from its host.
func (c *Config) UniqueName(rawURL string) string {
	base := "server"
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		base = strings.ReplaceAll(u.Hostname(), ".", "-")
	}
	name := base
	for n := 2; ; n++ {
		if _, err := c.Get(name); err != nil {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, n)
	}
}

// NormalizeURL makes https the default scheme and drops a trailing slash.
func NormalizeURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("%q has no host", raw)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("%q: the scheme must be https or http", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}
