package shared

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/charmbracelet/huh"
	"github.com/civo/civogo"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/cbridges1/hyve/internal/provider"
	"github.com/cbridges1/hyve/internal/providerconfig"
)

// FetchRegionGroups queries the provider API for available regions and groups
// them geographically. Returns nil when credentials are unavailable or the
// call fails — callers should surface a sign-in warning in that case.
func FetchRegionGroups(ctx context.Context, providerName, accountAlias string) []OptionGroup {
	switch providerName {
	case "civo":
		return fetchCivoRegionGroups(ctx, accountAlias)
	case "aws":
		return fetchAWSRegionGroups(ctx, accountAlias)
	case "gcp":
		return fetchGCPRegionGroups(ctx, accountAlias)
	case "azure":
		return fetchAzureRegionGroups(ctx, accountAlias)
	}
	return nil
}

// FetchNodeGroups queries the provider API for available node/instance types.
// Returns nil when credentials are unavailable or the call fails.
func FetchNodeGroups(ctx context.Context, providerName, region, accountAlias string) []OptionGroup {
	switch providerName {
	case "civo":
		return fetchCivoNodeGroups(ctx, accountAlias)
	case "aws":
		return fetchAWSNodeGroups(ctx, region, accountAlias)
	case "gcp":
		return fetchGCPNodeGroups(ctx, region, accountAlias)
	case "azure":
		return fetchAzureNodeGroups(ctx, region, accountAlias)
	}
	return nil
}

// FetchCloudClusterNames queries the given provider for clusters running in region.
func FetchCloudClusterNames(ctx context.Context, providerName, region, accountAlias string) []string {
	opts := provider.ProviderOptions{Region: region}

	pcm := newProviderConfigManager()

	switch providerName {
	case "civo":
		var token string
		if pcm != nil && accountAlias != "" {
			token, _ = pcm.GetCivoToken(accountAlias)
		}
		if token == "" {
			token = os.Getenv("CIVO_TOKEN")
		}
		if token == "" {
			return nil
		}
		opts.APIKey = token
		opts.AccountName = accountAlias

	case "aws":
		if pcm != nil && accountAlias != "" {
			keyID, secret, tok, err := pcm.GetAWSCredentials(accountAlias)
			if err == nil {
				opts.AccessKeyID = keyID
				opts.SecretAccessKey = secret
				opts.SessionToken = tok
			}
		}
		opts.AccountName = accountAlias

	case "gcp":
		if pcm != nil && accountAlias != "" {
			projectID, _ := pcm.GetGCPProjectID(accountAlias)
			credJSON, _ := pcm.GetGCPCredentialsJSON(accountAlias)
			opts.ProjectID = projectID
			opts.GCPCredentialsJSON = credJSON
		}
		opts.AccountName = accountAlias
		opts.Region = "-"

	case "azure":
		if pcm != nil && accountAlias != "" {
			subID, _ := pcm.GetAzureSubscriptionID(accountAlias)
			tenantID, clientID, clientSecret, err := pcm.GetAzureCredentials(accountAlias)
			if err == nil {
				opts.AzureSubscriptionID = subID
				opts.AzureTenantID = tenantID
				opts.AzureClientID = clientID
				opts.AzureClientSecret = clientSecret
			}
		}
		opts.AccountName = accountAlias

	default:
		return nil
	}

	prov, err := provider.NewFactory().CreateProviderWithOptions(providerName, opts)
	if err != nil {
		return nil
	}

	clusters, err := prov.ListClusters(ctx)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(clusters))
	for _, c := range clusters {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return names
}

// ── helpers ───────────────────────────────────────────────────────────────────

func groupsFromMap(m map[string][]huh.Option[string]) []OptionGroup {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	groups := make([]OptionGroup, 0, len(keys))
	for _, k := range keys {
		groups = append(groups, OptionGroup{Name: k, Options: m[k]})
	}
	return groups
}

func newProviderConfigManager() *providerconfig.Manager {
	repoPath := GetRepoPath()
	if repoPath == "" {
		return nil
	}
	return providerconfig.NewManager(repoPath)
}

// ── Civo ──────────────────────────────────────────────────────────────────────

func getCivoClient(orgName string) (*civogo.Client, error) {
	if pcm := newProviderConfigManager(); pcm != nil {
		if orgName != "" {
			if token, err := pcm.GetCivoToken(orgName); err == nil && token != "" {
				if client, err := civogo.NewClient(token, ""); err == nil {
					return client, nil
				}
			}
		}
		if cfg, err := pcm.LoadCivoConfig(); err == nil && len(cfg.Organizations) > 0 {
			firstName := cfg.Organizations[0].Name
			if firstName != orgName {
				if token, err := pcm.GetCivoToken(firstName); err == nil && token != "" {
					if client, err := civogo.NewClient(token, ""); err == nil {
						return client, nil
					}
				}
			}
		}
	}
	if token := providerconfig.ReadCivoCLIToken(); token != "" {
		if client, err := civogo.NewClient(token, ""); err == nil {
			return client, nil
		}
	}
	token := os.Getenv("CIVO_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("no Civo credentials available")
	}
	return civogo.NewClient(token, "")
}

