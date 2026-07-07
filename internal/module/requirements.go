package module

import (
	"fmt"
	"os/exec"
	"strings"
)

// ValidateToolRequirements checks that every required tool is present on
// PATH. Deliberately does not enforce ToolRequirement.Version — no module.yaml
// in the wild sets it yet, and duplicating workflow.RequirementValidator's
// semver-ish comparison logic for an unused field isn't worth the complexity.
// Version/Description are still surfaced in the error message for context.
func ValidateToolRequirements(tools []ToolRequirement) error {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t.Name); err != nil {
			msg := fmt.Sprintf("required tool '%s' not found in PATH", t.Name)
			if t.Description != "" {
				msg = fmt.Sprintf("%s (%s)", msg, t.Description)
			}
			missing = append(missing, msg)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("module tool requirements not met:\n  - %s", strings.Join(missing, "\n  - "))
}
