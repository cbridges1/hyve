// Package migrate implements HYVE-CONTROLLER-ARCHITECTURE-PLAN.md's Phase
// 7 — copying ClusterDefinitions/HyveConfig/HyveAccessBindings from one
// reconcile.StateProvider to another. Both `hyve migrate to-cluster`
// (local/git mode -> cluster mode) and `hyve migrate cluster` (moving the
// primary cluster) share this same orchestration: source and destination
// are interchangeable because state.Manager and
// internal/controller.CRDStateProvider both implement
// reconcile.StateProvider — no new abstraction needed, per the plan's own
// "this is almost free" framing.
package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/controller"
	"github.com/cbridges1/hyve/internal/crdconv"
	"github.com/cbridges1/hyve/internal/credentials"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/template"
	"github.com/cbridges1/hyve/internal/workflow"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BuildClient constructs a controller-runtime client.Client against an
// external kubeconfig path, wrapped with the hyve.io scheme
// CRDStateProvider needs (plus corev1, already on scheme.Scheme, for
// credential Secrets). Registering hyvev1alpha1 onto the shared client-go
// scheme.Scheme mirrors cmd/controller/run.go's own one-line registration
// exactly — safe to call more than once per process (AddToScheme is
// idempotent).
//
// An empty kubeconfigPath resolves the same way a bare kubectl invocation
// does ($KUBECONFIG, else ~/.kube/config) via
// clientcmd.NewDefaultClientConfigLoadingRules — deliberately NOT
// clientcmd.BuildConfigFromFlags("", kubeconfigPath), which looks like it
// should do the same thing but doesn't: with both its arguments empty it
// tries in-cluster config first (never applicable here — every caller of
// BuildClient is a CLI command run by an operator, not something running
// inside a pod), and its post-in-cluster-failure fallback constructs a
// bare &ClientConfigLoadingRules{ExplicitPath: ""} with no search
// Precedence list at all — silently ignoring $KUBECONFIG entirely rather
// than falling through to it. Confirmed live: a valid $KUBECONFIG pointing
// at a real cluster still produced "no configuration has been provided"
// through the old BuildConfigFromFlags call.
func BuildClient(kubeconfigPath string) (client.Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build client config from %s: %w", kubeconfigPath, err)
	}
	if err := hyvev1alpha1.AddToScheme(scheme.Scheme); err != nil {
		return nil, fmt.Errorf("register hyve.io/v1alpha1 scheme: %w", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, fmt.Errorf("build client from %s: %w", kubeconfigPath, err)
	}
	return c, nil
}

// Summary reports what happened per named object — one instance shared
// across a whole migrate run's ClusterDefinitions/HyveConfig/
// AccessBindings calls, matching Phase 7's "per-cluster summary
// (created / skipped-already-exists / failed) rather than failing the
// whole run on one bad definition" requirement.
type Summary struct {
	Created []string
	Skipped []string
	Failed  map[string]error
}

func newSummary() *Summary {
	return &Summary{Failed: map[string]error{}}
}

// OK reports whether nothing failed — callers use this to decide the
// process exit code without inspecting Failed directly.
func (s *Summary) OK() bool {
	return len(s.Failed) == 0
}

