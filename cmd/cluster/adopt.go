package cluster

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
)

var adoptCmd = &cobra.Command{
	Use:   "adopt [cluster-name]",
	Short: "Adopt an already-existing cluster into hyve, from a template",
	Long: `Writes a cluster definition for a cluster that already exists in the
driver's backing system (e.g. created outside hyve, or before hyve managed
it), without ever running the driver's create operation.

Local mode only — there is no cluster-mode equivalent, since this runs the
driver module's status/describe operations directly against the target
provider rather than going through the controller.

Refuses (writes nothing) unless the driver module reports the cluster as
ACTIVE or FAILED. If the module implements an optional describe operation,
its output is used to verify spec.params against the live cluster before
writing them; otherwise adopt falls back to the template/--set values alone
and prints a warning that they weren't independently verified.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		templateName, _ := cmd.Flags().GetString("template")
		region, _ := cmd.Flags().GetString("region")
		setVals, _ := cmd.Flags().GetStringArray("set")

		if _, ok := shared.UseClusterMode(); ok {
			log.Fatal("hyve cluster adopt is local-mode only — it runs the driver module's status/describe operations directly, which has no cluster-mode equivalent")
		}

		if templateName == "" {
			log.Fatal("--template is required")
		}

		overrides := map[string]string{}
		for _, kv := range setVals {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				log.Fatalf("Invalid --set value %q (expected KEY=VALUE)", kv)
			}
			overrides[parts[0]] = parts[1]
		}

		adoptCluster(templateName, clusterName, region, overrides)
	},
}

func init() {
	adoptCmd.Flags().StringP("template", "t", "", "Template the cluster's driver/schema come from (required)")
	adoptCmd.Flags().StringP("region", "r", "", "Override the template's default region")
	adoptCmd.Flags().StringArray("set", nil, "Override driver params (repeatable): KEY=VALUE — wins over describe output")
}

func adoptCluster(templateName, clusterName, region string, overrides map[string]string) {
	ctx := context.Background()

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}
	repoRoot := currentRepo.LocalPath

	stateMgr := state.NewManagerFromPath(filepath.Join(repoRoot, "clusters"))
	templateMgr := template.NewManager(repoRoot)

	tmpl, err := templateMgr.GetTemplate(templateName)
	if err != nil {
		log.Fatalf("Failed to load template %q: %v", templateName, err)
	}

	if region == "" {
		region = tmpl.Spec.Region
	}

	lf, err := module.LoadLockFile(repoRoot)
	if err != nil {
		log.Fatalf("Failed to load hyve.lock: %v", err)
	}
	locked := lf.GetLocked(tmpl.Spec.Driver.Source, tmpl.Spec.Driver.Version)
	resolved, err := module.Resolve(tmpl.Spec.Driver.Source, tmpl.Spec.Driver.Version, locked, repoRoot)
	if err != nil {
		log.Fatalf("Failed to resolve driver module: %v", err)
	}

	manifest, _ := module.LoadManifestForSource(tmpl.Spec.Driver.Source, tmpl.Spec.Driver.Version, repoRoot, lf)
	if manifest != nil {
		if reqErr := module.ValidateToolRequirements(manifest.Spec.Requirements.Tools); reqErr != nil {
			log.Fatalf("%v", reqErr)
		}
	}

	// No ClusterDefinition exists yet — this env only carries what's needed
	// to ask the driver module about a cluster it already knows about by
	// name/region. Deliberately not internal/reconcile's buildModuleEnv:
	// that also flattens spec.Params/DriverOutputs, neither of which exist
	// yet at this point (discovering them is the whole point of adopt).
	env := []string{
		"HYVE_CLUSTER_NAME=" + clusterName,
		"HYVE_CLUSTER_REGION=" + region,
	}
	exec := &module.Executor{
		ModuleDir:   resolved.Dir,
		Env:         env,
		WorkDir:     repoRoot,
		ClusterName: clusterName,
	}

	log.Printf("🔍 Checking status of '%s' via %s...\n", clusterName, tmpl.Spec.Driver.Source)
	statusResult, err := exec.Execute(ctx, module.OperationStatus)
	if err != nil {
		log.Fatalf("Status check failed: %v", err)
	}
	status := statusResult.Outputs["HYVE_CLUSTER_STATUS"]
	if status != "ACTIVE" && status != "FAILED" {
		log.Fatalf("Cannot adopt '%s': driver reports status %q (want ACTIVE or FAILED) — use 'hyve cluster create' instead if it doesn't exist yet", clusterName, status)
	}
	log.Printf("  status: %s", status)

	describeOutputs := map[string]string{}
	describeResult, err := exec.Execute(ctx, module.OperationDescribe)
	if err != nil {
		log.Fatalf("Describe operation failed: %v", err)
	}
	for k, v := range describeResult.Outputs {
		describeOutputs[k] = v
	}

	describeParams, driverOutputs := splitDescribeOutputs(describeOutputs)

	if len(describeParams) == 0 {
		log.Println("⚠️  Warning: driver module has no describe operation (or it produced no output) — adopted params come from the template/--set only and were not independently verified against the live cluster")
	} else {
		log.Println("📋 Verified params from live cluster:")
		for k, v := range describeParams {
			log.Printf("    %s=%s", k, v)
		}
	}

	// Precedence: template default (applied inside GenerateClusterDefinition
	// itself) < describe output < explicit --set.
	renderOverrides := mergeAdoptOverrides(describeParams, overrides)

	clusterDef := tmpl.GenerateClusterDefinition(clusterName, region, renderOverrides)

	if clusterDef.Spec.DriverOutputs == nil {
		clusterDef.Spec.DriverOutputs = make(map[string]string)
	}
	for k, v := range driverOutputs {
		clusterDef.Spec.DriverOutputs[k] = v
	}
	clusterDef.Spec.DriverOutputs["HYVE_LAST_PARAMS_HASH"] = shared.ParamsHash(clusterDef.Spec.Params)

	log.Println("📋 Template Details:")
	log.Printf("  Driver: %s@%s", tmpl.Spec.Driver.Source, tmpl.Spec.Driver.Version)
	log.Printf("  Region: %s", clusterDef.Metadata.Region)
	if len(clusterDef.Spec.Params) > 0 {
		log.Println("  Params:")
		for k, v := range clusterDef.Spec.Params {
			log.Printf("    %s=%s", k, v)
		}
	}

	if err := stateMgr.SaveClusterDefinition(&clusterDef); err != nil {
		log.Fatalf("Failed to write cluster definition: %v", err)
	}

	log.Printf("\n✅ Cluster definition adopted: %s", filepath.Join(repoRoot, "clusters", clusterName+".yaml"))

	shared.CommitStateChanges(ctx, stateMgr, fmt.Sprintf("Adopt cluster %s from template %s", clusterName, templateName))

	log.Println("\n1️⃣ Reconciling cluster...")
	shared.RunReconciliation("", false)
	log.Printf("\n✅ Cluster adoption completed for '%s'", clusterName)
}

// splitDescribeOutputs separates a describe operation's raw HYVE_KEY=value
// outputs into template params (HYVE_PARAM_<KEY>, stripped and lowercased
// back to the key casing GenerateClusterDefinition's overrides map expects)
// and everything else, which is folded into DriverOutputs verbatim like
// createCluster does for a create operation's outputs.
func splitDescribeOutputs(outputs map[string]string) (params map[string]string, driverOutputs map[string]string) {
	params = map[string]string{}
	driverOutputs = map[string]string{}
	for k, v := range outputs {
		if rest, ok := strings.CutPrefix(k, "HYVE_PARAM_"); ok {
			params[strings.ToLower(rest)] = v
		} else {
			driverOutputs[k] = v
		}
	}
	return params, driverOutputs
}

// mergeAdoptOverrides layers describe output beneath explicit --set values
// into the single overrides map GenerateClusterDefinition takes — template
// defaults are already the lowest tier, applied inside that function itself.
func mergeAdoptOverrides(describeParams, explicitSet map[string]string) map[string]string {
	merged := make(map[string]string, len(describeParams)+len(explicitSet))
	for k, v := range describeParams {
		merged[k] = v
	}
	for k, v := range explicitSet {
		merged[k] = v
	}
	return merged
}
