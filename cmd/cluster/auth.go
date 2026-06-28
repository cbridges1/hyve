package cluster

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/cmd/shared"
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
	stateMgr, clustersDir := shared.CreateStateManager(ctx)
	repoPath := stateMgr.LocalPath()

	clusterPath := filepath.Join(clustersDir, name+".yaml")
	cluster, err := loadClusterFromFile(clusterPath)
	if err != nil {
		log.Fatalf("Failed to load cluster '%s': %v", name, err)
	}

	lf, err := mod.LoadLockFile(repoPath)
	if err != nil {
		log.Fatalf("Failed to load hyve.lock: %v", err)
	}
	locked := lf.GetLocked(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	if locked == nil {
		log.Fatalf("Module %s@%s not in hyve.lock — run `hyve module install`",
			cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	}

	resolved, err := mod.Resolve(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, locked, repoPath)
	if err != nil {
		log.Fatalf("Failed to resolve module: %v", err)
	}

	env := moduleEnv(cluster)
	executor := &mod.Executor{ModuleDir: resolved.Dir, Env: env, WorkDir: repoPath, AuthMethod: method}

	if _, err := executor.Execute(ctx, mod.OperationAuth); err != nil {
		log.Fatalf("Auth failed: %v", err)
	}

	fmt.Printf("kubectl context for '%s' configured\n", name)
}

func loadClusterFromFile(path string) (*types.ClusterDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c types.ClusterDefinition
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
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
