package provider

import (
	"fmt"
	"os"
	"strings"

	"github.com/cbridges1/hyve/internal/credentials"
	"github.com/cbridges1/hyve/internal/provider/aws"
	"github.com/cbridges1/hyve/internal/provider/azure"
	"github.com/cbridges1/hyve/internal/provider/civo"
	"github.com/cbridges1/hyve/internal/provider/gcp"
)

// Factory creates provider instances
type Factory struct{}

// NewFactory creates a new provider factory
func NewFactory() *Factory {
	return &Factory{}
}

// CreateProvider creates a provider based on the provider name
// For Civo, the apiKey parameter is used directly (or loaded from credentials store).
// For AWS, GCP, and Azure, authentication uses the native CLI credentials:
//   - AWS: Uses AWS CLI credentials (~/.aws/credentials) or environment variables
//   - GCP: Uses gcloud CLI credentials (Application Default Credentials)
//   - Azure: Uses Azure CLI credentials (az login)
func (f *Factory) CreateProvider(providerName, apiKey, region string) (Provider, error) {
	switch strings.ToLower(providerName) {
	case "civo":
		token := apiKey
		if token == "" {
			return nil, fmt.Errorf("Civo API token not found. Set the CIVO_TOKEN environment variable or pass the token directly")
		}
		civoProvider, err := civo.NewProvider(token, region)
		if err != nil {
			return nil, err
		}
		return &ProviderAdapter{civo: civoProvider}, nil

	case "aws":
		// AWS uses native CLI authentication via AWS SDK's default credential chain
		// This automatically checks: environment variables, ~/.aws/credentials, IAM roles, etc.
		// No credentials need to be stored in Hyve - use 'aws configure' to set up
		awsProvider, err := aws.NewProvider("", "", "", region)
		if err != nil {
			return nil, fmt.Errorf("AWS authentication failed. Please run 'aws configure' or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables: %w", err)
		}
		return &ProviderAdapter{aws: awsProvider}, nil

	case "gcp":
		// GCP uses Application Default Credentials (ADC)
		// This automatically checks: GOOGLE_APPLICATION_CREDENTIALS, gcloud auth, metadata server
		// No credentials need to be stored in Hyve - use 'gcloud auth application-default login' to set up
		projectID := os.Getenv("GCP_PROJECT_ID")
		if projectID == "" {
			projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
		}
		if projectID == "" {
			return nil, fmt.Errorf("GCP project ID not found. Please set GCP_PROJECT_ID or GOOGLE_CLOUD_PROJECT environment variable")
		}
		gcpProvider, err := gcp.NewProvider("", projectID, region)
		if err != nil {
			return nil, fmt.Errorf("GCP authentication failed. Please run 'gcloud auth application-default login': %w", err)
		}
		return &ProviderAdapter{gcp: gcpProvider}, nil

	case "azure":
		// Azure uses DefaultAzureCredential which checks:
		// Environment variables, managed identity, Azure CLI, Azure PowerShell, etc.
		// No credentials need to be stored in Hyve - use 'az login' to set up
		subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
		if subscriptionID == "" {
			return nil, fmt.Errorf("Azure subscription ID not found. Please set AZURE_SUBSCRIPTION_ID environment variable")
		}
		resourceGroup := os.Getenv("AZURE_RESOURCE_GROUP")
		if resourceGroup == "" {
			return nil, fmt.Errorf("Azure resource group not found. Please set AZURE_RESOURCE_GROUP environment variable")
		}
		azureProvider, err := azure.NewProvider(subscriptionID, resourceGroup, region, "", "", "")
		if err != nil {
			return nil, fmt.Errorf("Azure authentication failed. Please run 'az login': %w", err)
		}
		return &ProviderAdapter{azure: azureProvider}, nil

	default:
		return nil, fmt.Errorf("unsupported provider: %s. Valid providers are: civo, aws, gcp, azure", providerName)
	}
}

