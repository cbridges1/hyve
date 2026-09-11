package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

// resolveClusterKubeconfigPath resolves name (a ClusterDefinition hyve
// already knows about — local-mode or cluster-mode, whichever applies to
// however this process is currently invoked) to a local kubeconfig file
// path — the same primitive `hyve cluster auth <name>` already uses to get
// a working kubeconfig, reused here instead of requiring a migration
// command's caller to separately locate a kubeconfig file by hand. See
// HYVE-MULTI-TENANCY-PLAN.md's "resolve the migration target via an
// existing ClusterDefinition" refinement.
//
// Unlike `hyve cluster auth`, this never merges anything into
// ~/.kube/config — it only needs a file path migrate.BuildClient can read,
// which for local mode is exactly the per-cluster kubeconfig module auth
// already writes (module.KubeconfigPathForCluster), and for cluster mode is
// a fresh temp file this function writes itself (cleanup is the caller's
// responsibility, via the returned cleanup func).
func resolveClusterKubeconfigPath(name string) (path string, cleanup func(), err error) {
	noopCleanup := func() {}

	if sess, ok := shared.UseClusterMode(); ok {
		client := shared.NewAPIClient(sess)
		kc, err := client.GetKubeconfig(name)
		if err != nil {
			return "", noopCleanup, fmt.Errorf("fetch kubeconfig for %q via the API: %w", name, err)
		}
		f, err := os.CreateTemp("", "hyve-migrate-kubeconfig-*.yaml")
		if err != nil {
			return "", noopCleanup, fmt.Errorf("create temp kubeconfig file: %w", err)
		}
		if _, err := f.Write(kc); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", noopCleanup, fmt.Errorf("write temp kubeconfig file: %w", err)
		}
		f.Close()
		return f.Name(), func() { os.Remove(f.Name()) }, nil
	}

	ctx := context.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)
	repoPath := stateMgr.LocalPath()

	cluster, _, err := stateMgr.LoadClusterDefinition(name)
	if err != nil {
		return "", noopCleanup, fmt.Errorf("load cluster %q: %w", name, err)
	}

	lf, err := mod.LoadLockFile(repoPath)
	if err != nil {
		return "", noopCleanup, fmt.Errorf("load hyve.lock: %w", err)
	}
	locked := lf.GetLocked(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version)
	resolved, err := mod.Resolve(cluster.Spec.Driver.Source, cluster.Spec.Driver.Version, locked, repoPath)
	if err != nil {
		return "", noopCleanup, fmt.Errorf("resolve module for %q: %w", name, err)
	}

	env := []string{"HYVE_CLUSTER_NAME=" + cluster.Metadata.Name, "HYVE_CLUSTER_REGION=" + cluster.Metadata.Region}
	for k, v := range cluster.Spec.Params {
		env = append(env, "HYVE_PARAM_"+k+"="+v)
	}
	executor := &mod.Executor{ModuleDir: resolved.Dir, Env: env, WorkDir: repoPath, ClusterName: name}
	result, err := executor.Execute(ctx, mod.OperationAuth)
	if err != nil {
		return "", noopCleanup, fmt.Errorf("auth for %q: %w", name, err)
	}
	kcPath := result.Outputs["KUBECONFIG"]
	if kcPath == "" {
		return "", noopCleanup, fmt.Errorf("auth for %q produced no kubeconfig", name)
	}
	return kcPath, noopCleanup, nil
}

