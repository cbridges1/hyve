package module

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install all modules referenced by templates and clusters into hyve.lock",
	Run: func(cmd *cobra.Command, args []string) {
		if _, ok := shared.UseClusterMode(); ok {
			log.Fatal("`hyve module install` is local-mode only — modules resolve automatically when referenced by a cluster/template in cluster mode; see `hyve module list` to inspect what's been resolved.")
		}
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		refs, err := mod.GatherModuleRefs(stateMgr, repoPath)
		if err != nil {
			log.Fatalf("%v", err)
		}

		locked, alreadyLocked, resolveErrors, err := mod.InstallModules(repoPath, refs)
		if err != nil {
			log.Fatalf("%v", err)
		}
		for _, r := range alreadyLocked {
			log.Printf("Already locked: %s@%s", r.Source, r.Version)
		}
		for _, e := range resolveErrors {
			log.Printf("Warning: failed to resolve %s", e)
		}
		for _, l := range locked {
			log.Printf("Locked %s@%s (sha256: %s)", l.Source, l.Version, l.Entry.SHA256)
		}

		if len(locked) == 0 {
			log.Println("Lock file is up to date")
			return
		}

		log.Println("✅ hyve.lock updated")
		shared.CommitStateChanges(ctx, stateMgr, "chore: update hyve.lock")
	},
}
