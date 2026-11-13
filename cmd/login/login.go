// Package logincmd holds `hyve login`, `logout`, `token`, `kubeconfig` and
// `user`: signing in to a hyve host with one URL, and using that sign-in (HYVE-PROPOSAL.md, "Signing in: one URL, a username and a
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
	loginPasswordStdin bool
	loginCACert        string
	loginInsecure      bool
	loginTokenStdin    bool
	loginGitHubActions bool
	loginIDTokenEnv    string
	loginIDTokenFile   string
	loginAudience      string
)

// LoginCmd is `hyve login`.
var LoginCmd = &cobra.Command{
	Use:   "login URL",
	Short: "Sign in to a hyve host",
	Long: `Signs in to a hyve host and saves it as the server later commands use.

It opens the host's sign-in page in your browser: sign in there with your
password or the identity provider, and the CLI gets the session.

Unattended runs (CI, scripts) sign in one of these ways instead:

  --github-actions, --id-token-env, --id-token-file
      A CI job signs in with the ID token its CI system gives it, as a user
      that trusts that CI system ('hyve user trust add'). Nothing secret is
      kept in the pipeline; the session lasts an hour.
  --token-stdin
      An API token ('hyve user token create'), read from stdin.
  --username and --password-stdin
      A password, read from stdin.

A pipeline can also skip 'hyve login' and set HYVE_URL and HYVE_TOKEN (an
API token): every command uses them, and nothing is saved.

  hyve login https://hyve.example.com
  hyve login https://hyve.example.com --username ci --github-actions
  hyve login https://hyve.example.com --username ci --id-token-env HYVE_ID_TOKEN    # GitLab: id_tokens
  echo "$HYVE_TOKEN" | hyve login https://hyve.example.com --token-stdin
  echo "$PW" | hyve login https://hyve.example.com --username ci --password-stdin`,
	Args: cobra.ExactArgs(1),
	RunE: runLogin,
}

func init() {
	f := LoginCmd.Flags()
	f.StringVar(&loginName, "name", "", "name to save the server under (default: from the host name)")
	f.StringVarP(&loginUsername, "username", "u", "", "username or email address (with --password-stdin; otherwise filled in on the sign-in page)")
	f.BoolVar(&loginPasswordStdin, "password-stdin", false, "read the password from stdin (first line; a second line is the new password when one is required)")
	f.StringVar(&loginCACert, "ca-cert", "", "PEM CA certificate to trust for this server")
	f.BoolVar(&loginInsecure, "insecure-skip-tls-verify", false, "don't verify the server's certificate (testing only)")
	f.BoolVar(&loginTokenStdin, "token-stdin", false, "sign in with an API token read from stdin ('hyve user token create')")
	f.BoolVar(&loginGitHubActions, "github-actions", false, "sign in as --username with this GitHub Actions job's ID token (needs 'permissions: id-token: write')")
	f.StringVar(&loginIDTokenEnv, "id-token-env", "", "sign in as --username with the CI job's ID token in this environment variable")
	f.StringVar(&loginIDTokenFile, "id-token-file", "", "sign in as --username with the CI job's ID token in this file")
	f.StringVar(&loginAudience, "audience", "hyve", "the audience to ask GitHub Actions for (the user's trusted CI entry must name it)")
}

