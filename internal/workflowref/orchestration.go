package workflowref

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/types"
)

// NameCollision records that two different remote sources both resolved to
// the same bare workflow name during Install — `hyve workflow run <name>`
// will need the full source string in that case.
type NameCollision struct {
	Name           string
	FirstSource    string
	CollidedSource string
}

// LockedRef is one remote workflow file Install resolved and locked (or found
// already up to date).
type LockedRef struct {
	CanonicalSource string `json:"canonicalSource"`
	RawVersion      string `json:"rawVersion"`
	Name            string `json:"name"`
	SHA256          string `json:"sha256"`
}

// RefResult is the outcome of resolving one ref during Install — populated
// for every ref Install attempts, unlike LockedRef (only refs whose lock
// entry actually changed this call) or resolveErrors (a pre-formatted
// string, not machine-usable). This is what lets a caller mirror current
// status for every ref on every call, including the common steady-state
// case where nothing changed (see internal/controller/reconciler.go's
// resolveWorkflowIfNeeded, the reason this exists at all).
type RefResult struct {
	Name            string
	CanonicalSource string
	RawVersion      string
	ResolvedVersion string // concrete resolved ref (tag/branch/HEAD); "" on a cache-hit resolve
	SHA256          string
	Err             error // nil on success
}

// GatherWorkflowRefs collects the deduplicated remote WorkflowRefs referenced
// by every template's and cluster's lifecycle hooks in the repo — the input
// Install expects.
func GatherWorkflowRefs(stateMgr *state.Manager, repoPath string) ([]types.WorkflowRef, error) {
	tmplMgr := template.NewManager(repoPath)
	templates, err := tmplMgr.ListTemplates()
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}

	clusterDefs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster definitions: %w", err)
	}

	type key struct{ source, path string }
	seen := map[key]bool{}
	var refs []types.WorkflowRef
	add := func(list []types.WorkflowRef) {
		for _, r := range list {
			if !r.IsRemote() {
				continue
			}
			k := key{r.Source, r.Path}
			if seen[k] {
				continue
			}
			seen[k] = true
			refs = append(refs, r)
		}
	}
	for _, t := range templates {
		add(t.Spec.Workflows.BeforeCreate)
		add(t.Spec.Workflows.OnCreate)
		add(t.Spec.Workflows.AfterCreate)
		add(t.Spec.Workflows.OnDelete)
		add(t.Spec.Workflows.AfterDelete)
		add(t.Spec.Workflows.PreReconcile)
	}
	for _, c := range clusterDefs {
		add(c.Spec.Workflows.PreReconcile)
		add(c.Spec.Workflows.BeforeCreate)
		add(c.Spec.Workflows.OnCreate)
		add(c.Spec.Workflows.OnDelete)
		add(c.Spec.Workflows.AfterDelete)
	}
	return refs, nil
}

