package resourceref

import (
	"fmt"

	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/types"
)

// GatherResourceRefs collects the deduplicated remote ResourceRefs
// referenced by every template's and cluster's spec.resources[] in the
// repo — the input Install expects. Mirrors workflowref.GatherWorkflowRefs,
// scanning both Templates (whose resources are inherited by any
// ClusterDefinition created from them) and ClusterDefinitions directly.
func GatherResourceRefs(stateMgr *state.Manager, repoPath string) ([]types.ResourceRef, error) {
	tmplMgr := template.NewManager(repoPath)
	templates, err := tmplMgr.ListTemplates()
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}

	clusterDefs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster definitions: %w", err)
	}

	seen := map[string]bool{}
	var refs []types.ResourceRef
	add := func(list []types.ResourceRef) {
		for _, r := range list {
			if !r.IsRemote() {
				continue
			}
			if seen[r.Source] {
				continue
			}
			seen[r.Source] = true
			refs = append(refs, r)
		}
	}
	for _, t := range templates {
		add(t.Spec.Resources)
	}
	for _, c := range clusterDefs {
		add(c.Spec.Resources)
	}
	return refs, nil
}
