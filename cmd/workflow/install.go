package workflow

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflowref"
)

var workflowInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Resolve all remote workflow references found in templates and clusters into hyve.lock",
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

		type key struct{ source, path string }
		seen := map[key]bool{}
		var refs []types.WorkflowRef
		add := func(list []types.WorkflowRef) {
			for _, r := range list {
				if !r.IsRemote() {
					continue
				}
				k := key{r.Source, r.Path}
				if seen[k] {
					continue
				}
				seen[k] = true
				refs = append(refs, r)
			}
		}
		for _, t := range templates {
			add(t.Spec.Workflows.BeforeCreate)
			add(t.Spec.Workflows.OnCreate)
			add(t.Spec.Workflows.OnDelete)
			add(t.Spec.Workflows.AfterDelete)
			// TemplateWorkflowsSpec has no PreReconcile — nothing to add here.
		}
		for _, c := range clusterDefs {
			add(c.Spec.Workflows.PreReconcile)
			add(c.Spec.Workflows.BeforeCreate)
			add(c.Spec.Workflows.OnCreate)
			add(c.Spec.Workflows.OnDelete)
			add(c.Spec.Workflows.AfterDelete)
		}

		changed := false
		nameOwner := map[string]string{} // name -> first canonical source seen this run
		for _, ref := range refs {
			log.Printf("Resolving %s ...", ref.String())
			files, err := workflowref.Resolve(ref.Source, ref.Path, lf)
			if err != nil {
				log.Printf("Warning: failed to resolve %s: %v", ref.String(), err)
				continue
			}
			for _, f := range files {
				if owner, ok := nameOwner[f.Name]; ok && owner != f.CanonicalSource {
					log.Printf("Warning: workflow name %q is provided by both %s and %s — `hyve workflow run %s` will require the full source string",
						f.Name, owner, f.CanonicalSource, f.Name)
				}
				nameOwner[f.Name] = f.CanonicalSource

				existing := lf.GetLockedWorkflow(f.CanonicalSource, f.RawVersion)
				if existing != nil && existing.SHA256 == f.SHA256 {
					continue // unchanged — don't touch the lock, avoids a no-op commit
				}
				lf.SetLockedWorkflow(f.CanonicalSource, f.RawVersion, &mod.LockedWorkflow{
					Name:     f.Name,
					Source:   f.CanonicalSource,
					Resolved: f.Resolved,
					SHA256:   f.SHA256,
				})
				log.Printf("Locked %s@%s (name=%s, sha256=%s)", f.CanonicalSource, f.RawVersion, f.Name, f.SHA256)
				changed = true
			}
		}

		if !changed {
			log.Println("hyve.lock is up to date (workflows)")
			return
		}
		if err := mod.SaveLockFile(repoPath, lf); err != nil {
			log.Fatalf("Failed to save lock file: %v", err)
		}
		log.Println("✅ hyve.lock updated")
		shared.CommitStateChanges(ctx, stateMgr, "chore: update hyve.lock (workflows)")
	},
}

func init() { workflowCmd.AddCommand(workflowInstallCmd) }
