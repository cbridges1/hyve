package module

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MgmtKubeconfigEnv is set, for every operation, on a module whose
// module.yaml declares requirements.mgmtCluster: the path to a kubeconfig
// for that management cluster — e.g. the Cluster API management cluster a
// CAPI module creates its Cluster objects in. In cluster mode the file is
// relayed into the dispatched Job (see internal/k8sjob's
// inlinedKubeconfigs), so the path is always readable by the op's script.
const MgmtKubeconfigEnv = "HYVE_MGMT_KUBECONFIG"

// MgmtKubeconfigLocator returns a readable local kubeconfig path for the
// named management cluster.
type MgmtKubeconfigLocator func(ctx context.Context, cluster string) (string, error)

// DefaultMgmtKubeconfigLocator uses the kubeconfig the management
// cluster's own auth op last wrote (KubeconfigPathForCluster) — present
// once that cluster has reconciled, or after `hyve cluster auth <name>`.
func DefaultMgmtKubeconfigLocator(_ context.Context, cluster string) (string, error) {
	path, err := KubeconfigPathForCluster(cluster)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("no kubeconfig for management cluster %q yet (expected %s) — run `hyve cluster auth %s`, or let it reconcile first", cluster, path, cluster)
	}
	return path, nil
}

// mgmtClusterRequirement reads requirements.mgmtCluster from moduleDir's
// module.yaml. A missing or unparsable manifest means "none" — the same
// leniency LoadManifestForSource has; reconcile's own pre-flight already
// reports a bad manifest.
func mgmtClusterRequirement(moduleDir string) string {
	data, err := os.ReadFile(filepath.Join(moduleDir, "module.yaml"))
	if err != nil {
		return ""
	}
	var m ModuleManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return ""
	}
	return m.Spec.Requirements.MgmtCluster
}

// envWithMgmtKubeconfig returns e.Env plus MgmtKubeconfigEnv when the
// module requires a management cluster. A HYVE_MGMT_KUBECONFIG path the
// caller already put in e.Env wins.
func (e *Executor) envWithMgmtKubeconfig(ctx context.Context) ([]string, error) {
	mgmt := mgmtClusterRequirement(e.ModuleDir)
	if mgmt == "" {
		return e.Env, nil
	}
	for _, kv := range e.Env {
		if strings.HasPrefix(kv, MgmtKubeconfigEnv+"=") {
			return e.Env, nil
		}
	}
	locate := e.MgmtKubeconfigLocator
	if locate == nil {
		locate = DefaultMgmtKubeconfigLocator
	}
	path, err := locate(ctx, mgmt)
	if err != nil {
		return nil, fmt.Errorf("module requires mgmtCluster %q: %w", mgmt, err)
	}
	return append(append([]string{}, e.Env...), MgmtKubeconfigEnv+"="+path), nil
}
