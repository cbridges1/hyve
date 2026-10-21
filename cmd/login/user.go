package logincmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/cmd/shared"
	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/login"
	"github.com/cbridges1/hyve/internal/signin"
)

var (
	userShowOutput       string
	userDisplayName      string
	userGroups           []string
	userPasswordStdin    bool
	userNoPasswordChange bool
)

// UserCmd is `hyve user`.
var UserCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage a hyve host's users (standalone installs; with Rancher, use Rancher's)",
	Long: `Manages hyve.io User objects on the signed-in hyve host. Users sign in with
a username and password; Kubernetes sees them as hyve:<username>, in groups
hyve:<group>. Grant them access with RBAC on those names.

Everything goes through the host's /k8s/clusters/local as you, so it needs
the matching RBAC on users.hyve.io (and update on users/password to set
someone else's password).`,
}

type userContext struct {
	srv *login.Server
	hc  *http.Client
	kc  client.Client
}

func newUserContext() (*userContext, error) {
	cfg, err := login.Load(shared.HyveHome())
	if err != nil {
		return nil, err
	}
	s, err := cfg.Get(serverFlag)
	if err != nil {
		return nil, err
	}
	if s.Kind != login.KindHyve {
		return nil, fmt.Errorf("%s is Rancher; manage its users in Rancher", s.Name)
	}
	if !s.SignedIn(time.Now()) {
		return nil, fmt.Errorf("the session for %s has expired; run 'hyve login %s'", s.Name, s.URL)
	}
	hc, err := login.HTTPClient(s.CACertFile, s.InsecureSkipVerify)
	if err != nil {
		return nil, err
	}
	scheme := runtime.NewScheme()
	if err := hyvev1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	kc, err := client.New(login.RESTConfig(s, login.LocalCluster), client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}
	return &userContext{srv: s, hc: hc, kc: kc}, nil
}

// readNewPassword reads one line from stdin, or prompts twice.
func readNewPassword(in *input, fromStdin bool) (string, error) {
	if fromStdin {
		return in.line()
	}
	return newPasswordPrompt(in)
}

var userAddCmd = &cobra.Command{
	Use:   "add NAME",
	Short: "Create a user and set its password",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !signin.ValidUsername(name) {
			return errors.New("a username must be a DNS label: lowercase letters, digits and '-', at most 63 characters")
		}
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		pw, err := readNewPassword(newInput(os.Stdin), userPasswordStdin)
		if err != nil {
			return err
		}
		if err := signin.ValidatePassword(pw); err != nil {
			return err
		}
		ctx := cmd.Context()
		u := &hyvev1alpha1.User{}
		u.Name = name
		u.Spec = hyvev1alpha1.UserSpec{DisplayName: userDisplayName, Groups: userGroups}
		if err := uc.kc.Create(ctx, u); err != nil {
			return fmt.Errorf("creating user %s: %w", name, err)
		}
		if err := login.SetPassword(ctx, uc.hc, uc.srv, name, "", pw); err != nil {
			_ = uc.kc.Delete(context.Background(), u)
			return fmt.Errorf("setting %s's password (the user was removed again): %w", name, err)
		}
		// Setting the password clears MustChangePassword, so set it after.
		if !userNoPasswordChange {
			if err := uc.kc.Get(ctx, client.ObjectKeyFromObject(u), u); err != nil {
				return err
			}
			patch := client.MergeFrom(u.DeepCopy())
			u.Spec.MustChangePassword = true
			if err := uc.kc.Patch(ctx, u, patch); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "Created user %s (Kubernetes user %s%s).\n", name, signin.UserPrefix, name)
		return nil
	},
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List users",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		var list hyvev1alpha1.UserList
		if err := uc.kc.List(cmd.Context(), &list); err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDISPLAY NAME\tGROUPS\tDISABLED\tLAST LOGIN")
		for _, u := range list.Items {
			last := "never"
			if u.Status.LastLogin != nil {
				last = u.Status.LastLogin.Local().Format(time.DateTime)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", u.Name, u.Spec.DisplayName, strings.Join(u.Spec.Groups, ","), u.Spec.Disabled, last)
		}
		return w.Flush()
	},
}

