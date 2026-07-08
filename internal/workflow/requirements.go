package workflow

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// RequirementValidator validates workflow requirements
type RequirementValidator struct{}

// NewRequirementValidator creates a new requirement validator
func NewRequirementValidator() (*RequirementValidator, error) {
	return &RequirementValidator{}, nil
}

// Close is a no-op retained for API compatibility.
func (v *RequirementValidator) Close() error {
	return nil
}

// ValidateRequirements validates all workflow requirements
func (v *RequirementValidator) ValidateRequirements(requirements *WorkflowRequirements) error {
	if requirements == nil {
		return nil // No requirements to validate
	}

	var errors []string

	// Validate tool requirements
	for _, tool := range requirements.Tools {
		if err := v.validateTool(tool); err != nil {
			errors = append(errors, err.Error())
		}
	}

	// Validate secret requirements
	for _, secret := range requirements.Secrets {
		if err := v.validateSecret(secret); err != nil {
			errors = append(errors, err.Error())
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("requirement validation failed:\n  - %s", strings.Join(errors, "\n  - "))
	}

	return nil
}

// validateTool checks if a required tool is available
func (v *RequirementValidator) validateTool(tool ToolRequirement) error {
	// Check if tool is in PATH
	path, err := exec.LookPath(tool.Name)
	if err != nil {
		msg := fmt.Sprintf("Required tool '%s' not found in PATH", tool.Name)
		if tool.Description != "" {
			msg = fmt.Sprintf("%s (%s)", msg, tool.Description)
		}
		return fmt.Errorf("%s", msg)
	}

	// If version requirement specified, check version
	if tool.Version != "" {
		actualVersion, err := v.getToolVersion(tool.Name, path)
		if err != nil {
			return fmt.Errorf("failed to get version for '%s': %w", tool.Name, err)
		}

		if !v.versionSatisfies(actualVersion, tool.Version) {
			return fmt.Errorf("tool '%s' version mismatch: found %s, requires %s", tool.Name, actualVersion, tool.Version)
		}
	}

	return nil
}

// validateSecret checks if a required secret is available
func (v *RequirementValidator) validateSecret(secret SecretRequirement) error {
	// First check environment variable
	if value := os.Getenv(secret.Name); value != "" {
		return nil // Secret available in environment
	}

	// The legacy provider-specific credential lookup was removed when the
	// SDK-based providers were replaced by user-supplied modules. Secrets
	// should now be provided through environment variables (set externally
	// or via the module's auth operation).

	// Secret not found
	if secret.Required {
		msg := fmt.Sprintf("Required secret '%s' not found", secret.Name)
		if secret.Description != "" {
			msg = fmt.Sprintf("%s (%s)", msg, secret.Description)
		}

		// Add helpful suggestions based on provider
		suggestions := []string{}
		switch secret.Provider {
		case "civo":
			suggestions = append(suggestions, "civo apikey")
		case "aws":
			suggestions = append(suggestions, "aws configure")
		case "gcp":
			suggestions = append(suggestions, "gcloud auth application-default login")
		case "azure":
			suggestions = append(suggestions, "az login")
		}
		suggestions = append(suggestions, fmt.Sprintf("export %s=your-secret", secret.Name))

		msg = fmt.Sprintf("%s\n    Set via: %s", msg, strings.Join(suggestions, " OR "))
		return fmt.Errorf("%s", msg)
	}

	return nil // Non-required secret, not found but not an error
}

// getToolVersion attempts to get the version of a tool
func (v *RequirementValidator) getToolVersion(toolName, toolPath string) (string, error) {
	// Common version flags to try
	versionFlags := []string{"--version", "-v", "version"}

	for _, flag := range versionFlags {
		cmd := exec.Command(toolPath, flag)
		output, err := cmd.CombinedOutput()
		if err != nil {
			continue // Try next flag
		}

		// Extract version number from output
		version := v.extractVersion(string(output))
		if version != "" {
			return version, nil
		}
	}

	return "", fmt.Errorf("could not determine version")
}

// extractVersion extracts version number from command output
func (v *RequirementValidator) extractVersion(output string) string {
	// Common version patterns: X.Y.Z, vX.Y.Z
	versionPattern := regexp.MustCompile(`v?(\d+\.\d+(?:\.\d+)?)`)
	matches := versionPattern.FindStringSubmatch(output)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// versionSatisfies checks if actual version meets the requirement
func (v *RequirementValidator) versionSatisfies(actual, required string) bool {
	// Simple version comparison (can be enhanced with proper semver)
	// For now, just check if versions match or actual is newer

	// Remove 'v' prefix if present
	actual = strings.TrimPrefix(actual, "v")
	required = strings.TrimPrefix(required, "v")

	// Split versions into parts
	actualParts := strings.Split(actual, ".")
	requiredParts := strings.Split(required, ".")

	// Compare each part
	for i := 0; i < len(requiredParts) && i < len(actualParts); i++ {
		if actualParts[i] > requiredParts[i] {
			return true // Actual is newer
		}
		if actualParts[i] < requiredParts[i] {
			return false // Actual is older
		}
	}

	// If we get here, versions are equal up to the shortest length
	return len(actualParts) >= len(requiredParts)
}

// LoadSecretsIntoEnvironment loads required secrets into environment variables
func (v *RequirementValidator) LoadSecretsIntoEnvironment(requirements *WorkflowRequirements) error {
	if requirements == nil || len(requirements.Secrets) == 0 {
		return nil
	}

	// Secrets are now expected to be in the environment when the workflow
	// runs (set by the caller, by `--set KEY=VALUE`, or by the module's
	// auth operation). This call is a no-op kept for API stability.
	_ = requirements
	return nil
}
