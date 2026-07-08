package module

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/template"
)

// AddModule resolves source@version and locks it into hyve.lock. If an entry
// for the resolved lock version is already present, it is left untouched and
// alreadyLocked is true. The caller is responsible for committing the repo
// (hyve.lock changed on disk when alreadyLocked is false and err is nil).
func AddModule(repoPath, source, version string) (lockVersion string, entry *LockedModule, alreadyLocked bool, err error) {
	lf, err := LoadLockFile(repoPath)
	if err != nil {
		return "", nil, false, fmt.Errorf("failed to load lock file: %w", err)
	}

	resolved, err := Resolve(source, version, nil, repoPath)
	if err != nil {
		return "", nil, false, fmt.Errorf("failed to resolve module: %w", err)
	}

	// Use the canonical resolved version (e.g. "v1.2.3") as the lock key,
	// not the user-supplied version string (which may be "latest" or a constraint).
	lockVersion = resolved.Version
	if lockVersion == "" {
		lockVersion = version
	}

	if existing := lf.GetLocked(source, lockVersion); existing != nil {
		return lockVersion, existing, true, nil
	}

	entry = &LockedModule{
		Source:   source,
		Resolved: resolved.Resolved,
		SHA256:   resolved.SHA256,
		Runner:   resolved.Runner,
	}
	lf.SetLocked(source, lockVersion, entry)
	if err := SaveLockFile(repoPath, lf); err != nil {
		return "", nil, false, fmt.Errorf("failed to save lock file: %w", err)
	}
	return lockVersion, entry, false, nil
}

// UpdateModule forces a re-resolve of an already-locked source@version pair,
// refreshing its SHA256 (and resolved URL) in hyve.lock. The caller is
// responsible for committing the repo afterward.
func UpdateModule(repoPath, source, version string) (*LockedModule, error) {
	lf, err := LoadLockFile(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load lock file: %w", err)
	}

	resolved, err := Resolve(source, version, nil, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve module: %w", err)
	}

	entry := &LockedModule{
		Source:   source,
		Resolved: resolved.Resolved,
		SHA256:   resolved.SHA256,
		Runner:   resolved.Runner,
	}
	lf.SetLocked(source, version, entry)
	if err := SaveLockFile(repoPath, lf); err != nil {
		return nil, fmt.Errorf("failed to save lock file: %w", err)
	}
	return entry, nil
}

// RemoveModule removes a locked source@version entry from hyve.lock. removed
// is false if no such entry existed (in which case hyve.lock is left
// untouched). The caller is responsible for committing the repo afterward.
func RemoveModule(repoPath, source, version string) (removed bool, err error) {
	lf, err := LoadLockFile(repoPath)
	if err != nil {
		return false, fmt.Errorf("failed to load lock file: %w", err)
	}
	if lf.GetLocked(source, version) == nil {
		return false, nil
	}
	lf.RemoveLocked(source, version)
	if err := SaveLockFile(repoPath, lf); err != nil {
		return false, fmt.Errorf("failed to save lock file: %w", err)
	}
	return true, nil
}

// ModuleRef identifies one module a template or cluster references — the
// input shape for InstallModules.
type ModuleRef struct {
	Source  string
	Version string
}

// LockedModuleRef pairs a ModuleRef with the LockedModule entry InstallModules
// resolved and locked for it.
type LockedModuleRef struct {
	ModuleRef
	Entry *LockedModule
}

// GatherModuleRefs collects the deduplicated (source, version) driver
// references from every template and cluster definition in the repo — the
// input InstallModules expects.
func GatherModuleRefs(stateMgr *state.Manager, repoPath string) ([]ModuleRef, error) {
	tmplMgr := template.NewManager(repoPath)
	templates, err := tmplMgr.ListTemplates()
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}

	clusterDefs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster definitions: %w", err)
	}

	seen := map[string]bool{}
	var refs []ModuleRef
	add := func(src, ver string) {
		if src == "" {
			return
		}
		key := LockKey(src, ver)
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, ModuleRef{Source: src, Version: ver})
	}
	for _, t := range templates {
		add(t.Spec.Driver.Source, t.Spec.Driver.Version)
	}
	for _, c := range clusterDefs {
		add(c.Spec.Driver.Source, c.Spec.Driver.Version)
	}
	return refs, nil
}

