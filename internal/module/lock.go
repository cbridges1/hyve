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