var userPasswdCmd = &cobra.Command{
	Use:   "passwd [NAME]",
	Short: "Change your password, or set another user's",
	Long: `Without NAME, changes your own password (asking for the current one) and
signs out your other sessions. With NAME, sets that user's password, which
needs update on users/password, and signs them out everywhere.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		in := newInput(os.Stdin)
		name, current := uc.srv.Username, ""
		if len(args) == 1 && args[0] != uc.srv.Username {
			name = args[0]
		} else if userPasswordStdin {
			if current, err = in.line(); err != nil {
				return err
			}
		} else if current, err = in.secret("Current password: "); err != nil {
			return err
		}
		pw, err := readNewPassword(in, userPasswordStdin)
		if err != nil {
			return err
		}
		if err := login.SetPassword(cmd.Context(), uc.hc, uc.srv, name, current, pw); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Changed %s's password.\n", name)
		return nil
	},
}

func setDisabled(disabled bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		u := &hyvev1alpha1.User{}
		if err := uc.kc.Get(cmd.Context(), client.ObjectKey{Name: args[0]}, u); err != nil {
			return err
		}
		patch := client.MergeFrom(u.DeepCopy())
		u.Spec.Disabled = disabled
		if err := uc.kc.Patch(cmd.Context(), u, patch); err != nil {
			return err
		}
		state := "Enabled"
		if disabled {
			state = "Disabled"
		}
		fmt.Fprintf(os.Stderr, "%s user %s.\n", state, args[0])
		return nil
	}
}

var userDisableCmd = &cobra.Command{
	Use:   "disable NAME",
	Short: "Stop a user signing in; their sessions stop working within 30 seconds",
	Args:  cobra.ExactArgs(1),
	RunE:  setDisabled(true),
}

var userEnableCmd = &cobra.Command{
	Use:   "enable NAME",
	Short: "Let a disabled user sign in again",
	Args:  cobra.ExactArgs(1),
	RunE:  setDisabled(false),
}

var userRemoveCmd = &cobra.Command{
	Use:   "remove NAME",
	Short: "Delete a user, with its password and sessions",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		u := &hyvev1alpha1.User{}
		u.Name = args[0]
		if err := uc.kc.Delete(cmd.Context(), u); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Removed user %s.\n", args[0])
		return nil
	},
}

var userShowCmd = &cobra.Command{
	Use:   "show NAME",
	Short: "Show a user",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if userShowOutput != "" && userShowOutput != "yaml" && userShowOutput != "json" {
			return fmt.Errorf(`--output is "yaml" or "json", not %q`, userShowOutput)
		}
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		u := &hyvev1alpha1.User{}
		if err := uc.kc.Get(cmd.Context(), client.ObjectKey{Name: args[0]}, u); err != nil {
			return err
		}
		u.ManagedFields = nil
		u.APIVersion, u.Kind = hyvev1alpha1.GroupVersion.String(), "User"
		var out []byte
		if userShowOutput == "json" {
			out, err = json.MarshalIndent(u, "", "  ")
			out = append(out, '\n')
		} else {
			out, err = yaml.Marshal(u)
		}
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	},
}

func init() {
	userShowCmd.Flags().StringVarP(&userShowOutput, "output", "o", "", `"yaml" (the default) or "json"`)
	userAddCmd.Flags().StringVar(&userDisplayName, "display-name", "", "name shown in the UI")
	userAddCmd.Flags().StringSliceVar(&userGroups, "group", nil, "group to add the user to (repeatable)")
	userAddCmd.Flags().BoolVar(&userPasswordStdin, "password-stdin", false, "read the initial password from stdin")
	userAddCmd.Flags().BoolVar(&userNoPasswordChange, "no-password-change", false, "don't make the user change the password at first sign-in")
	userPasswdCmd.Flags().BoolVar(&userPasswordStdin, "password-stdin", false, "read passwords from stdin: the current one then the new one for your own, just the new one for another user")
	for _, c := range []*cobra.Command{userAddCmd, userListCmd, userShowCmd, userPasswdCmd, userDisableCmd, userEnableCmd, userRemoveCmd} {
		addServerFlag(c)
		c.SilenceUsage = true // session.go's init runs before this file's
		UserCmd.AddCommand(c)
	}
}
