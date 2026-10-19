// Package logincmd holds `hyve login`, `logout`, `token`, `kubeconfig` and
// `user`: signing in to a hyve host or to Rancher with one URL, and using
// that sign-in (HYVE-PROPOSAL.md, "Signing in: one URL, a username and a
// password"). These are separate from `hyve context login`, which signs in
// to the old hyve-api.
package logincmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/login"
)

var (
	loginName          string
	loginUsername      string
	loginProvider      string
	loginPasswordStdin bool
	loginTokenStdin    bool
	loginBrowser       bool
	loginAuthFlow      string
	loginCACert        string
	loginInsecure      bool
)

// LoginCmd is `hyve login`.
var LoginCmd = &cobra.Command{
	Use:   "login URL",
	Short: "Sign in to a hyve host or to Rancher",
	Long: `Signs in to a hyve host (a standalone install) or to Rancher, and saves it
as the server later commands use. The same command works for both.

hyve hosts sign in with a username and password. With Rancher, hyve signs
in the way Rancher's own CLI does for the auth provider in use:

  local, OpenLDAP, FreeIPA, Active Directory   username and password
  SAML (Okta, Ping, ADFS, Keycloak, Shibboleth) in the browser
  Azure AD                                      a device code (--auth-flow authcode for a browser)
  GitHub, Google, OIDC and others               in the browser, through the hyve UI extension

--browser uses the hyve UI extension's sign-in page with any provider.
--token-stdin takes an existing Rancher API key, for when there's no
other way (no extension, or no browser on this machine).

  hyve login https://hyve.example.com
  echo "$PW" | hyve login https://hyve.example.com --username ci --password-stdin`,
	Args: cobra.ExactArgs(1),
	RunE: runLogin,
}

func init() {
	f := LoginCmd.Flags()
	f.StringVar(&loginName, "name", "", "name to save the server under (default: from the host name)")
	f.StringVarP(&loginUsername, "username", "u", "", "username (prompted for when needed)")
	f.StringVar(&loginProvider, "provider", "", "Rancher auth provider type, e.g. activeDirectoryProvider (asked when there are several)")
	f.BoolVar(&loginPasswordStdin, "password-stdin", false, "read the password from stdin (first line; a second line is the new password when one is required)")
	f.BoolVar(&loginTokenStdin, "token-stdin", false, "read an existing Rancher API key from stdin instead of signing in")
	f.BoolVar(&loginBrowser, "browser", false, "sign in to Rancher in the browser through the hyve UI extension")
	f.StringVar(&loginAuthFlow, "auth-flow", "", "Azure AD only: devicecode (default) or authcode")
	f.StringVar(&loginCACert, "ca-cert", "", "PEM CA certificate to trust for this server")
	f.BoolVar(&loginInsecure, "insecure-skip-tls-verify", false, "don't verify the server's certificate (testing only)")
}

func runLogin(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	base, err := login.NormalizeURL(args[0])
	if err != nil {
		return err
	}
	if strings.HasPrefix(base, "http://") {
		fmt.Fprintln(os.Stderr, "warning: signing in over plain HTTP sends your password unencrypted; use the host's https:// URL")
	}
	cfg, err := login.Load(shared.HyveHome())
	if err != nil {
		return err
	}

	srv := login.Server{URL: base, CACertFile: loginCACert, InsecureSkipVerify: loginInsecure}
	if existing := cfg.FindByURL(base); existing != nil {
		srv = *existing
		if loginCACert != "" {
			srv.CACertFile = loginCACert
		}
		if loginInsecure {
			srv.InsecureSkipVerify = true
		}
	}
	switch {
	case loginName != "":
		srv.Name = loginName
	case srv.Name == "":
		srv.Name = cfg.UniqueName(base)
	}

	hc, err := login.HTTPClient(srv.CACertFile, srv.InsecureSkipVerify)
	if err != nil {
		return err
	}
	if srv.Kind, err = login.Detect(ctx, hc, base); err != nil {
		return err
	}
	if loginUsername != "" {
		srv.Username = loginUsername
	}
	if loginProvider != "" {
		srv.Provider = loginProvider
	}

	in := newInput(os.Stdin)
	sess, err := signIn(ctx, hc, &srv, in, signInOptions{
		passwordStdin: loginPasswordStdin,
		tokenStdin:    loginTokenStdin,
		browser:       loginBrowser,
		authFlow:      loginAuthFlow,
	})
	if err != nil {
		return err
	}
	srv.Token, srv.ExpiresAt = sess.Token, sess.ExpiresAt
	if sess.UserID != "" && srv.Username == "" {
		srv.Username = sess.UserID
	}

	cfg.Put(srv)
	cfg.Current = srv.Name
	if err := cfg.Save(); err != nil {
		return err
	}

	who := whoAmI(ctx, &srv)
	fmt.Fprintf(os.Stderr, "Signed in to %s (%s) as %s.\n", srv.Name, srv.Kind, who)
	if !srv.ExpiresAt.IsZero() {
		fmt.Fprintf(os.Stderr, "The session lasts until %s.\n", srv.ExpiresAt.Local().Format(time.DateTime))
	}
	return nil
}

type signInOptions struct {
	passwordStdin bool
	tokenStdin    bool
	browser       bool
	authFlow      string
}

