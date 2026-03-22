package workflow

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestEnvironment creates a test workflow manager
func setupTestEnvironment(t *testing.T) (*Manager, string) {
	t.Helper()
	tmpDir := t.TempDir()
	manager, err := NewManager(tmpDir)
	require.NoError(t, err, "Failed to create workflow manager")
	return manager, tmpDir
}

func TestNewManager(t *testing.T) {
	manager, _ := setupTestEnvironment(t)
	require.NotNil(t, manager)
	assert.NotEmpty(t, manager.workflowsPath)
}

func TestCreateWorkflow(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{
			Name:        "test-workflow",
			Description: "Test workflow",
		},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{Name: "test-step", Command: "echo 'test'"},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(manager.workflowsPath, "test-workflow.yaml"))
	assert.Equal(t, WorkflowAPIVersion, workflow.APIVersion)
	assert.Equal(t, WorkflowKind, workflow.Kind)
	assert.False(t, workflow.Metadata.Created.IsZero(), "Expected Created timestamp to be set")
}

func TestCreateWorkflow_DuplicateName(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: "duplicate-workflow"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{Name: "test-step", Command: "echo 'test'"},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	require.NoError(t, err)

	err = manager.CreateWorkflow(workflow)
	assert.Error(t, err)
}

func TestGetWorkflow(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	original := &Workflow{
		Metadata: WorkflowMetadata{
			Name:        "get-test-workflow",
			Description: "Test description",
		},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{Name: "test-step", Command: "echo 'test'"},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(original)
	require.NoError(t, err)

	retrieved, err := manager.GetWorkflow("get-test-workflow")
	require.NoError(t, err)

	assert.Equal(t, original.Metadata.Name, retrieved.Metadata.Name)
	assert.Equal(t, original.Metadata.Description, retrieved.Metadata.Description)
}

func TestGetWorkflow_NotFound(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	_, err := manager.GetWorkflow("nonexistent-workflow")
	assert.Error(t, err)
}

func TestUpdateWorkflow(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{
			Name:        "update-test-workflow",
			Description: "Original description",
		},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{Name: "test-step", Command: "echo 'test'"},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	require.NoError(t, err)

	originalUpdated := workflow.Metadata.Updated
	time.Sleep(10 * time.Millisecond) // Ensure timestamp difference

	workflow.Metadata.Description = "Updated description"
	err = manager.UpdateWorkflow(workflow)
	require.NoError(t, err)

	updated, err := manager.GetWorkflow("update-test-workflow")
	require.NoError(t, err)

	assert.Equal(t, "Updated description", updated.Metadata.Description)
	assert.True(t, updated.Metadata.Updated.After(originalUpdated), "Expected Updated timestamp to be newer")
}

func TestDeleteWorkflow(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: "delete-test-workflow"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{Name: "test-step", Command: "echo 'test'"},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	require.NoError(t, err)

	err = manager.DeleteWorkflow("delete-test-workflow")
	require.NoError(t, err)

	_, err = manager.GetWorkflow("delete-test-workflow")
	assert.Error(t, err)
}

func TestListWorkflows(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	for _, name := range []string{"workflow-1", "workflow-2", "workflow-3"} {
		wf := &Workflow{
			Metadata: WorkflowMetadata{Name: name},
			Spec: WorkflowSpec{
				Jobs: []WorkflowJob{
					{
						Name: "job-1",
						Steps: []WorkflowStep{
							{Name: "step-1", Command: "echo 'test'"},
						},
					},
				},
			},
		}
		err := manager.CreateWorkflow(wf)
		require.NoError(t, err)
	}

	list, err := manager.ListWorkflows()
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestValidateWorkflow_NoName(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: ""},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{Name: "test-step", Command: "echo 'test'"},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	assert.Error(t, err)
}

func TestValidateWorkflow_NoJobs(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: "no-jobs-workflow"},
		Spec:     WorkflowSpec{Jobs: []WorkflowJob{}},
	}

	err := manager.CreateWorkflow(workflow)
	assert.Error(t, err)
}

func TestValidateWorkflow_InvalidName(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: "invalid@name!"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{Name: "test-step", Command: "echo 'test'"},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	assert.Error(t, err)
}

func TestValidateWorkflow_DuplicateJobNames(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: "duplicate-jobs"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name:  "duplicate-job",
					Steps: []WorkflowStep{{Name: "step-1", Command: "echo 'test'"}},
				},
				{
					Name:  "duplicate-job",
					Steps: []WorkflowStep{{Name: "step-2", Command: "echo 'test'"}},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	assert.Error(t, err)
}

func TestValidateWorkflow_InvalidDependency(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: "invalid-dependency"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name:      "job-1",
					DependsOn: []string{"nonexistent-job"},
					Steps:     []WorkflowStep{{Name: "step-1", Command: "echo 'test'"}},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	assert.Error(t, err)
}

func TestValidateWorkflow_StepWithMultipleExecutionMethods(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: "multiple-execution"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{
							Name:    "invalid-step",
							Command: "echo 'test'",
							Script:  "echo 'test'", // Both command and script
						},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	assert.Error(t, err)
}

func TestValidateWorkflow_StepWithNoExecutionMethod(t *testing.T) {
	manager, _ := setupTestEnvironment(t)

	workflow := &Workflow{
		Metadata: WorkflowMetadata{Name: "no-execution"},
		Spec: WorkflowSpec{
			Jobs: []WorkflowJob{
				{
					Name: "test-job",
					Steps: []WorkflowStep{
						{Name: "invalid-step"},
					},
				},
			},
		},
	}

	err := manager.CreateWorkflow(workflow)
	assert.Error(t, err)
}

func TestIsValidWorkflowName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"Valid simple name", "my-workflow", true},
		{"Valid with numbers", "workflow-123", true},
		{"Valid with underscores", "my_workflow", true},
		{"Valid uppercase", "MY-WORKFLOW", true},
		{"Invalid with spaces", "my workflow", false},
		{"Invalid with special chars", "my@workflow", false},
		{"Invalid with dots", "my.workflow", false},
		{"Empty string", "", false},
		{"Valid long name", "very-long-workflow-name-123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isValidWorkflowName(tt.input))
		})
	}
}

func TestCreateWorkflowTemplate(t *testing.T) {
	workflow := CreateWorkflowTemplate("test-template", "Test template description")
	require.NotNil(t, workflow)

	assert.Equal(t, "test-template", workflow.Metadata.Name)
	assert.Equal(t, "Test template description", workflow.Metadata.Description)
	assert.Equal(t, WorkflowAPIVersion, workflow.APIVersion)
	assert.Equal(t, WorkflowKind, workflow.Kind)
	assert.NotEmpty(t, workflow.Spec.Jobs)
}

func TestGetWorkflowsPath(t *testing.T) {
	manager, tmpDir := setupTestEnvironment(t)

	assert.Equal(t, filepath.Join(tmpDir, WorkflowsDir), manager.GetWorkflowsPath())
}
