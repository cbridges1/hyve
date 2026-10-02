package reconcile

import "github.com/cbridges1/hyve/internal/types"

// resolveClusterRef finds the cluster a reference from `from` names — a
// dependsOn entry, or a module's requirements.mgmtCluster. An exact
// metadata.name match wins. Otherwise, when `from` belongs to a cluster-mode
// environment, the reference is read as a short name in that same
// environment: real ClusterDefinition names are "<environment>-<short
// name>" (internal/api's joinEnvironmentName), so "unraid-k3s" from a
// cluster in environment "default" means "default-unraid-k3s". Local mode
// has no environments, so only the exact match applies there.
func resolveClusterRef(defs []types.ClusterDefinition, from types.ClusterDefinition, ref string) (types.ClusterDefinition, bool) {
	for _, d := range defs {
		if d.Metadata.Name == ref {
			return d, true
		}
	}
	if env := from.Metadata.Environment; env != "" {
		qualified := env + "-" + ref
		for _, d := range defs {
			if d.Metadata.Name == qualified {
				return d, true
			}
		}
	}
	return types.ClusterDefinition{}, false
}
