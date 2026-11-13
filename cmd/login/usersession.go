package logincmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/login"
)

var (
	sessionUser   string
	sessionOthers bool
)

var userSessionCmd = &cobra.Command{
	Use:     "session",
	Aliases: []string{"sessions"},
	Short:   "List where you're signed in, and sign sessions out",
	Long: `A session is a sign-in: a browser's, the CLI's ('hyve login'), or a CI
job's. Each shows when and from where it started and when it was last
used. Signing one out ends it at once; API tokens are separate ('hyve
user token').

You can manage your own sessions. Another user's take the right to the
users/sessions subresource of that User: admins have it.`,
}

var userSessionListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List sessions: yours, or --user's",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		list, err := login.ListSessions(cmd.Context(), uc.hc, uc.srv, sessionUser)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tKIND\tSTARTED\tLAST USED\tEXPIRES\tFROM\tCLIENT")
		for _, s := range list {
			used := "-"
			if s.LastUsed != nil {
				used = s.LastUsed.Local().Format(time.DateTime)
			}
			id := s.ID
			if s.Current {
				id += " (this one)"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", id, s.Kind, s.CreatedAt.Local().Format(time.DateTime), used,
				s.ExpiresAt.Local().Format(time.DateTime), dash(s.Address), dash(shortAgent(s.UserAgent)))
		}
		return w.Flush()
	},
}

var userSessionRevokeCmd = &cobra.Command{
	Use:     "revoke (ID | --others)",
	Aliases: []string{"signout", "rm"},
	Short:   "Sign a session out, or every other one",
	Example: `  hyve user session revoke 0a1b2c3d4e
  hyve user session revoke --others
  hyve user session revoke --others --user alice`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if (len(args) == 1) == sessionOthers {
			return errors.New("give a session's ID or --others")
		}
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		user := sessionUser
		if user == "" {
			if user = uc.srv.Username; user == "" {
				return errors.New("give --user")
			}
		}
		id := ""
		if len(args) == 1 {
			id = args[0]
		}
		if err := login.RevokeSession(cmd.Context(), uc.hc, uc.srv, user, id); err != nil {
			return err
		}
		if id == "" {
			fmt.Fprintf(os.Stderr, "Signed out %s's other sessions.\n", user)
		} else {
			fmt.Fprintf(os.Stderr, "Signed out %s's session %s.\n", user, id)
		}
		return nil
	},
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// shortAgent is a User-Agent's first product (Mozilla/5.0 is every
// browser's, so a browser's is "browser").
func shortAgent(ua string) string {
	if strings.HasPrefix(ua, "Mozilla/") {
		return "browser"
	}
	first, _, _ := strings.Cut(ua, " ")
	return first
}

func init() {
	userSessionListCmd.Flags().StringVar(&sessionUser, "user", "", "this user's sessions (default: yours)")
	userSessionRevokeCmd.Flags().StringVar(&sessionUser, "user", "", "whose session (default: yours)")
	userSessionRevokeCmd.Flags().BoolVar(&sessionOthers, "others", false, "every session but this one (all of --user's)")
	userSessionCmd.AddCommand(userSessionListCmd, userSessionRevokeCmd)
	UserCmd.AddCommand(userSessionCmd)
}
