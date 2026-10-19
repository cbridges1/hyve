package cli

import (
	"context"
	"fmt"
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
	cmd.AddCommand(localUpCmd(), localStatusCmd(), localStopCmd(), localDownCmd(), localIdleStopCmd())
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
	f.StringVar(&o.Image, "image", "", "hyve image (default: the release's; a local Docker image is imported)")
	f.StringVar(&o.PublicURL, "public-url", "", "the host endpoint's URL for clusters outside Docker's kind network (a tunnel you run)")
	f.StringSliceVar(&o.Providers, "providers", nil, "Cluster API providers to add (default: docker, with kubeadm)")
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
			if err := local.Stop(cmd.Context()); err != nil {
				return err
			}
			logf("Stopped")
			return nil
		},
	}
}

func localDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Delete the local cluster and its context (delete child clusters first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := local.Down(cmd.Context(), Home()); err != nil {
				return err
			}
			logf("Deleted the local cluster")
			return nil
		},
	}
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
