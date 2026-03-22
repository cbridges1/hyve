package workflow

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	WorkflowsDir       = "workflows"
	WorkflowFileExt    = ".yaml"
	WorkflowAPIVersion = "v1"
	WorkflowKind       = "Workflow"
)

// Manager handles workflow operations
type Manager struct {
	workflowsPath string
	localPath     string
}

// NewManager creates a new workflow manager with the given repository local path
func NewManager(localPath string) (*Manager, error) {
	if localPath == "" {
		return nil, fmt.Errorf("local path is required")
	}

	workflowsPath := filepath.Join(localPath, WorkflowsDir)

	return &Manager{
		workflowsPath: workflowsPath,
		localPath:     localPath,
	}, nil
}

// CreateWorkflow creates a new workflow definition
func (m *Manager) CreateWorkflow(workflow *Workflow) error {
	// Set metadata
	workflow.APIVersion = WorkflowAPIVersion
	workflow.Kind = WorkflowKind
	workflow.Metadata.Created = time.Now()
	workflow.Metadata.Updated = time.Now()

	// Validate workflow
	if err := m.validateWorkflow(workflow); err != nil {
		return fmt.Errorf("workflow validation failed: %w", err)
	}

	// Ensure workflows directory exists
	if err := os.MkdirAll(m.workflowsPath, 0755); err != nil {
		return fmt.Errorf("failed to create workflows directory: %w", err)
	}

	// Write workflow file
	filename := workflow.Metadata.Name + WorkflowFileExt
	filePath := filepath.Join(m.workflowsPath, filename)

	if _, err := os.Stat(filePath); err == nil {
		return fmt.Errorf("workflow '%s' already exists", workflow.Metadata.Name)
	}

	data, err := yaml.Marshal(workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workflow file: %w", err)
	}

	return nil
}

// GetWorkflow retrieves a workflow by name
func (m *Manager) GetWorkflow(name string) (*Workflow, error) {
	filename := name + WorkflowFileExt
	filePath := filepath.Join(m.workflowsPath, filename)

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("workflow '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to read workflow file: %w", err)
	}

	var workflow Workflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return nil, fmt.Errorf("failed to parse workflow file: %w", err)
	}

	return &workflow, nil
}

// UpdateWorkflow updates an existing workflow
func (m *Manager) UpdateWorkflow(workflow *Workflow) error {
	filename := workflow.Metadata.Name + WorkflowFileExt
	filePath := filepath.Join(m.workflowsPath, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("workflow '%s' does not exist", workflow.Metadata.Name)
	}

	// Validate workflow
	if err := m.validateWorkflow(workflow); err != nil {
		return fmt.Errorf("workflow validation failed: %w", err)
	}

	// Update metadata
	workflow.Metadata.Updated = time.Now()

	data, err := yaml.Marshal(workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workflow file: %w", err)
	}

	return nil
}

// DeleteWorkflow deletes a workflow by name
func (m *Manager) DeleteWorkflow(name string) error {
	filename := name + WorkflowFileExt
	filePath := filepath.Join(m.workflowsPath, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("workflow '%s' does not exist", name)
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete workflow file: %w", err)
	}

	return nil
}

// ListWorkflows returns all available workflows
func (m *Manager) ListWorkflows() ([]*Workflow, error) {
	if _, err := os.Stat(m.workflowsPath); os.IsNotExist(err) {
		return []*Workflow{}, nil
	}

	var workflows []*Workflow

	err := filepath.WalkDir(m.workflowsPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(d.Name(), WorkflowFileExt) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read workflow file %s: %w", path, err)
		}

		var workflow Workflow
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			return fmt.Errorf("failed to parse workflow file %s: %w", path, err)
		}

		workflows = append(workflows, &workflow)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list workflows: %w", err)
	}

	return workflows, nil
}

// GetWorkflowsPath returns the path to the workflows directory
func (m *Manager) GetWorkflowsPath() string {
	return m.workflowsPath
}

// validateWorkflow validates a workflow definition
func (m *Manager) validateWorkflow(workflow *Workflow) error {
	if workflow.Metadata.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	// Validate workflow name (alphanumeric and dashes only)
	if !isValidWorkflowName(workflow.Metadata.Name) {
		return fmt.Errorf("workflow name must contain only alphanumeric characters and dashes")
	}

	if len(workflow.Spec.Jobs) == 0 {
		return fmt.Errorf("workflow must have at least one job")
	}

	// Validate jobs
	jobNames := make(map[string]bool)
	for i, job := range workflow.Spec.Jobs {
		if job.Name == "" {
			return fmt.Errorf("job %d: name is required", i)
		}

		if jobNames[job.Name] {
			return fmt.Errorf("duplicate job name: %s", job.Name)
		}
		jobNames[job.Name] = true

		if len(job.Steps) == 0 {
			return fmt.Errorf("job '%s': must have at least one step", job.Name)
		}

		// Validate job dependencies
		for _, dep := range job.DependsOn {
			if !jobNames[dep] && dep != job.Name {
				// This will be validated later as we might not have processed all jobs yet
			}
		}

		// Validate steps
		for j, step := range job.Steps {
			if step.Name == "" {
				return fmt.Errorf("job '%s', step %d: name is required", job.Name, j)
			}

			// Must have either command, script, or action
			if step.Command == "" && step.Script == "" && step.Action == "" {
				return fmt.Errorf("job '%s', step '%s': must specify either command, script, or action", job.Name, step.Name)
			}

			// Cannot have multiple execution methods
			count := 0
			if step.Command != "" {
				count++
			}
			if step.Script != "" {
				count++
			}
			if step.Action != "" {
				count++
			}
			if count > 1 {
				return fmt.Errorf("job '%s', step '%s': can only specify one of command, script, or action", job.Name, step.Name)
			}
		}
	}

	// Validate job dependencies exist
	for _, job := range workflow.Spec.Jobs {
		for _, dep := range job.DependsOn {
			if !jobNames[dep] {
				return fmt.Errorf("job '%s': dependency '%s' does not exist", job.Name, dep)
			}
		}
	}

	return nil
}

// isValidWorkflowName checks if a workflow name is valid
func isValidWorkflowName(name string) bool {
	if name == "" {
		return false
	}

	for _, char := range name {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_') {
			return false
		}
	}

	return true
}

// CreateWorkflowTemplate creates a sample workflow template
func CreateWorkflowTemplate(name, description string) *Workflow {
	return &Workflow{
		APIVersion: WorkflowAPIVersion,
		Kind:       WorkflowKind,
		Metadata: WorkflowMetadata{
			Name:        name,
			Description: description,
		},
		Spec: WorkflowSpec{
			Triggers: []WorkflowTrigger{
				{
					Type: "manual",
				},
			},
			Jobs: []WorkflowJob{
				{
					Name:        "setup",
					Description: "Setup and validation",
					Steps: []WorkflowStep{
						{
							Name:    "check-cluster",
							Command: "kubectl cluster-info",
						},
						{
							Name:    "list-nodes",
							Command: "kubectl get nodes",
						},
					},
				},
				{
					Name:        "deploy",
					Description: "Deploy application",
					DependsOn:   []string{"setup"},
					Steps: []WorkflowStep{
						{
							Name:    "apply-manifests",
							Command: "kubectl apply -f manifests/",
						},
						{
							Name:    "wait-for-rollout",
							Command: "kubectl rollout status deployment/my-app",
						},
					},
				},
			},
		},
	}
}
