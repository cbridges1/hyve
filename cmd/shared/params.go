package shared

import (
	"strings"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

// JoinWorkflowRefs renders a []types.WorkflowRef list for display —
// WorkflowRef.String() handles both local names and remote sources.
func JoinWorkflowRefs(refs []types.WorkflowRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = r.String()
	}
	return strings.Join(parts, ", ")
}

// LoadManifest loads the module.yaml for the given driver source/version.
// Returns nil if the manifest is not available locally.
func LoadManifest(source, version string) *module.ModuleManifest {
	repoRoot := GetRepoPath()
	lf, _ := module.LoadLockFile(repoRoot)
	m, _ := module.LoadManifestForSource(source, version, repoRoot, lf)
	return m
}

// ParseParamOverrides splits a "key=value,key2=value2" string into a map.
func ParseParamOverrides(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return out
}
