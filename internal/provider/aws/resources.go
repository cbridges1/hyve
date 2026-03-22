package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// ResourceManager handles AWS resource operations (IAM roles, VPCs, etc.)
type ResourceManager struct {
	iamClient *iam.Client
	ec2Client *ec2.Client
	region    string
}

// NewResourceManager creates a new AWS resource manager
func NewResourceManager(region string) (*ResourceManager, error) {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &ResourceManager{
		iamClient: iam.NewFromConfig(cfg),
		ec2Client: ec2.NewFromConfig(cfg),
		region:    region,
	}, nil
}

// EKS assume role policy document
const eksAssumeRolePolicyDocument = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Service": "eks.amazonaws.com"
      },
      "Action": "sts:AssumeRole"
    }
  ]
}`

// EC2 assume role policy document (for EKS node groups)
const ec2AssumeRolePolicyDocument = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Service": "ec2.amazonaws.com"
      },
      "Action": "sts:AssumeRole"
    }
  ]
}`

// EKSRoleInfo contains information about an EKS IAM role
type EKSRoleInfo struct {
	Name      string
	ARN       string
	CreatedAt time.Time
}

// CreateEKSRole creates an IAM role for EKS clusters
func (m *ResourceManager) CreateEKSRole(ctx context.Context, roleName string) (*EKSRoleInfo, error) {
	// Create the IAM role
	createRoleInput := &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(eksAssumeRolePolicyDocument),
		Description:              aws.String("IAM role for EKS cluster created by Hyve"),
		Tags: []iamtypes.Tag{
			{
				Key:   aws.String("CreatedBy"),
				Value: aws.String("hyve"),
			},
		},
	}

	createResp, err := m.iamClient.CreateRole(ctx, createRoleInput)
	if err != nil {
		return nil, fmt.Errorf("failed to create IAM role: %w", err)
	}

	// Attach required EKS policies
	policies := []string{
		"arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
	}

	for _, policyARN := range policies {
		_, err := m.iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policyARN),
		})
		if err != nil {
			// Try to clean up the role if policy attachment fails
			_, _ = m.iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(roleName)})
			return nil, fmt.Errorf("failed to attach policy %s: %w", policyARN, err)
		}
	}

	return &EKSRoleInfo{
		Name:      *createResp.Role.RoleName,
		ARN:       *createResp.Role.Arn,
		CreatedAt: *createResp.Role.CreateDate,
	}, nil
}

// DeleteEKSRole deletes an IAM role for EKS clusters
func (m *ResourceManager) DeleteEKSRole(ctx context.Context, roleName string) error {
	// First, detach all attached policies
	listPoliciesResp, err := m.iamClient.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("failed to list attached policies: %w", err)
	}

	for _, policy := range listPoliciesResp.AttachedPolicies {
		_, err := m.iamClient.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: policy.PolicyArn,
		})
		if err != nil {
			return fmt.Errorf("failed to detach policy %s: %w", *policy.PolicyArn, err)
		}
	}

	// Delete the role
	_, err = m.iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("failed to delete IAM role: %w", err)
	}

	return nil
}

// GetEKSRole gets information about an EKS IAM role
func (m *ResourceManager) GetEKSRole(ctx context.Context, roleName string) (*EKSRoleInfo, error) {
	resp, err := m.iamClient.GetRole(ctx, &iam.GetRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get IAM role: %w", err)
	}

	return &EKSRoleInfo{
		Name:      *resp.Role.RoleName,
		ARN:       *resp.Role.Arn,
		CreatedAt: *resp.Role.CreateDate,
	}, nil
}

// NodeRoleInfo contains information about an EKS node IAM role
type NodeRoleInfo struct {
	Name      string
	ARN       string
	CreatedAt time.Time
}

