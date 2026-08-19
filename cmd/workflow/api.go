package workflow

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/workflow"

	"sigs.k8s.io/yaml"
)

// createWorkflowTemplateAPI is cluster mode's counterpart to
// createWorkflowTemplate — see shared.UseClusterMode's doc comment for the
// local-vs-cluster dispatch pattern every cmd/* package follows.
// workflow.CreateWorkflowTemplate's JSON tags are wire-compatible with
// hyvev1alpha1.WorkflowSpec by design (see internal/workflow/convert.go),
// so its Spec is posted directly rather than reconstructing the same shape
// via internal/apis/hyve/v1alpha1 a second time.
func createWorkflowTemplateAPI(client *shared.APIClient, name, description string) {
	if name == "" {
		log.Fatal("Workflow name is required when using --template")
	}
	wf := workflow.CreateWorkflowTemplate(name, description)
	data, err := json.Marshal(specWithDescription{WorkflowSpec: wf.Spec, Description: description})
	if err != nil {
		log.Fatalf("Failed to marshal workflow spec: %v", err)
	}
	created, err := client.CreateWorkflow(name, data)
	if err != nil {
		log.Fatalf("Failed to create workflow: %v", err)
	}
	log.Printf("✅ Created workflow template '%s'", created.Name)
	log.Printf("🔧 Edit it with: hyve workflow show %s", created.Name)
}

// specWithDescription adds the description field workflow.WorkflowSpec has
// no room for (it lives on workflow.WorkflowMetadata in local mode, but on
// hyvev1alpha1.WorkflowSpec itself in cluster mode — same
// metadata-vs-spec split ClusterDefinitionSpec.Region already has).
// Embedding flattens WorkflowSpec's own JSON fields alongside Description
// automatically, no custom MarshalJSON needed.
type specWithDescription struct {
	workflow.WorkflowSpec
	Description string `json:"description,omitempty"`
}

// createWorkflowFromFileAPI reads path as the same apiVersion/kind/metadata/
// spec YAML shape a local workflows/<name>.yaml already uses (see
// internal/workflow's file format), extracts metadata.name and spec, and
// POSTs them — mirrors cmd/cluster/api.go's createClusterFromFileAPI.
func createWorkflowFromFileAPI(client *shared.APIClient, path string) {
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

	created, err := client.CreateWorkflow(parsed.Metadata.Name, parsed.Spec)
	if err != nil {
		log.Fatalf("Failed to create workflow: %v", err)
	}
	log.Printf("✅ Created workflow '%s' from file", created.Name)
}

func listWorkflowsAPI(client *shared.APIClient) {
	workflows, err := client.ListWorkflows()
	if err != nil {
		log.Fatalf("Failed to list workflows: %v", err)
	}
	if len(workflows) == 0 {
		log.Println("No workflows found")
		log.Printf("💡 Create a workflow with: hyve workflow create --template my-workflow")
		return
	}

	log.Printf("📋 Workflows (%d):\n", len(workflows))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tJOBS")
	for _, wf := range workflows {
		var summary workflowSpecSummary
		_ = json.Unmarshal(wf.Spec, &summary)
		description := summary.Description
		if len(description) > 50 {
			description = description[:47] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%d\n", wf.Name, description, len(summary.Jobs))
	}
	w.Flush()

	log.Printf("\n💡 Commands:")
	log.Printf("   hyve workflow show <name>     # Show workflow details")
	log.Printf("   hyve workflow delete <name>   # Delete workflow")
}

// workflowSpecSummary picks out the fields worth printing from a
// WorkflowDTO's raw Spec JSON.
type workflowSpecSummary struct {
	Description string `json:"description,omitempty"`
	Jobs        []struct {
		Name  string `json:"name"`
		Steps []struct {
			Name string `json:"name"`
		} `json:"steps"`
	} `json:"jobs"`
}

func showWorkflowAPI(client *shared.APIClient, name string) {
	wf, err := client.GetWorkflow(name)
	if err != nil {
		log.Fatalf("Failed to get workflow: %v", err)
	}
	var pretty map[string]interface{}
	_ = json.Unmarshal(wf.Spec, &pretty)
	data, _ := json.MarshalIndent(pretty, "", "  ")
	log.Printf("📋 Workflow: %s\n", wf.Name)
	log.Println(string(data))
}

func deleteWorkflowAPI(client *shared.APIClient, name string, force bool) {
	if !force {
		log.Printf("Are you sure you want to delete workflow '%s'? (y/N): ", name)
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			log.Println("Delete cancelled")
			return
		}
	}
	if err := client.DeleteWorkflow(name); err != nil {
		log.Fatalf("Failed to delete workflow: %v", err)
	}
	log.Printf("✅ Deleted workflow '%s'", name)
}