// ClusterDefinitions copies every ClusterDefinition source has into dest's
// namespace. dryRun lists what would happen without writing anything.
// Without force, an already-existing destination object is skipped, not
// overwritten — Phase 7's own stated default ("refuses to overwrite...
// unless --force is passed"). Status subresource fields (driverOutputs,
// appliedResources) are only ever set via CRDStateProvider.SaveClusterDefinition
// (a second, separate write — the ClusterDefinition CRD has the status
// subresource enabled, so a plain Create ignores whatever .status a
// caller sets on the object; this is not an oversight in this file, it's
// how Kubernetes CRDs with subresources: {status: {}} always behave), the
// same status-only contract that method already documents for the
// controller's own use — reused here for exactly the reason it exists,
// not repurposed.
func ClusterDefinitions(ctx context.Context, source reconcile.StateProvider, dest client.Client, destNamespace string, dryRun, force bool) (*Summary, error) {
	defs, err := source.LoadClusterDefinitions()
	if err != nil {
		return nil, fmt.Errorf("load source cluster definitions: %w", err)
	}

	summary := newSummary()
	destProvider := &controller.CRDStateProvider{Client: dest, Namespace: destNamespace}

	for i := range defs {
		def := defs[i]
		name := def.Metadata.Name

		exists, err := objectExists(ctx, dest, k8stypes.NamespacedName{Namespace: destNamespace, Name: name}, &hyvev1alpha1.ClusterDefinition{})
		if err != nil {
			summary.Failed[name] = err
			continue
		}
		if exists && !force {
			summary.Skipped = append(summary.Skipped, name)
			continue
		}
		if dryRun {
			// A real Get already ran above — this reports what would
			// ACTUALLY happen (create, or force-overwrite), not a blind
			// guess: an existing object without --force was already
			// routed to Skipped, not here.
			summary.Created = append(summary.Created, name)
			continue
		}

		cr := &hyvev1alpha1.ClusterDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: destNamespace},
			Spec:       crdconv.FromTypesClusterDefinitionSpec(&def),
		}
		if _, err := createOrUpdateSpec(ctx, dest, cr, force, func(existing *hyvev1alpha1.ClusterDefinition) {
			existing.Spec = cr.Spec
		}); err != nil {
			summary.Failed[name] = err
			continue
		}

		// Status (driverOutputs/appliedResources) is what lets the
		// destination's controller act on this cluster (delete/auth)
		// without mistaking it for brand new and re-running create.yaml —
		// preserving it verbatim is the whole point of migrating an
		// already-provisioned cluster, not an optional extra step.
		if err := destProvider.SaveClusterDefinition(&def); err != nil {
			summary.Failed[name] = fmt.Errorf("copy status: %w", err)
			continue
		}
		summary.Created = append(summary.Created, name)
	}
	return summary, nil
}

// objectExists Gets key into obj and reports whether it exists, treating
// only NotFound as "doesn't exist" — any other error (RBAC, network) is
// surfaced to the caller rather than silently treated as "safe to create",
// which matters most in dry-run mode: a masked permissions error there
// would otherwise print a false "would create" for an object the caller
// actually has no visibility into at all.
func objectExists(ctx context.Context, c client.Client, key k8stypes.NamespacedName, obj client.Object) (bool, error) {
	err := c.Get(ctx, key, obj)
	switch {
	case err == nil:
		return true, nil
	case apierrors.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("check existing %s: %w", key.Name, err)
	}
}

// HyveConfig migrates the source's singleton RepoConfig into a HyveConfig
// CR — only the fields with a controller-mode analogue carry over
// (StrictResourceDelete; see HyveConfigSpec's own doc comment for why
// Reconcile.Mode and Env.File don't). Never overwrites an existing
// HyveConfig object — mirrors deploy/helm/hyve's own
// controller.hyveConfig.create semantics ("this chart never overwrites an
// existing HyveConfig object it doesn't own"), the same stance applied
// here for the same reason.
func HyveConfig(ctx context.Context, source reconcile.StateProvider, dest client.Client, destNamespace, destConfigName string, dryRun bool) (skipped bool, err error) {
	cfg, err := source.LoadRepoConfig()
	if err != nil {
		return false, fmt.Errorf("load source repo config: %w", err)
	}

	exists, err := objectExists(ctx, dest, k8stypes.NamespacedName{Namespace: destNamespace, Name: destConfigName}, &hyvev1alpha1.HyveConfig{})
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}
	if dryRun {
		return false, nil
	}

	hc := &hyvev1alpha1.HyveConfig{
		ObjectMeta: metav1.ObjectMeta{Name: destConfigName, Namespace: destNamespace},
		Spec:       hyvev1alpha1.HyveConfigSpec{StrictResourceDelete: cfg.Reconcile.StrictResourceDelete},
	}
	if err := dest.Create(ctx, hc); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return true, nil
		}
		return false, fmt.Errorf("create HyveConfig %s/%s: %w", destNamespace, destConfigName, err)
	}
	return false, nil
}

