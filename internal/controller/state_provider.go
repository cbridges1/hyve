package controller

import (
	"context"
	"fmt"
	"path/filepath"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/resource"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
	"github.com/cbridges1/hyve/internal/workflow"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CRDStateProvider implements reconcile.StateProvider against a
// controller-runtime client.Client, backed by ClusterDefinition/HyveConfig
// custom resources instead of a git checkout or plain local directory. It
// is server-side only — used by the controller's own reconcile loop and
// (once it exists) Phase 6's internal/api handlers — never wired into
// cmd/cluster directly; that path is local-mode-only, per Phase 4's design.
type CRDStateProvider struct {
	// Client is the controller-runtime client used for every CR operation
	// below. Its cache is already watch-synced, so LoadClusterDefinitions'
	// client.List needs no separate "sync with remote" step — the interface
	// doesn't have one, by design (see reconcile.StateProvider).
	Client client.Client

	// Namespace is where ClusterDefinition and HyveConfig objects live.
	Namespace string

	// ConfigName is the singleton HyveConfig object's name within
	// Namespace, read by LoadRepoConfig.
	ConfigName string

	// ModulesDirPath is the baked-in modules/hyve.lock/workflows directory
	// root returned by LocalPath() — see HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's
	// "Local cache root problem" section. Populated at controller image
	// build time; not managed by this type at all.
	ModulesDirPath string
}

var _ reconcile.StateProvider = (*CRDStateProvider)(nil)

// LocalPath returns the baked-in directory module resolution, hyve.lock,
// and workflow definitions read from — see ModulesDirPath's doc comment.
func (p *CRDStateProvider) LocalPath() string {
	return p.ModulesDirPath
}

// LoadRepoConfig reads the singleton HyveConfig object named ConfigName in
// Namespace. A missing object returns a default config (Reconcile.Mode:
// local — the only value ReconcileOne's call graph ever actually branches
// on is StrictResourceDelete, so Mode here is a formality, not a real
// control), mirroring state.Manager.LoadRepoConfig's same treatment of a
// missing hyve.yaml.
func (p *CRDStateProvider) LoadRepoConfig() (*state.RepoConfig, error) {
	var cfg hyvev1alpha1.HyveConfig
	err := p.Client.Get(context.Background(), k8stypes.NamespacedName{Namespace: p.Namespace, Name: p.ConfigName}, &cfg)
	if apierrors.IsNotFound(err) {
		return &state.RepoConfig{Reconcile: state.ReconcileConfig{Mode: state.ReconcileModeLocal}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get HyveConfig %s/%s: %w", p.Namespace, p.ConfigName, err)
	}
	return &state.RepoConfig{
		Reconcile: state.ReconcileConfig{
			Mode:                 state.ReconcileModeLocal,
			StrictResourceDelete: cfg.Spec.StrictResourceDelete,
		},
	}, nil
}

// DefaultWorkflowImage reads HyveConfig.spec.defaultWorkflowImage — the
// fallback KubernetesJobStepRunner uses for a job/step with no container:
// of its own. Not part of the reconcile.StateProvider interface (that
// interface is deliberately state-shape-only, no workflow-execution
// concepts); callers needing it (cmd/controller/run.go, when constructing
// the workflow.Executor) call this directly.
func (p *CRDStateProvider) DefaultWorkflowImage(ctx context.Context) (string, error) {
	var cfg hyvev1alpha1.HyveConfig
	err := p.Client.Get(ctx, k8stypes.NamespacedName{Namespace: p.Namespace, Name: p.ConfigName}, &cfg)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get HyveConfig %s/%s: %w", p.Namespace, p.ConfigName, err)
	}
	return cfg.Spec.DefaultWorkflowImage, nil
}

// LoadClusterDefinitions lists every ClusterDefinition in Namespace,
// merging each one's spec+status into the same in-memory shape file mode
// produces by merging a primary file with its state sidecar.
func (p *CRDStateProvider) LoadClusterDefinitions() ([]types.ClusterDefinition, error) {
	var list hyvev1alpha1.ClusterDefinitionList
	if err := p.Client.List(context.Background(), &list, client.InNamespace(p.Namespace)); err != nil {
		return nil, fmt.Errorf("list ClusterDefinitions: %w", err)
	}
	defs := make([]types.ClusterDefinition, len(list.Items))
	for i := range list.Items {
		defs[i] = crdconv.ToTypesClusterDefinition(&list.Items[i])
	}
	return defs, nil
}

// LoadClusterDefinition fetches one ClusterDefinition by name — not part of
// the reconcile.StateProvider interface (state.Manager doesn't expose a
// single-cluster CRD-mode equivalent either; LoadClusterDefinitions is what
// ReconcileOne's callers use), but useful for dependsOn status checks and
// tests without listing every object in the namespace.
func (p *CRDStateProvider) LoadClusterDefinition(ctx context.Context, name string) (*types.ClusterDefinition, error) {
	var cr hyvev1alpha1.ClusterDefinition
	if err := p.Client.Get(ctx, k8stypes.NamespacedName{Namespace: p.Namespace, Name: name}, &cr); err != nil {
		return nil, err
	}
	def := crdconv.ToTypesClusterDefinition(&cr)
	return &def, nil
}

// SaveClusterDefinition persists def's reconciler-owned DriverOutputs/
// AppliedResources via the status subresource only — durable the moment
// the API server accepts it, no separate commit/push step needed or
// possible. def.Spec is deliberately never written back here: unlike file
// mode (where SaveClusterDefinition rewrites the whole primary YAML,
// including any spec-level mutation the reconcile engine made, e.g.
// resources.go pruning a delete:true entry from Spec.Resources), a
// Kubernetes controller conventionally treats .spec as external, user-owned
// input and only ever writes .status — the one place this deviates from
// file mode's literal behavior, and deliberately so, not an oversight. The
// practical consequence: a delete:true resource entry lingers in
// spec.resources after its cleanup runs, rather than being auto-removed —
// harmless (idempotent: the next reconcile finds no matching
// AppliedResources entry and no-ops), just cosmetically different from
// file mode.
func (p *CRDStateProvider) SaveClusterDefinition(def *types.ClusterDefinition) error {
	ctx := context.Background()
	var cr hyvev1alpha1.ClusterDefinition
	if err := p.Client.Get(ctx, k8stypes.NamespacedName{Namespace: p.Namespace, Name: def.Metadata.Name}, &cr); err != nil {
		return fmt.Errorf("get ClusterDefinition %s/%s: %w", p.Namespace, def.Metadata.Name, err)
	}
	driverOutputs, applied := crdconv.FromTypesStatus(def)
	cr.Status.DriverOutputs = driverOutputs
	cr.Status.AppliedResources = applied
	if err := p.Client.Status().Update(ctx, &cr); err != nil {
		return fmt.Errorf("update status for ClusterDefinition %s/%s: %w", p.Namespace, def.Metadata.Name, err)
	}
	return nil
}

// RemoveClusterFile deletes the named ClusterDefinition. Safe to call
// whether or not the object is already pending deletion (a real `kubectl
// delete` sets .metadata.deletionTimestamp and waits on
// hyvev1alpha1.ClusterDefinitionFinalizer — see internal/controller's
// reconciler, which owns adding/removing that finalizer): calling Delete
// again on an object already being deleted is a harmless no-op confirming
// the same request, not a second delete. A NotFound error is treated as
// success, matching state.Manager.RemoveClusterFile's/os.Remove's own
// idempotent-delete convention elsewhere in this codebase.
func (p *CRDStateProvider) RemoveClusterFile(name string) error {
	cr := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.Namespace},
	}
	if err := p.Client.Delete(context.Background(), cr); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete ClusterDefinition %s/%s: %w", p.Namespace, name, err)
	}
	return nil
}

