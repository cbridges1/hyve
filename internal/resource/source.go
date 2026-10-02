// Package resource resolves a Name-only ResourceRef (see
// types.ResourceRef.Name) into raw manifest bytes — the cluster-mode-native
// counterpart to internal/resourceref's Source-based (local path / remote
// git) resolution. Split into its own package, deliberately separate from
// internal/resourceref, mirroring the existing internal/workflow (this
// package's role) vs internal/workflowref (install/gather, needs
// internal/state) split exactly: internal/state.Manager.ResourceSource
// needs to import this package, and internal/resourceref needs to import
// internal/state (for GatherResourceRefs) — those two facts together would
// be an import cycle if both lived in one package.
package resource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8syaml "sigs.k8s.io/yaml"
)

const (
	ResourcesDir    = "resources"
	ResourceFileExt = ".yaml"

	// ResourceAPIVersion/ResourceKind must match the real Resource CRD
	// exactly (group hyve.io/v1alpha1) — a local resources/<name>.yaml
	// file is real Resource CR YAML, `kubectl apply -f`-able unmodified
	// once the CRD is installed. Mirrors internal/workflow's own
	// WorkflowAPIVersion/WorkflowKind convention.
	ResourceAPIVersion = "hyve.io/v1alpha1"
	ResourceKind       = "Resource"
)

// Source resolves a local-name ResourceRef into raw manifest bytes — the
// one thing that differs between local file mode and cluster mode,
// mirroring internal/workflow.Source exactly. Only consulted for a
// Name-only ResourceRef (Source/Helm/Secret all unset) — no role in
// resolving a remote-git Source, which goes through
// internal/resourceref.Resolve instead.
type Source interface {
	GetResource(name string) ([]byte, error)
}

// FileSource reads resources/<name>.yaml under Dir — today's local-mode
// convention, and controller mode's fallback once ChainSource can't find a
// Resource CR. Mirrors internal/workflow.FileSource exactly.
type FileSource struct {
	Dir string
}

func (s FileSource) GetResource(name string) ([]byte, error) {
	filePath := filepath.Join(s.Dir, name+ResourceFileExt)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("resource '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to read resource file: %w", err)
	}
	return decodeResource(filePath, data)
}

// CRDSource reads a Resource custom resource by name — the controller-mode
// default: resources referenced by Name are supposed to be cluster-native
// objects, not files baked into the controller's container image. Mirrors
// internal/workflow.CRDSource exactly.
type CRDSource struct {
	Client    client.Client
	Namespace string
}

func (s CRDSource) GetResource(name string) ([]byte, error) {
	var cr hyvev1alpha1.Resource
	if err := s.Client.Get(context.Background(), k8stypes.NamespacedName{Namespace: s.Namespace, Name: name}, &cr); err != nil {
		return nil, err
	}
	return []byte(cr.Spec.Manifest), nil
}

// ChainSource tries Primary first and falls back to Fallback only when
// Primary reports the resource doesn't exist there (a real lookup error —
// anything other than "not found" — is returned as-is). Mirrors
// internal/workflow.ChainSource exactly.
type ChainSource struct {
	Primary  Source
	Fallback Source
}

func (s ChainSource) GetResource(name string) ([]byte, error) {
	data, err := s.Primary.GetResource(name)
	if err == nil {
		return data, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	return s.Fallback.GetResource(name)
}

// decodeResource parses a resource file's bytes, validates its
// apiVersion/kind against the real Resource CRD, and returns its manifest
// bytes. Mirrors internal/workflow.decodeWorkflow's own validation.
func decodeResource(path string, data []byte) ([]byte, error) {
	var cr hyvev1alpha1.Resource
	if err := k8syaml.Unmarshal(data, &cr); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if cr.APIVersion != ResourceAPIVersion || cr.Kind != ResourceKind {
		return nil, fmt.Errorf("%s: apiVersion/kind must be %q/%q, got %q/%q",
			path, ResourceAPIVersion, ResourceKind, cr.APIVersion, cr.Kind)
	}
	return []byte(cr.Spec.Manifest), nil
}
