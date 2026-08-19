package template

import (
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/types"

	"sigs.k8s.io/yaml"
)

// createTemplateAPI is cluster mode's counterpart to createTemplate — see
// shared.UseClusterMode's doc comment for the local-vs-cluster dispatch
// pattern every cmd/* package follows. Builds the exact same
// template.TemplateSpec shape the local flow does (its JSON tags are
// designed to be wire-compatible with hyvev1alpha1.TemplateSpec — see
// internal/crdconv, which exists precisely to keep the two symmetric) and
// posts it directly, rather than importing internal/apis/hyve/v1alpha1
// into the CLI just to reconstruct the same shape a second time.
func createTemplateAPI(
	client *shared.APIClient,
	name, description, driverSource, driverVersion, region string,
	params map[string]string,
	beforeCreateStr, onCreateStr, onDeleteStr, afterDeleteStr string,
	schedule string,
	lockParams bool,
) {
	parseWorkflows := func(s string) []types.WorkflowRef {
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		out := make([]types.WorkflowRef, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, types.WorkflowRef{Name: p})
			}
		}
		return out
	}

	spec := template.TemplateSpec{
		Driver: types.DriverRef{Source: driverSource, Version: driverVersion},
		Region: region,
		Params: params,
		Workflows: types.WorkflowsSpec{
			BeforeCreate: parseWorkflows(beforeCreateStr),
			OnCreate:     parseWorkflows(onCreateStr),
			OnDelete:     parseWorkflows(onDeleteStr),
			AfterDelete:  parseWorkflows(afterDeleteStr),
		},
		Schedule:   schedule,
		LockParams: lockParams,
	}
	data, err := json.Marshal(specWithDescription{TemplateSpec: spec, Description: description})
	if err != nil {
		log.Fatalf("Failed to marshal template spec: %v", err)
	}

	tpl, err := client.CreateTemplate(name, data)
	if err != nil {
		log.Fatalf("Failed to create template: %v", err)
	}
	log.Printf("✅ Template '%s' created successfully", tpl.Name)
	log.Println("\n💡 Create a cluster from this template with:")
	log.Printf("  hyve cluster create <cluster-name> --template %s", name)
}

// specWithDescription adds the description field template.TemplateSpec has
// no room for (it lives on template.TemplateMetadata in local mode, but on
// hyvev1alpha1.TemplateSpec itself in cluster mode — same
// metadata-vs-spec split ClusterDefinitionSpec.Region already has).
type specWithDescription struct {
	template.TemplateSpec
	Description string `json:"description,omitempty"`
}

// createTemplateFromFileAPI reads path as the same apiVersion/kind/metadata/
// spec YAML shape a local templates/<name>.yaml already uses, extracts
// metadata.name and spec, and POSTs them — mirrors
// cmd/cluster/api.go's createClusterFromFileAPI.
func createTemplateFromFileAPI(client *shared.APIClient, path string) {
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

	created, err := client.CreateTemplate(parsed.Metadata.Name, parsed.Spec)
	if err != nil {
		log.Fatalf("Failed to create template: %v", err)
	}
	log.Printf("✅ Template '%s' created from file", created.Name)
}

func listTemplatesAPI(client *shared.APIClient) {
	templates, err := client.ListTemplates()
	if err != nil {
		log.Fatalf("Failed to list templates: %v", err)
	}
	if len(templates) == 0 {
		log.Println("No templates found")
		log.Println("\n💡 Create a template with:")
		log.Println("  hyve template create <name> --driver <source> --driver-version <ver>")
		return
	}

	log.Printf("📋 Available templates (%d):\n", len(templates))
	for _, t := range templates {
		var summary templateSpecSummary
		_ = json.Unmarshal(t.Spec, &summary)
		log.Printf("  %s", t.Name)
		if summary.Description != "" {
			log.Printf("    Description: %s", summary.Description)
		}
		log.Printf("    Driver: %s@%s", summary.Driver.Source, summary.Driver.Version)
		if summary.Region != "" {
			log.Printf("    Region: %s", summary.Region)
		}
		if summary.Schedule != "" {
			log.Printf("    Expiry Schedule: %s", summary.Schedule)
		}
		log.Println()
	}
	log.Println("💡 Create a cluster from a template with:")
	log.Println("  hyve cluster create <cluster-name> --template <template-name>")
}

// templateSpecSummary picks out the fields worth printing from a
// TemplateDTO's raw Spec JSON — deliberately not a full mirror of
// hyvev1alpha1.TemplateSpec (nothing here needs params/workflows/resources
// displayed field-by-field the way `show` below dumps the whole thing).
type templateSpecSummary struct {
	Description string `json:"description,omitempty"`
	Driver      struct {
		Source  string `json:"source"`
		Version string `json:"version"`
	} `json:"driver"`
	Region   string `json:"region,omitempty"`
	Schedule string `json:"schedule,omitempty"`
}

func deleteTemplateAPI(client *shared.APIClient, name string) {
	if err := client.DeleteTemplate(name); err != nil {
		log.Fatalf("Failed to delete template: %v", err)
	}
	log.Printf("✅ Template '%s' deleted successfully", name)
}

func showTemplateAPI(client *shared.APIClient, name string) {
	tpl, err := client.GetTemplate(name)
	if err != nil {
		log.Fatalf("Failed to get template: %v", err)
	}
	var pretty map[string]interface{}
	_ = json.Unmarshal(tpl.Spec, &pretty)
	data, _ := json.MarshalIndent(pretty, "", "  ")
	log.Printf("📋 Template: %s\n", tpl.Name)
	log.Println(string(data))
}
