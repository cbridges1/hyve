package cluster

import (
	"context"
	"errors"
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
	Short: "Configure kubeconfig for a cluster",
	Long: `Local mode (no 'hyve login' session active): runs the driver module's auth
operation directly against the target cloud/cluster and writes the result to
your local kubeconfig, exactly as before.

Cluster mode (a valid 'hyve login' session exists): by default, runs the
exact same client-side flow — the API just supplies the driver info
(source/version/params/outputs) via GET /api/clusters/<name>/auth-context
instead of a git-checked-out clusters/<name>.yaml. Module auth.yaml scripts
are written assuming your own local credentials/tools, so this still needs
the module locally resolvable (run 'hyve module install' first).

If the cluster has opted into server-side auth (spec.access.method:
module-auth on its ClusterDefinition), the module instead runs inside the
API pod and this fetches an already-minted kubeconfig (GET /api/kubeconfig)
and merges it in — --method isn't supported for that path, since the server
always uses the module's default auth method.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runClusterAuth(args[0], authMethodFlag)
	},
}

func init() {
	authCmd.Flags().StringVar(&authMethodFlag, "method", "", "auth method name to use (default: first method in auth.yaml)")
}

func runClusterAuth(name string, method string) {
	if sess, ok := shared.UseClusterMode(); ok {
		authClusterAPI(shared.NewAPIClient(sess), name, method)
		return
	}

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

// authClusterAPI is cluster mode's counterpart to runClusterAuth's local
// flow. Default: fetch driver info via GET /api/clusters/<name>/auth-context
// and run the module client-side, same as local mode — the API never sees
// the resulting credentials. Only for a cluster that's explicitly opted
// into the server-side override (or tunnel access) does this fall back to
// fetching an already-minted kubeconfig and merging it in.
func authClusterAPI(client *shared.APIClient, name string, method string) {
	authCtx, err := client.GetAuthContext(name)
	if err == nil {
		runModuleAuthLocally(name, authCtx, method)
		return
	}
	if !errors.Is(err, shared.ErrClientSideAuthUnavailable) {
		log.Fatalf("Failed to fetch auth context for '%s': %v", name, err)
	}

	if method != "" {
		log.Printf("Warning: --method is ignored — '%s' uses server-side auth, where the API always uses the module's default auth method", name)
	}

	kc, err := client.GetKubeconfig(name)
	if err != nil {
		log.Fatalf("Failed to fetch kubeconfig for '%s': %v", name, err)
	}

	kcPath, err := mod.DefaultKubeconfigPath()
	if err != nil {
		log.Fatalf("Failed to resolve local kubeconfig path: %v", err)
	}
	if err := kubeconfig.MergeKubeconfigEntry(kcPath, kc, name); err != nil {
		log.Fatalf("Failed to merge kubeconfig: %v", err)
	}

	fmt.Printf("kubectl context for '%s' configured (via the API, server-side auth)\n", name)
}

// runModuleAuthLocally is cluster mode's client-side-default path: resolves
// and executes name's driver module locally, exactly like local mode's own
// flow above, except driver source/version/params/driverOutputs come from
// the API's auth-context response instead of a git-checked-out
// clusters/<name>.yaml. The module still needs to be locked locally via
// `hyve module install` regardless of which mode owns the ClusterDefinition
// it's being run for — module resolution/caching has no cluster-mode
// equivalent (yet), so this reuses the same git-backed repoPath local mode
// requires.
func runModuleAuthLocally(name string, authCtx *shared.AuthContextDTO, method string) {
	ctx := context.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)
	repoPath := stateMgr.LocalPath()

	lf, err := mod.LoadLockFile(repoPath)
	if err != nil {
		log.Fatalf("Failed to load hyve.lock: %v", err)
	}
	locked := lf.GetLocked(authCtx.DriverSource, authCtx.DriverVersion)
	if locked == nil && !mod.IsLocalSource(authCtx.DriverSource) {
		log.Fatalf("Module %s@%s not in hyve.lock — run `hyve module install`",
			authCtx.DriverSource, authCtx.DriverVersion)
	}

	resolved, err := mod.Resolve(authCtx.DriverSource, authCtx.DriverVersion, locked, repoPath)
	if err != nil {
		log.Fatalf("Failed to resolve module: %v", err)
	}

	manifest, _ := mod.LoadManifestForSource(authCtx.DriverSource, authCtx.DriverVersion, repoPath, lf)
	if manifest != nil {
		if reqErr := mod.ValidateToolRequirements(manifest.Spec.Requirements.Tools); reqErr != nil {
			log.Fatalf("%v", reqErr)
		}
	}

	env := authContextEnv(name, authCtx)
	executor := &mod.Executor{ModuleDir: resolved.Dir, Env: env, WorkDir: repoPath, AuthMethod: method}

	if _, err := executor.Execute(ctx, mod.OperationAuth); err != nil {
		log.Fatalf("Auth failed: %v", err)
	}

	if kcPath, pathErr := mod.DefaultKubeconfigPath(); pathErr != nil {
		log.Printf("Warning: could not resolve kubeconfig path: %v", pathErr)
	} else if err := kubeconfig.DeduplicateKubeconfigEntries(kcPath); err != nil {
		log.Printf("Warning: failed to deduplicate kubeconfig: %v", err)
	}

	fmt.Printf("kubectl context for '%s' configured (module run locally)\n", name)
}

// authContextEnv mirrors moduleEnv but builds off shared.AuthContextDTO
// (the API's auth-context response) instead of a locally-loaded
// types.ClusterDefinition — same duplication precedent as
// internal/api/access.go's own moduleEnvForClusterDefinition.
func authContextEnv(clusterName string, authCtx *shared.AuthContextDTO) []string {
	env := []string{
		"HYVE_CLUSTER_NAME=" + clusterName,
		"HYVE_CLUSTER_REGION=" + authCtx.Region,
	}
	for k, v := range authCtx.Params {
		env = append(env, "HYVE_PARAM_"+strings.ToUpper(k)+"="+v)
	}
	for k, v := range authCtx.DriverOutputs {
		env = append(env, k+"="+v)
	}
	return env
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
