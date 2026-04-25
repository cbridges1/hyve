package reconcile

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/civo/civogo"

	"github.com/cbridges1/hyve/internal/providerconfig"
	"github.com/cbridges1/hyve/internal/state"
)

// SyncProviderConfigFields queries each configured cloud account and reconciles
// the provider config YAML files: adding entries for resources that exist in the
// cloud but are absent from the YAML, and removing entries for resources that no
// longer exist. Returns the number of resources added and removed.
func SyncProviderConfigFields(ctx context.Context, stateMgr *state.Manager) (added, removed int) {
	pcMgr := providerconfig.NewManager(stateMgr.GetStateRoot())

	a, r := syncAWSProviderConfig(ctx, pcMgr)
	added += a
	removed += r

	a, r = syncAzureProviderConfig(ctx, pcMgr)
	added += a
	removed += r

	a, r = syncCivoProviderConfig(ctx, pcMgr)
	added += a
	removed += r

	return added, removed
}

// ── AWS ───────────────────────────────────────────────────────────────────────

func syncAWSProviderConfig(ctx context.Context, pcMgr *providerconfig.Manager) (added, removed int) {
	accounts, err := pcMgr.ListAWSAccounts()
	if err != nil {
		log.Printf("provider-config sync: failed to list AWS accounts: %v", err)
		return
	}

	for _, account := range accounts {
		a, r := syncAWSAccount(ctx, pcMgr, account.Name)
		added += a
		removed += r
	}
	return
}

func buildAWSConfig(ctx context.Context, pcMgr *providerconfig.Manager, accountName string) (aws.Config, error) {
	region := "us-east-1"
	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(region))

	if accountName != "" {
		keyID, secret, token, err := pcMgr.GetAWSCredentials(accountName)
		if err == nil && keyID != "" {
			opts = append(opts, awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(keyID, secret, token),
			))
		}
	}

	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

func syncAWSAccount(ctx context.Context, pcMgr *providerconfig.Manager, accountName string) (added, removed int) {
	cfg, err := buildAWSConfig(ctx, pcMgr, accountName)
	if err != nil {
		log.Printf("provider-config sync [aws/%s]: failed to build AWS config: %v", accountName, err)
		return
	}

	ec2Client := ec2.NewFromConfig(cfg)
	iamClient := iam.NewFromConfig(cfg)

	a, r := syncAWSVPCs(ctx, pcMgr, accountName, ec2Client)
	added += a
	removed += r

	a, r = syncAWSIAMRoles(ctx, pcMgr, accountName, iamClient)
	added += a
	removed += r

	return
}

func syncAWSVPCs(ctx context.Context, pcMgr *providerconfig.Manager, accountName string, ec2Client *ec2.Client) (added, removed int) {
	out, err := ec2Client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("isDefault"), Values: []string{"false"}},
		},
	})
	if err != nil {
		log.Printf("provider-config sync [aws/%s]: failed to list VPCs: %v", accountName, err)
		return
	}

	// Build set of VPC IDs currently in the cloud.
	cloudVPCs := make(map[string]string) // vpc-id → name
	for _, v := range out.Vpcs {
		name := ""
		for _, t := range v.Tags {
			if aws.ToString(t.Key) == "Name" {
				name = aws.ToString(t.Value)
				break
			}
		}
		if name == "" {
			name = aws.ToString(v.VpcId)
		}
		cloudVPCs[aws.ToString(v.VpcId)] = name
	}

	// Load existing config.
	existingVPCs, err := pcMgr.ListAWSVPCs(accountName)
	if err != nil {
		log.Printf("provider-config sync [aws/%s]: failed to list configured VPCs: %v", accountName, err)
		return
	}

	// Remove stale entries.
	existingByID := make(map[string]string) // vpc-id → name
	for _, v := range existingVPCs {
		existingByID[v.VPCID] = v.Name
	}
	for _, v := range existingVPCs {
		if _, ok := cloudVPCs[v.VPCID]; !ok {
			if err := pcMgr.RemoveAWSVPC(accountName, v.Name); err != nil {
				log.Printf("provider-config sync [aws/%s]: failed to remove stale VPC '%s': %v", accountName, v.Name, err)
				continue
			}
			log.Printf("provider-config sync [aws/%s]: removed stale VPC '%s' (%s)", accountName, v.Name, v.VPCID)
			removed++
		}
	}

	// Add missing entries.
	for vpcID, name := range cloudVPCs {
		if _, found := existingByID[vpcID]; !found {
			if err := pcMgr.AddAWSVPC(accountName, name, vpcID); err != nil {
				log.Printf("provider-config sync [aws/%s]: failed to add VPC '%s': %v", accountName, name, err)
				continue
			}
			log.Printf("provider-config sync [aws/%s]: added VPC '%s' (%s)", accountName, name, vpcID)
			added++
		}
	}

	return
}

