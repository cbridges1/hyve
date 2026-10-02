package resource

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/resource"
	"github.com/cbridges1/hyve/internal/resourceref"
)

// Cmd is the root resource command exposed to the parent.
var Cmd = resourceCmd

var resourceCmd = &cobra.Command{
	Use:   "resource",
	Short: "Manage resources",
	Long: `Manage Kubernetes-manifest resources referenced by name from a
ClusterDefinition's spec.resources[] — the CRD/local-file counterpart to a
local-path or remote-git resource source. Resources are stored as Resource
CRs (cluster mode) or resources/<name>.yaml files (local mode) wrapping a
raw manifest.`,
}

var resourceCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a resource from an existing manifest file",
	Long: `Wraps an existing raw Kubernetes manifest file (possibly
multi-document) into a Resource, named <name>, referenceable afterward via
{name: <name>} on any ClusterDefinition's spec.resources[].`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		file, _ := cmd.Flags().GetString("file")
		if file == "" {
			log.Fatal("--file is required — a resource is created by wrapping an existing manifest file")
		}

		if sess, ok := shared.UseClusterMode(); ok {
			createResourceFromFileAPI(shared.NewAPIClient(sess), name, file)
			return
		}
		createResourceFromFile(name, file)
	},
}

var resourceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all resources",
	Long:  "List all Resource definitions available by name in the current repository/cluster.",
	Run: func(cmd *cobra.Command, args []string) {
		if sess, ok := shared.UseClusterMode(); ok {
			listResourcesAPI(shared.NewAPIClient(sess))
			return
		}
		listResources()
	},
}

var resourceShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show a resource's manifest",
	Long:  "Display the raw manifest wrapped by a named Resource.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if sess, ok := shared.UseClusterMode(); ok {
			showResourceAPI(shared.NewAPIClient(sess), args[0])
			return
		}
		showResource(args[0])
	},
}

var resourceDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a resource",
	Long:  "Remove a named Resource definition. Does not touch anything already applied to a cluster via it.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")
		if sess, ok := shared.UseClusterMode(); ok {
			deleteResourceAPI(shared.NewAPIClient(sess), args[0], force)
			return
		}
		deleteResource(args[0], force)
	},
}

func init() {
	resourceCreateCmd.Flags().StringP("file", "f", "", "Wrap this existing manifest file's raw content into the new resource")
	resourceDeleteCmd.Flags().BoolP("force", "f", false, "Delete without confirmation")

	resourceCmd.AddCommand(resourceCreateCmd)
	resourceCmd.AddCommand(resourceListCmd)
	resourceCmd.AddCommand(resourceShowCmd)
	resourceCmd.AddCommand(resourceDeleteCmd)
}

func getResourcesDir() string {
	return filepath.Join(shared.GetLocalPath(), resource.ResourcesDir)
}

func createResourceFromFile(name, filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read file '%s': %v", filePath, err)
	}

	cr := hyvev1alpha1.Resource{
		TypeMeta:   metav1.TypeMeta{APIVersion: resource.ResourceAPIVersion, Kind: resource.ResourceKind},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       hyvev1alpha1.ResourceSpec{Manifest: string(data)},
	}
	out, err := yaml.Marshal(&cr)
	if err != nil {
		log.Fatalf("Failed to marshal resource: %v", err)
	}

	dir := getResourcesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("Failed to create resources directory: %v", err)
	}
	dest := filepath.Join(dir, name+resource.ResourceFileExt)
	if err := os.WriteFile(dest, out, 0o644); err != nil {
		log.Fatalf("Failed to write resource file: %v", err)
	}

	log.Printf("✅ Created resource '%s' from file", name)
	log.Printf("📁 Location: %s", dest)
}

func listResources() {
	dir := getResourcesDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("Failed to list resources: %v", err)
	}

	// Best-effort; a missing/unreadable hyve.lock just means no
	// git-referenced resources to add to the listing below — local mode's
	// equivalent of cluster mode's ResourceRefStatus merge
	// (internal/api/resources.go's handleListResources).
	lf, _ := module.LoadLockFile(shared.GetLocalPath())

	src := resource.FileSource{Dir: dir}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE (bytes)\tSOURCE")
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), resource.ResourceFileExt) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), resource.ResourceFileExt)
		manifest, err := src.GetResource(name)
		if err != nil {
			log.Printf("Warning: failed to read resource %q: %v", name, err)
			continue
		}
		fmt.Fprintf(w, "%s\t%d\t%s\n", name, len(manifest), "")
		count++
	}
	if lf != nil {
		for _, locked := range lf.Resources {
			fmt.Fprintf(w, "%s\t%s\t%s\n", locked.Name, "", locked.Source)
			count++
		}
	}
	w.Flush()

	if count == 0 {
		log.Println("No resources found in repository")
		log.Printf("💡 Create a resource with: hyve resource create <name> --file <path>")
		return
	}

	log.Printf("\n💡 Commands:")
	log.Printf("   hyve resource show <name>     # Show resource manifest")
	log.Printf("   hyve resource delete <name>   # Delete resource")
}

func showResource(name string) {
	src := resource.FileSource{Dir: getResourcesDir()}
	manifest, err := src.GetResource(name)
	if err != nil {
		showRemoteResourceFromLock(name)
		return
	}
	log.Printf("📋 Resource: %s\n", name)
	fmt.Println(string(manifest))
}

// showRemoteResourceFromLock is showResource's fallback once the local
// resources/ dir has no such name — mirrors cmd/workflow/cmd.go's
// showRemoteWorkflowFromLock exactly, one tier below it.
func showRemoteResourceFromLock(name string) {
	repoPath := shared.GetLocalPath()
	lf, err := module.LoadLockFile(repoPath)
	if err != nil {
		log.Fatalf("Failed to get resource: not found locally, and failed to load hyve.lock: %v", err)
	}
	matches := lf.FindLockedResourcesByName(name)
	switch len(matches) {
	case 0:
		log.Fatalf("resource %q not found locally or in hyve.lock — run `hyve resource list` or `hyve resource install`", name)
	case 1:
		full := matches[0].Source
		if matches[0].Version != "" {
			full += "@" + matches[0].Version
		}
		resolved, err := resourceref.Resolve(full, repoPath, lf, "")
		if err != nil {
			log.Fatalf("Failed to resolve resource %q: %v", full, err)
		}
		log.Printf("📋 Resource: %s (git-referenced: %s)\n", name, full)
		fmt.Println(string(resolved.Data))
	default:
		var b strings.Builder
		for _, m := range matches {
			fmt.Fprintf(&b, "  %s@%s\n", m.Source, m.Version)
		}
		log.Fatalf("resource name %q is ambiguous across %d locked sources — run with the full source string instead:\n%s", name, len(matches), b.String())
	}
}

func deleteResource(name string, force bool) {
	if !force {
		log.Printf("Are you sure you want to delete resource '%s'? (y/N): ", name)
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			log.Println("Delete cancelled")
			return
		}
	}

	dest := filepath.Join(getResourcesDir(), name+resource.ResourceFileExt)
	if err := os.Remove(dest); err != nil {
		log.Fatalf("Failed to delete resource: %v", err)
	}
	log.Printf("✅ Deleted resource '%s'", name)
}