// Install resolves every ref (as gathered by GatherWorkflowRefs) into
// hyve.lock, skipping any file whose content hasn't changed since the last
// install (to avoid a no-op commit). The caller is responsible for
// committing the repo when changed is true. token is an explicit GitHub-
// style access token for a private source (see Resolve's own doc comment);
// empty falls back to the process's own GITHUB_TOKEN env var.
func Install(repoPath string, refs []types.WorkflowRef, token string) (locked []LockedRef, collisions []NameCollision, resolveErrors []string, results []RefResult, changed bool, err error) {
	lf, err := module.LoadLockFile(repoPath)
	if err != nil {
		return nil, nil, nil, nil, false, fmt.Errorf("failed to load lock file: %w", err)
	}

	nameOwner := map[string]string{} // name -> first canonical source seen this run
	for _, ref := range refs {
		files, resolveErr := Resolve(ref.Source, ref.Path, lf, token)
		if resolveErr != nil {
			resolveErrors = append(resolveErrors, fmt.Sprintf("%s: %v", ref.String(), resolveErr))
			results = append(results, RefResult{CanonicalSource: ref.Source, Err: resolveErr})
			continue
		}
		for _, f := range files {
			if owner, ok := nameOwner[f.Name]; ok && owner != f.CanonicalSource {
				collisions = append(collisions, NameCollision{Name: f.Name, FirstSource: owner, CollidedSource: f.CanonicalSource})
			}
			nameOwner[f.Name] = f.CanonicalSource

			results = append(results, RefResult{
				Name: f.Name, CanonicalSource: f.CanonicalSource, RawVersion: f.RawVersion,
				ResolvedVersion: f.ResolvedVersion, SHA256: f.SHA256,
			})

			existing := lf.GetLockedWorkflow(f.CanonicalSource, f.RawVersion)
			if existing != nil && existing.SHA256 == f.SHA256 {
				continue // unchanged — don't touch the lock, avoids a no-op commit
			}
			lf.SetLockedWorkflow(f.CanonicalSource, f.RawVersion, &module.LockedWorkflow{
				Name:     f.Name,
				Source:   f.CanonicalSource,
				Resolved: f.Resolved,
				SHA256:   f.SHA256,
			})
			locked = append(locked, LockedRef{CanonicalSource: f.CanonicalSource, RawVersion: f.RawVersion, Name: f.Name, SHA256: f.SHA256})
			changed = true
		}
	}

	if !changed {
		return nil, collisions, resolveErrors, results, false, nil
	}
	if err := module.SaveLockFile(repoPath, lf); err != nil {
		return nil, collisions, resolveErrors, results, false, fmt.Errorf("failed to save lock file: %w", err)
	}
	return locked, collisions, resolveErrors, results, true, nil
}

// Update forces a full re-resolve (bypassing the lock-file cache hint) of one
// source, refreshing hyve.lock entries for every file it resolves to. The
// caller is responsible for committing the repo afterward.
func Update(repoPath, source, pathOverride, token string) ([]LockedRef, error) {
	lf, err := module.LoadLockFile(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load lock file: %w", err)
	}

	// nil lf forces a full re-resolve, bypassing any cache short-circuit.
	files, err := Resolve(source, pathOverride, nil, token)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workflow: %w", err)
	}

	var updated []LockedRef
	for _, f := range files {
		lf.SetLockedWorkflow(f.CanonicalSource, f.RawVersion, &module.LockedWorkflow{
			Name:     f.Name,
			Source:   f.CanonicalSource,
			Resolved: f.Resolved,
			SHA256:   f.SHA256,
		})
		updated = append(updated, LockedRef{CanonicalSource: f.CanonicalSource, RawVersion: f.RawVersion, Name: f.Name, SHA256: f.SHA256})
	}

	if err := module.SaveLockFile(repoPath, lf); err != nil {
		return nil, fmt.Errorf("failed to save lock file: %w", err)
	}
	return updated, nil
}

// VerifyResult reports the verification outcome for one locked workflow entry.
type VerifyResult struct {
	Key    string `json:"key"` // hyve.lock key ("source@version")
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"` // set when OK is false
}

// Verify checks that every locked workflow's cached content still matches its
// recorded sha256.
func Verify(repoPath string) (results []VerifyResult, failed int, err error) {
	lf, err := module.LoadLockFile(repoPath)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to load lock file: %w", err)
	}

	for key, w := range lf.Workflows {
		if w.SHA256 == "" || !IsCached(w.SHA256) {
			results = append(results, VerifyResult{Key: key, Name: w.Name, OK: false, Reason: "not cached — run `hyve workflow install`"})
			failed++
			continue
		}
		data, readErr := ReadCached(w.SHA256)
		if readErr != nil {
			results = append(results, VerifyResult{Key: key, Name: w.Name, OK: false, Reason: fmt.Sprintf("failed to read cache: %v", readErr)})
			failed++
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != w.SHA256 {
			results = append(results, VerifyResult{Key: key, Name: w.Name, OK: false, Reason: "cached content does not match sha256 (corrupted cache)"})
			failed++
			continue
		}
		results = append(results, VerifyResult{Key: key, Name: w.Name, OK: true})
	}

	return results, failed, nil
}
