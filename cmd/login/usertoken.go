package logincmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/login"
	"github.com/cbridges1/hyve/internal/signin"
)

var (
	tokenUser        string
	tokenAll         bool
	tokenExpires     string
	tokenDescription string

	trustIssuer   string
	trustAudience string
	trustClaims   []string
	trustGitHub   string
	trustGitLab   string
)

// The issuers `hyve user trust add --github/--gitlab` fill in.
const (
	githubIssuer = "https://token.actions.githubusercontent.com"
	gitlabIssuer = "https://gitlab.com"
)

var userTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Make, list and revoke API tokens (for scripts and CI)",
	Long: `An API token signs in as a user without a password, for scripts and CI:
'hyve login URL --token-stdin', or HYVE_URL and HYVE_TOKEN in the
environment. It acts as that user, with that user's access, and lasts until
it expires (at most a year) or is revoked. Changing the user's password
doesn't revoke it; disabling the user stops it.

You can manage your own tokens. Another user's (a CI user's) take the right
to the users/tokens subresource of that User: admins have it. A token can't
make tokens, so one that leaks can't outlive its expiry.

A CI job can often sign in with no token at all: see 'hyve user trust'.`,
}

var userTokenCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Make an API token; it's printed once, on stdout",
	Example: `  hyve user token create laptop --expires 30d
  hyve user token create github --user ci --expires 90d --description "web's release pipeline"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		token, info, err := login.CreateToken(cmd.Context(), uc.hc, uc.srv, tokenUser, args[0], tokenDescription, tokenExpires)
		if err != nil {
			return err
		}
		fmt.Println(token)
		fmt.Fprintf(os.Stderr, "Made API token %s for %s, until %s. It isn't shown again.\n",
			info.Name, info.User, info.ExpiresAt.Local().Format(time.DateTime))
		return nil
	},
}

var userTokenListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List API tokens: yours, --user's, or --all",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		list, err := login.ListTokens(cmd.Context(), uc.hc, uc.srv, tokenUser, tokenAll)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "USER\tNAME\tEXPIRES\tLAST USED\tMADE BY\tDESCRIPTION")
		for _, t := range list {
			used := "never"
			if t.LastUsed != nil {
				used = t.LastUsed.Local().Format(time.DateTime)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", t.User, t.Name, t.ExpiresAt.Local().Format(time.DateOnly), used, t.CreatedBy, t.Description)
		}
		return w.Flush()
	},
}

var userTokenRevokeCmd = &cobra.Command{
	Use:     "revoke NAME",
	Aliases: []string{"delete", "rm"},
	Short:   "Revoke an API token; it stops working at once",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		user := tokenUser
		if user == "" {
			if user = uc.srv.Username; user == "" {
				return errors.New("give --user")
			}
		}
		if err := login.RevokeToken(cmd.Context(), uc.hc, uc.srv, user, args[0]); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Revoked API token %s of %s.\n", args[0], user)
		return nil
	},
}

var userTrustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Let CI jobs sign in as a user with their own ID token (no secret to keep)",
	Long: `CI systems give each job an ID token: a short-lived, signed statement of
which repository, branch and workflow it is. A user that trusts a CI
system's jobs lets them sign in as it with that token ('hyve login URL
--username USER --github-actions', or --id-token-env), for an hour. The
pipeline keeps no password or API token.

An entry names the CI system's issuer and the claims a job's token must
have. One claim must pin the repository or project (repository, sub,
project_path...); a '*' in a value matches anything. The job's token must
be for the entry's audience ("hyve" unless --audience says).

Editing a user's trusted CI takes the right to update that User: admins
have it.`,
}

var userTrustAddCmd = &cobra.Command{
	Use:   "add USER NAME (--github OWNER/REPO | --gitlab GROUP/PROJECT | --issuer URL --claim K=V...)",
	Short: "Trust a CI system's jobs to sign in as USER",
	Example: `  hyve user trust add ci web-releases --github org/web --claim ref='refs/tags/*'
  hyve user trust add ci web-main --github org/web --claim ref=refs/heads/main --claim environment=production
  hyve user trust add ci infra --gitlab platform/infra --claim ref_protected=true
  hyve user trust add ci builds --issuer https://agent.buildkite.com --claim organization_slug=acme --claim pipeline_slug=web`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		t := hyvev1alpha1.TrustedCI{Name: args[1], Audience: trustAudience, Claims: map[string]string{}}
		switch {
		case countTrue(trustGitHub != "", trustGitLab != "", trustIssuer != "") != 1:
			return errors.New("give one of --github, --gitlab and --issuer")
		case trustGitHub != "":
			t.Issuer, t.Claims["repository"] = githubIssuer, trustGitHub
		case trustGitLab != "":
			t.Issuer, t.Claims["project_path"] = gitlabIssuer, trustGitLab
		default:
			t.Issuer = strings.TrimRight(trustIssuer, "/")
		}
		for _, kv := range trustClaims {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return fmt.Errorf("--claim %q: want KEY=VALUE", kv)
			}
			t.Claims[k] = v
		}
		if err := signin.ValidateTrustedCI(t); err != nil {
			return err
		}
		return editTrust(cmd, args[0], func(u *hyvev1alpha1.User) error {
			for _, e := range u.Spec.TrustedCI {
				if e.Name == t.Name {
					return fmt.Errorf("%s already trusts %s; remove it first", args[0], t.Name)
				}
			}
			u.Spec.TrustedCI = append(u.Spec.TrustedCI, t)
			return nil
		}, fmt.Sprintf("%s now trusts %s: %s", args[0], t.Name, describeTrust(t)))
	},
}

