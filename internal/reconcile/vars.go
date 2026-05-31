package reconcile

import (
	"strings"

	"github.com/cbridges1/hyve/internal/types"
)

// buildModuleEnv constructs the HYVE_* environment variables passed to a module
// operation. Cluster metadata is exposed verbatim, params are flattened into
// HYVE_PARAM_<KEY>=<value>, and previously-captured driver outputs are passed
// through unchanged. `extra` always wins.
func buildModuleEnv(cluster types.ClusterDefinition, extra map[string]string) []string {
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
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
