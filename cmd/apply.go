package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/cbridges1/hyve/cmd/shared"
)

var applyFile string

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Create a cluster, template, or workflow from a CR-shaped YAML file, auto-detected by kind",
	Long: `Reads a YAML file's kind (ClusterDefinition, Template, or Workflow) and
creates it via the API — the same file shape 'clusters/', 'templates/', and
'workflows/' already use locally, and the same thing 'kubectl apply -f'
accepts once the cluster's CRDs are installed. Equivalent to picking the
matching 'hyve cluster/template/workflow create --file' by hand, without
needing to know which one matches a given file.

Requires an active environment with cluster-mode credentials (see 'hyve
login'). Create-only: fails if a resource with the same name already
exists — see 'hyve migrate' for a bulk, safer-by-default (dry-run by
default, skip-existing) alternative.`,
	Run: func(cmd *cobra.Command, args []string) {
		runApply()
	},
}

func init() {
	applyCmd.Flags().StringVarP(&applyFile, "file", "f", "", "Path to a CR-shaped YAML file (required)")
	applyCmd.MarkFlagRequired("file")
	rootCmd.AddCommand(applyCmd)
}

func runApply() {
	sess, ok := shared.UseClusterMode()
	if !ok {
		log.Fatal("`hyve apply` requires an active environment with cluster-mode credentials — run `hyve login` first")
	}
	client := shared.NewAPIClient(sess)

	kind, name, spec, err := parseCRFile(applyFile)
	if err != nil {
		log.Fatal(err)
	}

	desc, err := createByKind(client, kind, name, spec)
	if err != nil {
		log.Fatalf("%s: %v", applyFile, err)
	}
	log.Printf("✅ %s '%s' created", desc, name)
}

// parseCRFile reads path as CR-shaped YAML (apiVersion/kind/metadata/spec)
// and extracts just enough to dispatch by kind — shared by `hyve apply` and
// `hyve migrate`'s single-file mode.
func parseCRFile(path string) (kind, name string, spec json.RawMessage, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	var parsed struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		return "", "", nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if parsed.Metadata.Name == "" {
		return "", "", nil, fmt.Errorf("%s: metadata.name is required", path)
	}

	return parsed.Kind, parsed.Metadata.Name, parsed.Spec, nil
}

// createByKind dispatches to the matching APIClient.CreateX call and
// returns a short description of what was created (for logging) — shared
// by `hyve apply` and `hyve migrate`.
func createByKind(client *shared.APIClient, kind, name string, spec json.RawMessage) (string, error) {
	switch kind {
	case "ClusterDefinition":
		c, err := client.CreateCluster(name, spec)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Cluster (driver: %s)", c.Driver), nil
	case "Template":
		if _, err := client.CreateTemplate(name, spec); err != nil {
			return "", err
		}
		return "Template", nil
	case "Workflow":
		if _, err := client.CreateWorkflow(name, spec); err != nil {
			return "", err
		}
		return "Workflow", nil
	case "":
		return "", fmt.Errorf("kind is required (must be ClusterDefinition, Template, or Workflow)")
	default:
		return "", fmt.Errorf("unrecognized kind %q (must be ClusterDefinition, Template, or Workflow)", kind)
	}
}