// CreateNodeRole creates an IAM role for EKS node groups
func (m *ResourceManager) CreateNodeRole(ctx context.Context, roleName string) (*NodeRoleInfo, error) {
	// Create the IAM role with EC2 assume role policy
	createRoleInput := &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(ec2AssumeRolePolicyDocument),
		Description:              aws.String("IAM role for EKS node group created by Hyve"),
		Tags: []iamtypes.Tag{
			{
				Key:   aws.String("CreatedBy"),
				Value: aws.String("hyve"),
			},
		},
	}

	createResp, err := m.iamClient.CreateRole(ctx, createRoleInput)
	if err != nil {
		return nil, fmt.Errorf("failed to create IAM role: %w", err)
	}

	// Attach required EKS node policies
	policies := []string{
		"arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
		"arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
		"arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
	}

	for _, policyARN := range policies {
		_, err := m.iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policyARN),
		})
		if err != nil {
			// Try to clean up the role if policy attachment fails
			_, _ = m.iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(roleName)})
			return nil, fmt.Errorf("failed to attach policy %s: %w", policyARN, err)
		}
	}

	return &NodeRoleInfo{
		Name:      *createResp.Role.RoleName,
		ARN:       *createResp.Role.Arn,
		CreatedAt: *createResp.Role.CreateDate,
	}, nil
}

// DeleteNodeRole deletes an IAM role for EKS node groups
func (m *ResourceManager) DeleteNodeRole(ctx context.Context, roleName string) error {
	// First, detach all attached policies
	listPoliciesResp, err := m.iamClient.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("failed to list attached policies: %w", err)
	}

	for _, policy := range listPoliciesResp.AttachedPolicies {
		_, err := m.iamClient.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: policy.PolicyArn,
		})
		if err != nil {
			return fmt.Errorf("failed to detach policy %s: %w", *policy.PolicyArn, err)
		}
	}

	// Delete the role
	_, err = m.iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("failed to delete IAM role: %w", err)
	}

	return nil
}

// GetNodeRole gets information about an EKS node IAM role
func (m *ResourceManager) GetNodeRole(ctx context.Context, roleName string) (*NodeRoleInfo, error) {
	resp, err := m.iamClient.GetRole(ctx, &iam.GetRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get IAM role: %w", err)
	}

	return &NodeRoleInfo{
		Name:      *resp.Role.RoleName,
		ARN:       *resp.Role.Arn,
		CreatedAt: *resp.Role.CreateDate,
	}, nil
}

// VPCInfo contains information about a VPC
type VPCInfo struct {
	ID        string
	CIDR      string
	Name      string
	IsDefault bool
	State     string
	Subnets   []SubnetInfo
}

// SubnetInfo contains information about a subnet
type SubnetInfo struct {
	ID               string
	CIDR             string
	AvailabilityZone string
	IsPublic         bool
}

// CreateVPCInput contains parameters for creating a VPC
type CreateVPCInput struct {
	Name              string
	CIDR              string
	EnableDNSSupport  bool
	EnableDNSHostname bool
	CreateSubnets     bool
	SubnetCIDRs       []string
}