// AccessBindings previously copied every HyveAccessBinding CRD (+ its
// paired credentials Secret) from namespace on source to dest — used only
// by `hyve migrate cluster` (moving the primary/control plane), gated
// behind --skip-access-bindings.
//
// As of HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 4,
// bindings are Postgres/SQLite rows (internal/orgdb), not CRDs — copying
// them now means copying rows between source's and dest's own,
// independent databases, not CRDs between two kubeconfigs, which this
// package (built entirely around client.Client/reconcile.StateProvider)
// isn't structured for. That cross-database copy is real, scoped, future
// work — deliberately not attempted here rather than rushed; see
// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's own "Documentation
// updates" section for the same "deferred, not silently" stance already
// taken for the CLI --env flags this same milestone left undone.
//
// This function is kept, narrowed to what's still real and
// Kubernetes-native: copying every namespace-scoped Secret named
// "*-credentials" (see credentials.UserCredentialsSecretName) verbatim —
// harmless on its own (an orphaned credentials Secret with no matching
// binding row is inert, exactly like the "no binding but a Secret exists"
// case handleDeleteAccount already tolerates), and saves a re-provisioned
// account from needing a new password chosen for it if the same username
// gets recreated on the destination. The binding (the actual access
// grant) itself does not get recreated — every account needs
// `hyve cluster-config api create-user`/`POST /accounts` run again on the
// destination.
func AccessBindings(ctx context.Context, source, dest client.Client, namespace string, dryRun, force bool) (*Summary, error) {
	var list corev1.SecretList
	if err := source.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list source credentials secrets: %w", err)
	}

	summary := newSummary()
	for i := range list.Items {
		secret := &list.Items[i]
		if !strings.HasSuffix(secret.Name, credentials.UserCredentialsSecretSuffix) {
			continue
		}
		name := secret.Name

		exists, err := objectExists(ctx, dest, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &corev1.Secret{})
		if err != nil {
			summary.Failed[name] = err
			continue
		}
		if exists && !force {
			summary.Skipped = append(summary.Skipped, name)
			continue
		}
		if dryRun {
			summary.Created = append(summary.Created, name)
			continue
		}

		bindingName := strings.TrimSuffix(name, credentials.UserCredentialsSecretSuffix)
		if err := copyCredentialsSecret(ctx, source, dest, namespace, bindingName, force); err != nil {
			summary.Failed[name] = err
			continue
		}
		summary.Created = append(summary.Created, name)
	}
	return summary, nil
}

func createOrUpdateSpec(ctx context.Context, dest client.Client, cr *hyvev1alpha1.ClusterDefinition, force bool, applySpec func(*hyvev1alpha1.ClusterDefinition)) (created bool, err error) {
	err = dest.Create(ctx, cr)
	switch {
	case err == nil:
		return true, nil
	case apierrors.IsAlreadyExists(err) && force:
		var existing hyvev1alpha1.ClusterDefinition
		key := k8stypes.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}
		if getErr := dest.Get(ctx, key, &existing); getErr != nil {
			return false, fmt.Errorf("get existing %s for --force update: %w", cr.Name, getErr)
		}
		applySpec(&existing)
		if updErr := dest.Update(ctx, &existing); updErr != nil {
			return false, fmt.Errorf("update existing %s for --force: %w", cr.Name, updErr)
		}
		return true, nil
	case apierrors.IsAlreadyExists(err):
		return false, nil
	default:
		return false, fmt.Errorf("create %s: %w", cr.Name, err)
	}
}