func syncAWSIAMRoles(ctx context.Context, pcMgr *providerconfig.Manager, accountName string, iamClient *iam.Client) (added, removed int) {
	// Only reconcile roles that are already referenced in the config: verify they
	// still exist and remove stale entries. New roles are added via beforeCreate hooks.
	eksRoles, err := pcMgr.ListAWSEKSRoles(accountName)
	if err != nil {
		log.Printf("provider-config sync [aws/%s]: failed to list EKS roles: %v", accountName, err)
		return
	}
	for _, r := range eksRoles {
		if err := verifyIAMRole(ctx, iamClient, r.Name); err != nil {
			if removeErr := pcMgr.RemoveAWSEKSRole(accountName, r.Name); removeErr != nil {
				log.Printf("provider-config sync [aws/%s]: failed to remove stale EKS role '%s': %v", accountName, r.Name, removeErr)
				continue
			}
			log.Printf("provider-config sync [aws/%s]: removed stale EKS role '%s'", accountName, r.Name)
			removed++
		}
	}

	nodeRoles, err := pcMgr.ListAWSNodeRoles(accountName)
	if err != nil {
		log.Printf("provider-config sync [aws/%s]: failed to list node roles: %v", accountName, err)
		return
	}
	for _, r := range nodeRoles {
		if err := verifyIAMRole(ctx, iamClient, r.Name); err != nil {
			if removeErr := pcMgr.RemoveAWSNodeRole(accountName, r.Name); removeErr != nil {
				log.Printf("provider-config sync [aws/%s]: failed to remove stale node role '%s': %v", accountName, r.Name, removeErr)
				continue
			}
			log.Printf("provider-config sync [aws/%s]: removed stale node role '%s'", accountName, r.Name)
			removed++
		}
	}

	return
}

func verifyIAMRole(ctx context.Context, iamClient *iam.Client, roleName string) error {
	_, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	return err
}

// ── Azure ─────────────────────────────────────────────────────────────────────

func syncAzureProviderConfig(ctx context.Context, pcMgr *providerconfig.Manager) (added, removed int) {
	subs, err := pcMgr.ListAzureSubscriptions()
	if err != nil {
		log.Printf("provider-config sync: failed to list Azure subscriptions: %v", err)
		return
	}

	for _, sub := range subs {
		a, r := syncAzureSubscription(ctx, pcMgr, sub)
		added += a
		removed += r
	}
	return
}

func syncAzureSubscription(ctx context.Context, pcMgr *providerconfig.Manager, sub providerconfig.AzureSubscription) (added, removed int) {
	tenantID := resolveField(sub.TenantID)
	clientID := resolveField(sub.ClientID)
	clientSecret := resolveField(sub.ClientSecret)
	subID := sub.SubscriptionID

	var rgClient *armresources.ResourceGroupsClient
	var err error
	if tenantID != "" && clientID != "" && clientSecret != "" {
		cred, credErr := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
		if credErr != nil {
			log.Printf("provider-config sync [azure/%s]: failed to create credentials: %v", sub.Name, credErr)
			return
		}
		rgClient, err = armresources.NewResourceGroupsClient(subID, cred, nil)
	} else {
		defCred, credErr := azidentity.NewDefaultAzureCredential(nil)
		if credErr != nil {
			log.Printf("provider-config sync [azure/%s]: failed to create default credentials: %v", sub.Name, credErr)
			return
		}
		rgClient, err = armresources.NewResourceGroupsClient(subID, defCred, nil)
	}
	if err != nil {
		log.Printf("provider-config sync [azure/%s]: failed to create resource groups client: %v", sub.Name, err)
		return
	}

	// List all resource groups in the subscription.
	cloudRGs := make(map[string]string) // name → location
	pager := rgClient.NewListPager(nil)
	for pager.More() {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			log.Printf("provider-config sync [azure/%s]: failed to list resource groups: %v", sub.Name, pageErr)
			return
		}
		for _, rg := range page.Value {
			if rg.Name != nil && rg.Location != nil {
				cloudRGs[*rg.Name] = *rg.Location
			}
		}
	}

	existingRGs, err := pcMgr.ListAzureResourceGroups(sub.Name)
	if err != nil {
		log.Printf("provider-config sync [azure/%s]: failed to list configured resource groups: %v", sub.Name, err)
		return
	}

	existingByName := make(map[string]bool)
	for _, rg := range existingRGs {
		existingByName[rg.Name] = true
	}

	// Remove stale entries.
	for _, rg := range existingRGs {
		if _, ok := cloudRGs[rg.Name]; !ok {
			if removeErr := pcMgr.RemoveAzureResourceGroup(sub.Name, rg.Name); removeErr != nil {
				log.Printf("provider-config sync [azure/%s]: failed to remove stale resource group '%s': %v", sub.Name, rg.Name, removeErr)
				continue
			}
			log.Printf("provider-config sync [azure/%s]: removed stale resource group '%s'", sub.Name, rg.Name)
			removed++
		}
	}

	// Add missing entries.
	for name, location := range cloudRGs {
		if !existingByName[name] {
			if addErr := pcMgr.AddAzureResourceGroup(sub.Name, name, location); addErr != nil {
				log.Printf("provider-config sync [azure/%s]: failed to add resource group '%s': %v", sub.Name, name, addErr)
				continue
			}
			log.Printf("provider-config sync [azure/%s]: added resource group '%s' (%s)", sub.Name, name, location)
			added++
		}
	}

	return
}

