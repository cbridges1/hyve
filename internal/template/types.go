package template

import "github.com/cbridges1/hyve/internal/types"

// TemplateMetadata represents template metadata
type TemplateMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

// TemplateWorkflowsSpec defines workflows to run on cluster lifecycle events
type TemplateWorkflowsSpec struct {
	BeforeCreate []string `yaml:"beforeCreate,omitempty"` // Workflows to run before cluster creation
	OnCreated    []string `yaml:"onCreated,omitempty"`    // Workflows to run after cluster creation
	OnDestroy    []string `yaml:"onDestroy,omitempty"`    // Workflows to run before cluster destruction
	AfterDelete  []string `yaml:"afterDelete,omitempty"`  // Workflows to run after cluster deletion
}

// TemplateSpec represents the template specification.
// Provider-specific account fields are optional in the template; if omitted they
// must be supplied via the corresponding flag when running `template execute`.
// A flag value always overrides the template value when both are present.
type TemplateSpec struct {
	Provider    string            `yaml:"provider"`
	Region      string            `yaml:"region"`
	Nodes       []string          `yaml:"nodes,omitempty"`
	NodeGroups  []types.NodeGroup `yaml:"nodeGroups,omitempty"`
	ClusterType string            `yaml:"clusterType"`
	Ingress     struct {
		Enabled      bool   `yaml:"enabled"`
		LoadBalancer bool   `yaml:"loadBalancer"`
		ChartVersion string `yaml:"chartVersion,omitempty"`
	} `yaml:"ingress"`
	Workflows TemplateWorkflowsSpec `yaml:"workflows,omitempty"`

	// Schedule is a 5-field cron expression (e.g. "0 20 * * 5").
	// At template execution time the next occurrence is calculated and written
	// to the generated cluster's spec.expiresAt field.
	Schedule string `yaml:"schedule,omitempty"`

	// AWS-specific configuration
	AWSAccount      string `yaml:"awsAccount,omitempty"`      // AWS account alias
	AWSVPCID        string `yaml:"awsVpcId,omitempty"`        // AWS VPC ID
	AWSEKSRoleName  string `yaml:"awsEksRoleName,omitempty"`  // IAM role name for EKS control plane
	AWSNodeRoleName string `yaml:"awsNodeRoleName,omitempty"` // IAM role name for EKS node groups

	// Azure-specific configuration (alias names defined in provider-configs/azure.yaml)
	AzureSubscription  string `yaml:"azureSubscription,omitempty"`  // Azure subscription alias
	AzureResourceGroup string `yaml:"azureResourceGroup,omitempty"` // Azure resource group name

	// GCP-specific configuration (alias names defined in provider-configs/gcp.yaml)
	GCPProject string `yaml:"gcpProject,omitempty"` // GCP project alias

	// Civo-specific configuration
	CivoOrganization string `yaml:"civoOrganization,omitempty"` // Civo organization alias
}

// Template represents a complete cluster template definition
type Template struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   TemplateMetadata `yaml:"metadata"`
	Spec       TemplateSpec     `yaml:"spec"`
	// Filename is the on-disk filename (e.g. "my-template.yaml"). It is
	// populated at runtime by the manager and never written to the YAML file.
	Filename string `yaml:"-"`
}
