package resource

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/resourceref"
)

var resourceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Resolve all remote resource references found in templates and clusters into hyve.lock",
	Run: func(cmd *cobra.Command, args []string) {
		if _, ok := shared.UseClusterMode(); ok {
			log.Fatal("`hyve resource install` is a local-checkout-only command — there's no local hyve.lock for a cluster-mode CLI session to write to. The controller itself already resolves remote resource refs live, per-reconcile (see internal/controller/reconciler.go's resolveResourceIfNeeded), so no separate install step is needed in cluster mode at all.")
		}
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		refs, err := resourceref.GatherResourceRefs(stateMgr, repoPath)
		if err != nil {
			log.Fatalf("%v", err)
		}
		for _, ref := range refs {
			log.Printf("Resolving %s ...", ref.Source)
		}

		locked, collisions, resolveErrors, _, changed, err := resourceref.Install(repoPath, refs, "")
		if err != nil {
			log.Fatalf("%v", err)
		}
		for _, c := range collisions {
			log.Printf("Warning: resource name %q is provided by both %s and %s — reference it by its full source string to disambiguate",
				c.Name, c.FirstSource, c.CollidedSource)
		}
		for _, e := range resolveErrors {
			log.Printf("Warning: failed to resolve %s", e)
		}
		for _, l := range locked {
			log.Printf("Locked %s@%s (name=%s, sha256=%s)", l.CanonicalSource, l.RawVersion, l.Name, l.SHA256)
		}

		if !changed {
			log.Println("hyve.lock is up to date (resources)")
			return
		}
		log.Println("✅ hyve.lock updated")
		shared.CommitStateChanges(ctx, stateMgr, "chore: update hyve.lock (resources)")
	},
}

func init() { resourceCmd.AddCommand(resourceInstallCmd) }
