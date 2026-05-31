package module

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/template"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install all modules referenced by templates and clusters into hyve.lock",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		tmplMgr := template.NewManager(repoPath)
		templates, err := tmplMgr.ListTemplates()
		if err != nil {
			log.Fatalf("Failed to list templates: %v", err)
		}

		clusterDefs, err := stateMgr.LoadClusterDefinitions()
		if err != nil {
			log.Fatalf("Failed to load cluster definitions: %v", err)
		}

		lf, err := mod.LoadLockFile(repoPath)
		if err != nil {
			log.Fatalf("Failed to load lock file: %v", err)
		}

		type ref struct{ src, ver string }
		seen := map[string]bool{}
		var refs []ref
		add := func(src, ver string) {
			if src == "" {
				return
			}
			key := mod.LockKey(src, ver)
			if seen[key] {
				return
			}
			seen[key] = true
			refs = append(refs, ref{src, ver})
		}
		for _, t := range templates {
			add(t.Spec.Driver.Source, t.Spec.Driver.Version)
		}
		for _, c := range clusterDefs {
			add(c.Spec.Driver.Source, c.Spec.Driver.Version)
		}

		updated := false
		for _, r := range refs {
			if lf.GetLocked(r.src, r.ver) != nil {
				log.Printf("Already locked: %s@%s", r.src, r.ver)
				continue
			}
			log.Printf("Resolving %s@%s...", r.src, r.ver)
			resolved, err := mod.Resolve(r.src, r.ver, nil, repoPath)
			if err != nil {
				log.Printf("Warning: failed to resolve %s@%s: %v", r.src, r.ver, err)
				continue
			}
			lf.SetLocked(r.src, r.ver, &mod.LockedModule{
				Source:   r.src,
				Resolved: resolved.Resolved,
				SHA256:   resolved.SHA256,
				Runner:   resolved.Runner,
			})
			log.Printf("Locked %s@%s (sha256: %s)", r.src, r.ver, resolved.SHA256)
			updated = true
		}

		if !updated {
			log.Println("Lock file is up to date")
			return
		}

		if err := mod.SaveLockFile(repoPath, lf); err != nil {
			log.Fatalf("Failed to save lock file: %v", err)
		}
		log.Println("✅ hyve.lock updated")

		shared.CommitStateChanges(ctx, stateMgr, "chore: update hyve.lock")
	},
}
