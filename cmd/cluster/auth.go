package cluster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
same auth operation client-side too, but with no local module resolution
required at all — GET /api/clusters/<name>/auth-context delivers the
resolved auth operation file's content directly (resolved against the API's
own module cache, not yours), along with driver info
(source/version/params/outputs). No local environment, git checkout, or
'hyve module install' is needed; only your own local credentials/tools
(civo, aws, gcloud, etc.) still apply, since the script runs on your
machine.

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
	executor := &mod.Executor{ModuleDir: resolved.Dir, Env: env, WorkDir: repoPath, ClusterName: name, AuthMethod: method}

	result, err := executor.Execute(ctx, mod.OperationAuth)
	if err != nil {
		log.Fatalf("Auth failed: %v", err)
	}

	mergeAuthResultIntoDefaultKubeconfig(name, result.Outputs["KUBECONFIG"])

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

// runModuleAuthLocally is cluster mode's client-side-default path: runs the
// auth operation file the API's auth-context response already delivered
// (AuthFileContent, resolved server-side against the API's own module
// cache — see internal/api's handleAuthContext) by writing it to a fresh
// temp directory and pointing Executor at that, instead of resolving the
// module from a local hyve.lock. No local environment/git checkout is
// required for this at all — matching how cluster-mode login and local
// directories are otherwise completely independent (see internal/session's
// own doc comment); requiring `hyve module install` here would have been
// exactly the kind of silent re-coupling that split was meant to prevent.
func runModuleAuthLocally(name string, authCtx *shared.AuthContextDTO, method string) {
	ctx := context.Background()

	if len(authCtx.Tools) > 0 {
		tools := make([]mod.ToolRequirement, len(authCtx.Tools))
		for i, t := range authCtx.Tools {
			tools[i] = mod.ToolRequirement{Name: t.Name, Description: t.Description}
		}
		if reqErr := mod.ValidateToolRequirements(tools); reqErr != nil {
			log.Fatalf("%v", reqErr)
		}
	}

	tmpDir, err := os.MkdirTemp("", "hyve-cluster-auth-*")
	if err != nil {
		log.Fatalf("Failed to create temp directory for auth module: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, authCtx.AuthFileName), []byte(authCtx.AuthFileContent), 0600); err != nil {
		log.Fatalf("Failed to write auth module file: %v", err)
	}

	env := authContextEnv(name, authCtx)
	executor := &mod.Executor{ModuleDir: tmpDir, Env: env, WorkDir: tmpDir, ClusterName: name, AuthMethod: method}

	result, err := executor.Execute(ctx, mod.OperationAuth)
	if err != nil {
		log.Fatalf("Auth failed: %v", err)
	}

	mergeAuthResultIntoDefaultKubeconfig(name, result.Outputs["KUBECONFIG"])

	fmt.Printf("kubectl context for '%s' configured (module run locally)\n", name)
}

// mergeAuthResultIntoDefaultKubeconfig reads the per-cluster kubeconfig
// executeAuth just wrote (see module.KubeconfigPathForCluster) and merges
// its cluster/context/user entry into the user's real default kubeconfig
// (~/.kube/config), named after the cluster — this is what actually makes
// "kubectl context for '%s' configured" true; without it, the per-cluster
// file gets written but kubectl (which reads ~/.kube/config by default)
// never sees it. Best-effort no-op when perClusterKcPath is empty — not
// every auth method exports a KUBECONFIG (see ClusterAuth's Exports
// field), and that's not an error.
func mergeAuthResultIntoDefaultKubeconfig(name, perClusterKcPath string) {
	if perClusterKcPath == "" {
		return
	}
	// Dedupe the per-cluster file first — MergeKubeconfigEntry only ever
	// reads its first cluster/context/user entry, so a stale duplicate left
	// over from an earlier `hyve cluster auth` run (if the module's own
	// script appends rather than overwrites) would otherwise silently win
	// over the fresh entry just written.
	if err := kubeconfig.DeduplicateKubeconfigEntries(perClusterKcPath); err != nil {
		log.Printf("Warning: failed to deduplicate %s: %v", perClusterKcPath, err)
	}
	data, err := os.ReadFile(perClusterKcPath)
	if err != nil {
		log.Printf("Warning: failed to read %s: %v", perClusterKcPath, err)
		return
	}
	defaultKcPath, err := mod.DefaultKubeconfigPath()
	if err != nil {
		log.Printf("Warning: could not resolve kubeconfig path: %v", err)
		return
	}
	if err := kubeconfig.MergeKubeconfigEntry(defaultKcPath, data, name); err != nil {
		log.Printf("Warning: failed to merge kubeconfig: %v", err)
	}
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