// CreateVPC creates a VPC with optional subnets
func (m *ResourceManager) CreateVPC(ctx context.Context, input *CreateVPCInput) (*VPCInfo, error) {
	// Default CIDR if not specified
	cidr := input.CIDR
	if cidr == "" {
		cidr = "10.0.0.0/16"
	}

	// Create VPC
	createVPCResp, err := m.ec2Client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String(cidr),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeVpc,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(input.Name)},
					{Key: aws.String("CreatedBy"), Value: aws.String("hyve")},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC: %w", err)
	}

	vpcID := *createVPCResp.Vpc.VpcId

	// Enable DNS support if requested
	if input.EnableDNSSupport {
		_, err := m.ec2Client.ModifyVpcAttribute(ctx, &ec2.ModifyVpcAttributeInput{
			VpcId:            aws.String(vpcID),
			EnableDnsSupport: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to enable DNS support: %w", err)
		}
	}

	// Enable DNS hostnames if requested
	if input.EnableDNSHostname {
		_, err := m.ec2Client.ModifyVpcAttribute(ctx, &ec2.ModifyVpcAttributeInput{
			VpcId:              aws.String(vpcID),
			EnableDnsHostnames: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to enable DNS hostnames: %w", err)
		}
	}

	// Create subnets if requested
	var subnets []SubnetInfo
	if input.CreateSubnets && len(input.SubnetCIDRs) > 0 {
		// Get available AZs
		azsResp, err := m.ec2Client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{
			Filters: []ec2types.Filter{
				{Name: aws.String("state"), Values: []string{"available"}},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get availability zones: %w", err)
		}

		for i, subnetCIDR := range input.SubnetCIDRs {
			az := ""
			if i < len(azsResp.AvailabilityZones) {
				az = *azsResp.AvailabilityZones[i].ZoneName
			}

			subnetResp, err := m.ec2Client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
				VpcId:            aws.String(vpcID),
				CidrBlock:        aws.String(subnetCIDR),
				AvailabilityZone: aws.String(az),
				TagSpecifications: []ec2types.TagSpecification{
					{
						ResourceType: ec2types.ResourceTypeSubnet,
						Tags: []ec2types.Tag{
							{Key: aws.String("Name"), Value: aws.String(fmt.Sprintf("%s-subnet-%d", input.Name, i+1))},
							{Key: aws.String("CreatedBy"), Value: aws.String("hyve")},
						},
					},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create subnet: %w", err)
			}

			subnets = append(subnets, SubnetInfo{
				ID:               *subnetResp.Subnet.SubnetId,
				CIDR:             *subnetResp.Subnet.CidrBlock,
				AvailabilityZone: *subnetResp.Subnet.AvailabilityZone,
			})
		}
	}

	return &VPCInfo{
		ID:      vpcID,
		CIDR:    cidr,
		Name:    input.Name,
		State:   string(createVPCResp.Vpc.State),
		Subnets: subnets,
	}, nil
}

// DeleteVPC deletes a VPC and its associated resources
func (m *ResourceManager) DeleteVPC(ctx context.Context, vpcID string) error {
	// First, delete all subnets
	subnetsResp, err := m.ec2Client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to list subnets: %w", err)
	}

	for _, subnet := range subnetsResp.Subnets {
		_, err := m.ec2Client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
			SubnetId: subnet.SubnetId,
		})
		if err != nil {
			return fmt.Errorf("failed to delete subnet %s: %w", *subnet.SubnetId, err)
		}
	}

	// Delete internet gateways
	igwsResp, err := m.ec2Client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to list internet gateways: %w", err)
	}

	for _, igw := range igwsResp.InternetGateways {
		_, err := m.ec2Client.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
			InternetGatewayId: igw.InternetGatewayId,
			VpcId:             aws.String(vpcID),
		})
		if err != nil {
			return fmt.Errorf("failed to detach internet gateway: %w", err)
		}

		_, err = m.ec2Client.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
			InternetGatewayId: igw.InternetGatewayId,
		})
		if err != nil {
			return fmt.Errorf("failed to delete internet gateway: %w", err)
		}
	}

	// Delete the VPC
	_, err = m.ec2Client.DeleteVpc(ctx, &ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	})
	if err != nil {
		return fmt.Errorf("failed to delete VPC: %w", err)
	}

	return nil
}

// GetVPC gets information about a VPC
func (m *ResourceManager) GetVPC(ctx context.Context, vpcID string) (*VPCInfo, error) {
	resp, err := m.ec2Client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		VpcIds: []string{vpcID},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe VPC: %w", err)
	}

	if len(resp.Vpcs) == 0 {
		return nil, fmt.Errorf("VPC %s not found", vpcID)
	}

	vpc := resp.Vpcs[0]
	name := ""
	for _, tag := range vpc.Tags {
		if *tag.Key == "Name" {
			name = *tag.Value
			break
		}
	}

	// Get subnets
	subnetsResp, err := m.ec2Client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe subnets: %w", err)
	}

	var subnets []SubnetInfo
	for _, subnet := range subnetsResp.Subnets {
		subnets = append(subnets, SubnetInfo{
			ID:               *subnet.SubnetId,
			CIDR:             *subnet.CidrBlock,
			AvailabilityZone: *subnet.AvailabilityZone,
			IsPublic:         *subnet.MapPublicIpOnLaunch,
		})
	}

	return &VPCInfo{
		ID:        *vpc.VpcId,
		CIDR:      *vpc.CidrBlock,
		Name:      name,
		IsDefault: *vpc.IsDefault,
		State:     string(vpc.State),
		Subnets:   subnets,
	}, nil
}

// Helper to convert struct to JSON for policy documents
func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