func fetchCivoRegionGroups(ctx context.Context, orgName string) []OptionGroup {
	client, err := getCivoClient(orgName)
	if err != nil {
		return nil
	}
	regions, err := client.ListRegions()
	if err != nil {
		return nil
	}
	byCountry := map[string][]huh.Option[string]{}
	for _, r := range regions {
		group := r.CountryName
		if group == "" {
			group = "Other"
		}
		label := fmt.Sprintf("%s  (%s)", r.Code, r.Name)
		byCountry[group] = append(byCountry[group], huh.NewOption(label, r.Code))
	}
	return groupsFromMap(byCountry)
}

func fetchCivoNodeGroups(ctx context.Context, orgName string) []OptionGroup {
	client, err := getCivoClient(orgName)
	if err != nil {
		return nil
	}
	sizes, err := client.ListInstanceSizes()
	if err != nil {
		return nil
	}
	byFamily := map[string][]huh.Option[string]{}
	for _, s := range sizes {
		if !s.Selectable {
			continue
		}
		lower := strings.ToLower(s.Name)
		if !strings.Contains(lower, "kube") && !strings.Contains(lower, "k3s") {
			continue
		}
		label := fmt.Sprintf("%-24s  (%d vCPU, %d MB RAM)", s.Name, s.CPUCores, s.RAMMegabytes)
		parts := strings.SplitN(s.Name, ".", 2)
		family := parts[0]
		byFamily[family] = append(byFamily[family], huh.NewOption(label, s.Name))
	}
	if len(byFamily) == 0 {
		byFamily = map[string][]huh.Option[string]{}
		for _, s := range sizes {
			if !s.Selectable {
				continue
			}
			label := fmt.Sprintf("%-24s  (%d vCPU, %d MB RAM)", s.Name, s.CPUCores, s.RAMMegabytes)
			parts := strings.SplitN(s.Name, ".", 2)
			family := parts[0]
			byFamily[family] = append(byFamily[family], huh.NewOption(label, s.Name))
		}
	}
	return groupsFromMap(byFamily)
}

// ── AWS ───────────────────────────────────────────────────────────────────────

func getAWSEC2Client(ctx context.Context, region, accountAlias string) (*ec2.Client, error) {
	if region == "" {
		region = "us-east-1"
	}
	var loadOpts []func(*awsconfig.LoadOptions) error
	loadOpts = append(loadOpts, awsconfig.WithRegion(region))

	if pcm := newProviderConfigManager(); pcm != nil {
		tryAccount := func(name string) bool {
			keyID, secret, token, err := pcm.GetAWSCredentials(name)
			if err == nil && keyID != "" {
				loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
					awscredentials.NewStaticCredentialsProvider(keyID, secret, token),
				))
				return true
			}
			return false
		}
		if accountAlias != "" {
			tryAccount(accountAlias)
		} else if accounts, err := pcm.ListAWSAccounts(); err == nil && len(accounts) > 0 {
			tryAccount(accounts[0].Name)
		}
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	return ec2.NewFromConfig(cfg), nil
}

func awsRegionContinent(name string) string {
	switch {
	case strings.HasPrefix(name, "us-") || strings.HasPrefix(name, "ca-"):
		return "Americas"
	case strings.HasPrefix(name, "eu-"):
		return "Europe"
	case strings.HasPrefix(name, "ap-"):
		return "Asia Pacific"
	case strings.HasPrefix(name, "sa-"):
		return "South America"
	case strings.HasPrefix(name, "me-"):
		return "Middle East"
	case strings.HasPrefix(name, "af-"):
		return "Africa"
	case strings.HasPrefix(name, "il-"):
		return "Israel"
	case strings.HasPrefix(name, "us-gov-"):
		return "US GovCloud"
	default:
		return "Other"
	}
}

func fetchAWSRegionGroups(ctx context.Context, accountAlias string) []OptionGroup {
	client, err := getAWSEC2Client(ctx, "us-east-1", accountAlias)
	if err != nil {
		return nil
	}
	out, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: boolPtr(true),
	})
	if err != nil {
		return nil
	}
	byCont := map[string][]huh.Option[string]{}
	for _, r := range out.Regions {
		name := stringVal(r.RegionName)
		continent := awsRegionContinent(name)
		label := fmt.Sprintf("%-20s  (%s)", name, stringVal(r.OptInStatus))
		byCont[continent] = append(byCont[continent], huh.NewOption(label, name))
	}
	for k := range byCont {
		opts := byCont[k]
		sort.Slice(opts, func(i, j int) bool {
			return opts[i].Value < opts[j].Value
		})
		byCont[k] = opts
	}
	return groupsFromMap(byCont)
}

