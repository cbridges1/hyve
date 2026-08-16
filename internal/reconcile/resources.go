package reconcile

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/cbridges1/hyve/internal/resourceref"
	"github.com/cbridges1/hyve/internal/types"
)

// reconcileResources runs the resource drift-detection/apply/delete
// algorithm for one ACTIVE, non-deleting cluster, mutating
// cluster.Spec.Resources and cluster.Spec.AppliedResources in place,
// persisting via SaveClusterDefinition (mirroring how createCluster persists
// DriverOutputs) if anything changed, and returning a non-nil error (without
// rollback) on the first delete/apply failure so the whole reconcile cycle
// for this cluster is retried next cycle.
//
// Called unconditionally from reconcileCluster's ACTIVE-and-not-deleting
// case, regardless of param drift — it is the caller's responsibility to
// have already run module.OperationAuth so KUBECONFIG is set.
//
// Each resource entry is either manifest-based (Source set) or Helm-based
// (Helm set) — validateResourceRef enforces exactly one. Manifest resources
// resolve via resourceref and apply/diff via kubectl; Helm resources render
// via `helm template` (fed into the same kubectlDiff used for manifests) and
// apply via `helm upgrade --install`. Deletion mirrors this split:
// AppliedResource.Helm records which mechanism (kubectlDeleteObjects vs
// helmUninstall) owns cleanup, since the original ResourceRef may be gone by
// the time an entry is pruned.
//
// When dryRun is true, resolution and diffing still run for real (both
// read-only) so drift can be reported accurately, but every mutating call
// (kubectl apply/delete, helm upgrade/uninstall, and the final
// SaveClusterDefinition) is skipped and logged as "would" instead.
// cluster.Spec.Resources/AppliedResources are left untouched in dry-run
// mode — the caller's in-memory copy is discarded either way since nothing
// is persisted.
func (r *Reconciler) reconcileResources(ctx context.Context, cluster *types.ClusterDefinition, strictResourceDelete, dryRun bool) error {
	name := cluster.Metadata.Name
	repoRoot := r.stateMgr.LocalPath()

	if cluster.Spec.AppliedResources == nil {
		cluster.Spec.AppliedResources = map[string]*types.AppliedResource{}
	}

	original := cluster.Spec.Resources
	removeIdx := map[int]bool{}
	// Force one save+commit for a cluster whose driverOutputs/appliedResources
	// still live inline in its primary file (pre state-sidecar-split format,
	// no cluster-state/<name>.state.yaml yet) even if nothing else about it
	// drifted this cycle — SaveClusterDefinition unconditionally splits on
	// every write, so this is the entire migration mechanism: no separate
	// migrate command or script needed, every cluster converges within one
	// reconcile cycle it isn't paused for.
	changed := !r.stateMgr.HasStateSidecar(name) &&
		(len(cluster.Spec.DriverOutputs) > 0 || len(cluster.Spec.AppliedResources) > 0)
	var loopErr error
	driftCount, unchangedCount := 0, 0

	for i, res := range original {
		if res.Delete {
			if applied, ok := cluster.Spec.AppliedResources[res.Name]; ok {
				if dryRun {
					log.Printf("[%s] DRY RUN: resource %s marked delete:true — would remove %d tracked object(s)", name, res.Name, len(applied.Objects))
					driftCount++
					continue
				}
				if applied.Helm {
					log.Printf("[%s] Resource %s: delete:true — uninstalling helm release", name, res.Name)
					if err := helmUninstall(ctx, repoRoot, res.Name, applied.Namespace); err != nil {
						loopErr = fmt.Errorf("resource %s: helm uninstall failed: %w", res.Name, err)
						break
					}
				} else {
					log.Printf("[%s] Resource %s: delete:true — removing %d tracked object(s)", name, res.Name, len(applied.Objects))
					if err := kubectlDeleteObjects(ctx, repoRoot, applied.Objects); err != nil {
						loopErr = fmt.Errorf("resource %s: delete failed: %w", res.Name, err)
						break
					}
				}
				delete(cluster.Spec.AppliedResources, res.Name)
			}
			removeIdx[i] = true
			changed = true
			continue
		}

		if err := validateResourceRef(res); err != nil {
			loopErr = err
			break
		}

		var configHash string
		var liveManifest []byte
		var applyNamespace string
		var resolvedHelmValues map[string]string

		if res.Helm != nil {
			resolved, err := resolveHelmValues(res.Helm)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: resolve helm values failed: %w", res.Name, err)
				break
			}
			resolvedHelmValues = resolved
			configHash = helmConfigHash(res.Helm, resolvedHelmValues)
			applyNamespace = res.Helm.Namespace
			rendered, err := helmRenderManifest(ctx, repoRoot, res.Name, res.Helm, resolvedHelmValues)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: helm template failed: %w", res.Name, err)
				break
			}
			liveManifest = rendered
		} else if res.Secret != nil {
			rendered, resolved, err := renderSecretManifest(res.Name, res.Secret)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: render secret failed: %w", res.Name, err)
				break
			}
			configHash = secretConfigHash(res.Secret, resolved)
			applyNamespace = res.Secret.Namespace
			liveManifest = rendered
		} else {
			resolved, err := resourceref.Resolve(res.Source, repoRoot)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: resolve failed: %w", res.Name, err)
				break
			}
			configHash = resolved.SHA256
			applyNamespace = res.Namespace
			liveManifest = resolved.Data
		}

		applied := cluster.Spec.AppliedResources[res.Name]
		configChanged := applied == nil || applied.SourceSHA256 != configHash

		liveDiff, err := kubectlDiff(ctx, repoRoot, liveManifest, applyNamespace)
		if err != nil {
			loopErr = fmt.Errorf("resource %s: kubectl diff failed: %w", res.Name, err)
			break
		}

		if !needsApply(configChanged, liveDiff) {
			log.Printf("[%s] Resource %s: in sync", name, res.Name)
			unchangedCount++
			continue
		}
		driftCount++

		if dryRun {
			log.Printf("[%s] DRY RUN: resource %s drift detected (%s) — would apply", name, res.Name, driftReason(configChanged, liveDiff))
			continue
		}
		log.Printf("[%s] Resource %s: drift detected (%s) — applying", name, res.Name, driftReason(configChanged, liveDiff))

		var objects []types.AppliedObject
		if res.Helm != nil {
			if err := helmUpgradeInstall(ctx, repoRoot, res.Name, res.Helm, resolvedHelmValues); err != nil {
				loopErr = fmt.Errorf("resource %s: apply failed: %w", res.Name, err)
				break
			}
			deployed, err := helmGetManifest(ctx, repoRoot, res.Name, res.Helm.Namespace)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: helm get manifest failed: %w", res.Name, err)
				break
			}
			objs, err := parseManifestObjects(deployed, res.Helm.Namespace)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: parse applied objects: %w", res.Name, err)
				break
			}
			objects = objs
		} else {
			if err := kubectlApply(ctx, repoRoot, liveManifest, applyNamespace); err != nil {
				loopErr = fmt.Errorf("resource %s: apply failed: %w", res.Name, err)
				break
			}
			objs, err := parseManifestObjects(liveManifest, applyNamespace)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: parse applied objects: %w", res.Name, err)
				break
			}
			objects = objs
		}

		cluster.Spec.AppliedResources[res.Name] = &types.AppliedResource{
			SourceSHA256: configHash,
			Helm:         res.Helm != nil,
			Namespace:    applyNamespace,
			AppliedAt:    time.Now().UTC().Format(time.RFC3339),
			Objects:      objects,
		}
		changed = true
	}

	if !dryRun && len(removeIdx) > 0 {
		kept := make([]types.ResourceRef, 0, len(original)-len(removeIdx))
		for i, res := range original {
			if !removeIdx[i] {
				kept = append(kept, res)
			}
		}
		cluster.Spec.Resources = kept
	}

	// Orphan pass: names in AppliedResources with no corresponding entry left
	// in Resources at all (never went through the delete:true path above).
	// Runs regardless of loopErr — it's independent per-name bookkeeping, not
	// affected by an apply/delete failure earlier in the list. In dry-run
	// mode this still evaluates against the *current* (unmodified) Resources
	// list, since delete:true entries above were reported, not removed.
	for _, orphan := range findOrphanedResources(cluster.Spec.Resources, cluster.Spec.AppliedResources) {
		if !strictResourceDelete {
			log.Printf("[%s] Warning: resource %q is orphaned (removed from spec.resources without delete:true) — set delete:true to remove it, or reconcile.strictResourceDelete: true to auto-prune", name, orphan)
			continue
		}
		ar := cluster.Spec.AppliedResources[orphan]
		if dryRun {
			log.Printf("[%s] DRY RUN: resource %s is orphaned, strictResourceDelete=true — would prune", name, orphan)
			driftCount++
			continue
		}
		var err error
		if ar.Helm {
			log.Printf("[%s] Resource %s: orphaned, strictResourceDelete=true — uninstalling helm release", name, orphan)
			err = helmUninstall(ctx, repoRoot, orphan, ar.Namespace)
		} else {
			log.Printf("[%s] Resource %s: orphaned, strictResourceDelete=true — pruning %d tracked object(s)", name, orphan, len(ar.Objects))
			err = kubectlDeleteObjects(ctx, repoRoot, ar.Objects)
		}
		if err != nil {
			log.Printf("[%s] Warning: failed to prune orphaned resource %s: %v", name, orphan, err)
			continue
		}
		delete(cluster.Spec.AppliedResources, orphan)
		changed = true
	}

	if dryRun {
		log.Printf("[%s] DRY RUN: %d resource(s) with drift, %d unchanged", name, driftCount, unchangedCount)
		return loopErr
	}

	if changed {
		if err := r.stateMgr.SaveClusterDefinition(cluster); err != nil {
			log.Printf("[%s] Warning: failed to save resource state: %v", name, err)
		}
	}

	return loopErr
}

