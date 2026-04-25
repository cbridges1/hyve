package sync

import (
	"testing"

	"github.com/cbridges1/hyve/internal/types"
)

// ── buildClusterDefinition ────────────────────────────────────────────────────

func TestBuildClusterDefinition_Civo(t *testing.T) {
	c := discoveredCluster{name: "my-cluster", provider: "civo", account: "my-org", region: "PHX1"}
	def := buildClusterDefinition(c)

	if def.APIVersion != "v1" {
		t.Errorf("APIVersion: got %q, want %q", def.APIVersion, "v1")
	}
	if def.Kind != "Cluster" {
		t.Errorf("Kind: got %q, want %q", def.Kind, "Cluster")
	}
	if def.Metadata.Name != "my-cluster" {
		t.Errorf("Metadata.Name: got %q, want %q", def.Metadata.Name, "my-cluster")
	}
	if def.Metadata.Region != "PHX1" {
		t.Errorf("Metadata.Region: got %q, want %q", def.Metadata.Region, "PHX1")
	}
	if def.Spec.Provider != "civo" {
		t.Errorf("Spec.Provider: got %q, want %q", def.Spec.Provider, "civo")
	}
	if def.Spec.CivoOrganization != "my-org" {
		t.Errorf("Spec.CivoOrganization: got %q, want %q", def.Spec.CivoOrganization, "my-org")
	}
	assertAccountFieldsEmpty(t, def, "civo")
}

func TestBuildClusterDefinition_AWS(t *testing.T) {
	c := discoveredCluster{name: "prod-cluster", provider: "aws", account: "123456789012", region: "us-east-1"}
	def := buildClusterDefinition(c)

	if def.Spec.Provider != "aws" {
		t.Errorf("Spec.Provider: got %q, want %q", def.Spec.Provider, "aws")
	}
	if def.Spec.AWSAccount != "123456789012" {
		t.Errorf("Spec.AWSAccount: got %q, want %q", def.Spec.AWSAccount, "123456789012")
	}
	if def.Metadata.Name != "prod-cluster" {
		t.Errorf("Metadata.Name: got %q, want %q", def.Metadata.Name, "prod-cluster")
	}
	assertAccountFieldsEmpty(t, def, "aws")
}

func TestBuildClusterDefinition_GCP(t *testing.T) {
	c := discoveredCluster{name: "gcp-cluster", provider: "gcp", account: "my-gcp-project", region: "us-central1"}
	def := buildClusterDefinition(c)

	if def.Spec.Provider != "gcp" {
		t.Errorf("Spec.Provider: got %q, want %q", def.Spec.Provider, "gcp")
	}
	if def.Spec.GCPProject != "my-gcp-project" {
		t.Errorf("Spec.GCPProject: got %q, want %q", def.Spec.GCPProject, "my-gcp-project")
	}
	assertAccountFieldsEmpty(t, def, "gcp")
}

func TestBuildClusterDefinition_Azure(t *testing.T) {
	c := discoveredCluster{name: "aks-cluster", provider: "azure", account: "my-subscription", region: "eastus"}
	def := buildClusterDefinition(c)

	if def.Spec.Provider != "azure" {
		t.Errorf("Spec.Provider: got %q, want %q", def.Spec.Provider, "azure")
	}
	if def.Spec.AzureSubscription != "my-subscription" {
		t.Errorf("Spec.AzureSubscription: got %q, want %q", def.Spec.AzureSubscription, "my-subscription")
	}
	assertAccountFieldsEmpty(t, def, "azure")
}

func TestBuildClusterDefinition_UnknownProvider_NoAccountField(t *testing.T) {
	c := discoveredCluster{name: "other-cluster", provider: "unknown", account: "some-account", region: "us-1"}
	def := buildClusterDefinition(c)

	if def.Spec.Provider != "unknown" {
		t.Errorf("Spec.Provider: got %q, want %q", def.Spec.Provider, "unknown")
	}
	// No provider-specific account field should be set for an unknown provider.
	if def.Spec.CivoOrganization != "" {
		t.Errorf("CivoOrganization should be empty, got %q", def.Spec.CivoOrganization)
	}
	if def.Spec.AWSAccount != "" {
		t.Errorf("AWSAccount should be empty, got %q", def.Spec.AWSAccount)
	}
}

func TestBuildClusterDefinition_RegionPassedThrough(t *testing.T) {
	c := discoveredCluster{name: "test", provider: "civo", account: "", region: "LON1"}
	def := buildClusterDefinition(c)

	if def.Metadata.Region != "LON1" {
		t.Errorf("expected region 'LON1', got %q", def.Metadata.Region)
	}
}

func TestBuildClusterDefinition_EmptyRegion(t *testing.T) {
	c := discoveredCluster{name: "test", provider: "aws", account: "acc", region: ""}
	def := buildClusterDefinition(c)

	if def.Metadata.Region != "" {
		t.Errorf("expected empty region, got %q", def.Metadata.Region)
	}
}

// assertAccountFieldsEmpty checks that provider-specific account fields NOT
// belonging to the current provider are left empty.
func assertAccountFieldsEmpty(t *testing.T, def types.ClusterDefinition, currentProvider string) {
	t.Helper()
	if currentProvider != "civo" && def.Spec.CivoOrganization != "" {
		t.Errorf("CivoOrganization should be empty for provider %q, got %q", currentProvider, def.Spec.CivoOrganization)
	}
	if currentProvider != "aws" && def.Spec.AWSAccount != "" {
		t.Errorf("AWSAccount should be empty for provider %q, got %q", currentProvider, def.Spec.AWSAccount)
	}
	if currentProvider != "gcp" && def.Spec.GCPProject != "" {
		t.Errorf("GCPProject should be empty for provider %q, got %q", currentProvider, def.Spec.GCPProject)
	}
	if currentProvider != "azure" && def.Spec.AzureSubscription != "" {
		t.Errorf("AzureSubscription should be empty for provider %q, got %q", currentProvider, def.Spec.AzureSubscription)
	}
}