// InstallModules resolves and locks every module referenced by templates or
// cluster definitions that isn't already in hyve.lock. It returns the newly
// locked entries and the refs that were already locked (both empty if
// hyve.lock was already up to date and nothing needed resolving). The caller
// is responsible for committing the repo when len(locked) > 0.
//
// refs is the deduplicated (source, version) list to consider — the caller
// gathers this from templates.Spec.Driver and clusterDefs.Spec.Driver, since
// that traversal already depends on internal/template and internal/state
// types this package does not import.
func InstallModules(repoPath string, refs []ModuleRef) (locked []LockedModuleRef, alreadyLocked []ModuleRef, resolveErrors []string, err error) {
	lf, err := LoadLockFile(repoPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to load lock file: %w", err)
	}

	updated := false
	for _, r := range refs {
		if r.Source == "" {
			continue
		}
		if lf.GetLocked(r.Source, r.Version) != nil {
			alreadyLocked = append(alreadyLocked, r)
			continue
		}
		resolved, resolveErr := Resolve(r.Source, r.Version, nil, repoPath)
		if resolveErr != nil {
			resolveErrors = append(resolveErrors, fmt.Sprintf("%s@%s: %v", r.Source, r.Version, resolveErr))
			continue
		}
		entry := &LockedModule{
			Source:   r.Source,
			Resolved: resolved.Resolved,
			SHA256:   resolved.SHA256,
			Runner:   resolved.Runner,
		}
		lf.SetLocked(r.Source, r.Version, entry)
		locked = append(locked, LockedModuleRef{ModuleRef: r, Entry: entry})
		updated = true
	}

	if !updated {
		return nil, alreadyLocked, resolveErrors, nil
	}
	if err := SaveLockFile(repoPath, lf); err != nil {
		return nil, alreadyLocked, resolveErrors, fmt.Errorf("failed to save lock file: %w", err)
	}
	return locked, alreadyLocked, resolveErrors, nil
}

// ValidateModule resolves a locked (or local) module and checks it has the
// expected structure: a valid module.yaml, at least one operation file, and
// (if present) a structurally valid auth.yaml. err is non-nil only for
// infrastructure failures (can't load hyve.lock, can't resolve the module);
// structural problems are returned as validation error strings instead.
func ValidateModule(repoPath, source, version string) (validationErrors []string, err error) {
	lf, err := LoadLockFile(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load lock file: %w", err)
	}
	locked := lf.GetLocked(source, version)
	if locked == nil && !IsLocalSource(source) {
		return nil, fmt.Errorf("module %s@%s not in hyve.lock", source, version)
	}
	resolved, err := Resolve(source, version, locked, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve module: %w", err)
	}

	var errs []string
	var manifest ModuleManifest
	manifestPath := filepath.Join(resolved.Dir, "module.yaml")
	authPath := filepath.Join(resolved.Dir, "auth.yaml")
	data, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		errs = append(errs, fmt.Sprintf("missing module.yaml: %v", readErr))
	} else {
		if unmarshalErr := yaml.Unmarshal(data, &manifest); unmarshalErr != nil {
			errs = append(errs, fmt.Sprintf("invalid module.yaml: %v", unmarshalErr))
		} else {
			if manifest.Metadata.Name == "" {
				errs = append(errs, "module.yaml: metadata.name is required")
			}
			if manifest.Metadata.Version == "" {
				errs = append(errs, "module.yaml: metadata.version is required")
			}
		}
	}

	// Check at least one operation file exists
	ops := []string{"create", "delete", "status", "auth", "scale"}
	anyOp := false
	for _, op := range ops {
		for _, ext := range []string{".yaml", ".sh", ""} {
			p := filepath.Join(resolved.Dir, op+ext)
			if _, statErr := os.Stat(p); statErr == nil {
				anyOp = true
				break
			}
		}
	}
	if !anyOp {
		errs = append(errs, "no operation files found (expected create.yaml/create.sh, etc.)")
	}

	// authOnly modules specifically need auth.yaml/auth.sh — give a targeted
	// error instead of relying on the generic anyOp check above (which a
	// create/delete/scale-only module lacking auth would also pass).
	if manifest.Metadata.Type == ModuleTypeAuthOnly {
		hasAuth := false
		for _, ext := range []string{".yaml", ".sh", ""} {
			if _, statErr := os.Stat(filepath.Join(resolved.Dir, "auth"+ext)); statErr == nil {
				hasAuth = true
				break
			}
		}
		if !hasAuth {
			errs = append(errs, "module.yaml declares metadata.type: authOnly but auth.yaml is missing — authOnly modules must provide auth.yaml or auth.sh")
		}
	}

	// Validate auth.yaml if present
	if authData, readErr := os.ReadFile(authPath); readErr == nil {
		var ca ClusterAuth
		if unmarshalErr := yaml.Unmarshal(authData, &ca); unmarshalErr != nil {
			errs = append(errs, fmt.Sprintf("auth.yaml: invalid YAML: %v", unmarshalErr))
		} else if len(ca.Spec.Methods) > 0 {
			validExports := map[string]bool{"": true, "KUBECONFIG": true, "KEEPER_KUBECONFIG": true}
			seen := map[string]bool{}
			for i, m := range ca.Spec.Methods {
				if m.Name == "" {
					errs = append(errs, fmt.Sprintf("auth.yaml: methods[%d]: name is required", i))
				} else if seen[m.Name] {
					errs = append(errs, fmt.Sprintf("auth.yaml: duplicate method name %q", m.Name))
				} else {
					seen[m.Name] = true
				}
				if m.Auth.Script == "" {
					errs = append(errs, fmt.Sprintf("auth.yaml: method %q: auth.script is required", m.Name))
				}
				if !validExports[m.Exports] {
					errs = append(errs, fmt.Sprintf("auth.yaml: method %q: exports must be KUBECONFIG, KEEPER_KUBECONFIG, or empty", m.Name))
				}
			}
		}
	}

	return errs, nil
}

