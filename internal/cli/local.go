package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/local"
)

// LocalCmd is `hyve local`: local mode's cluster.
func LocalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "local",
		Short: "Local mode: hyve on a K3s cluster in Docker, for laptops and CI",
		Long: `Local mode runs a standalone hyve on a K3s cluster in Docker that the CLI
manages: the same templates and commands as a server, with nothing to run.
'hyve local up' creates it and signs you in as its admin, as the "local"
context. CAPD clusters run as containers next to it.`,
	}
	cmd.AddCommand(localUpCmd(), localStatusCmd(), localStopCmd(), localDownCmd(), localSaveCmd(), localRestoreCmd(), localUICmd(), localIdleStopCmd())
	return cmd
}

func localUpCmd() *cobra.Command {
	o := local.Options{}
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Create or start the local cluster, install hyve in it and sign in",
		Example: `  hyve local up
  hyve local up --public-url https://hyve.example.com   # children outside Docker reach it through your tunnel`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o.Home = Home()
			o.Log = logf
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := local.Up(ctx, o); err != nil {
				return err
			}
			logf("\nLocal mode is up; the \"local\" context is signed in as admin.")
			logf("Next: hyve context use local && hyve template list")
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&o.Port, "port", local.DefaultPort, "host port the host endpoint listens on")
	f.StringVar(&o.K3sImage, "k3s-image", local.DefaultK3s, "K3s image")
	f.StringVar(&o.Image, "image", "", "hyve image (default: the release's, or 'hyve:dev' for a build from source; a local Docker image is imported)")
	f.StringVar(&o.StateDir, "state-dir", "", "keep the saved state here (remembered) instead of ~/.hyve/local/state, such as in a directory CI caches")
	f.StringVar(&o.PublicURL, "public-url", "", "the host endpoint's URL for clusters outside Docker's kind network (a tunnel you run)")
	f.StringSliceVar(&o.Providers, "providers", nil, "Cluster API providers to add now (default: none; known ones, such as docker and kubeadm, are added when first needed)")
	f.StringVar(&o.GOGC, "gogc", "40", "GOGC for K3s and every controller, trading a little CPU for memory")
	f.BoolVar(&o.Full, "full", false, "install the CAPI Operator and cert-manager, as a server install does, instead of the lean install")
	f.StringArrayVar(&o.APIServerArg, "apiserver-arg", nil, "extra kube-apiserver flag for K3s (repeatable)")
	f.DurationVar(&timeout, "timeout", 20*time.Minute, "how long to wait")
	return cmd
}

func localStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the local cluster exists and is running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ex, running := local.Running(cmd.Context())
			switch {
			case !ex:
				fmt.Println("not created ('hyve local up')")
			case running:
				fmt.Println("running")
			default:
				fmt.Println("stopped ('hyve local up' starts it)")
			}
			return nil
		},
	}
}

func localStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the local cluster (child clusters keep running; their agents reconnect when it starts)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			saveLocalState(cmd.Context())
			if err := local.Stop(cmd.Context()); err != nil {
				return err
			}
			logf("Stopped")
			return nil
		},
	}
}

func localDownCmd() *cobra.Command {
	var discard bool
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Delete the local cluster and its context, keeping its saved state for the next 'hyve local up'",
		Long: `Deletes the local cluster and its context. Its state (Cluster API objects,
templates, resources) is saved first and restored by the next 'hyve local
up', whose clusters' agents reconnect by themselves. CAPD clusters are
containers of their own and keep running; delete them first ('hyve
cluster delete') if you're done with them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if !discard {
				saveLocalState(ctx)
			}
			if err := local.Down(ctx, Home(), !discard); err != nil {
				return err
			}
			dir := local.StateDir(Home())
			if discard {
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
			} else if local.HasState(dir) {
				logf("Kept the saved state in %s ('hyve local up' restores it; --discard-state removes it)", dir)
			}
			logf("Deleted the local cluster")
			return nil
		},
	}
	cmd.Flags().BoolVar(&discard, "discard-state", false, "remove the saved state too, forgetting the clusters")
	return cmd
}

func localSaveCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save the local cluster's state now (hyve also saves after each change)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			n, err := local.Save(cmd.Context(), Home(), dir)
			if err != nil {
				return err
			}
			if dir == "" {
				dir = local.StateDir(Home())
			}
			logf("Saved %d objects to %s", n, dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "where to save (default: the state directory)")
	return cmd
}

func localRestoreCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore saved state into the local cluster ('hyve local up' does this for a new one)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				dir = local.StateDir(Home())
			}
			if !local.HasState(dir) {
				return fmt.Errorf("no saved state in %s", dir)
			}
			if err := local.Resume(cmd.Context(), Home(), logf); err != nil {
				return err
			}
			n, err := local.Restore(cmd.Context(), Home(), dir, logf)
			if err != nil {
				return err
			}
			logf("Restored %d objects from %s", n, dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "where to restore from (default: the state directory)")
	return cmd
}

// saveLocalState saves local mode's state if its cluster is running,
// reporting rather than failing.
func saveLocalState(ctx context.Context) {
	if _, running := local.Running(ctx); !running {
		return
	}
	if _, err := local.Save(ctx, Home(), ""); err != nil {
		logf("Not saving local mode's state: %v", err)
	}
}

// mutatingVerbs are the commands after which local mode saves its state.
var mutatingVerbs = map[string]bool{
	"create": true, "delete": true, "scale": true, "expire": true, "apply": true,
	"add": true, "remove": true, "import": true,
}

// SaveLocalStateAfter saves local mode's state after a command that
// changed something through the local context (the root's post-run).
func SaveLocalStateAfter(cmd *cobra.Command) {
	if !usedLocal || !mutatingVerbs[cmd.Name()] {
		return
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "local" {
			return
		}
	}
	saveLocalState(cmd.Context())
}

// localIdleStopCmd is the background watcher 'hyve local up' and every
// local-mode command start; hidden.
func localIdleStopCmd() *cobra.Command {
	var after time.Duration
	cmd := &cobra.Command{
		Use:    "idle-stop",
		Short:  "Stop the local cluster once it has been idle (run in the background by hyve)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return local.IdleStop(cmd.Context(), Home(), after)
		},
	}
	cmd.Flags().DurationVar(&after, "after", local.DefaultIdle, "idle time before stopping")
	return cmd
}
