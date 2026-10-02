package cluster

import (
	gocontext "context"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
)

var resourcesCmd = &cobra.Command{
	Use:   "resources [cluster-name]",
	Short: "Show resources hyve currently tracks for a cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if sess, ok := shared.UseClusterMode(); ok {
			showClusterResourcesAPI(shared.NewAPIClient(sess), args[0])
			return
		}
		showClusterResources(args[0])
	},
}

func init() { Cmd.AddCommand(resourcesCmd) }

// showClusterResources reads the cluster YAML directly (no live cluster
// calls, same read-only pattern as showCluster) and prints both declared
// (spec.resources) and reconciler-tracked (spec.appliedResources) state.
func showClusterResources(clusterName string) {
	ctx := gocontext.Background()
	stateMgr, _ := shared.CreateStateManager(ctx)

	clusterDef, _, err := stateMgr.LoadClusterDefinition(clusterName)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("Cluster '%s' not found. Use 'hyve cluster list' to see available clusters.", clusterName)
		}
		log.Fatalf("Failed to read cluster file: %v", err)
	}

	if len(clusterDef.Spec.Resources) == 0 && len(clusterDef.Spec.AppliedResources) == 0 {
		fmt.Printf("No resources declared or tracked for cluster '%s'\n", clusterName)
		return
	}

	if len(clusterDef.Spec.Resources) > 0 {
		fmt.Printf("Declared (spec.resources):\n")
		for _, res := range clusterDef.Spec.Resources {
			switch {
			case res.Delete:
				fmt.Printf("  %s  (marked delete: true)\n", res.Name)
			case res.Helm != nil:
				fmt.Printf("  %s  helm chart=%s version=%s namespace=%s\n", res.Name, res.Helm.Chart, res.Helm.Version, res.Helm.Namespace)
			case res.Secret != nil:
				keys := make([]string, 0, len(res.Secret.Keys))
				for _, k := range res.Secret.Keys {
					if k.Key != "" && k.Key != k.Env {
						keys = append(keys, k.Env+"->"+k.Key)
					} else {
						keys = append(keys, k.Env)
					}
				}
				fmt.Printf("  %s  secret namespace=%s keys=%v\n", res.Name, res.Secret.Namespace, keys)
			default:
				ns := res.Namespace
				if ns == "" {
					ns = "(manifest default)"
				}
				fmt.Printf("  %s  source=%s namespace=%s\n", res.Name, res.Source, ns)
			}
		}
		fmt.Println()
	}

	if len(clusterDef.Spec.AppliedResources) > 0 {
		fmt.Printf("Tracked (spec.appliedResources):\n")
		names := make([]string, 0, len(clusterDef.Spec.AppliedResources))
		for n := range clusterDef.Spec.AppliedResources {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			ar := clusterDef.Spec.AppliedResources[n]
			kind := "manifest"
			if ar.Helm {
				kind = "helm"
			}
			hashPrefix := ar.SourceSHA256
			if len(hashPrefix) > 12 {
				hashPrefix = hashPrefix[:12]
			}
			fmt.Printf("  %s  [%s]  appliedAt=%s  sha256=%s…  objects=%d\n", n, kind, ar.AppliedAt, hashPrefix, len(ar.Objects))
		}
	}
}

// showClusterResourcesAPI is cluster mode's counterpart to
// showClusterResources — same output shape, sourced from
// GET /api/clusters/<name>/resources instead of a local file. Declared
// resources still come from spec.resources; tracked ones from
// status.appliedResources (not spec.appliedResources — cluster mode has a
// real spec/status split, unlike local mode's single merged file).
func showClusterResourcesAPI(client *shared.APIClient, clusterName string) {
	res, err := client.GetClusterResources(clusterName)
	if err != nil {
		log.Fatalf("Failed to get cluster resources: %v", err)
	}

	if len(res.Resources) == 0 && len(res.AppliedResources) == 0 {
		fmt.Printf("No resources declared or tracked for cluster '%s'\n", clusterName)
		return
	}

	if len(res.Resources) > 0 {
		fmt.Printf("Declared (spec.resources):\n")
		for _, r := range res.Resources {
			switch {
			case r.Delete:
				fmt.Printf("  %s  (marked delete: true)\n", r.Name)
			case r.Helm != nil:
				fmt.Printf("  %s  helm chart=%s version=%s namespace=%s\n", r.Name, r.Helm.Chart, r.Helm.Version, r.Helm.Namespace)
			case r.Secret != nil:
				keys := make([]string, 0, len(r.Secret.Keys))
				for _, k := range r.Secret.Keys {
					if k.Key != "" && k.Key != k.Env {
						keys = append(keys, k.Env+"->"+k.Key)
					} else {
						keys = append(keys, k.Env)
					}
				}
				fmt.Printf("  %s  secret namespace=%s keys=%v\n", r.Name, r.Secret.Namespace, keys)
			default:
				ns := r.Namespace
				if ns == "" {
					ns = "(manifest default)"
				}
				fmt.Printf("  %s  source=%s namespace=%s\n", r.Name, r.Source, ns)
			}
		}
		fmt.Println()
	}

	if len(res.AppliedResources) > 0 {
		fmt.Printf("Tracked (status.appliedResources):\n")
		names := make([]string, 0, len(res.AppliedResources))
		for n := range res.AppliedResources {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			ar := res.AppliedResources[n]
			kind := "manifest"
			if ar.Helm {
				kind = "helm"
			}
			hashPrefix := ar.SourceSHA256
			if len(hashPrefix) > 12 {
				hashPrefix = hashPrefix[:12]
			}
			fmt.Printf("  %s  [%s]  appliedAt=%s  sha256=%s…  objects=%d\n", n, kind, ar.AppliedAt, hashPrefix, len(ar.Objects))
		}
	}
}
