// Package cli is hyve's cluster command tree: clusters, templates,
// resources, environments, providers, repos and the controller
// (HYVE-PROPOSAL.md, "Commands"). Every command reaches the management
// cluster the same way: through the signed-in server's /k8s/clusters/local
// proxy, a hyve host or Rancher alike.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cbridges1/hyve/internal/install"
	"github.com/cbridges1/hyve/internal/kube"
	"github.com/cbridges1/hyve/internal/local"
	"github.com/cbridges1/hyve/internal/login"
)

// Default namespaces when the server has none set.
const (
	DefaultEnvironment       = install.DefaultEnvironment
	DefaultTemplateNamespace = install.DefaultTemplateNamespace
)

// Home returns the hyve home directory, and ExplicitHome the same when it
// was chosen explicitly (else ""); set by the root command.
var Home, ExplicitHome func() string

var (
	serverFlag     string
	kubeconfigFlag string
	envFlag        string
	tplNSFlag      string
)

// AddGlobalFlags adds the flags every cluster command shares.
func AddGlobalFlags(c *cobra.Command) {
	f := c.PersistentFlags()
	f.StringVar(&serverFlag, "server", "", "saved server to use (default: the current one; see 'hyve context list')")
	f.StringVar(&kubeconfigFlag, "kubeconfig", "", "talk to a management cluster with this kubeconfig instead of a signed-in server (admins, CI)")
}

func addEnvFlag(c *cobra.Command) {
	c.PersistentFlags().StringVarP(&envFlag, "env", "e", "", "environment (namespace) of the clusters (default: the context's; see 'hyve env use')")
}

func addTemplateNSFlag(c *cobra.Command) {
	c.PersistentFlags().StringVar(&tplNSFlag, "template-namespace", "", "namespace of templates and shared resources (default: the context's, or "+DefaultTemplateNamespace+")")
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

// session is a client for the management cluster.
type session struct {
	kube   *kube.Client
	rest   *rest.Config
	server *login.Server // nil with --kubeconfig
}

// usedLocal says the command went through the local context, so its
// changes are saved afterwards (SaveLocalStateAfter).
var usedLocal bool

func newSession() (*session, error) {
	if kubeconfigFlag != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigFlag)
		if err != nil {
			return nil, err
		}
		kc, err := kube.New(cfg)
		if err != nil {
			return nil, err
		}
		return &session{kube: kc, rest: cfg}, nil
	}
	cfg, err := login.Load(Home())
	if err != nil {
		return nil, err
	}
	s, err := cfg.Get(serverFlag)
	if err != nil {
		return nil, err
	}
	if s.Name == local.ContextName && s.Kind == login.KindHyve && strings.HasPrefix(s.URL, "https://127.0.0.1:") {
		// Local mode: start the cluster if it's stopped, and sign in again
		// if the session ran out.
		if err := local.Resume(context.Background(), Home(), logf); err != nil {
			return nil, err
		}
		usedLocal = true
		if cfg, err = login.Load(Home()); err != nil {
			return nil, err
		}
		if s, err = cfg.Get(local.ContextName); err != nil {
			return nil, err
		}
	}
	if !s.SignedIn(time.Now()) {
		return nil, fmt.Errorf("the session for %s has expired; run 'hyve login %s'", s.Name, s.URL)
	}
	rc := login.RESTConfig(s, login.LocalCluster)
	kc, err := kube.New(rc)
	if err != nil {
		return nil, err
	}
	return &session{kube: kc, rest: rc, server: s}, nil
}

// env is the environment namespace clusters are in.
func (s *session) env() string {
	switch {
	case envFlag != "":
		return envFlag
	case s.server != nil && s.server.Environment != "":
		return s.server.Environment
	}
	return DefaultEnvironment
}

// templateNS is the namespace templates and shared Resources are in.
func (s *session) templateNS() string {
	switch {
	case tplNSFlag != "":
		return tplNSFlag
	case s.server != nil && s.server.TemplateNamespace != "":
		return s.server.TemplateNamespace
	}
	return DefaultTemplateNamespace
}

// notInstalled turns "no such kind" into advice.
func notInstalled(err error) error {
	if err != nil && apierrors.IsNotFound(err) && strings.Contains(err.Error(), "the server could not find the requested resource") {
		return fmt.Errorf("hyve's CRDs aren't installed on this management cluster (install the hyve chart): %w", err)
	}
	return err
}

// parseKV parses k=v pairs; JSON values (numbers, lists, objects, booleans)
// are decoded when parseJSON is set, anything else stays a string.
func parseKV(pairs []string, parseJSON bool) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%q: want key=value", p)
		}
		var val any = v
		if parseJSON {
			var j any
			if err := json.Unmarshal([]byte(v), &j); err == nil {
				val = j
			}
		}
		out[k] = val
	}
	return out, nil
}

func parseLabels(pairs []string) (map[string]string, error) {
	kv, err := parseKV(pairs, false)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range kv {
		out[k] = v.(string)
	}
	return out, nil
}

// setPath sets a dotted key (a.b.c) in a nested map.
func setPath(m map[string]any, key string, v any) {
	parts := strings.Split(key, ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