// copyCredentialsSecret copies bindingName's paired credentials Secret
// verbatim (whatever Data keys it has — no assumption about the bcrypt
// hash's exact key name baked in here) rather than failing the whole
// binding migration when it's missing (an operator-created binding via
// `hyve cluster-config api create-user` might legitimately have no
// matching Secret yet, e.g. mid-provisioning).
func copyCredentialsSecret(ctx context.Context, source, dest client.Client, namespace, bindingName string, force bool) error {
	secretName := credentials.UserCredentialsSecretName(bindingName)
	var secret corev1.Secret
	if err := source.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: secretName}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read credentials secret %s: %w", secretName, err)
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
		Type:       secret.Type,
		Data:       secret.Data,
	}
	err := dest.Create(ctx, newSecret)
	switch {
	case err == nil:
		return nil
	case apierrors.IsAlreadyExists(err) && force:
		var existing corev1.Secret
		key := k8stypes.NamespacedName{Namespace: namespace, Name: secretName}
		if getErr := dest.Get(ctx, key, &existing); getErr != nil {
			return fmt.Errorf("get existing credentials secret %s for --force update: %w", secretName, getErr)
		}
		existing.Data = secret.Data
		existing.Type = secret.Type
		if updErr := dest.Update(ctx, &existing); updErr != nil {
			return fmt.Errorf("update existing credentials secret %s for --force: %w", secretName, updErr)
		}
		return nil
	case apierrors.IsAlreadyExists(err):
		return nil // already-exists binding case above already recorded the skip
	default:
		return fmt.Errorf("create credentials secret %s: %w", secretName, err)
	}
}

// convertViaJSON round-trips src (a local-format Spec, e.g.
// template.TemplateSpec/workflow.WorkflowSpec) through JSON into dst (the
// corresponding CRD Spec type) — the same trick cmd/migrate.go's own
// migrateTemplates/migrateWorkflows already rely on when they marshal a
// local Spec and let the API's create handler unmarshal the bytes directly
// into its CRD Spec field (see internal/api/templates.go's
// createTemplateRequest). Confirms the two shapes are already JSON-
// compatible in practice; used here to avoid a second, hand-written
// field-by-field converter for what's already a proven-safe conversion.
func convertViaJSON(src, dst interface{}) error {
	b, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return nil
}

// TemplatesFromDir copies every Template a local directory (templates/) has
// into dest's namespace — client.Client-direct, no API/login round-trip,
// used by `to-cluster` (bootstrapping a target cluster from local files).
// See HYVE-MULTI-TENANCY-PLAN.md's "Bootstrap and migration flow" section
// for why this exists: to-cluster previously had no way to carry Templates/
// Workflows at all, only ClusterDefinition/HyveConfig.
func TemplatesFromDir(ctx context.Context, localPath string, dest client.Client, destNamespace string, dryRun, force bool) (*Summary, error) {
	mgr := template.NewManager(localPath)
	tpls, err := mgr.ListTemplates()
	if err != nil {
		return nil, fmt.Errorf("list local templates: %w", err)
	}
	summary := newSummary()
	for _, tpl := range tpls {
		name := tpl.Metadata.Name
		var spec hyvev1alpha1.TemplateSpec
		if err := convertViaJSON(tpl.Spec, &spec); err != nil {
			summary.Failed[name] = fmt.Errorf("convert template %q: %w", name, err)
			continue
		}
		if err := createOrSkipTemplate(ctx, dest, name, destNamespace, spec, nil, dryRun, force, summary); err != nil {
			summary.Failed[name] = err
		}
	}
	return summary, nil
}

// WorkflowsFromDir is TemplatesFromDir's workflow equivalent — see its own
// doc comment.
func WorkflowsFromDir(ctx context.Context, localPath string, dest client.Client, destNamespace string, dryRun, force bool) (*Summary, error) {
	mgr, err := workflow.NewManager(localPath)
	if err != nil {
		return nil, fmt.Errorf("create local workflow manager: %w", err)
	}
	wfs, err := mgr.ListWorkflows()
	if err != nil {
		return nil, fmt.Errorf("list local workflows: %w", err)
	}
	summary := newSummary()
	for _, wf := range wfs {
		name := wf.Metadata.Name
		var spec hyvev1alpha1.WorkflowSpec
		if err := convertViaJSON(wf.Spec, &spec); err != nil {
			summary.Failed[name] = fmt.Errorf("convert workflow %q: %w", name, err)
			continue
		}
		if err := createOrSkipWorkflow(ctx, dest, name, destNamespace, spec, nil, dryRun, force, summary); err != nil {
			summary.Failed[name] = err
		}
	}
	return summary, nil
}