func awsInstanceFamily(name string) string {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 0 {
		return "other"
	}
	return parts[0]
}

func fetchAWSNodeGroups(ctx context.Context, region, accountAlias string) []OptionGroup {
	if region == "" {
		region = "us-east-1"
	}
	client, err := getAWSEC2Client(ctx, region, accountAlias)
	if err != nil {
		return nil
	}
	paginator := ec2.NewDescribeInstanceTypeOfferingsPaginator(client, &ec2.DescribeInstanceTypeOfferingsInput{
		LocationType: ec2types.LocationTypeRegion,
	})
	byFamily := map[string][]huh.Option[string]{}
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			break
		}
		for _, o := range page.InstanceTypeOfferings {
			name := string(o.InstanceType)
			family := awsInstanceFamily(name)
			byFamily[family] = append(byFamily[family], huh.NewOption(name, name))
		}
	}
	for k := range byFamily {
		opts := byFamily[k]
		sort.Slice(opts, func(i, j int) bool {
			return opts[i].Value < opts[j].Value
		})
		byFamily[k] = opts
	}
	return groupsFromMap(byFamily)
}

// ── GCP ───────────────────────────────────────────────────────────────────────

func getGCPInfo(projectAlias string) (credJSON, projectID string) {
	if pcm := newProviderConfigManager(); pcm != nil {
		tryProject := func(name string) bool {
			id, err := pcm.GetGCPProjectID(name)
			if err != nil || id == "" {
				return false
			}
			projectID = id
			j, _ := pcm.GetGCPCredentialsJSON(name)
			credJSON = j
			return true
		}
		if projectAlias != "" && tryProject(projectAlias) {
			return
		}
		if projects, err := pcm.ListGCPProjects(); err == nil && len(projects) > 0 {
			tryProject(projects[0].Name)
		}
	}
	return
}

func getGCPComputeService(ctx context.Context, projectAlias string) (*compute.Service, string, error) {
	credJSON, projectID := getGCPInfo(projectAlias)
	var svc *compute.Service
	var err error
	if credJSON != "" {
		svc, err = compute.NewService(ctx, option.WithCredentialsJSON([]byte(credJSON)))
	} else {
		svc, err = compute.NewService(ctx)
	}
	if err != nil {
		return nil, "", err
	}
	return svc, projectID, nil
}

func gcpRegionContinent(name string) string {
	switch {
	case strings.HasPrefix(name, "us-") || strings.HasPrefix(name, "northamerica-"):
		return "Americas"
	case strings.HasPrefix(name, "southamerica-"):
		return "South America"
	case strings.HasPrefix(name, "europe-"):
		return "Europe"
	case strings.HasPrefix(name, "asia-"):
		return "Asia Pacific"
	case strings.HasPrefix(name, "australia-"):
		return "Australia"
	case strings.HasPrefix(name, "me-"):
		return "Middle East"
	case strings.HasPrefix(name, "africa-"):
		return "Africa"
	default:
		return "Other"
	}
}

func fetchGCPRegionGroups(ctx context.Context, projectAlias string) []OptionGroup {
	svc, projectID, err := getGCPComputeService(ctx, projectAlias)
	if err != nil || projectID == "" {
		return nil
	}
	resp, err := svc.Regions.List(projectID).Context(ctx).Do()
	if err != nil {
		return nil
	}
	byCont := map[string][]huh.Option[string]{}
	for _, r := range resp.Items {
		continent := gcpRegionContinent(r.Name)
		byCont[continent] = append(byCont[continent], huh.NewOption(r.Name, r.Name))
	}
	for k := range byCont {
		opts := byCont[k]
		sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
		byCont[k] = opts
	}
	return groupsFromMap(byCont)
}

func gcpMachineFamily(name string) string {
	parts := strings.SplitN(name, "-", 2)
	if len(parts) == 0 {
		return "other"
	}
	return strings.ToUpper(parts[0])
}

func fetchGCPNodeGroups(ctx context.Context, region, projectAlias string) []OptionGroup {
	svc, projectID, err := getGCPComputeService(ctx, projectAlias)
	if err != nil || projectID == "" {
		return nil
	}
	zone := region
	if region != "" && !strings.Contains(region, "-b") && !strings.Contains(region, "-a") {
		zone = region + "-b"
	}
	resp, err := svc.MachineTypes.List(projectID, zone).Context(ctx).Do()
	if err != nil {
		return nil
	}
	byFamily := map[string][]huh.Option[string]{}
	for _, mt := range resp.Items {
		family := gcpMachineFamily(mt.Name)
		label := fmt.Sprintf("%-28s  (%d vCPU, %d MB RAM)", mt.Name, mt.GuestCpus, mt.MemoryMb)
		byFamily[family] = append(byFamily[family], huh.NewOption(label, mt.Name))
	}
	for k := range byFamily {
		opts := byFamily[k]
		sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
		byFamily[k] = opts
	}
	return groupsFromMap(byFamily)
}