// CreateProviderWithOptions creates a provider using credentials supplied directly in opts.
// Credential values are resolved from the provider config YAML files by the caller
// (via providerconfig.Manager) before this function is invoked. Values may be literal
// strings or the result of ${ENV_VAR} resolution performed by the config manager.
func (f *Factory) CreateProviderWithOptions(providerName string, opts ProviderOptions) (Provider, error) {
	switch strings.ToLower(providerName) {
	case "civo":
		// Token is pre-resolved from provider-configs/civo.yaml (or local DB for local mode).
		token := opts.APIKey
		if token == "" && opts.AccountName != "" {
			credsMgr, err := credentials.NewManager()
			if err == nil {
				defer credsMgr.Close()
				token, _ = credsMgr.GetCivoToken(opts.AccountName)
			}
		}
		if token == "" {
			return nil, fmt.Errorf("Civo API token not found. Set token in provider-configs/civo.yaml or run 'hyve config civo token set --org %s'", opts.AccountName)
		}
		civoProvider, err := civo.NewProvider(token, opts.Region)
		if err != nil {
			return nil, err
		}
		return &ProviderAdapter{civo: civoProvider}, nil

	case "gcp":
		// CredentialsJSON and ProjectID are pre-resolved from provider-configs/gcp.yaml.
		projectID := opts.ProjectID
		if projectID == "" {
			projectID = os.Getenv("GCP_PROJECT_ID")
		}
		if projectID == "" {
			projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
		}
		if opts.Region == "" {
			return nil, fmt.Errorf("GCP cluster requires a region (e.g. us-central1). Set 'region' in the cluster definition")
		}
		gcpProvider, err := gcp.NewProvider(opts.GCPCredentialsJSON, projectID, opts.Region)
		if err != nil {
			return nil, err
		}
		return &ProviderAdapter{gcp: gcpProvider}, nil

	case "aws":
		// Credentials are pre-resolved from provider-configs/aws.yaml.
		// Falls back to AWS SDK default credential chain when fields are empty.
		awsProvider, err := aws.NewProvider(opts.AccessKeyID, opts.SecretAccessKey, opts.SessionToken, opts.Region)
		if err != nil {
			return nil, err
		}
		return &ProviderAdapter{aws: awsProvider}, nil

	case "azure":
		// SubscriptionID and credentials are pre-resolved from provider-configs/azure.yaml.
		subscriptionID := opts.AzureSubscriptionID
		if subscriptionID == "" {
			subscriptionID = os.Getenv("AZURE_SUBSCRIPTION_ID")
		}
		resourceGroup := opts.AzureResourceGroup
		if resourceGroup == "" {
			resourceGroup = os.Getenv("AZURE_RESOURCE_GROUP")
		}
		azureProvider, err := azure.NewProvider(subscriptionID, resourceGroup, opts.Region, opts.AzureTenantID, opts.AzureClientID, opts.AzureClientSecret)
		if err != nil {
			return nil, err
		}
		return &ProviderAdapter{azure: azureProvider}, nil

	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}

// ProviderOptions contains configuration options for creating providers.
// Credential fields are resolved from provider-configs YAML by the caller
// (via providerconfig.Manager.resolveValue) before being passed here.
type ProviderOptions struct {
	// AccountName is the account/project/org alias (used for logging only).
	AccountName string

	// Common
	Region string

	// Civo
	APIKey string // API token (resolved from civo.yaml or local DB)

	// GCP
	ProjectID          string // GCP project ID (resolved from gcp.yaml or env)
	GCPCredentialsJSON string // Service account credentials JSON (resolved from gcp.yaml)

	// AWS
	AccessKeyID     string // Resolved from aws.yaml; falls back to SDK default chain if empty
	SecretAccessKey string
	SessionToken    string

	// Azure
	AzureSubscriptionID string // Resolved from azure.yaml or AZURE_SUBSCRIPTION_ID env
	AzureResourceGroup  string // Resource group for this operation
	AzureTenantID       string // Resolved from azure.yaml
	AzureClientID       string
	AzureClientSecret   string
}

// GetSupportedProviders returns list of supported providers
func (f *Factory) GetSupportedProviders() []string {
	return []string{"civo", "gcp", "aws", "azure"}
}
