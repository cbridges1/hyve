package cloudlookup

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// AWSCreds holds credentials for AWS API calls. All fields are optional; empty
// fields fall back to the default credential chain (env vars, instance role, etc.).
type AWSCreds struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// VPCOption represents a non-default VPC available for selection.
type VPCOption struct {
	ID   string
	Name string
	CIDR string
}

// RoleOption represents an IAM role available for selection.
type RoleOption struct {
	Name string
	ARN  string
}

// ListVPCs returns all non-default VPCs in the given region.
func ListVPCs(ctx context.Context, creds AWSCreds, region string) ([]VPCOption, error) {
	cfg, err := buildAWSCfg(ctx, creds, region)
	if err != nil {
		return nil, err
	}

	out, err := ec2.NewFromConfig(cfg).DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("isDefault"), Values: []string{"false"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeVpcs: %w", err)
	}

	result := make([]VPCOption, 0, len(out.Vpcs))
	for _, v := range out.Vpcs {
		name := aws.ToString(v.VpcId)
		for _, t := range v.Tags {
			if aws.ToString(t.Key) == "Name" && aws.ToString(t.Value) != "" {
				name = aws.ToString(t.Value)
				break
			}
		}
		result = append(result, VPCOption{
			ID:   aws.ToString(v.VpcId),
			Name: name,
			CIDR: aws.ToString(v.CidrBlock),
		})
	}
	return result, nil
}

// ListIAMRoles returns IAM roles whose names contain the optional filter string.
// Pass an empty filter to return all roles.
func ListIAMRoles(ctx context.Context, creds AWSCreds, filter string) ([]RoleOption, error) {
	cfg, err := buildAWSCfg(ctx, creds, "us-east-1")
	if err != nil {
		return nil, err
	}

	iamClient := iam.NewFromConfig(cfg)
	var result []RoleOption
	paginator := iam.NewListRolesPaginator(iamClient, &iam.ListRolesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("ListRoles: %w", err)
		}
		for _, r := range page.Roles {
			name := aws.ToString(r.RoleName)
			if filter == "" || containsFold(name, filter) {
				result = append(result, RoleOption{
					Name: name,
					ARN:  aws.ToString(r.Arn),
				})
			}
		}
	}
	return result, nil
}

func buildAWSCfg(ctx context.Context, creds AWSCreds, region string) (aws.Config, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	if creds.AccessKeyID != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken),
		))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

func containsFold(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	sLower := toLower(s)
	subLower := toLower(substr)
	return len(sLower) >= len(subLower) && contains(sLower, subLower)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
