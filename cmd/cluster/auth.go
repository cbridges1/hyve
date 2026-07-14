package cluster

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	"github.com/cbridges1/hyve/internal/kubeconfig"
	mod "github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

var authMethodFlag string

var authCmd = &cobra.Command{
	Use:   "auth [cluster-name]",
	Short: "Configure kubeconfig for a cluster via the module's auth operation",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runClusterAuth(args[0], authMethodFlag)
	},
}

func init() {
	authCmd.Flags().StringVar(&authMethodFlag, "method", "", "auth method name to use (default: first method in auth.yaml)")
}

func runClusterAuth(name string, method string) {
	ctx := context.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)
	repoPath := stateMgr.LocalPath()

	cluster, _, err := stateMgr.LoadClusterDefinition(name)
	if err != nil {
		log.Fatalf("Failed to load cluster '%s': %v", name, err)
	}

	lf, err := mod.LoadLockFile(repoPath)
	if err != nil {
		log.Fatalf("Failed to load hyve.lock: %v", err)
	}
	locked := lf.GetLocked(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	if locked == nil && !mod.IsLocalSource(cluster.Spec.Driver.Source) {
		log.Fatalf("Module %s@%s not in hyve.lock — run `hyve module install`",
			cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	}

	resolved, err := mod.Resolve(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, locked, repoPath)
	if err != nil {
		log.Fatalf("Failed to resolve module: %v", err)
	}

	manifest, _ := mod.LoadManifestForSource(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, repoPath, lf)
	if manifest != nil {
		if reqErr := mod.ValidateToolRequirements(manifest.Spec.Requirements.Tools); reqErr != nil {
			log.Fatalf("%v", reqErr)
		}
	}

	env := moduleEnv(cluster)
	executor := &mod.Executor{ModuleDir: resolved.Dir, Env: env, WorkDir: repoPath, AuthMethod: method}

	if _, err := executor.Execute(ctx, mod.OperationAuth); err != nil {
		log.Fatalf("Auth failed: %v", err)
	}

	if kcPath, pathErr := mod.DefaultKubeconfigPath(); pathErr != nil {
		log.Printf("Warning: could not resolve kubeconfig path: %v", pathErr)
	} else if err := kubeconfig.DeduplicateKubeconfigEntries(kcPath); err != nil {
		log.Printf("Warning: failed to deduplicate kubeconfig: %v", err)
	}

	fmt.Printf("kubectl context for '%s' configured\n", name)
}

func moduleEnv(cluster *types.ClusterDefinition) []string {
	env := []string{
		"HYVE_CLUSTER_NAME=" + cluster.Metadata.Name,
		"HYVE_CLUSTER_REGION=" + cluster.Metadata.Region,
	}
	for k, v := range cluster.Spec.Params {
		env = append(env, "HYVE_PARAM_"+strings.ToUpper(k)+"="+v)
	}
	for k, v := range cluster.Spec.DriverOutputs {
		env = append(env, k+"="+v)
	}
	return env
}
