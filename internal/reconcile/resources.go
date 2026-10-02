package reconcile

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cbridges1/hyve/internal/module"
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
func (r *Reconciler) reconcileResources(ctx context.Context, cluster *types.ClusterDefinition, env []string, lf *module.LockFile, strictResourceDelete, dryRun bool) error {
	name := cluster.Metadata.Name
	repoRoot := r.stateMgr.LocalPath()
	githubToken := envValue(env, "GITHUB_TOKEN")

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
	driftCount, unchangedCount := 0, 0

	// Directory sources expand into one entry per file ("<name>/<stem>"),
	// each applied, drift-checked, and tracked like any single-file source.
	items, heldPrefixes, loopErr := expandResourceItems(original, repoRoot, githubToken)

itemLoop:
	for _, item := range items {
		res := item.ref
		i := item.origIdx
		if res.Delete {
			// A directory entry's own children are tracked as "<name>/..."
			// — delete those too, found by prefix, so removing a directory
			// never needs the directory itself to still resolve.
			for _, key := range appliedKeysFor(cluster.Spec.AppliedResources, res.Name) {
				applied := cluster.Spec.AppliedResources[key]
				if dryRun {
					log.Printf("[%s] DRY RUN: resource %s marked delete:true — would remove %d tracked object(s)", name, key, len(applied.Objects))
					driftCount++
					continue
				}
				if applied.Helm {
					log.Printf("[%s] Resource %s: delete:true — uninstalling helm release", name, key)
					if err := helmUninstall(ctx, repoRoot, env, key, applied.Namespace); err != nil {
						loopErr = fmt.Errorf("resource %s: helm uninstall failed: %w", key, err)
						break itemLoop
					}
				} else {
					log.Printf("[%s] Resource %s: delete:true — removing %d tracked object(s)", name, key, len(applied.Objects))
					if err := kubectlDeleteObjects(ctx, repoRoot, env, applied.Objects); err != nil {
						loopErr = fmt.Errorf("resource %s: delete failed: %w", key, err)
						break itemLoop
					}
				}
				delete(cluster.Spec.AppliedResources, key)
			}
			if dryRun {
				continue
			}
			removeIdx[i] = true
			changed = true
			continue
		}

		if err := validateResourceRef(res); err != nil {
			loopErr = err
			break itemLoop
		}

		var configHash string
		var liveManifest []byte
		var applyNamespace string
		var resolvedHelmValues map[string]string

		if res.Helm != nil {
			resolved, err := resolveHelmValues(res.Helm)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: resolve helm values failed: %w", res.Name, err)
				break itemLoop
			}
			resolvedHelmValues = resolved
			configHash = helmConfigHash(res.Helm, resolvedHelmValues)
			applyNamespace = res.Helm.Namespace
			rendered, err := helmRenderManifest(ctx, repoRoot, env, res.Name, res.Helm, resolvedHelmValues)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: helm template failed: %w", res.Name, err)
				break itemLoop
			}
			liveManifest = rendered
		} else if res.Secret != nil {
			rendered, resolved, err := renderSecretManifest(res.Name, res.Secret)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: render secret failed: %w", res.Name, err)
				break itemLoop
			}
			configHash = secretConfigHash(res.Secret, resolved)
			applyNamespace = res.Secret.Namespace
			liveManifest = rendered
		} else if res.Source != "" {
			resolved := item.pre
			if resolved == nil {
				var err error
				resolved, err = resourceref.Resolve(res.Source, repoRoot, lf, githubToken)
				if err != nil {
					loopErr = fmt.Errorf("resource %s: resolve failed: %w", res.Name, err)
					break itemLoop
				}
			}
			configHash = resolved.SHA256
			applyNamespace = res.Namespace
			liveManifest = resolved.Data
		} else {
			// Neither Source, Helm, nor Secret set — resolve by Name via a
			// Resource CRD (cluster mode) or a local resources/<name>.yaml
			// file (local mode), mirroring WorkflowRef.Name's own
			// ChainSource resolution exactly. See validateResourceRef's
			// relaxed "at most one" check, which is what makes this a
			// legitimate zero-set case rather than a validation error.
			manifest, err := r.stateMgr.ResourceSource().GetResource(res.Name)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: resolve failed: %w", res.Name, err)
				break itemLoop
			}
			sum := sha256.Sum256(manifest)
			configHash = fmt.Sprintf("%x", sum)
			applyNamespace = res.Namespace
			liveManifest = manifest
		}

		applied := cluster.Spec.AppliedResources[res.Name]
		configChanged := applied == nil || applied.SourceSHA256 != configHash

		// Skip the diff entirely on a resource's first-ever apply: needsApply
		// already returns true unconditionally whenever configChanged is true
		// (which applied == nil forces), so liveDiff can't change the outcome
		// — and running it anyway is actively harmful, not just wasted work,
		// for a Helm chart whose rendered templates reference a CRD type its
		// own crds/ directory hasn't installed yet (kubectl diff has no
		// install step of its own, so "ensure CRDs are installed first" is
		// unavoidable on a from-scratch helm template render — confirmed live
		// against a real chart). helmUpgradeInstall/kubectlApply below runs
		// the real install, which — for Helm specifically — does install
		// crds/ first, same as a normal `helm install` always has.
		var liveDiff bool
		if applied != nil {
			var err error
			liveDiff, err = kubectlDiff(ctx, repoRoot, env, liveManifest, applyNamespace)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: kubectl diff failed: %w", res.Name, err)
				break itemLoop
			}
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
			if err := helmUpgradeInstall(ctx, repoRoot, env, res.Name, res.Helm, resolvedHelmValues); err != nil {
				loopErr = fmt.Errorf("resource %s: apply failed: %w", res.Name, err)
				break itemLoop
			}
			deployed, err := helmGetManifest(ctx, repoRoot, env, res.Name, res.Helm.Namespace)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: helm get manifest failed: %w", res.Name, err)
				break itemLoop
			}
			objs, err := parseManifestObjects(deployed, res.Helm.Namespace)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: parse applied objects: %w", res.Name, err)
				break itemLoop
			}
			objects = objs
		} else {
			if err := kubectlApply(ctx, repoRoot, env, liveManifest, applyNamespace); err != nil {
				loopErr = fmt.Errorf("resource %s: apply failed: %w", res.Name, err)
				break itemLoop
			}
			objs, err := parseManifestObjects(liveManifest, applyNamespace)
			if err != nil {
				loopErr = fmt.Errorf("resource %s: parse applied objects: %w", res.Name, err)
				break itemLoop
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
	for _, orphan := range findOrphanedResources(specResourceNames(cluster.Spec.Resources, items), heldPrefixes, cluster.Spec.AppliedResources) {
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
			err = helmUninstall(ctx, repoRoot, env, orphan, ar.Namespace)
		} else {
			log.Printf("[%s] Resource %s: orphaned, strictResourceDelete=true — pruning %d tracked object(s)", name, orphan, len(ar.Objects))
			err = kubectlDeleteObjects(ctx, repoRoot, env, ar.Objects)
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

// resourceItem is one entry to reconcile: a spec.resources entry as-is, or
// one file expanded from a directory entry (ref.Name "<dir-name>/<stem>",
// pre holding its already-fetched content). origIdx is the spec.resources
// index it came from.
type resourceItem struct {
	ref     types.ResourceRef
	origIdx int
	pre     *resourceref.ResolvedResource
}

// expandResourceItems turns spec.resources into the items to reconcile,
// expanding each directory source (see resourceref.IsDirSource) into its
// files in name order. It stops at the first directory that fails to
// resolve — returning that error, like the apply loop stops at its first
// error — and heldPrefixes names every directory entry left unexpanded
// (that one and any after it), so the orphan pass never mistakes their
// already-applied children for removed ones and prunes them over a
// transient fetch failure. delete:true entries are passed through
// unexpanded; the apply loop deletes their children by name prefix.
func expandResourceItems(resources []types.ResourceRef, repoRoot, token string) (items []resourceItem, heldPrefixes []string, err error) {
	for i, res := range resources {
		if err != nil {
			if res.Source != "" && resourceref.IsDirSource(res.Source) {
				heldPrefixes = append(heldPrefixes, res.Name)
			}
			continue
		}
		if res.Delete || res.Source == "" || !resourceref.IsDirSource(res.Source) {
			items = append(items, resourceItem{ref: res, origIdx: i})
			continue
		}
		entries, dirErr := resourceref.ResolveDir(res.Source, repoRoot, token)
		if dirErr != nil {
			err = fmt.Errorf("resource %s: resolve directory failed: %w", res.Name, dirErr)
			heldPrefixes = append(heldPrefixes, res.Name)
			continue
		}
		for _, e := range entries {
			child := res
			child.Name = res.Name + "/" + e.Stem
			child.Source = e.Resolved.CanonicalSource
			items = append(items, resourceItem{ref: child, origIdx: i, pre: e.Resolved})
		}
	}
	return items, heldPrefixes, err
}

// appliedKeysFor returns name itself (if applied) plus every applied
// "<name>/..." child, sorted — everything a spec.resources entry named name
// owns, whether it was a single file or a directory.
func appliedKeysFor(applied map[string]*types.AppliedResource, name string) []string {
	var keys []string
	for key := range applied {
		if key == name || strings.HasPrefix(key, name+"/") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// specResourceNames is every name the current spec accounts for: each
// remaining spec.resources entry's own name, plus each expanded directory
// child's name.
func specResourceNames(resources []types.ResourceRef, items []resourceItem) map[string]bool {
	names := make(map[string]bool, len(resources)+len(items))
	for _, r := range resources {
		names[r.Name] = true
	}
	for _, it := range items {
		names[it.ref.Name] = true
	}
	return names
}

// findOrphanedResources returns the sorted set of AppliedResources keys the
// spec no longer accounts for — neither a name in inSpec nor a child of a
// held (unresolved) directory entry. Pure — table-testable.
func findOrphanedResources(inSpec map[string]bool, heldPrefixes []string, applied map[string]*types.AppliedResource) []string {
	var orphans []string
	for name := range applied {
		if inSpec[name] {
			continue
		}
		held := false
		for _, p := range heldPrefixes {
			if strings.HasPrefix(name, p+"/") {
				held = true
				break
			}
		}
		if !held {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	return orphans
}

// validateResourceRef checks that exactly one of Source or Helm is set on a
// non-delete resource entry. Pure — table-testable.
// validateResourceRef requires at most one of Source/Helm/Secret — zero is
// now a legitimate case too (see the resolution loop above): it means
// "resolve by Name" via a Resource CRD or local resources/<name>.yaml file,
// mirroring how a bare WorkflowRef.Name needs neither Source nor any other
// field set either.
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
	if set > 1 {
		return fmt.Errorf("resource %s: at most one of source, helm, or secret may be set", res.Name)
	}
	return nil
}