// resolveCurrentHostKubeconfigPath resolves "the current host" for `hyve
// migrate cluster`, which no longer takes an explicit --from — see
// HYVE-MULTI-TENANCY-PLAN.md's "--from dropped entirely" decision.
// "Current host" means whatever `hyve env current` would resolve to:
//
//   - Cluster mode (a `hyve env login` session is active): the ClusterDefinition
//     with access.method: primary on the cluster that session is logged
//     into — there's exactly one per install by convention (see
//     AccessMethodPrimary's own doc comment). That marker no longer routes
//     to a server-minted kubeconfig (GET /api/kubeconfig) — a primary-marked
//     cluster has a real driver module now, and gets its kubeconfig the
//     same client-side way any other default-auth cluster does (GET
//     /api/clusters/<name>/auth-context + running its auth op locally, see
//     resolveViaAuthContext).
//   - Local mode: no access.method: primary concept applies at all (that's
//     cluster-mode-only) — "current host" is simply whatever kubeconfig is
//     already active on this machine, i.e. the default kubeconfig loading
//     rules (empty path — the same as a bare `kubectl` invocation with no
//     --kubeconfig flag, respecting $KUBECONFIG or ~/.kube/config).
func resolveCurrentHostKubeconfigPath() (path string, cleanup func(), err error) {
	noopCleanup := func() {}

	sess, ok := shared.UseClusterMode()
	if !ok {
		return "", noopCleanup, nil // empty path -> migrate.BuildClient's default kubeconfig loading rules
	}

	client := shared.NewAPIClient(sess)
	clusters, err := client.ListClusters()
	if err != nil {
		return "", noopCleanup, fmt.Errorf("list clusters to find the current host: %w", err)
	}
	var hostName string
	for _, c := range clusters {
		if c.AccessMethod == "primary" {
			hostName = c.Name
			break
		}
	}
	if hostName == "" {
		return "", noopCleanup, fmt.Errorf("no ClusterDefinition with access.method: primary found — the current host has no self-registered ClusterDefinition yet (see HYVE-MULTI-TENANCY-PLAN.md's \"Bootstrap and migration flow\")")
	}

	return resolveViaAuthContext(client, hostName)
}

// resolveViaAuthContext runs name's driver module auth operation entirely
// client-side — the same GET /api/clusters/<name>/auth-context + local
// Executor flow cmd/cluster/auth.go's runModuleAuthLocally uses — and
// returns the resulting per-cluster kubeconfig's own file path, without
// merging it into ~/.kube/config (unlike runModuleAuthLocally, this
// package only needs a path migrate.BuildClient can read).
func resolveViaAuthContext(client *shared.APIClient, name string) (path string, cleanup func(), err error) {
	noopCleanup := func() {}

	authCtx, err := client.GetAuthContext(name)
	if err != nil {
		if errors.Is(err, shared.ErrClientSideAuthUnavailable) {
			return "", noopCleanup, fmt.Errorf("cluster %q doesn't use client-side auth — fetch its kubeconfig via `hyve cluster auth %s` first, then re-run this against the resulting kubeconfig by hand", name, name)
		}
		return "", noopCleanup, fmt.Errorf("fetch auth context for %q: %w", name, err)
	}

	tmpDir, err := os.MkdirTemp("", "hyve-migrate-auth-*")
	if err != nil {
		return "", noopCleanup, fmt.Errorf("create temp directory for auth module: %w", err)
	}
	cleanup = func() { os.RemoveAll(tmpDir) }

	if err := os.WriteFile(filepath.Join(tmpDir, authCtx.AuthFileName), []byte(authCtx.AuthFileContent), 0600); err != nil {
		cleanup()
		return "", noopCleanup, fmt.Errorf("write auth module file: %w", err)
	}

	env := []string{"HYVE_CLUSTER_NAME=" + name, "HYVE_CLUSTER_REGION=" + authCtx.Region}
	for k, v := range authCtx.Params {
		env = append(env, "HYVE_PARAM_"+strings.ToUpper(k)+"="+v)
	}
	for k, v := range authCtx.DriverOutputs {
		env = append(env, k+"="+v)
	}
	executor := &mod.Executor{ModuleDir: tmpDir, Env: env, WorkDir: tmpDir, ClusterName: name}

	result, err := executor.Execute(context.Background(), mod.OperationAuth)
	if err != nil {
		cleanup()
		return "", noopCleanup, fmt.Errorf("auth for %q: %w", name, err)
	}
	kcPath := result.Outputs["KUBECONFIG"]
	if kcPath == "" {
		cleanup()
		return "", noopCleanup, fmt.Errorf("auth for %q produced no kubeconfig", name)
	}
	return kcPath, cleanup, nil
}