// needsApply is the pure hash-compare + live-diff decision: apply if either
// the desired config changed since last applied, or the live object(s)
// drifted out-of-band. Pure — table-testable.
func needsApply(configChanged, liveDiff bool) bool {
	return configChanged || liveDiff
}

func driftReason(configChanged, liveDiff bool) string {
	switch {
	case configChanged && liveDiff:
		return "config changed + live drift"
	case configChanged:
		return "config changed"
	default:
		return "live drift"
	}
}

// findOrphanedResources returns the sorted set of AppliedResources keys with
// no corresponding entry in resources. Pure — table-testable with two
// "string sets" (resource names vs. applied-resource names).
func findOrphanedResources(resources []types.ResourceRef, applied map[string]*types.AppliedResource) []string {
	inSpec := make(map[string]bool, len(resources))
	for _, r := range resources {
		inSpec[r.Name] = true
	}
	var orphans []string
	for name := range applied {
		if !inSpec[name] {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	return orphans
}

// validateResourceRef checks that exactly one of Source or Helm is set on a
// non-delete resource entry. Pure — table-testable.
func validateResourceRef(res types.ResourceRef) error {
	set := 0
	if res.Source != "" {
		set++
	}
	if res.Helm != nil {
		set++
	}
	if res.Secret != nil {
		set++
	}
	if set != 1 {
		return fmt.Errorf("resource %s: exactly one of source, helm, or secret must be set", res.Name)
	}
	return nil
}
