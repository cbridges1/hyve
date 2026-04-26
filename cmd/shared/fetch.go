package shared

import (
	gocontext "context"

	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/template"
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

// FetchTemplateNames returns a slice of template names from the current repository.
func FetchTemplateNames() []string {
	repoMgr, err := repository.NewManager()
	if err != nil {
		return nil
	}
	defer repoMgr.Close()
	repo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		return nil
	}
	mgr := template.NewManager(repo.LocalPath)
	list, err := mgr.ListTemplates()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, t := range list {
		names = append(names, t.Metadata.Name)
	}
	return names
}

// FetchTemplate returns a template by name from the current repository.
func FetchTemplate(name string) *template.Template {
	repoMgr, err := repository.NewManager()
	if err != nil {
		return nil
	}
	defer repoMgr.Close()
	repo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		return nil
	}
	mgr := template.NewManager(repo.LocalPath)
	tmpl, err := mgr.GetTemplate(name)
	if err != nil {
		return nil
	}
	return tmpl
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

// FetchKubeconfigClusterNames returns a slice of cluster names from the kubeconfig manager.
func FetchKubeconfigClusterNames() []string {
	mgr, _, err := CreateKubeconfigManager()
	if err != nil {
		return nil
	}
	list, err := mgr.ListKubeconfigs()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, k := range list {
		names = append(names, k.ClusterName)
	}
	return names
}

// FetchAWSAccountNames returns a slice of AWS account names.
func FetchAWSAccountNames() []string {
	mgr := providerconfig.NewManager(GetRepoPath())
	accounts, err := mgr.ListAWSAccounts()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(accounts))
	for _, a := range accounts {
		names = append(names, a.Name)
	}
	return names
}

// FetchGCPProjectNames returns a slice of GCP project names.
func FetchGCPProjectNames() []string {
	mgr := providerconfig.NewManager(GetRepoPath())
	projects, err := mgr.ListGCPProjects()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	return names
}

// FetchAzureSubscriptionNames returns a slice of Azure subscription names.
func FetchAzureSubscriptionNames() []string {
	mgr := providerconfig.NewManager(GetRepoPath())
	subs, err := mgr.ListAzureSubscriptions()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(subs))
	for _, s := range subs {
		names = append(names, s.Name)
	}
	return names
}

// FetchCivoOrgNames returns a slice of Civo organization names.
func FetchCivoOrgNames() []string {
	mgr := providerconfig.NewManager(GetRepoPath())
	orgs, err := mgr.ListCivoOrganizations()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(orgs))
	for _, o := range orgs {
		names = append(names, o.Name)
	}
	return names
}