// ── Azure ─────────────────────────────────────────────────────────────────────

func getAzureInfo(subAlias string) (cred *azidentity.ClientSecretCredential, subscriptionID string, defCred *azidentity.DefaultAzureCredential) {
	if pcm := newProviderConfigManager(); pcm != nil {
		trySubscription := func(name string) bool {
			id, err := pcm.GetAzureSubscriptionID(name)
			if err != nil || id == "" {
				return false
			}
			subscriptionID = id
			tenantID, clientID, clientSecret, err := pcm.GetAzureCredentials(name)
			if err == nil {
				c, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
				if err == nil {
					cred = c
					return true
				}
			}
			return true
		}
		if subAlias != "" && trySubscription(subAlias) {
			if cred != nil {
				return
			}
		}
		if subs, err := pcm.ListAzureSubscriptions(); err == nil && len(subs) > 0 {
			if subs[0].Name != subAlias {
				trySubscription(subs[0].Name)
				if cred != nil {
					return
				}
			}
		}
	}
	dc, _ := azidentity.NewDefaultAzureCredential(nil)
	defCred = dc
	return
}

func fetchAzureRegionGroups(ctx context.Context, subAlias string) []OptionGroup {
	cred, subID, defCred := getAzureInfo(subAlias)
	if subID == "" {
		return nil
	}
	var tokenCred interface {
		GetToken(context.Context, interface{}) (interface{}, error)
	}
	_ = tokenCred

	var subsClient *armsubscriptions.Client
	var err error
	if cred != nil {
		subsClient, err = armsubscriptions.NewClient(cred, nil)
	} else if defCred != nil {
		subsClient, err = armsubscriptions.NewClient(defCred, nil)
	} else {
		return nil
	}
	if err != nil {
		return nil
	}

	pager := subsClient.NewListLocationsPager(subID, nil)
	byGroup := map[string][]huh.Option[string]{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			break
		}
		for _, loc := range page.Value {
			name := stringVal(loc.Name)
			display := stringVal(loc.DisplayName)
			group := "Other"
			if loc.Metadata != nil && loc.Metadata.GeographyGroup != nil {
				group = *loc.Metadata.GeographyGroup
			}
			label := fmt.Sprintf("%-24s  (%s)", name, display)
			byGroup[group] = append(byGroup[group], huh.NewOption(label, name))
		}
	}
	for k := range byGroup {
		opts := byGroup[k]
		sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
		byGroup[k] = opts
	}
	return groupsFromMap(byGroup)
}

func azureVMFamily(name string) string {
	parts := strings.SplitN(name, "_", 3)
	if len(parts) < 2 {
		return "Other"
	}
	family := strings.TrimRight(parts[1], "0123456789")
	if family == "" {
		family = parts[1]
	}
	return family
}

func fetchAzureNodeGroups(ctx context.Context, location, subAlias string) []OptionGroup {
	if location == "" {
		return nil
	}
	cred, subID, defCred := getAzureInfo(subAlias)
	if subID == "" {
		return nil
	}

	var vmClient *armcompute.VirtualMachineSizesClient
	var err error
	if cred != nil {
		vmClient, err = armcompute.NewVirtualMachineSizesClient(subID, cred, nil)
	} else if defCred != nil {
		vmClient, err = armcompute.NewVirtualMachineSizesClient(subID, defCred, nil)
	} else {
		return nil
	}
	if err != nil {
		return nil
	}

	pager := vmClient.NewListPager(location, nil)
	byFamily := map[string][]huh.Option[string]{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			break
		}
		for _, s := range page.Value {
			name := stringVal(s.Name)
			family := azureVMFamily(name)
			label := fmt.Sprintf("%-32s  (%d vCPU, %d MB RAM)", name, int32Val(s.NumberOfCores), int32Val(s.MemoryInMB))
			byFamily[family] = append(byFamily[family], huh.NewOption(label, name))
		}
	}
	for k := range byFamily {
		opts := byFamily[k]
		sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
		byFamily[k] = opts
	}
	return groupsFromMap(byFamily)
}

// ── pointer helpers ───────────────────────────────────────────────────────────

func stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func int32Val(i *int32) int32 {
	if i == nil {
		return 0
	}
	return *i
}

func boolPtr(b bool) *bool {
	return &b
}
