package kubeconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// KubeConfigStructure represents the structure of a kubeconfig file
type KubeConfigStructure struct {
	APIVersion     string                   `yaml:"apiVersion"`
	Kind           string                   `yaml:"kind"`
	CurrentContext string                   `yaml:"current-context,omitempty"`
	Clusters       []map[string]interface{} `yaml:"clusters"`
	Contexts       []map[string]interface{} `yaml:"contexts"`
	Users          []map[string]interface{} `yaml:"users"`
}

// RemoveKubeconfigContext removes a context and its associated cluster and user from a kubeconfig file
func RemoveKubeconfigContext(configContent, contextName, configPath string) error {
	var config KubeConfigStructure

	// Parse config
	if err := yaml.Unmarshal([]byte(configContent), &config); err != nil {
		return fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	// Find the context to determine cluster and user names
	var clusterName, userName string
	for _, context := range config.Contexts {
		if name, ok := context["name"].(string); ok && name == contextName {
			if ctx, ok := context["context"].(map[string]interface{}); ok {
				if cluster, ok := ctx["cluster"].(string); ok {
					clusterName = cluster
				}
				if user, ok := ctx["user"].(string); ok {
					userName = user
				}
			}
			break
		}
	}

	// Remove context
	config.Contexts = removeItemByName(config.Contexts, contextName)

	// Remove cluster if found
	if clusterName != "" {
		config.Clusters = removeItemByName(config.Clusters, clusterName)
	}

	// Remove user if found
	if userName != "" {
		config.Users = removeItemByName(config.Users, userName)
	}

	// Clear current-context if it matches the removed context
	if config.CurrentContext == contextName {
		config.CurrentContext = ""
	}

	// Marshal back to YAML
	result, err := yaml.Marshal(&config)
	if err != nil {
		return fmt.Errorf("failed to marshal updated kubeconfig: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, result, 0600); err != nil {
		return fmt.Errorf("failed to write updated kubeconfig: %w", err)
	}

	return nil
}

// removeItemByName removes an item from a slice by its name field
func removeItemByName(items []map[string]interface{}, name string) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if itemName, ok := item["name"].(string); !ok || itemName != name {
			result = append(result, item)
		}
	}
	return result
}

// DeduplicateKubeconfigEntries rewrites configPath so that, for each of
// clusters/contexts/users, only the LAST entry with a given "name" survives
// — the entry an external tool (civo, aws eks update-kubeconfig, k3d, ...)
// just wrote as a side effect of `hyve cluster auth`. Earlier duplicate
// entries sharing that name are dropped. A no-op if the file doesn't exist
// yet, or if nothing was actually duplicated.
func DeduplicateKubeconfigEntries(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read kubeconfig: %w", err)
	}

	var config KubeConfigStructure
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	before := len(config.Clusters) + len(config.Contexts) + len(config.Users)
	config.Clusters = keepLastByName(config.Clusters)
	config.Contexts = keepLastByName(config.Contexts)
	config.Users = keepLastByName(config.Users)
	if len(config.Clusters)+len(config.Contexts)+len(config.Users) == before {
		return nil // nothing was duplicated — skip the rewrite
	}

	result, err := yaml.Marshal(&config)
	if err != nil {
		return fmt.Errorf("failed to marshal deduplicated kubeconfig: %w", err)
	}
	return os.WriteFile(configPath, result, 0600)
}

// keepLastByName keeps, for each unique "name" value, only the entry at the
// last index it appears at — the freshest write — dropping earlier entries
// with the same name. Relative order of surviving entries is preserved.
func keepLastByName(items []map[string]interface{}) []map[string]interface{} {
	lastIdx := make(map[string]int, len(items))
	for i, item := range items {
		if name, ok := item["name"].(string); ok {
			lastIdx[name] = i
		}
	}
	result := make([]map[string]interface{}, 0, len(items))
	for i, item := range items {
		name, ok := item["name"].(string)
		if !ok || lastIdx[name] == i {
			result = append(result, item)
		}
	}
	return result
}

// MergeKubeconfigEntry merges a single-cluster kubeconfig document (as
// returned by GET /api/kubeconfig, e.g. via `hyve cluster auth` in cluster
// mode) into the kubeconfig file at configPath. Its cluster/context/user
// entries are renamed to entryName regardless of what the source called
// them — every minted kubeconfig has exactly one of each, so this sidesteps
// any collision with whatever server-side name was used (e.g.
// PrimaryClusterProvider hardcodes "hyve") and instead keys everything by
// the real cluster name, matching how multiple clusters coexist in one
// local kubeconfig today. Any existing entries already named entryName are
// replaced. Sets current-context to entryName. Creates configPath (and its
// parent directory) if it doesn't exist yet.
func MergeKubeconfigEntry(configPath string, newConfigContent []byte, entryName string) error {
	var incoming KubeConfigStructure
	if err := yaml.Unmarshal(newConfigContent, &incoming); err != nil {
		return fmt.Errorf("failed to parse fetched kubeconfig: %w", err)
	}
	if len(incoming.Clusters) == 0 || len(incoming.Contexts) == 0 || len(incoming.Users) == 0 {
		return fmt.Errorf("fetched kubeconfig has no cluster/context/user entries to merge")
	}

	renamedCluster := renameEntry(incoming.Clusters[0], entryName)
	renamedUser := renameEntry(incoming.Users[0], entryName)
	renamedContext := renameEntry(incoming.Contexts[0], entryName)
	if ctxBody, ok := renamedContext["context"].(map[string]interface{}); ok {
		ctxBody["cluster"] = entryName
		ctxBody["user"] = entryName
	}

	var existing KubeConfigStructure
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("failed to parse existing kubeconfig: %w", err)
		}
	case os.IsNotExist(err):
		existing = KubeConfigStructure{APIVersion: "v1", Kind: "Config"}
	default:
		return fmt.Errorf("failed to read existing kubeconfig: %w", err)
	}

	existing.Clusters = append(removeItemByName(existing.Clusters, entryName), renamedCluster)
	existing.Users = append(removeItemByName(existing.Users, entryName), renamedUser)
	existing.Contexts = append(removeItemByName(existing.Contexts, entryName), renamedContext)
	existing.CurrentContext = entryName

	result, err := yaml.Marshal(&existing)
	if err != nil {
		return fmt.Errorf("failed to marshal merged kubeconfig: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return fmt.Errorf("failed to create kubeconfig directory: %w", err)
	}
	return os.WriteFile(configPath, result, 0600)
}

// renameEntry returns a shallow copy of item with its "name" field replaced
// — the source map isn't mutated in place since it's shared with the parsed
// incoming document.
func renameEntry(item map[string]interface{}, name string) map[string]interface{} {
	out := make(map[string]interface{}, len(item))
	for k, v := range item {
		out[k] = v
	}
	out["name"] = name
	return out
}

// ContextNames returns the name of every context entry in configContent —
// used by `hyve cluster auth sync` to diff against known cluster names.
func ContextNames(configContent string) ([]string, error) {
	var config KubeConfigStructure
	if err := yaml.Unmarshal([]byte(configContent), &config); err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	names := make([]string, 0, len(config.Contexts))
	for _, ctx := range config.Contexts {
		if name, ok := ctx["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names, nil
}
