package provider

import (
	"context"

	"github.com/cbridges1/hyve/internal/provider/aws"
	"github.com/cbridges1/hyve/internal/provider/azure"
	"github.com/cbridges1/hyve/internal/provider/civo"
	"github.com/cbridges1/hyve/internal/provider/gcp"
)

// ProviderAdapter adapts provider implementations to the generic provider interface
type ProviderAdapter struct {
	civo  *civo.Provider
	gcp   *gcp.Provider
	aws   *aws.Provider
	azure *azure.Provider
}

// Name returns the provider name
func (a *ProviderAdapter) Name() string {
	if a.aws != nil {
		return a.aws.Name()
	}
	if a.azure != nil {
		return a.azure.Name()
	}
	if a.gcp != nil {
		return a.gcp.Name()
	}
	return a.civo.Name()
}

// Region returns the provider region
func (a *ProviderAdapter) Region() string {
	if a.aws != nil {
		return a.aws.Region()
	}
	if a.azure != nil {
		return a.azure.Region()
	}
	if a.gcp != nil {
		return a.gcp.Region()
	}
	return a.civo.Region()
}

// EnsureAccessEntry implements AccessEntryGranter for AWS clusters.
// Returns nil for non-AWS providers (no-op).
func (a *ProviderAdapter) EnsureAccessEntry(ctx context.Context, clusterName, principalARN string) error {
	if a.aws != nil {
		return a.aws.EnsureAccessEntry(ctx, clusterName, principalARN)
	}
	return nil
}