// Templates copies every Template CRD source has (in namespace) onto dest
// — cluster-to-cluster, used by `migrate cluster` (moving the primary/host
// cluster, which needs the same Templates the old one had, not just tenant
// ClusterDefinition/HyveConfig/HyveAccessBinding data) and by Milestone 6's
// PATCH /organizations/{name} reconciling-cluster migration.
// list.Items[i].Labels is carried through unchanged — most notably
// HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's Milestone 3
// hyve.io/environment label, whose loss during a Milestone 6 migration
// would silently break environment-scoped naming/lookup on the
// destination. Confirmed live: this was missing entirely before, since
// both this function and createOrSkipTemplate only ever set Name/Namespace
// on the destination object.
func Templates(ctx context.Context, source, dest client.Client, namespace string, dryRun, force bool) (*Summary, error) {
	var list hyvev1alpha1.TemplateList
	if err := source.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list source templates: %w", err)
	}
	summary := newSummary()
	for i := range list.Items {
		name := list.Items[i].Name
		if err := createOrSkipTemplate(ctx, dest, name, namespace, list.Items[i].Spec, list.Items[i].Labels, dryRun, force, summary); err != nil {
			summary.Failed[name] = err
		}
	}
	return summary, nil
}

// Workflows is Templates' workflow equivalent — see its own doc comment,
// including the label-preservation note.
func Workflows(ctx context.Context, source, dest client.Client, namespace string, dryRun, force bool) (*Summary, error) {
	var list hyvev1alpha1.WorkflowList
	if err := source.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list source workflows: %w", err)
	}
	summary := newSummary()
	for i := range list.Items {
		name := list.Items[i].Name
		if err := createOrSkipWorkflow(ctx, dest, name, namespace, list.Items[i].Spec, list.Items[i].Labels, dryRun, force, summary); err != nil {
			summary.Failed[name] = err
		}
	}
	return summary, nil
}

// Resources is Templates'/Workflows' Resource equivalent — see Templates'
// own doc comment, including the label-preservation note. Added alongside
// Milestone 6's PATCH /organizations/{name} reconciling-cluster migration
// (HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md), which needed it and
// discovered it had never existed at all: neither `hyve migrate cluster`
// nor any other path in this package ever copied Resource CRs — a
// pre-existing gap, not introduced by this change, now closed for both
// callers (this function is also wired into cmd/migrate_cluster.go
// alongside Templates/Workflows).
func Resources(ctx context.Context, source, dest client.Client, namespace string, dryRun, force bool) (*Summary, error) {
	var list hyvev1alpha1.ResourceList
	if err := source.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list source resources: %w", err)
	}
	summary := newSummary()
	for i := range list.Items {
		name := list.Items[i].Name
		if err := createOrSkipResource(ctx, dest, name, namespace, list.Items[i].Spec, list.Items[i].Labels, dryRun, force, summary); err != nil {
			summary.Failed[name] = err
		}
	}
	return summary, nil
}

func createOrSkipResource(ctx context.Context, dest client.Client, name, namespace string, spec hyvev1alpha1.ResourceSpec, labels map[string]string, dryRun, force bool, summary *Summary) error {
	exists, err := objectExists(ctx, dest, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &hyvev1alpha1.Resource{})
	if err != nil {
		return err
	}
	if exists && !force {
		summary.Skipped = append(summary.Skipped, name)
		return nil
	}
	if dryRun {
		summary.Created = append(summary.Created, name)
		return nil
	}
	cr := &hyvev1alpha1.Resource{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}, Spec: spec}
	err = dest.Create(ctx, cr)
	switch {
	case err == nil:
		summary.Created = append(summary.Created, name)
		return nil
	case apierrors.IsAlreadyExists(err) && force:
		var existing hyvev1alpha1.Resource
		if getErr := dest.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &existing); getErr != nil {
			return fmt.Errorf("get existing resource %s for --force update: %w", name, getErr)
		}
		existing.Spec = spec
		existing.Labels = labels
		if updErr := dest.Update(ctx, &existing); updErr != nil {
			return fmt.Errorf("update existing resource %s for --force: %w", name, updErr)
		}
		summary.Created = append(summary.Created, name)
		return nil
	case apierrors.IsAlreadyExists(err):
		summary.Skipped = append(summary.Skipped, name)
		return nil
	default:
		return fmt.Errorf("create resource %s: %w", name, err)
	}
}

