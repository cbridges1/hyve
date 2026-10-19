package logincmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/local"
	"github.com/cbridges1/hyve/internal/login"
)

var metav1CreateOptions = metav1.CreateOptions{}

var serverFlag string

func addServerFlag(c *cobra.Command) {
	c.Flags().StringVar(&serverFlag, "server", "", "saved server to use (default: the one last signed in to)")
}

// LogoutCmd is `hyve logout`.
var LogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "End the session with a hyve host or Rancher",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := login.Load(shared.HyveHome())
		if err != nil {
			return err
		}
		s, err := cfg.Get(serverFlag)
		if err != nil {
			return err
		}
		if s.Token != "" {
			hc, err := login.HTTPClient(s.CACertFile, s.InsecureSkipVerify)
			if err != nil {
				return err
			}
			if err := login.Logout(cmd.Context(), hc, s); err != nil {
				fmt.Fprintf(os.Stderr, "warning: the server didn't confirm the sign-out: %v\n", err)
			}
		}
		s.Token, s.ExpiresAt = "", time.Time{}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Signed out of %s.\n", s.Name)
		return nil
	},
}

// TokenCmd is `hyve token`, the exec credential plugin that kubeconfigs
// from `hyve kubeconfig` call.
var TokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print the current session as a kubectl exec credential",
	Long: `Prints the saved session for a server as a client.authentication.k8s.io/v1
ExecCredential. Kubeconfigs from 'hyve kubeconfig' run this, so they hold
no secret themselves. When the session has expired and a terminal is
available, it signs in again first, prompting on stderr.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := login.Load(shared.HyveHome())
		if err != nil {
			return err
		}
		s, err := cfg.Get(serverFlag)
		if err != nil {
			return err
		}
		if s.Name == local.ContextName && strings.HasPrefix(s.URL, "https://127.0.0.1:") {
			// Local mode: start the cluster if it's stopped, and sign in
			// again if needed, so kubectl just works.
			if err := local.Resume(cmd.Context(), shared.HyveHome(), func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }); err != nil {
				return err
			}
			if cfg, err = login.Load(shared.HyveHome()); err != nil {
				return err
			}
			if s, err = cfg.Get(local.ContextName); err != nil {
				return err
			}
		}
		if !s.SignedIn(time.Now()) {
			in := newInput(os.Stdin)
			if !in.tty {
				return fmt.Errorf("the session for %s has expired; run 'hyve login %s'", s.Name, s.URL)
			}
			fmt.Fprintf(os.Stderr, "The session for %s has expired; signing in again.\n", s.Name)
			hc, err := login.HTTPClient(s.CACertFile, s.InsecureSkipVerify)
			if err != nil {
				return err
			}
			sess, err := signIn(cmd.Context(), hc, s, in, signInOptions{})
			if err != nil {
				return err
			}
			s.Token, s.ExpiresAt = sess.Token, sess.ExpiresAt
			if err := cfg.Save(); err != nil {
				return err
			}
		}
		out, err := login.ExecCredential(s.Token, s.ExpiresAt)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(out, '\n'))
		return err
	},
}

// KubeconfigCmd is `hyve kubeconfig`.
var KubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig [CLUSTER-ID]",
	Short: "Print a kubeconfig that reaches a cluster through the hyve host or Rancher",
	Long: `Prints a kubeconfig whose server is <url>/k8s/clusters/<id> on the saved
server: the management cluster for "local" (the default), or a child
cluster's ID. Requests go through the proxy as you, so kubectl, helm, k9s,
exec and port-forward all work under your own permissions.

The kubeconfig holds no secret: it runs 'hyve token' for the current
session.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterID := login.LocalCluster
		if len(args) == 1 {
			clusterID = args[0]
		}
		cfg, err := login.Load(shared.HyveHome())
		if err != nil {
			return err
		}
		s, err := cfg.Get(serverFlag)
		if err != nil {
			return err
		}
		bin, err := os.Executable()
		if err != nil {
			return errors.New("can't find the hyve binary's path for the exec credential")
		}
		if strings.HasPrefix(s.URL, "http://") {
			fmt.Fprintln(os.Stderr, "warning: kubectl doesn't run exec credential plugins for plain-HTTP servers, so this kubeconfig won't authenticate; sign in to the host's https:// URL")
		}
		out, err := login.Kubeconfig(s, clusterID, bin, shared.ExplicitHome())
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	},
}

func init() {
	addServerFlag(LogoutCmd)
	addServerFlag(TokenCmd)
	addServerFlag(KubeconfigCmd)
	// Errors here are about sign-in, not about how the command was typed.
	for _, c := range []*cobra.Command{LoginCmd, LogoutCmd, TokenCmd, KubeconfigCmd, UserCmd} {
		c.SilenceUsage = true
	}
}