var userTrustRemoveCmd = &cobra.Command{
	Use:     "remove USER NAME",
	Aliases: []string{"rm"},
	Short:   "Stop trusting a CI system's jobs (sessions they have last out their hour)",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return editTrust(cmd, args[0], func(u *hyvev1alpha1.User) error {
			kept := u.Spec.TrustedCI[:0]
			for _, e := range u.Spec.TrustedCI {
				if e.Name != args[1] {
					kept = append(kept, e)
				}
			}
			if len(kept) == len(u.Spec.TrustedCI) {
				return fmt.Errorf("%s doesn't trust %s", args[0], args[1])
			}
			u.Spec.TrustedCI = kept
			return nil
		}, fmt.Sprintf("%s no longer trusts %s.", args[0], args[1]))
	},
}

var userTrustListCmd = &cobra.Command{
	Use:     "list USER",
	Aliases: []string{"ls"},
	Short:   "List the CI systems' jobs a user trusts",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		uc, err := newUserContext()
		if err != nil {
			return err
		}
		u := &hyvev1alpha1.User{}
		if err := uc.kc.Get(cmd.Context(), client.ObjectKey{Name: args[0]}, u); err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tISSUER\tAUDIENCE\tCLAIMS")
		for _, t := range u.Spec.TrustedCI {
			aud := t.Audience
			if aud == "" {
				aud = signin.DefaultCIAudience
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, t.Issuer, aud, claimList(t.Claims))
		}
		return w.Flush()
	},
}

func editTrust(cmd *cobra.Command, user string, change func(*hyvev1alpha1.User) error, done string) error {
	uc, err := newUserContext()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	u := &hyvev1alpha1.User{}
	if err := uc.kc.Get(ctx, client.ObjectKey{Name: user}, u); err != nil {
		return err
	}
	patch := client.MergeFrom(u.DeepCopy())
	if err := change(u); err != nil {
		return err
	}
	if err := uc.kc.Patch(ctx, u, patch); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, done)
	return nil
}

func claimList(claims map[string]string) string {
	var out []string
	for k, v := range claims {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func describeTrust(t hyvev1alpha1.TrustedCI) string {
	return fmt.Sprintf("jobs from %s with %s", t.Issuer, claimList(t.Claims))
}

func init() {
	userTokenCreateCmd.Flags().StringVar(&tokenUser, "user", "", "the user it signs in as (default: you)")
	userTokenCreateCmd.Flags().StringVar(&tokenExpires, "expires", "90d", "how long it lasts: 30d, 1y (the most), 720h")
	userTokenCreateCmd.Flags().StringVar(&tokenDescription, "description", "", "what it's for")
	userTokenListCmd.Flags().StringVar(&tokenUser, "user", "", "this user's tokens (default: yours)")
	userTokenListCmd.Flags().BoolVar(&tokenAll, "all", false, "every user's tokens")
	userTokenRevokeCmd.Flags().StringVar(&tokenUser, "user", "", "whose token (default: yours)")
	for _, c := range []*cobra.Command{userTokenCreateCmd, userTokenListCmd, userTokenRevokeCmd} {
		addServerFlag(c)
		c.SilenceUsage = true
		userTokenCmd.AddCommand(c)
	}

	f := userTrustAddCmd.Flags()
	f.StringVar(&trustGitHub, "github", "", "GitHub Actions jobs of this repository (OWNER/REPO)")
	f.StringVar(&trustGitLab, "gitlab", "", "GitLab CI jobs (gitlab.com) of this project (GROUP/PROJECT)")
	f.StringVar(&trustIssuer, "issuer", "", "another CI system's OIDC issuer URL")
	f.StringArrayVar(&trustClaims, "claim", nil, "a claim the job's token must have, KEY=VALUE; '*' matches anything (repeatable)")
	f.StringVar(&trustAudience, "audience", "", `the audience the job's token must be for (default "hyve")`)
	for _, c := range []*cobra.Command{userTrustAddCmd, userTrustRemoveCmd, userTrustListCmd} {
		addServerFlag(c)
		c.SilenceUsage = true
		userTrustCmd.AddCommand(c)
	}
	UserCmd.AddCommand(userTokenCmd, userTrustCmd)
}