// HasStateSidecar reports whether the named ClusterDefinition's status has
// been populated at least once — the CRD-mode equivalent of file mode's
// cluster-state/<name>.state.yaml existing on disk. Any error (including
// not-found) is treated as "no" — HasStateSidecar's signature has no error
// return, matching state.Manager's own os.Stat-based implementation, which
// makes the same call for the same reason: an absent sidecar and a
// filesystem error are both just "not populated" to every caller of this
// method today (internal/reconcile/resources.go's migration-on-first-write
// check).
func (p *CRDStateProvider) HasStateSidecar(name string) bool {
	var cr hyvev1alpha1.ClusterDefinition
	if err := p.Client.Get(context.Background(), k8stypes.NamespacedName{Namespace: p.Namespace, Name: name}, &cr); err != nil {
		return false
	}
	return len(cr.Status.DriverOutputs) > 0 || len(cr.Status.AppliedResources) > 0
}

// WorkflowSource resolves a local-name WorkflowRef against a live Workflow
// CR first — the CRD-mode default: workflows should be cluster-native
// resources, not files baked into the controller's image — falling back to
// the baked-in ModulesDirPath only when no such CR exists, so an existing
// deployment relying entirely on baked-in workflows/ keeps working
// unmodified after this change.
func (p *CRDStateProvider) WorkflowSource() workflow.Source {
	return workflow.ChainSource{
		Primary:  workflow.CRDSource{Client: p.Client, Namespace: p.Namespace},
		Fallback: workflow.FileSource{Dir: filepath.Join(p.ModulesDirPath, workflow.WorkflowsDir)},
	}
}

// ResourceSource mirrors WorkflowSource exactly, one tier below it: a
// Resource CR takes precedence, falling back to a local resources/<name>.yaml
// file only when no CR by that name exists.
func (p *CRDStateProvider) ResourceSource() resource.Source {
	return resource.ChainSource{
		Primary:  resource.CRDSource{Client: p.Client, Namespace: p.Namespace},
		Fallback: resource.FileSource{Dir: filepath.Join(p.ModulesDirPath, resource.ResourcesDir)},
	}
}