func runLogin(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	base, err := login.NormalizeURL(args[0])
	if err != nil {
		return err
	}
	if strings.HasPrefix(base, "http://") {
		fmt.Fprintln(os.Stderr, "warning: over plain HTTP your session (and with --password-stdin, your password) travels unencrypted; use the host's https:// URL")
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
	if err := login.Detect(ctx, hc, base); err != nil {
		return err
	}
	srv.Kind = login.KindHyve
	if loginUsername != "" {
		srv.Username = loginUsername
	}

	in := newInput(os.Stdin)
	var sess login.Session
	switch ci, ways := loginGitHubActions || loginIDTokenEnv != "" || loginIDTokenFile != "", countTrue(loginPasswordStdin, loginTokenStdin,
		loginGitHubActions, loginIDTokenEnv != "", loginIDTokenFile != ""); {
	case ways > 1:
		return errors.New("give one of --password-stdin, --token-stdin, --github-actions, --id-token-env and --id-token-file")
	case loginTokenStdin:
		if sess, err = tokenSignIn(ctx, hc, &srv, in); err != nil {
			return err
		}
	case ci:
		if srv.Username == "" {
			return errors.New("a CI sign-in needs --username: the user that trusts this CI system")
		}
		idToken, err := ciIDToken(ctx)
		if err != nil {
			return err
		}
		if sess, err = login.CILogin(ctx, hc, base, srv.Username, idToken); err != nil {
			return err
		}
	default:
		if sess, err = signIn(ctx, hc, &srv, in, signInOptions{passwordStdin: loginPasswordStdin}); err != nil {
			return err
		}
	}
	srv.Token, srv.ExpiresAt = sess.Token, sess.ExpiresAt
	// The host says who signed in: an email address signs in as its user.
	if sess.UserID != "" {
		srv.Username = sess.UserID
	}

	cfg.Put(srv)
	cfg.Current = srv.Name
	if err := cfg.Save(); err != nil {
		return err
	}

	who := whoAmI(ctx, &srv)
	// A browser sign-in doesn't say who: ask the cluster.
	if srv.Username == "" && strings.HasPrefix(who, "hyve:") {
		srv.Username = strings.TrimPrefix(who, "hyve:")
		cfg.Put(srv)
		if err := cfg.Save(); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "Signed in to %s as %s.\n", srv.Name, who)
	if !srv.ExpiresAt.IsZero() {
		fmt.Fprintf(os.Stderr, "The session lasts until %s.\n", srv.ExpiresAt.Local().Format(time.DateTime))
	}
	return nil
}

func countTrue(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// tokenSignIn reads an API token from stdin and asks the host who it is.
func tokenSignIn(ctx context.Context, hc *http.Client, srv *login.Server, in *input) (login.Session, error) {
	token, err := in.line()
	if err != nil {
		return login.Session{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return login.Session{}, errors.New("no token on stdin")
	}
	me, err := login.WhoAmI(ctx, hc, srv.URL, token)
	if err != nil {
		return login.Session{}, err
	}
	if loginUsername != "" && loginUsername != me.Username {
		return login.Session{}, fmt.Errorf("the token is %s's, not %s's", me.Username, loginUsername)
	}
	srv.Username = me.Username
	sess := login.Session{Token: token, UserID: me.Username}
	if me.ExpiresAt != "" {
		sess.ExpiresAt, _ = time.Parse(time.RFC3339, me.ExpiresAt)
	}
	return sess, nil
}

// ciIDToken is the CI job's ID token: GitHub Actions', or one in an
// environment variable or a file.
func ciIDToken(ctx context.Context) (string, error) {
	switch {
	case loginGitHubActions:
		return login.GitHubActionsIDToken(ctx, http.DefaultClient, loginAudience)
	case loginIDTokenEnv != "":
		t := strings.TrimSpace(os.Getenv(loginIDTokenEnv))
		if t == "" {
			return "", fmt.Errorf("$%s is empty", loginIDTokenEnv)
		}
		return t, nil
	default:
		raw, err := os.ReadFile(loginIDTokenFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
}

type signInOptions struct {
	passwordStdin bool
}

// signIn signs in to srv: in the browser, or with a password on stdin for
// unattended runs. It's shared by `hyve login` and by `hyve token` when a
// session has expired.
func signIn(ctx context.Context, hc *http.Client, srv *login.Server, in *input, opts signInOptions) (login.Session, error) {
	if opts.passwordStdin {
		return passwordSignIn(ctx, hc, srv, in)
	}
	return login.BrowserLogin(ctx, srv.URL, login.OpenBrowser, os.Stderr, login.BrowserOptions{Username: srv.Username})
}

// passwordSignIn reads the password (and, when the server wants it
// changed, a new one on a second line) from stdin: unattended sign-in.
func passwordSignIn(ctx context.Context, hc *http.Client, srv *login.Server, in *input) (login.Session, error) {
	if srv.Username == "" {
		return login.Session{}, errors.New("--password-stdin needs --username")
	}
	password, err := in.line()
	if err != nil {
		return login.Session{}, err
	}
	sess, err := login.PasswordLogin(ctx, hc, srv.URL, srv.Username, password, "")
	if !errors.Is(err, login.ErrMustChangePassword) {
		return sess, err
	}
	newPassword, err := in.line()
	if err != nil || newPassword == "" {
		return login.Session{}, errors.New("the password must be changed: put the new password on a second line of stdin, or sign in in the browser")
	}
	return login.PasswordLogin(ctx, hc, srv.URL, srv.Username, password, newPassword)
}

// newPasswordPrompt asks for a new password twice (`hyve user`).
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
