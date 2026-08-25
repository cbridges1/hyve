package resourceref

import (
	"fmt"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/types"
)

// NameCollision records that two different remote sources both resolved to
// the same declared Name during Install — `hyve resource install`
// warns about this the same way workflowref.Install's own NameCollision
// does, but the collided value here is the ResourceRef.Name each source
// was referenced under, not a content-derived name (see LockedResource's
// own doc comment for why resources differ from workflows here).
type NameCollision struct {
	Name           string
	FirstSource    string
	CollidedSource string
}

// LockedRef is one remote resource file Install resolved and locked (or
// found already up to date).
type LockedRef struct {
	CanonicalSource string `json:"canonicalSource"`
	RawVersion      string `json:"rawVersion"`
	Name            string `json:"name"`
	SHA256          string `json:"sha256"`
}

// RefResult is the outcome of resolving one ref during Install — populated
// for every ref Install attempts, not just ones whose lock entry changed.
// Mirrors workflowref.RefResult exactly, minus ResolvedVersion:
// resourceref.ResolvedResource has no such field.
type RefResult struct {
	Name            string
	CanonicalSource string
	RawVersion      string
	SHA256          string
	Err             error // nil on success
}

// Install resolves every remote ref (as gathered by GatherResourceRefs)
// into hyve.lock, skipping any file whose content hasn't changed since the
// last install (to avoid a no-op commit). The caller is responsible for
// committing the repo when changed is true. token is an explicit GitHub-
// style access token for a private source; empty falls back to the
// process's own GITHUB_TOKEN env var. Mirrors workflowref.Install, but
// delegates the actual fetch/hash to the existing, cache-aware Resolve
// rather than a resource-specific resolver.
func Install(repoPath string, refs []types.ResourceRef, token string) (locked []LockedRef, collisions []NameCollision, resolveErrors []string, results []RefResult, changed bool, err error) {
	lf, err := module.LoadLockFile(repoPath)
	if err != nil {
		return nil, nil, nil, nil, false, fmt.Errorf("failed to load lock file: %w", err)
	}

	nameOwner := map[string]string{} // name -> first canonical source seen this run
	for _, ref := range refs {
		if !ref.IsRemote() {
			continue
		}
		resolved, resolveErr := Resolve(ref.Source, repoPath, lf, token)
		if resolveErr != nil {
			resolveErrors = append(resolveErrors, fmt.Sprintf("%s: %v", ref.Source, resolveErr))
			results = append(results, RefResult{Name: ref.Name, CanonicalSource: ref.Source, Err: resolveErr})
			continue
		}

		if owner, ok := nameOwner[ref.Name]; ok && owner != resolved.CanonicalSource {
			collisions = append(collisions, NameCollision{Name: ref.Name, FirstSource: owner, CollidedSource: resolved.CanonicalSource})
		}
		nameOwner[ref.Name] = resolved.CanonicalSource

		results = append(results, RefResult{
			Name: ref.Name, CanonicalSource: resolved.CanonicalSource, RawVersion: resolved.RawVersion, SHA256: resolved.SHA256,
		})

		existing := lf.GetLockedResource(resolved.CanonicalSource, resolved.RawVersion)
		if existing != nil && existing.SHA256 == resolved.SHA256 {
			continue // unchanged — don't touch the lock, avoids a no-op commit
		}
		lf.SetLockedResource(resolved.CanonicalSource, resolved.RawVersion, &module.LockedResource{
			Name:     ref.Name,
			Source:   resolved.CanonicalSource,
			Resolved: resolved.Resolved,
			SHA256:   resolved.SHA256,
		})
		locked = append(locked, LockedRef{CanonicalSource: resolved.CanonicalSource, RawVersion: resolved.RawVersion, Name: ref.Name, SHA256: resolved.SHA256})
		changed = true
	}

	if !changed {
		return nil, collisions, resolveErrors, results, false, nil
	}
	if err := module.SaveLockFile(repoPath, lf); err != nil {
		return nil, collisions, resolveErrors, results, false, fmt.Errorf("failed to save lock file: %w", err)
	}
	return locked, collisions, resolveErrors, results, true, nil
}