// ── Civo ──────────────────────────────────────────────────────────────────────

func syncCivoProviderConfig(ctx context.Context, pcMgr *providerconfig.Manager) (added, removed int) {
	orgs, err := pcMgr.ListCivoOrganizations()
	if err != nil {
		log.Printf("provider-config sync: failed to list Civo organizations: %v", err)
		return
	}

	for _, org := range orgs {
		a, r := syncCivoOrg(ctx, pcMgr, org)
		added += a
		removed += r
	}
	return
}

func syncCivoOrg(ctx context.Context, pcMgr *providerconfig.Manager, org providerconfig.CivoOrganization) (added, removed int) {
	token := resolveField(org.Token)
	if token == "" {
		token = providerconfig.ReadCivoCLIToken()
	}
	if token == "" {
		log.Printf("provider-config sync [civo/%s]: no API token available, skipping", org.Name)
		return
	}

	client, err := civogo.NewClient(token, "")
	if err != nil {
		log.Printf("provider-config sync [civo/%s]: failed to create Civo client: %v", org.Name, err)
		return
	}

	cloudNets, err := client.ListNetworks()
	if err != nil {
		log.Printf("provider-config sync [civo/%s]: failed to list networks: %v", org.Name, err)
		return
	}

	cloudByID := make(map[string]string) // network-id → name
	for _, n := range cloudNets {
		cloudByID[n.ID] = n.Name
	}

	existingNets, err := pcMgr.ListCivoNetworks(org.Name)
	if err != nil {
		log.Printf("provider-config sync [civo/%s]: failed to list configured networks: %v", org.Name, err)
		return
	}

	existingByID := make(map[string]bool)
	for _, n := range existingNets {
		existingByID[n.NetworkID] = true
	}

	// Remove stale entries.
	for _, n := range existingNets {
		if _, ok := cloudByID[n.NetworkID]; !ok {
			if removeErr := pcMgr.RemoveCivoNetwork(org.Name, n.Name); removeErr != nil {
				log.Printf("provider-config sync [civo/%s]: failed to remove stale network '%s': %v", org.Name, n.Name, removeErr)
				continue
			}
			log.Printf("provider-config sync [civo/%s]: removed stale network '%s'", org.Name, n.Name)
			removed++
		}
	}

	// Add missing entries.
	for netID, name := range cloudByID {
		if !existingByID[netID] {
			if addErr := pcMgr.AddCivoNetwork(org.Name, name, netID); addErr != nil {
				log.Printf("provider-config sync [civo/%s]: failed to add network '%s': %v", org.Name, name, addErr)
				continue
			}
			log.Printf("provider-config sync [civo/%s]: added network '%s' (%s)", org.Name, name, netID)
			added++
		}
	}

	return
}

// resolveField expands ${ENV_VAR} references; returns the literal value otherwise.
func resolveField(v string) string {
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return os.Getenv(strings.TrimSpace(v[2 : len(v)-1]))
	}
	return v
}
