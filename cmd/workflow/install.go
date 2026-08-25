package workflow

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/workflowref"
)

var workflowInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Resolve all remote workflow references found in templates and clusters into hyve.lock",
	Run: func(cmd *cobra.Command, args []string) {
		if _, ok := shared.UseClusterMode(); ok {
			log.Fatal("`hyve workflow install` is a local-checkout-only command — there's no local hyve.lock for a cluster-mode CLI session to write to. The controller itself already resolves remote workflow refs live, per-reconcile (see internal/controller/reconciler.go's resolveWorkflowIfNeeded), so no separate install step is needed in cluster mode at all.")
		}
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		refs, err := workflowref.GatherWorkflowRefs(stateMgr, repoPath)
		if err != nil {
			log.Fatalf("%v", err)
		}
		for _, ref := range refs {
			log.Printf("Resolving %s ...", ref.String())
		}

		locked, collisions, resolveErrors, _, changed, err := workflowref.Install(repoPath, refs, "")
		if err != nil {
			log.Fatalf("%v", err)
		}
		for _, c := range collisions {
			log.Printf("Warning: workflow name %q is provided by both %s and %s — `hyve workflow run %s` will require the full source string",
				c.Name, c.FirstSource, c.CollidedSource, c.Name)
		}
		for _, e := range resolveErrors {
			log.Printf("Warning: failed to resolve %s", e)
		}
		for _, l := range locked {
			log.Printf("Locked %s@%s (name=%s, sha256=%s)", l.CanonicalSource, l.RawVersion, l.Name, l.SHA256)
		}

		if !changed {
			log.Println("hyve.lock is up to date (workflows)")
			return
		}
		log.Println("✅ hyve.lock updated")
		shared.CommitStateChanges(ctx, stateMgr, "chore: update hyve.lock (workflows)")
	},
}

func init() { workflowCmd.AddCommand(workflowInstallCmd) }
