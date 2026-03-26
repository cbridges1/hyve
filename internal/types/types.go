package types

// NodeGroupTaint represents a Kubernetes node taint
type NodeGroupTaint struct {
	Key    string `yaml:"key"`
	Value  string `yaml:"value"`
	Effect string `yaml:"effect"` // NoSchedule, PreferNoSchedule, or NoExecute
}

// NodeGroup represents a named group of nodes with shared configuration
type NodeGroup struct {
	Name         string            `yaml:"name"`
	InstanceType string            `yaml:"instanceType"`
	Count        int               `yaml:"count"`
	MinCount     int               `yaml:"minCount,omitempty"`
	MaxCount     int               `yaml:"maxCount,omitempty"`
	DiskSize     int               `yaml:"diskSize,omitempty"` // Disk size in GB
	Labels       map[string]string `yaml:"labels,omitempty"`
	Taints       []NodeGroupTaint  `yaml:"taints,omitempty"`
	Mode         string            `yaml:"mode,omitempty"` // Azure: System or User
	Spot         bool              `yaml:"spot,omitempty"`
}

// IngressSpec represents nginx ingress controller configuration
type IngressSpec struct {
	Enabled      bool   `yaml:"enabled"`
	LoadBalancer bool   `yaml:"loadBalancer"`
	ChartVersion string `yaml:"chartVersion,omitempty"` // Specific helm chart version to install
}

// WorkflowsSpec defines workflows to run on cluster lifecycle events
type WorkflowsSpec struct {
	OnCreated []string `yaml:"onCreated,omitempty"` // Workflows to run after cluster creation
	OnDestroy []string `yaml:"onDestroy,omitempty"` // Workflows to run before cluster destruction
}

// ClusterSpec represents the desired cluster configuration
type ClusterSpec struct {
	Provider    string        `yaml:"provider"`
	Nodes       []string      `yaml:"nodes,omitempty"`
	NodeGroups  []NodeGroup   `yaml:"nodeGroups,omitempty"`
	ClusterType string        `yaml:"clusterType"`
	Ingress     IngressSpec   `yaml:"ingress"`
	Workflows   WorkflowsSpec `yaml:"workflows,omitempty"`
	// Delete marks this cluster for deletion. When true, the reconciler runs any
	// onDestroy workflows, deletes the cluster from the cloud provider, and removes
	// this YAML file from the repository. Do not delete the file directly if you
	// need onDestroy workflows to run — set this flag instead.
	Delete bool `yaml:"delete,omitempty"`

	// GCP-specific configuration
	GCPProject   string `yaml:"gcpProject,omitempty"`   // GCP project name alias
	GCPProjectID string `yaml:"gcpProjectId,omitempty"` // GCP project ID (resolved from alias)

	// AWS-specific configuration
	AWSAccount     string `yaml:"awsAccount,omitempty"`     // AWS account name alias
	AWSAccountID   string `yaml:"awsAccountId,omitempty"`   // AWS account ID (resolved from alias)
	AWSVPCName     string `yaml:"awsVpcName,omitempty"`     // AWS VPC name alias
	AWSVPCID       string `yaml:"awsVpcId,omitempty"`       // AWS VPC ID (resolved from alias)
	AWSEKSRole     string `yaml:"awsEksRole,omitempty"`     // AWS EKS role name alias
	AWSEKSRoleARN  string `yaml:"awsEksRoleArn,omitempty"`  // AWS EKS role ARN (resolved from alias)
	AWSNodeRole    string `yaml:"awsNodeRole,omitempty"`    // AWS EKS node role name alias
	AWSNodeRoleARN string `yaml:"awsNodeRoleArn,omitempty"` // AWS EKS node role ARN (resolved from alias)

	// Azure-specific configuration
	AzureSubscription   string `yaml:"azureSubscription,omitempty"`   // Azure subscription name alias
	AzureSubscriptionID string `yaml:"azureSubscriptionId,omitempty"` // Azure subscription ID (resolved from alias)
	AzureResourceGroup  string `yaml:"azureResourceGroup,omitempty"`  // Azure resource group name

	// Civo-specific configuration
	CivoOrganization string `yaml:"civoOrganization,omitempty"` // Civo organization name alias
	CivoOrgID        string `yaml:"civoOrgId,omitempty"`        // Civo organization ID (resolved from alias)
}

// ClusterMetadata represents cluster metadata
type ClusterMetadata struct {
	Name   string `yaml:"name"`
	Region string `yaml:"region"`
}

// ClusterDefinition represents a complete cluster definition
type ClusterDefinition struct {
	APIVersion string          `yaml:"apiVersion"`
	Kind       string          `yaml:"kind"`
	Metadata   ClusterMetadata `yaml:"metadata"`
	Spec       ClusterSpec     `yaml:"spec"`
}

// ReconcileAction represents the type of action to take on a cluster
type ReconcileAction int

const (
	ActionNone ReconcileAction = iota
	ActionCreate
	ActionUpdate
	ActionDelete
)
