package kubeconfig

import (
	"fmt"
	"os"

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
