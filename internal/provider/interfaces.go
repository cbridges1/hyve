package provider

import (
	"context"
	"time"

	"github.com/cbridges1/hyve/internal/types"
)

// Cluster represents a generic cluster
type Cluster struct {
	ID         string
	Name       string
	Status     string
	FirewallID string
	MasterIP   string
	KubeConfig string
	CreatedAt  time.Time
}

// Firewall represents a generic firewall
type Firewall struct {
	ID    string
	Name  string
	Rules []FirewallRule
}

// FirewallRule represents a generic firewall rule
type FirewallRule struct {
	Protocol  string
	StartPort string
	EndPort   string
	Cidr      []string
	Direction string
}

// ClusterConfig represents cluster creation configuration
type ClusterConfig struct {
	Name         string
	Region       string
	Nodes        []string
	NodeGroups   []types.NodeGroup
	ClusterType  string
	FirewallID   string
	Applications []string

	// AWS-specific configuration
	AWSRoleARN     string   // IAM role ARN for EKS cluster
	AWSNodeRoleARN string   // IAM role ARN for EKS node group
	AWSVPCID       string   // VPC ID for EKS cluster
	AWSSubnetIDs   []string // Subnet IDs for EKS cluster (optional, discovered from VPC if not provided)
}

// ClusterUpdateConfig represents cluster update configuration
type ClusterUpdateConfig struct {
	Name       string
	Nodes      []string
	NodeGroups []types.NodeGroup
}

// FirewallConfig represents firewall creation configuration
type FirewallConfig struct {
	Name  string
	Rules []FirewallRule
}

// ClusterInfo represents exported cluster information
type ClusterInfo struct {
	Name       string
	IPAddress  string
	AccessPort string
	Kubeconfig string
	Status     string
	ID         string
	NodeGroups []types.NodeGroup
}

// ClusterProvider interface defines the operations a cloud provider must implement
type ClusterProvider interface {
	// Cluster operations
	ListClusters(ctx context.Context) ([]*Cluster, error)
	GetCluster(ctx context.Context, clusterID string) (*Cluster, error)
	FindClusterByName(ctx context.Context, name string) (*Cluster, error)
	CreateCluster(ctx context.Context, config *ClusterConfig) (*Cluster, error)
	UpdateCluster(ctx context.Context, clusterID string, config *ClusterUpdateConfig) (*Cluster, error)
	DeleteCluster(ctx context.Context, clusterID string) error
	WaitForClusterReady(ctx context.Context, clusterID string) error
	GetClusterInfo(ctx context.Context, name string) (*ClusterInfo, error)
}

// FirewallProvider interface defines firewall operations
type FirewallProvider interface {
	ListFirewalls(ctx context.Context) ([]*Firewall, error)
	CreateFirewall(ctx context.Context, config *FirewallConfig) (*Firewall, error)
	DeleteFirewall(ctx context.Context, firewallID string) error
	FindFirewallByName(ctx context.Context, name string) (*Firewall, error)
}

// AccessEntryGranter is an optional interface implemented by cloud providers that support
// granting a principal direct cluster API access (e.g. EKS access entries).
type AccessEntryGranter interface {
	EnsureAccessEntry(ctx context.Context, clusterName, principalARN string) error
}

// Provider combines all provider interfaces
type Provider interface {
	ClusterProvider
	FirewallProvider

	// Provider metadata
	Name() string
	Region() string
}
