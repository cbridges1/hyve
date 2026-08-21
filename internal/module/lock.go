package module

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const lockFileName = "hyve.lock"

func LoadLockFile(repoDir string) (*LockFile, error) {
	path := filepath.Join(repoDir, lockFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &LockFile{Version: 1, Modules: map[string]*LockedModule{}, Workflows: map[string]*LockedWorkflow{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var lf LockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, err
	}
	if lf.Modules == nil {
		lf.Modules = map[string]*LockedModule{}
	}
	if lf.Workflows == nil {
		lf.Workflows = map[string]*LockedWorkflow{}
	}
	return &lf, nil
}

func SaveLockFile(repoDir string, lf *LockFile) error {
	data, err := yaml.Marshal(lf)
	if err != nil {
		return err
	}
	tmp := filepath.Join(repoDir, lockFileName+".tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(repoDir, lockFileName))
}

func LockKey(source, version string) string {
	return source + "@" + version
}

func (lf *LockFile) GetLocked(source, version string) *LockedModule {
	if lf.Modules == nil {
		return nil
	}
	return lf.Modules[LockKey(source, version)]
}

func (lf *LockFile) SetLocked(source, version string, m *LockedModule) {
	if lf.Modules == nil {
		lf.Modules = map[string]*LockedModule{}
	}
	lf.Modules[LockKey(source, version)] = m
}

func (lf *LockFile) RemoveLocked(source, version string) {
	delete(lf.Modules, LockKey(source, version))
}

func (lf *LockFile) GetLockedWorkflow(source, version string) *LockedWorkflow {
	if lf.Workflows == nil {
		return nil
	}
	return lf.Workflows[LockKey(source, version)]
}

func (lf *LockFile) SetLockedWorkflow(source, version string, w *LockedWorkflow) {
	if lf.Workflows == nil {
		lf.Workflows = map[string]*LockedWorkflow{}
	}
	lf.Workflows[LockKey(source, version)] = w
}

func (lf *LockFile) RemoveLockedWorkflow(source, version string) {
	delete(lf.Workflows, LockKey(source, version))
}

// splitLockKey reverses LockKey. Safe because canonical workflow sources and
// version strings never themselves contain "@".
func splitLockKey(key string) (source, version string) {
	idx := strings.LastIndex(key, "@")
	if idx < 0 {
		return key, ""
	}
	return key[:idx], key[idx+1:]
}

// CRName derives a deterministic, valid Kubernetes object name from a
// module ref — same source+version always produces the same name, so
// recording a resolve outcome on a Module CR (see
// internal/controller/reconciler.go) is a plain upsert, never a growing
// pile of near-duplicate objects.
func CRName(source, version string) string {
	raw := strings.ToLower(source + "-" + version)
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 253 {
		name = strings.TrimRight(name[:253], "-")
	}
	if name == "" {
		name = "module"
	}
	return name
}

// EnsureResolved returns source@version's locked entry, resolving and
// persisting it first if it isn't already locked — the mechanism cluster
// mode's controller uses in place of a human running `hyve module install`
// first (see internal/controller/reconciler.go's resolveModuleIfNeeded).
// Local mode does not call this; it keeps requiring an explicit install
// step (see cmd/module/install.go) — see this session's design discussion
// on why the two modes deliberately differ here.
func EnsureResolved(repoPath, source, version string) (*LockedModule, error) {
	return EnsureResolvedWithToken(repoPath, source, version, "")
}

// EnsureResolvedWithToken is EnsureResolved, but with an explicit GitHub
// token to use instead of reading GITHUB_TOKEN from the process environment
// — see ResolveWithToken/resolveGitHubToken. Used by
// internal/controller/reconciler.go's resolveModuleIfNeeded, which fetches
// the token live from hyve-cli-secrets per-reconcile rather than relying on
// a pod-start env var.
func EnsureResolvedWithToken(repoPath, source, version, token string) (*LockedModule, error) {
	lf, err := LoadLockFile(repoPath)
	if err != nil {
		return nil, err
	}
	if locked := lf.GetLocked(source, version); locked != nil {
		return locked, nil
	}
	resolved, err := ResolveWithToken(source, version, nil, repoPath, token)
	if err != nil {
		return nil, err
	}
	entry := &LockedModule{Source: source, Resolved: resolved.Resolved, SHA256: resolved.SHA256, Runner: resolved.Runner}
	lf.SetLocked(source, version, entry)
	if err := SaveLockFile(repoPath, lf); err != nil {
		return nil, err
	}
	return entry, nil
}

// LockedWorkflowMatch pairs a LockedWorkflow with the (source, version) pair
// needed to re-resolve it via workflowref.Resolve.
type LockedWorkflowMatch struct {
	Source  string
	Version string
	Locked  *LockedWorkflow
}

// FindLockedWorkflowsByName returns every locked workflow entry whose
// declared Name matches. Used by `hyve workflow run <name>` after the local
// workflows/ directory lookup misses — multiple results mean the name is
// ambiguous and the caller must require the full source string.
func (lf *LockFile) FindLockedWorkflowsByName(name string) []LockedWorkflowMatch {
	var out []LockedWorkflowMatch
	for key, w := range lf.Workflows {
		if w.Name != name {
			continue
		}
		source, version := splitLockKey(key)
		out = append(out, LockedWorkflowMatch{Source: source, Version: version, Locked: w})
	}
	return out
}