func createOrSkipTemplate(ctx context.Context, dest client.Client, name, namespace string, spec hyvev1alpha1.TemplateSpec, labels map[string]string, dryRun, force bool, summary *Summary) error {
	exists, err := objectExists(ctx, dest, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &hyvev1alpha1.Template{})
	if err != nil {
		return err
	}
	if exists && !force {
		summary.Skipped = append(summary.Skipped, name)
		return nil
	}
	if dryRun {
		summary.Created = append(summary.Created, name)
		return nil
	}
	cr := &hyvev1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}, Spec: spec}
	err = dest.Create(ctx, cr)
	switch {
	case err == nil:
		summary.Created = append(summary.Created, name)
		return nil
	case apierrors.IsAlreadyExists(err) && force:
		var existing hyvev1alpha1.Template
		if getErr := dest.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &existing); getErr != nil {
			return fmt.Errorf("get existing template %s for --force update: %w", name, getErr)
		}
		existing.Spec = spec
		existing.Labels = labels
		if updErr := dest.Update(ctx, &existing); updErr != nil {
			return fmt.Errorf("update existing template %s for --force: %w", name, updErr)
		}
		summary.Created = append(summary.Created, name)
		return nil
	case apierrors.IsAlreadyExists(err):
		summary.Skipped = append(summary.Skipped, name)
		return nil
	default:
		return fmt.Errorf("create template %s: %w", name, err)
	}
}

func createOrSkipWorkflow(ctx context.Context, dest client.Client, name, namespace string, spec hyvev1alpha1.WorkflowSpec, labels map[string]string, dryRun, force bool, summary *Summary) error {
	exists, err := objectExists(ctx, dest, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &hyvev1alpha1.Workflow{})
	if err != nil {
		return err
	}
	if exists && !force {
		summary.Skipped = append(summary.Skipped, name)
		return nil
	}
	if dryRun {
		summary.Created = append(summary.Created, name)
		return nil
	}
	cr := &hyvev1alpha1.Workflow{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}, Spec: spec}
	err = dest.Create(ctx, cr)
	switch {
	case err == nil:
		summary.Created = append(summary.Created, name)
		return nil
	case apierrors.IsAlreadyExists(err) && force:
		var existing hyvev1alpha1.Workflow
		if getErr := dest.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &existing); getErr != nil {
			return fmt.Errorf("get existing workflow %s for --force update: %w", name, getErr)
		}
		existing.Spec = spec
		existing.Labels = labels
		if updErr := dest.Update(ctx, &existing); updErr != nil {
			return fmt.Errorf("update existing workflow %s for --force: %w", name, updErr)
		}
		summary.Created = append(summary.Created, name)
		return nil
	case apierrors.IsAlreadyExists(err):
		summary.Skipped = append(summary.Skipped, name)
		return nil
	default:
		return fmt.Errorf("create workflow %s: %w", name, err)
	}
}

// Tenant enumeration for `migrate cluster --namespace hyve-system` moved to
// cmd/migrate_cluster.go, querying the source host's own internal/orgdb
// Store directly, as of HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md's
// Milestone 2 — organizations are Postgres/SQLite rows now, not
// HyveEnvironment CRDs, so this package (which only ever talks to a
// client.Client) isn't the right layer for that lookup any more. See this
// package's own git history for the CRD-based version this replaced.
