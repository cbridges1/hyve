package cluster

import (
	"encoding/json"
	"log"
	"os"

	"github.com/cbridges1/hyve/cmd/shared"

	"sigs.k8s.io/yaml"
)

// listClustersAPI is cluster mode's counterpart to listClusters (crud.go)
// — see shared.UseClusterMode's doc comment for how a command picks
// between the two. Errors are fatal, not a silent fall-back to local
// files: a caller who's logged in and expects cluster mode should never
// get local (possibly stale, possibly nonexistent) data without knowing
// their actual request failed.
func listClustersAPI(client *shared.APIClient) {
	clusters, err := client.ListClusters()
	if err != nil {
		log.Fatalf("Failed to list clusters: %v", err)
	}

	if len(clusters) == 0 {
		log.Println("❌ No clusters found")
		log.Println("\n💡 Run 'hyve cluster create <cluster> --file <path>' to create a cluster")
		return
	}

	log.Printf("📦 Clusters (%d):\n", len(clusters))
	for _, c := range clusters {
		log.Printf("  %s", c.Name)
		log.Printf("    Driver: %s", c.Driver)
		if c.AccessMethod != "" {
			log.Printf("    Access: %s", c.AccessMethod)
		}
		for _, cond := range c.Conditions {
			log.Printf("    %s: %s", cond.Type, cond.Status)
		}
		log.Println()
	}

	log.Println("💡 Commands:")
	log.Println("  hyve cluster show <name>      # Show cluster definition")
	log.Println("  hyve cluster delete <name>    # Delete cluster")
}

func showClusterAPI(client *shared.APIClient, name string) {
	c, err := client.GetCluster(name)
	if err != nil {
		log.Fatalf("Failed to get cluster %q: %v", name, err)
	}

	log.Printf("Name:   %s", c.Name)
	log.Printf("Driver: %s", c.Driver)
	log.Printf("Observed generation: %d", c.ObservedGeneration)
	if c.AccessMethod != "" {
		log.Printf("Access method: %s", c.AccessMethod)
	}
	if c.AccessLastMinted != "" {
		log.Printf("Access last minted: %s", c.AccessLastMinted)
	}
	if len(c.Conditions) > 0 {
		log.Println("Conditions:")
		for _, cond := range c.Conditions {
			log.Printf("  %s: %s (%s)", cond.Type, cond.Status, cond.Reason)
			if cond.Message != "" {
				log.Printf("    %s", cond.Message)
			}
		}
	}
}

func deleteClusterAPI(client *shared.APIClient, name string) {
	if err := client.DeleteCluster(name); err != nil {
		log.Fatalf("Failed to delete cluster %q: %v", name, err)
	}
	log.Printf("📝 Cluster '%s' marked for deletion", name)
	log.Printf("   The controller will run onDelete workflows, delete via the module, and remove the object.")
}

// createClusterFromFileAPI reads path as the same apiVersion/kind/metadata/
// spec YAML shape a local clusters/<name>.yaml already uses, extracts
// metadata.name and spec, and POSTs them to the API — cluster mode has no
// server-side template rendering (see createClusterFromTemplate's local-
// only design), so unlike local mode's --template flow, this expects an
// already-fully-specified cluster definition.
func createClusterFromFileAPI(client *shared.APIClient, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Failed to read %s: %v", path, err)
	}
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		log.Fatalf("Failed to parse %s: %v", path, err)
	}

	var parsed struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		log.Fatalf("Failed to parse %s: %v", path, err)
	}
	if parsed.Metadata.Name == "" {
		log.Fatalf("%s: metadata.name is required", path)
	}

	log.Printf("🚀 Creating cluster '%s' via the API...", parsed.Metadata.Name)
	c, err := client.CreateCluster(parsed.Metadata.Name, parsed.Spec)
	if err != nil {
		log.Fatalf("Failed to create cluster: %v", err)
	}
	log.Printf("✅ Cluster '%s' created (driver: %s)", c.Name, c.Driver)
}
