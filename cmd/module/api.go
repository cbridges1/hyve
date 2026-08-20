package module

import (
	"fmt"
	"log"

	"github.com/cbridges1/hyve/cmd/shared"
)

// listModulesAPI is cluster mode's counterpart to the local listCmd — shows
// every Module CR the controller has recorded a resolve attempt for (see
// internal/controller/reconciler.go's resolveModuleIfNeeded), not a
// human-curated hyve.lock (cluster mode has none).
func listModulesAPI(client *shared.APIClient) {
	modules, err := client.ListModules()
	if err != nil {
		log.Fatalf("Failed to list modules: %v", err)
	}
	if len(modules) == 0 {
		fmt.Println("No modules resolved yet.")
		fmt.Println("\nModules resolve automatically the first time a cluster/template references them.")
		return
	}
	fmt.Printf("📦 Resolved modules (%d):\n", len(modules))
	for _, m := range modules {
		fmt.Printf("  %s@%s\n", m.Spec.Source, m.Spec.Version)
		if m.Status.Resolved {
			fmt.Printf("    resolved: true\n")
			if m.Status.SHA256 != "" {
				fmt.Printf("    sha256:   %s\n", m.Status.SHA256)
			}
		} else {
			fmt.Printf("    resolved: false\n")
			if m.Status.Error != "" {
				fmt.Printf("    error:    %s\n", m.Status.Error)
			}
		}
	}
}

// showModuleAPI is cluster mode's counterpart to the local infoCmd — a
// narrower view than local mode's (which reads the resolved module's own
// module.yaml manifest for name/description/params): cluster mode only
// has what the controller recorded on the Module CR itself (source,
// version, resolved, sha256, error), not the manifest's contents, since
// that lives inside the controller pod's own PVC, not exposed via the API.
func showModuleAPI(client *shared.APIClient, name string) {
	m, err := client.GetModule(name)
	if err != nil {
		log.Fatalf("Failed to get module: %v", err)
	}
	fmt.Printf("📦 %s@%s\n", m.Spec.Source, m.Spec.Version)
	fmt.Printf("  Resolved: %v\n", m.Status.Resolved)
	if m.Status.SHA256 != "" {
		fmt.Printf("  SHA256:   %s\n", m.Status.SHA256)
	}
	if m.Status.Error != "" {
		fmt.Printf("  Error:    %s\n", m.Status.Error)
	}
}
