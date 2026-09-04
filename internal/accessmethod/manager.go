// Package accessmethod reads AccessMethod objects in local/file mode —
// the local-mode half of the symmetry every other hyve resource has,
// following Template's pattern (internal/template), not Workflow/
// Resource's (reconcile.StateProvider): AccessMethod is never read by
// internal/reconcile.Reconciler, only by `hyve cluster auth` at
// kubeconfig-request time, so it doesn't belong on StateProvider — see
// HYVE-ACCESS-METHOD-DESIGN.md's "Local-mode symmetry" section.
//
// Unlike internal/template, there's no separate in-memory type here:
// AccessMethodSpec (Provider, ServerURL) is simple enough that this
// package operates on hyvev1alpha1.AccessMethod directly, local file or
// CR — Template's toTemplate/fromTemplate indirection exists mostly to
// bridge types.DriverRef-shaped fields Template needs and AccessMethod
// doesn't have.
package accessmethod

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	k8syaml "sigs.k8s.io/yaml"
)

// APIVersion/Kind must match the real AccessMethod CRD exactly (group
// hyve.io/v1alpha1) — a local access-methods/<name>.yaml file is real
// AccessMethod CR YAML, kubectl apply -f-able unmodified once the CRD is
// installed. Mirrors internal/template's identical APIVersion/Kind
// constants and validation.
const (
	APIVersion = "hyve.io/v1alpha1"
	Kind       = "AccessMethod"
)

// Manager reads AccessMethod objects from a local directory — the
// read-only, local-mode counterpart to the small API endpoint cluster mode
// uses for the same lookup. Admins own writes in both modes (kubectl apply,
// or hand-editing a file here); this package never writes.
type Manager struct {
	accessMethodsDir string
}

// NewManager creates a Manager rooted at repoPath/access-methods — a new
// sibling directory next to clusters/, templates/, and workflows/.
func NewManager(repoPath string) *Manager {
	return &Manager{accessMethodsDir: filepath.Join(repoPath, "access-methods")}
}

// decodeAccessMethod parses a file's bytes and validates its apiVersion/
// kind against the real AccessMethod CRD — the same validation
// internal/template's decodeTemplate performs.
func decodeAccessMethod(path string, data []byte) (*hyvev1alpha1.AccessMethod, error) {
	var am hyvev1alpha1.AccessMethod
	if err := k8syaml.Unmarshal(data, &am); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if am.APIVersion != APIVersion || am.Kind != Kind {
		return nil, fmt.Errorf("%s: apiVersion/kind must be %q/%q, got %q/%q",
			path, APIVersion, Kind, am.APIVersion, am.Kind)
	}
	return &am, nil
}

// findAccessMethodFile scans the access-methods directory and returns the
// file path of the AccessMethod whose metadata.name matches name — same
// scan-and-match shape as internal/template's findTemplateFile, including
// not letting one unrelated unreadable file abort the whole scan.
func (m *Manager) findAccessMethodFile(name string) (string, error) {
	entries, err := os.ReadDir(m.accessMethodsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("access method '%s' not found", name)
		}
		return "", fmt.Errorf("failed to read access-methods directory: %w", err)
	}

	var decodeErrs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(m.accessMethodsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			decodeErrs = append(decodeErrs, err)
			continue
		}
		am, err := decodeAccessMethod(path, data)
		if err != nil {
			decodeErrs = append(decodeErrs, err)
			continue
		}
		if am.Name == name {
			return path, nil
		}
	}

	if len(decodeErrs) > 0 {
		msgs := make([]string, len(decodeErrs))
		for i, e := range decodeErrs {
			msgs[i] = "  " + e.Error()
		}
		return "", fmt.Errorf("access method '%s' not found; %d file(s) in %s could not be read:\n%s",
			name, len(decodeErrs), m.accessMethodsDir, strings.Join(msgs, "\n"))
	}
	return "", fmt.Errorf("access method '%s' not found", name)
}

// GetAccessMethod reads an AccessMethod from disk by metadata.name.
func (m *Manager) GetAccessMethod(name string) (*hyvev1alpha1.AccessMethod, error) {
	path, err := m.findAccessMethodFile(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read access method: %w", err)
	}
	return decodeAccessMethod(path, data)
}

// ListAccessMethods lists every AccessMethod under access-methods/ — an
// absent directory is an empty list, not an error, matching
// ListTemplates'/LoadClusterDefinitions' own "missing dir means nothing
// configured yet" convention.
func (m *Manager) ListAccessMethods() ([]*hyvev1alpha1.AccessMethod, error) {
	if _, err := os.Stat(m.accessMethodsDir); os.IsNotExist(err) {
		return []*hyvev1alpha1.AccessMethod{}, nil
	}

	entries, err := os.ReadDir(m.accessMethodsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read access-methods directory: %w", err)
	}

	var result []*hyvev1alpha1.AccessMethod
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(m.accessMethodsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", path, err)
		}
		am, err := decodeAccessMethod(path, data)
		if err != nil {
			return nil, err
		}
		result = append(result, am)
	}
	return result, nil
}
