package workflow

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractVersion(t *testing.T) {
	validator := &RequirementValidator{}

	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{"kubectl version", "kubectl version v1.28.0", "1.28.0"},
		{"helm version", `version.BuildInfo{Version:"v3.12.0", GitCommit:"", GitTreeState:""}`, "3.12.0"},
		{"docker version", "Docker version 20.10.21, build baeda1f", "20.10.21"},
		{"version without v prefix", "Version: 2.8.1", "2.8.1"},
		{"no version found", "Some random output", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, validator.extractVersion(tt.output))
		})
	}
}

func TestVersionSatisfies(t *testing.T) {
	validator := &RequirementValidator{}

	tests := []struct {
		name     string
		actual   string
		required string
		expected bool
	}{
		{"exact match", "1.28.0", "1.28.0", true},
		{"newer version", "1.29.0", "1.28.0", true},
		{"older version", "1.27.0", "1.28.0", false},
		{"with v prefix", "v1.28.0", "v1.28.0", true},
		{"mixed v prefix", "v1.28.0", "1.28.0", true},
		{"major version newer", "2.0.0", "1.28.0", true},
		{"minor version newer", "1.29.0", "1.28.5", true},
		{"patch version newer", "1.28.5", "1.28.0", true},
		{"two-part version", "3.12", "3.12", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, validator.versionSatisfies(tt.actual, tt.required))
		})
	}
}

func TestValidateSecret_EnvironmentVariable(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	secretName := "TEST_SECRET_VAR"
	os.Setenv(secretName, "test-value")
	defer os.Unsetenv(secretName)

	err = validator.validateSecret(SecretRequirement{Name: secretName, Required: true})
	assert.NoError(t, err)
}

func TestValidateSecret_NotFoundRequired(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.validateSecret(SecretRequirement{
		Name:        "NONEXISTENT_SECRET",
		Provider:    "nonexistent-provider",
		Required:    true,
		Description: "Test secret",
	})
	assert.Error(t, err)
}

func TestValidateSecret_NotFoundOptional(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.validateSecret(SecretRequirement{
		Name:        "OPTIONAL_SECRET",
		Provider:    "optional-provider",
		Required:    false,
		Description: "Optional secret",
	})
	assert.NoError(t, err)
}

func TestValidateRequirements_NoRequirements(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	assert.NoError(t, validator.ValidateRequirements(nil))
	assert.NoError(t, validator.ValidateRequirements(&WorkflowRequirements{}))
}

func TestValidateRequirements_ToolNotFound(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.ValidateRequirements(&WorkflowRequirements{
		Tools: []ToolRequirement{
			{Name: "nonexistent-tool-xyz123", Description: "This tool does not exist"},
		},
	})
	assert.Error(t, err)
}

func TestValidateRequirements_ToolFound(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	// Use a tool that should be available on most systems
	err = validator.ValidateRequirements(&WorkflowRequirements{
		Tools: []ToolRequirement{
			{Name: "go", Description: "Go programming language"},
		},
	})
	assert.NoError(t, err)
}

func TestLoadSecretsIntoEnvironment(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	existingSecret := "EXISTING_SECRET"
	os.Setenv(existingSecret, "existing-value")
	defer os.Unsetenv(existingSecret)

	err = validator.LoadSecretsIntoEnvironment(&WorkflowRequirements{
		Secrets: []SecretRequirement{{Name: existingSecret, Required: false}},
	})
	require.NoError(t, err)
	assert.Equal(t, "existing-value", os.Getenv(existingSecret))
}

func TestLoadSecretsIntoEnvironment_NoRequirements(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	assert.NoError(t, validator.LoadSecretsIntoEnvironment(nil))
	assert.NoError(t, validator.LoadSecretsIntoEnvironment(&WorkflowRequirements{}))
}

func TestNewRequirementValidator(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	assert.NotNil(t, validator.credsMgr)
}

func TestRequirementValidator_Close(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)

	assert.NoError(t, validator.Close())

	// Test closing with nil credsMgr
	validator2 := &RequirementValidator{credsMgr: nil}
	assert.NoError(t, validator2.Close())
}

func TestValidateSecret_CivoProvider_Required_NoEnvVar(t *testing.T) {
	// getCivoOrgName() returns "" so the DB path is skipped.
	// A required Civo secret absent from the environment must fail.
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.validateSecret(SecretRequirement{
		Name:     "CIVO_TOKEN",
		Provider: "civo",
		Required: true,
	})
	assert.Error(t, err)
}

func TestValidateSecret_CivoProvider_Optional_NoEnvVar(t *testing.T) {
	// Optional Civo secret absent from env should pass gracefully.
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.validateSecret(SecretRequirement{
		Name:     "CIVO_TOKEN",
		Provider: "civo",
		Required: false,
	})
	assert.NoError(t, err)
}

func TestValidateSecret_CivoProvider_PresentInEnv(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	os.Setenv("CIVO_TOKEN", "test-token")
	defer os.Unsetenv("CIVO_TOKEN")

	err = validator.validateSecret(SecretRequirement{
		Name:     "CIVO_TOKEN",
		Provider: "civo",
		Required: true,
	})
	assert.NoError(t, err)
}

func TestValidateSecret_AWSProvider_AlwaysPass(t *testing.T) {
	// AWS uses native CLI auth — secret validation is skipped regardless.
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.validateSecret(SecretRequirement{
		Name:     "AWS_SECRET_ACCESS_KEY",
		Provider: "aws",
		Required: true,
	})
	assert.NoError(t, err)
}

func TestValidateSecret_AzureProvider_AlwaysPass(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.validateSecret(SecretRequirement{
		Name:     "AZURE_CLIENT_SECRET",
		Provider: "azure",
		Required: true,
	})
	assert.NoError(t, err)
}

func TestValidateSecret_GCPProvider_AlwaysPass(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.validateSecret(SecretRequirement{
		Name:     "GOOGLE_APPLICATION_CREDENTIALS",
		Provider: "gcp",
		Required: true,
	})
	assert.NoError(t, err)
}

func TestValidateRequirements_MultipleErrors(t *testing.T) {
	validator, err := NewRequirementValidator()
	require.NoError(t, err)
	defer validator.Close()

	err = validator.ValidateRequirements(&WorkflowRequirements{
		Tools: []ToolRequirement{
			{Name: "nonexistent-tool-1", Description: "First missing tool"},
			{Name: "nonexistent-tool-2", Description: "Second missing tool"},
		},
		Secrets: []SecretRequirement{
			{Name: "MISSING_SECRET_1", Provider: "provider1", Required: true, Description: "First missing secret"},
		},
	})
	require.Error(t, err)

	assert.ErrorContains(t, err, "nonexistent-tool-1")
	assert.ErrorContains(t, err, "nonexistent-tool-2")
	assert.ErrorContains(t, err, "MISSING_SECRET_1")
}
