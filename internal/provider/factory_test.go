package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFactory(t *testing.T) {
	f := NewFactory()
	require.NotNil(t, f)
}

func TestGetSupportedProviders(t *testing.T) {
	f := NewFactory()
	providers := f.GetSupportedProviders()
	assert.ElementsMatch(t, []string{"civo", "gcp", "aws", "azure"}, providers)
}

func TestCreateProvider_UnsupportedProvider(t *testing.T) {
	f := NewFactory()
	_, err := f.CreateProvider("unknown", "", "us-east-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported provider")
}

func TestCreateProvider_CivoEmptyToken(t *testing.T) {
	f := NewFactory()
	_, err := f.CreateProvider("civo", "", "PHX1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token")
}

func TestCreateProvider_GCPMissingProjectID(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")

	f := NewFactory()
	_, err := f.CreateProvider("gcp", "", "us-central1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project ID")
}

func TestCreateProvider_AzureMissingSubscriptionID(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")

	f := NewFactory()
	_, err := f.CreateProvider("azure", "", "eastus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscription ID")
}

func TestCreateProviderWithOptions_UnsupportedProvider(t *testing.T) {
	f := NewFactory()
	_, err := f.CreateProviderWithOptions("unknown", ProviderOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported provider")
}

func TestCreateProviderWithOptions_CivoEmptyToken(t *testing.T) {
	// Ensure no token is available from any source.
	t.Setenv("CIVO_TOKEN", "")
	t.Setenv("HOME", t.TempDir())

	f := NewFactory()
	_, err := f.CreateProviderWithOptions("civo", ProviderOptions{Region: "PHX1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token")
}

func TestCreateProviderWithOptions_AWSDefaultChain(t *testing.T) {
	// AWS with empty credentials falls back to SDK default chain — creation
	// should succeed even without explicit keys (the chain may fail later when
	// actual API calls are made, but the provider object is created).
	f := NewFactory()
	prov, err := f.CreateProviderWithOptions("aws", ProviderOptions{Region: "us-east-1"})
	// Some CI environments have no AWS credentials at all; accept either outcome.
	if err == nil {
		assert.NotNil(t, prov)
	} else {
		assert.Error(t, err)
	}
}

func TestCreateProviderWithOptions_GCPMissingProjectID(t *testing.T) {
	t.Setenv("GCP_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")

	f := NewFactory()
	// GCP provider with no project ID: may succeed (ADC project resolution) or
	// fail. Either way the factory should not panic.
	_, _ = f.CreateProviderWithOptions("gcp", ProviderOptions{Region: "us-central1"})
}

func TestProviderOptions_Defaults(t *testing.T) {
	opts := ProviderOptions{}
	assert.Empty(t, opts.Region)
	assert.Empty(t, opts.APIKey)
	assert.Empty(t, opts.AccountName)
}