// ModuleInfo resolves a locked (or local) module and returns its parsed
// module.yaml manifest along with the resolved on-disk directory.
func ModuleInfo(repoPath, source, version string) (manifest *ModuleManifest, resolvedDir string, err error) {
	lf, err := LoadLockFile(repoPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load lock file: %w", err)
	}
	locked := lf.GetLocked(source, version)
	if locked == nil && !IsLocalSource(source) {
		return nil, "", fmt.Errorf("module %s@%s not in hyve.lock — run `hyve module add %s %s`", source, version, source, version)
	}
	resolved, err := Resolve(source, version, locked, repoPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve module: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(resolved.Dir, "module.yaml"))
	if err != nil {
		return nil, "", fmt.Errorf("failed to read module.yaml: %w", err)
	}
	var m ModuleManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, "", fmt.Errorf("failed to parse module.yaml: %w", err)
	}
	return &m, resolved.Dir, nil
}

// InitModuleSkeleton scaffolds a new module directory at repoPath/modules/<name>/
// with a starter module.yaml and no-op operation scripts. When authOnly is
// true, it scaffolds only module.yaml (with metadata.type: authOnly) and
// auth.sh — appropriate for a module that only configures kubeconfig access
// to an already-existing, non-provisionable cluster (e.g. k3d) and has no
// create/delete/status/scale of its own.
func InitModuleSkeleton(repoPath, name string, authOnly bool) (dir string, err error) {
	dir = filepath.Join(repoPath, "modules", name)
	if _, statErr := os.Stat(dir); statErr == nil {
		return "", fmt.Errorf("directory %s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create %s: %w", dir, err)
	}

	typeField := ""
	if authOnly {
		typeField = "\n  type: authOnly"
	}
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Module
metadata:
  name: %s
  version: 0.1.0
  description: TODO — describe what this module manages%s
spec:
  params:
    - name: example
      description: Example parameter
      required: false
      default: ""
`, name, typeField)

	writeFile := func(rel, content string) error {
		p := filepath.Join(dir, rel)
		return os.WriteFile(p, []byte(content), 0644)
	}

	var scripts []string
	files := map[string]string{"module.yaml": manifest}
	if authOnly {
		files["auth.sh"] = "#!/bin/sh\nset -e\n" +
			"# TODO — write this cluster's kubeconfig entry, e.g.:\n" +
			"#   k3d kubeconfig merge \"$HYVE_CLUSTER_NAME\" --kubeconfig-merge-default\n" +
			"echo 'auth: no-op'\n"
		scripts = []string{"auth.sh"}
	} else {
		files["create.sh"] = "#!/bin/sh\nset -e\necho 'HYVE_CLUSTER_STATUS=ACTIVE'\n"
		files["delete.sh"] = "#!/bin/sh\nset -e\necho 'HYVE_CLUSTER_STATUS=NOT_FOUND'\n"
		files["status.sh"] = "#!/bin/sh\nset -e\necho 'HYVE_CLUSTER_STATUS=NOT_FOUND'\n"
		files["auth.sh"] = "#!/bin/sh\nset -e\necho 'auth: no-op'\n"
		files["scale.sh"] = "#!/bin/sh\nset -e\necho 'scale: no-op'\n"
		scripts = []string{"create.sh", "delete.sh", "status.sh", "auth.sh", "scale.sh"}
	}

	if err := writeFile("module.yaml", files["module.yaml"]); err != nil {
		return "", fmt.Errorf("failed to write module.yaml: %w", err)
	}
	for _, s := range scripts {
		if err := writeFile(s, files[s]); err != nil {
			return "", fmt.Errorf("failed to write %s: %w", s, err)
		}
		os.Chmod(filepath.Join(dir, s), 0755)
	}
	return dir, nil
}
