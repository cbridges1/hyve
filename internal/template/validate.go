package template

import (
	"fmt"

	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"
)

// Validate checks a template's structure (required fields) and, for any
// local-name workflow refs in onCreate/afterCreate/onDelete, that they exist
// in the repository's workflows/ directory. Remote refs can't be validated
// without a network fetch — that's out of scope here, matching the existing
// `hyve template validate` behavior.
func Validate(repoPath string, tmpl *Template) (errors, warnings []string) {
	if tmpl.APIVersion == "" {
		errors = append(errors, "Missing apiVersion")
	}
	if tmpl.Kind != "Template" {
		errors = append(errors, fmt.Sprintf("Invalid kind '%s', expected 'Template'", tmpl.Kind))
	}
	if tmpl.Metadata.Name == "" {
		errors = append(errors, "Missing metadata.name")
	}
	if tmpl.Spec.Driver.Source == "" {
		errors = append(errors, "Missing spec.driver.source")
	}
	if tmpl.Spec.Driver.Version == "" {
		warnings = append(warnings, "Missing spec.driver.version (will default to 'latest')")
	}

	hasLocalWorkflowRefs := false
	for _, ref := range append(append(append([]types.WorkflowRef{}, tmpl.Spec.Workflows.OnCreate...), tmpl.Spec.Workflows.AfterCreate...), tmpl.Spec.Workflows.OnDelete...) {
		if !ref.IsRemote() {
			hasLocalWorkflowRefs = true
			break
		}
	}
	if hasLocalWorkflowRefs {
		workflowMgr, err := workflow.NewManager(repoPath)
		if err == nil {
			availableWorkflows, err := workflowMgr.ListWorkflows()
			if err == nil {
				workflowMap := make(map[string]bool)
				for _, wf := range availableWorkflows {
					workflowMap[wf.Metadata.Name] = true
				}
				for _, ref := range tmpl.Spec.Workflows.OnCreate {
					if !ref.IsRemote() && !workflowMap[ref.Name] {
						warnings = append(warnings, fmt.Sprintf("OnCreate workflow '%s' not found in repository", ref.Name))
					}
				}
				for _, ref := range tmpl.Spec.Workflows.AfterCreate {
					if !ref.IsRemote() && !workflowMap[ref.Name] {
						warnings = append(warnings, fmt.Sprintf("AfterCreate workflow '%s' not found in repository", ref.Name))
					}
				}
				for _, ref := range tmpl.Spec.Workflows.OnDelete {
					if !ref.IsRemote() && !workflowMap[ref.Name] {
						warnings = append(warnings, fmt.Sprintf("OnDelete workflow '%s' not found in repository", ref.Name))
					}
				}
			}
		}
	}

	return errors, warnings
}
