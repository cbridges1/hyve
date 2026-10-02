package workflow

import "fmt"

// Validate checks a workflow definition's structure: required fields, job/step
// shape, exactly-one-execution-method-per-step, known step actions and their
// required params, dependency references, and circular dependencies.
func Validate(wf *Workflow) (errors, warnings []string) {
	if wf.APIVersion == "" {
		errors = append(errors, "Missing apiVersion")
	} else if wf.APIVersion != WorkflowAPIVersion {
		warnings = append(warnings, fmt.Sprintf("Unexpected apiVersion '%s', expected '%s'", wf.APIVersion, WorkflowAPIVersion))
	}

	if wf.Kind == "" {
		errors = append(errors, "Missing kind")
	} else if wf.Kind != "Workflow" {
		errors = append(errors, fmt.Sprintf("Invalid kind '%s', expected 'Workflow'", wf.Kind))
	}

	if wf.Metadata.Name == "" {
		errors = append(errors, "Missing metadata.name")
	}

	if len(wf.Spec.Jobs) == 0 {
		errors = append(errors, "No jobs defined in workflow")
	}

	jobNames := make(map[string]bool)
	for i, job := range wf.Spec.Jobs {
		if job.Name == "" {
			errors = append(errors, fmt.Sprintf("Job %d is missing a name", i+1))
			continue
		}

		if jobNames[job.Name] {
			errors = append(errors, fmt.Sprintf("Duplicate job name: %s", job.Name))
		}
		jobNames[job.Name] = true

		if len(job.Steps) == 0 {
			errors = append(errors, fmt.Sprintf("Job '%s' has no steps", job.Name))
		}

		for j, step := range job.Steps {
			if step.Name == "" {
				warnings = append(warnings, fmt.Sprintf("Job '%s', step %d is missing a name", job.Name, j+1))
			}

			hasExecution := step.Command != "" || step.Script != "" || step.Action != ""
			if !hasExecution {
				errors = append(errors, fmt.Sprintf("Job '%s', step '%s' has no command, script, or action", job.Name, step.Name))
			}

			methods := 0
			if step.Command != "" {
				methods++
			}
			if step.Script != "" {
				methods++
			}
			if step.Action != "" {
				methods++
			}
			if methods > 1 {
				errors = append(errors, fmt.Sprintf("Job '%s', step '%s' has multiple execution methods (command/script/action)", job.Name, step.Name))
			}

			if step.Action != "" {
				switch step.Action {
				case "kubectl-apply":
					if step.With == nil || step.With["file"] == "" {
						errors = append(errors, fmt.Sprintf("Job '%s', step '%s': kubectl-apply action requires 'file' parameter", job.Name, step.Name))
					}
				case "kubectl-delete":
					if step.With == nil || step.With["file"] == "" {
						errors = append(errors, fmt.Sprintf("Job '%s', step '%s': kubectl-delete action requires 'file' parameter", job.Name, step.Name))
					}
				default:
					warnings = append(warnings, fmt.Sprintf("Job '%s', step '%s': unknown action '%s'", job.Name, step.Name, step.Action))
				}
			}
		}
	}

	for _, job := range wf.Spec.Jobs {
		for _, dep := range job.DependsOn {
			if !jobNames[dep] {
				errors = append(errors, fmt.Sprintf("Job '%s' depends on non-existent job '%s'", job.Name, dep))
			}
		}
	}

	if hasCircularDependencies(wf.Spec.Jobs) {
		errors = append(errors, "Circular dependency detected in job dependencies")
	}

	if wf.Spec.Runtime == RuntimeClient {
		for _, job := range wf.Spec.Jobs {
			if job.Container != "" {
				warnings = append(warnings, fmt.Sprintf("Job '%s': container has no effect on this runtime: client workflow", job.Name))
			}
			for _, step := range job.Steps {
				if step.Container != "" {
					warnings = append(warnings, fmt.Sprintf("Job '%s', step '%s': container has no effect on this runtime: client workflow", job.Name, step.Name))
				}
			}
		}
	}

	return errors, warnings
}

func hasCircularDependencies(jobs []WorkflowJob) bool {
	graph := make(map[string][]string)
	for _, job := range jobs {
		graph[job.Name] = job.DependsOn
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var hasCycle func(string) bool
	hasCycle = func(jobName string) bool {
		visited[jobName] = true
		recStack[jobName] = true

		for _, dep := range graph[jobName] {
			if !visited[dep] {
				if hasCycle(dep) {
					return true
				}
			} else if recStack[dep] {
				return true
			}
		}

		recStack[jobName] = false
		return false
	}

	for _, job := range jobs {
		if !visited[job.Name] {
			if hasCycle(job.Name) {
				return true
			}
		}
	}

	return false
}