// signIn runs whichever sign-in the server and its provider need. It's
// shared by `hyve login` and by `hyve token` when a session has expired.
func signIn(ctx context.Context, hc *http.Client, srv *login.Server, in *input, opts signInOptions) (login.Session, error) {
	out := os.Stderr
	if opts.tokenStdin {
		tok, err := in.line()
		if err != nil {
			return login.Session{}, fmt.Errorf("reading the token: %w", err)
		}
		if tok == "" {
			return login.Session{}, errors.New("no token on stdin")
		}
		return login.Session{Token: tok}, nil
	}
	if opts.browser {
		if srv.Kind != login.KindRancher {
			return login.Session{}, errors.New("--browser is for Rancher; hyve hosts sign in with a username and password")
		}
		return login.BrowserLogin(ctx, srv.URL, login.OpenBrowser, out, login.BrowserOptions{})
	}

	providers, err := login.ListProviders(ctx, hc, srv.URL)
	if err != nil {
		return login.Session{}, err
	}
	p, err := pickProvider(providers, srv.Provider, in)
	if err != nil {
		return login.Session{}, err
	}
	srv.Provider = p.Type

	switch login.FlowFor(p.Type) {
	case login.FlowSAML:
		return login.SAMLLogin(ctx, hc, srv.URL, login.OpenBrowser, out, login.SAMLOptions{})
	case login.FlowOAuth:
		return login.OAuthLogin(ctx, hc, srv.URL, p, opts.authFlow, login.OpenBrowser, out)
	case login.FlowBrowser:
		fmt.Fprintf(out, "Rancher's %s users can't sign in with a password from a CLI; using the hyve UI extension.\n", p.Type)
		return login.BrowserLogin(ctx, srv.URL, login.OpenBrowser, out, login.BrowserOptions{})
	}
	return passwordSignIn(ctx, hc, srv, p.Type, in, opts.passwordStdin)
}

func passwordSignIn(ctx context.Context, hc *http.Client, srv *login.Server, providerType string, in *input, passwordStdin bool) (login.Session, error) {
	if srv.Username == "" {
		if passwordStdin {
			return login.Session{}, errors.New("--password-stdin needs --username")
		}
		u, err := in.prompt("Username: ")
		if err != nil {
			return login.Session{}, err
		}
		srv.Username = u
	}
	var (
		password string
		err      error
	)
	if passwordStdin {
		password, err = in.line()
	} else {
		password, err = in.secret(fmt.Sprintf("Password for %s: ", srv.Username))
	}
	if err != nil {
		return login.Session{}, err
	}

	sess, err := login.PasswordLogin(ctx, hc, srv.URL, providerType, srv.Username, password, "")
	if !errors.Is(err, login.ErrMustChangePassword) {
		return sess, err
	}
	var newPassword string
	if passwordStdin {
		if newPassword, err = in.line(); err != nil || newPassword == "" {
			return login.Session{}, errors.New("the password must be changed: put the new password on a second line of stdin, or sign in interactively")
		}
	} else {
		fmt.Fprintln(os.Stderr, "The password must be changed before signing in.")
		if newPassword, err = newPasswordPrompt(in); err != nil {
			return login.Session{}, err
		}
	}
	return login.PasswordLogin(ctx, hc, srv.URL, providerType, srv.Username, password, newPassword)
}

func newPasswordPrompt(in *input) (string, error) {
	pw, err := in.secret("New password: ")
	if err != nil {
		return "", err
	}
	again, err := in.secret("New password again: ")
	if err != nil {
		return "", err
	}
	if pw != again {
		return "", errors.New("the passwords don't match")
	}
	return pw, nil
}

func pickProvider(providers []login.Provider, want string, in *input) (login.Provider, error) {
	if len(providers) == 0 {
		return login.Provider{}, errors.New("the server offers no auth providers")
	}
	if want != "" {
		for _, p := range providers {
			if p.Type == want {
				return p, nil
			}
		}
		return login.Provider{}, fmt.Errorf("the server has no %s auth provider", want)
	}
	if len(providers) == 1 {
		return providers[0], nil
	}
	fmt.Fprintln(os.Stderr, "Auth providers:")
	for i, p := range providers {
		fmt.Fprintf(os.Stderr, "  %d  %s\n", i, p.Type)
	}
	choice, err := in.prompt("Sign in with: ")
	if err != nil {
		return login.Provider{}, err
	}
	i, err := strconv.Atoi(choice)
	if err != nil || i < 0 || i >= len(providers) {
		return login.Provider{}, fmt.Errorf("%q isn't one of the numbers listed", choice)
	}
	return providers[i], nil
}

// whoAmI asks the management cluster, through the proxy, who the session
// is. It's informational: an error just shows the username hyve knows.
func whoAmI(ctx context.Context, srv *login.Server) string {
	fallback := srv.Username
	if fallback == "" {
		fallback = "(unknown user)"
	}
	cs, err := kubernetes.NewForConfig(login.RESTConfig(srv, login.LocalCluster))
	if err != nil {
		return fallback
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := cs.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1CreateOptions)
	if err != nil || r.Status.UserInfo.Username == "" {
		return fallback
	}
	return r.Status.UserInfo.Username
}

// input reads stdin once, so piped lines aren't lost between reads.
type input struct {
	r   *bufio.Reader
	fd  int
	tty bool
}

func newInput(f *os.File) *input {
	return &input{r: bufio.NewReader(f), fd: int(f.Fd()), tty: term.IsTerminal(int(f.Fd()))}
}

func (in *input) line() (string, error) {
	s, err := in.r.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		if errors.Is(err, io.EOF) {
			return "", errors.New("unexpected end of stdin")
		}
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func (in *input) prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	s, err := in.line()
	return strings.TrimSpace(s), err
}

func (in *input) secret(label string) (string, error) {
	if !in.tty {
		return in.prompt(label)
	}
	fmt.Fprint(os.Stderr, label)
	b, err := term.ReadPassword(in.fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
}
