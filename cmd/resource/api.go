package resource

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/cbridges1/hyve/cmd/shared"
)

// createResourceFromFileAPI is cluster mode's counterpart to
// createResourceFromFile — see shared.UseClusterMode's doc comment for the
// local-vs-cluster dispatch pattern every cmd/* package follows.
func createResourceFromFileAPI(client *shared.APIClient, name, filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read file '%s': %v", filePath, err)
	}
	spec, err := json.Marshal(hyvev1alpha1.ResourceSpec{Manifest: string(data)})
	if err != nil {
		log.Fatalf("Failed to marshal resource spec: %v", err)
	}
	created, err := client.CreateResource(name, spec)
	if err != nil {
		log.Fatalf("Failed to create resource: %v", err)
	}
	log.Printf("✅ Created resource '%s' from file", created.Name)
}

func listResourcesAPI(client *shared.APIClient) {
	resources, err := client.ListResources()
	if err != nil {
		log.Fatalf("Failed to list resources: %v", err)
	}
	if len(resources) == 0 {
		log.Println("No resources found")
		log.Printf("💡 Create a resource with: hyve resource create <name> --file <path>")
		return
	}

	log.Printf("📋 Resources (%d):\n", len(resources))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE (bytes)")
	for _, res := range resources {
		var summary hyvev1alpha1.ResourceSpec
		_ = json.Unmarshal(res.Spec, &summary)
		fmt.Fprintf(w, "%s\t%d\n", res.Name, len(summary.Manifest))
	}
	w.Flush()

	log.Printf("\n💡 Commands:")
	log.Printf("   hyve resource show <name>     # Show resource manifest")
	log.Printf("   hyve resource delete <name>   # Delete resource")
}

func showResourceAPI(client *shared.APIClient, name string) {
	res, err := client.GetResource(name)
	if err != nil {
		log.Fatalf("Failed to get resource: %v", err)
	}
	var spec hyvev1alpha1.ResourceSpec
	_ = json.Unmarshal(res.Spec, &spec)
	log.Printf("📋 Resource: %s\n", res.Name)
	fmt.Println(spec.Manifest)
}

func deleteResourceAPI(client *shared.APIClient, name string, force bool) {
	if !force {
		log.Printf("Are you sure you want to delete resource '%s'? (y/N): ", name)
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			log.Println("Delete cancelled")
			return
		}
	}
	if err := client.DeleteResource(name); err != nil {
		log.Fatalf("Failed to delete resource: %v", err)
	}
	log.Printf("✅ Deleted resource '%s'", name)
}
