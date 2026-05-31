package shared

import (
	gocontext "context"

	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/workflow"
)

// FetchClusterNames returns a slice of cluster names from the current state manager.
func FetchClusterNames() []string {
	sm, _ := CreateStateManager(gocontext.Background())
	defs, err := sm.LoadClusterDefinitions()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		if !d.Spec.Delete {
			names = append(names, d.Metadata.Name)
		}
	}
	return names
}

// FetchWorkflowNames returns a slice of workflow names from the current repository.
func FetchWorkflowNames() []string {
	mgr, err := workflow.NewManager(GetLocalPath())
	if err != nil {
		return nil
	}
	list, err := mgr.ListWorkflows()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, w := range list {
		names = append(names, w.Metadata.Name)
	}
	return names
}

// FetchGitRepoNames returns a slice of git repository names.
func FetchGitRepoNames() []string {
	repoMgr, err := repository.NewManager()
	if err != nil {
		return nil
	}
	defer repoMgr.Close()
	repos, err := repoMgr.ListRepositories()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return names
}
