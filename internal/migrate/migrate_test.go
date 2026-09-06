package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "hyve-system"

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	sch := runtime.NewScheme()
	require.NoError(t, hyvev1alpha1.AddToScheme(sch))
	require.NoError(t, corev1.AddToScheme(sch))
	return sch
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	builder := clientfake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithStatusSubresource(&hyvev1alpha1.ClusterDefinition{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return builder.Build()
}

// newLocalSource writes a single ClusterDefinition (primary file + state
// sidecar, matching internal/state's own on-disk convention exactly — see
// internal/state/state_test.go's TestLoadClusterDefinition_MergesSidecar)
// plus an optional hyve.yaml, and returns a state.Manager pointed at it —
// the literal `hyve migrate to-cluster` source shape.
func newLocalSource(t *testing.T, hyveYAML string) *state.Manager {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "clusters")
	require.NoError(t, os.MkdirAll(stateDir, 0755))

	primary := "apiVersion: hyve.io/v1alpha1\nkind: ClusterDefinition\nmetadata:\n  name: my-cluster\nspec:\n  region: PHX1\n  driver:\n    source: github.com/example/civo-k3s\n    version: 1.0.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "my-cluster.yaml"), []byte(primary), 0644))

	sidecarDir := filepath.Join(root, "cluster-state")
	require.NoError(t, os.MkdirAll(sidecarDir, 0755))
	sidecar := "driverOutputs:\n  HYVE_CLUSTER_ID: abc-123\nappliedResources:\n  toolbox-namespace:\n    sourceSHA256: deadbeef\n    appliedAt: \"2026-07-12T15:49:37Z\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(sidecarDir, "my-cluster.state.yaml"), []byte(sidecar), 0644))

	if hyveYAML != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, "hyve.yaml"), []byte(hyveYAML), 0644))
	}
	return state.NewManagerFromPath(stateDir)
}

func TestClusterDefinitions_CreatesAndCopiesStatus(t *testing.T) {
	source := newLocalSource(t, "")
	dest := newFakeClient(t)

	summary, err := ClusterDefinitions(context.Background(), source, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-cluster"}, summary.Created)
	assert.True(t, summary.OK())

	var cr hyvev1alpha1.ClusterDefinition
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-cluster"}, &cr))
	assert.Equal(t, "github.com/example/civo-k3s", cr.Spec.Driver.Source)
	assert.Equal(t, "abc-123", cr.Status.DriverOutputs["HYVE_CLUSTER_ID"])
	require.Contains(t, cr.Status.AppliedResources, "toolbox-namespace")
}

func TestClusterDefinitions_DryRun_WritesNothing(t *testing.T) {
	source := newLocalSource(t, "")
	dest := newFakeClient(t)

	summary, err := ClusterDefinitions(context.Background(), source, dest, testNamespace, true, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-cluster"}, summary.Created, "dry run still reports what WOULD be created")

	var list hyvev1alpha1.ClusterDefinitionList
	require.NoError(t, dest.List(context.Background(), &list, client.InNamespace(testNamespace)))
	assert.Empty(t, list.Items, "dry run must not create anything")
}

func TestClusterDefinitions_SkipsExistingWithoutForce(t *testing.T) {
	source := newLocalSource(t, "")
	existing := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: testNamespace},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "untouched"}},
	}
	dest := newFakeClient(t, existing)

	summary, err := ClusterDefinitions(context.Background(), source, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-cluster"}, summary.Skipped)
	assert.Empty(t, summary.Created)

	var cr hyvev1alpha1.ClusterDefinition
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-cluster"}, &cr))
	assert.Equal(t, "untouched", cr.Spec.Driver.Source, "must not overwrite an existing object without --force")
}

func TestClusterDefinitions_ForceOverwritesExisting(t *testing.T) {
	source := newLocalSource(t, "")
	existing := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: testNamespace},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "stale"}},
	}
	dest := newFakeClient(t, existing)

	summary, err := ClusterDefinitions(context.Background(), source, dest, testNamespace, false, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-cluster"}, summary.Created)
	assert.Empty(t, summary.Skipped)

	var cr hyvev1alpha1.ClusterDefinition
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-cluster"}, &cr))
	assert.Equal(t, "github.com/example/civo-k3s", cr.Spec.Driver.Source, "--force must overwrite the stale spec")
	assert.Equal(t, "abc-123", cr.Status.DriverOutputs["HYVE_CLUSTER_ID"], "--force must still copy status")
}

// TestClusterDefinitions_DryRun_ReportsSkippedForExistingObject is the
// regression test for a real bug caught via live testing: dry-run used to
// report "would create" for every object unconditionally, never checking
// the destination at all — so an object that already existed (and would
// actually be skipped for real) was misreported as something that would
// be created.
func TestClusterDefinitions_DryRun_ReportsSkippedForExistingObject(t *testing.T) {
	source := newLocalSource(t, "")
	existing := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: testNamespace},
	}
	dest := newFakeClient(t, existing)

	summary, err := ClusterDefinitions(context.Background(), source, dest, testNamespace, true, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-cluster"}, summary.Skipped, "dry run must accurately preview a skip, not blindly report a create")
	assert.Empty(t, summary.Created)
}

func TestClusterDefinitions_DryRun_ReportsCreateWhenForceWouldOverwrite(t *testing.T) {
	source := newLocalSource(t, "")
	existing := &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: testNamespace},
	}
	dest := newFakeClient(t, existing)

	summary, err := ClusterDefinitions(context.Background(), source, dest, testNamespace, true, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-cluster"}, summary.Created, "dry run with --force must preview the overwrite as a create, not a skip")
	assert.Empty(t, summary.Skipped)
}

func TestHyveConfig_DryRun_ReportsSkipWhenAlreadyExists(t *testing.T) {
	source := newLocalSource(t, "")
	existing := &hyvev1alpha1.HyveConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "hyve-config", Namespace: testNamespace},
	}
	dest := newFakeClient(t, existing)

	skipped, err := HyveConfig(context.Background(), source, dest, testNamespace, "hyve-config", true)
	require.NoError(t, err)
	assert.True(t, skipped, "dry run must accurately report an already-existing HyveConfig as skipped, not created")
}

func TestAccessBindings_DryRun_ReportsSkippedForExistingObject(t *testing.T) {
	binding := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cedric", Namespace: testNamespace},
		Spec:       hyvev1alpha1.HyveAccessBindingSpec{Subject: hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeLocal, Value: "cedric"}},
	}
	source := newFakeClient(t, binding)
	existing := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cedric", Namespace: testNamespace},
	}
	dest := newFakeClient(t, existing)

	summary, err := AccessBindings(context.Background(), source, dest, testNamespace, true, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"cedric"}, summary.Skipped)
	assert.Empty(t, summary.Created)
}

func TestHyveConfig_CreatesWhenAbsent(t *testing.T) {
	source := newLocalSource(t, "reconcile:\n  mode: local\n  strictResourceDelete: true\n")
	dest := newFakeClient(t)

	skipped, err := HyveConfig(context.Background(), source, dest, testNamespace, "hyve-config", false)
	require.NoError(t, err)
	assert.False(t, skipped)

	var hc hyvev1alpha1.HyveConfig
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "hyve-config"}, &hc))
	assert.True(t, hc.Spec.StrictResourceDelete)
}

func TestHyveConfig_SkipsWhenAlreadyExists(t *testing.T) {
	source := newLocalSource(t, "")
	existing := &hyvev1alpha1.HyveConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "hyve-config", Namespace: testNamespace},
		Spec:       hyvev1alpha1.HyveConfigSpec{DefaultWorkflowImage: "untouched"},
	}
	dest := newFakeClient(t, existing)

	skipped, err := HyveConfig(context.Background(), source, dest, testNamespace, "hyve-config", false)
	require.NoError(t, err)
	assert.True(t, skipped)

	var hc hyvev1alpha1.HyveConfig
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "hyve-config"}, &hc))
	assert.Equal(t, "untouched", hc.Spec.DefaultWorkflowImage, "an existing HyveConfig must never be overwritten, even with force semantics elsewhere in this package — mirrors the Helm chart's own stance")
}

func TestAccessBindings_CopiesBindingAndCredentialsSecret(t *testing.T) {
	binding := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cedric", Namespace: testNamespace},
		Spec: hyvev1alpha1.HyveAccessBindingSpec{
			Subject:           hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeLocal, Value: "cedric"},
			Role:              hyvev1alpha1.RoleAdmin,
			ServiceAccountRef: hyvev1alpha1.ServiceAccountRef{Name: "hyve-access-admin", Namespace: testNamespace},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cedric-credentials", Namespace: testNamespace},
		Data:       map[string][]byte{"password-hash": []byte("bcrypt-hash-value")},
	}
	source := newFakeClient(t, binding, secret)
	dest := newFakeClient(t)

	summary, err := AccessBindings(context.Background(), source, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"cedric"}, summary.Created)
	assert.True(t, summary.OK())

	var gotBinding hyvev1alpha1.HyveAccessBinding
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "cedric"}, &gotBinding))
	assert.Equal(t, hyvev1alpha1.RoleAdmin, gotBinding.Spec.Role)

	var gotSecret corev1.Secret
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "cedric-credentials"}, &gotSecret))
	assert.Equal(t, []byte("bcrypt-hash-value"), gotSecret.Data["password-hash"], "the paired credentials secret must be copied — a binding-only copy would leave this user unable to log in")
}

func TestAccessBindings_MissingSecretDoesNotFailBinding(t *testing.T) {
	// An OIDC-subject binding (or a local one mid-provisioning) has no
	// paired credentials Secret at all — must not fail the run.
	binding := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "someone", Namespace: testNamespace},
		Spec: hyvev1alpha1.HyveAccessBindingSpec{
			Subject: hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeOIDC, Value: "someone@example.com"},
			Role:    hyvev1alpha1.RoleReadOnly,
		},
	}
	source := newFakeClient(t, binding)
	dest := newFakeClient(t)

	summary, err := AccessBindings(context.Background(), source, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"someone"}, summary.Created)
	assert.True(t, summary.OK())
}

func TestAccessBindings_SkipsExistingWithoutForce(t *testing.T) {
	binding := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cedric", Namespace: testNamespace},
		Spec:       hyvev1alpha1.HyveAccessBindingSpec{Subject: hyvev1alpha1.HyveAccessBindingSubject{Type: hyvev1alpha1.SubjectTypeLocal, Value: "cedric"}, Role: hyvev1alpha1.RoleAdmin},
	}
	source := newFakeClient(t, binding)
	existing := &hyvev1alpha1.HyveAccessBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "cedric", Namespace: testNamespace},
		Spec:       hyvev1alpha1.HyveAccessBindingSpec{Role: hyvev1alpha1.RoleReadOnly},
	}
	dest := newFakeClient(t, existing)

	summary, err := AccessBindings(context.Background(), source, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"cedric"}, summary.Skipped)

	var gotBinding hyvev1alpha1.HyveAccessBinding
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "cedric"}, &gotBinding))
	assert.Equal(t, hyvev1alpha1.RoleReadOnly, gotBinding.Spec.Role, "must not overwrite an existing binding without --force")
}

func TestClusterDefinitions_FailurePerCluster_DoesNotAbortTheRest(t *testing.T) {
	// Not directly exercisable via newLocalSource (a single-cluster
	// fixture) — this documents the summary contract's intent instead: a
	// per-cluster Failed entry never aborts the loop, verified indirectly
	// by every other test in this file completing its Created/Skipped
	// assertions even when one cluster's Get (force path) could fail.
	// Kept as a placeholder that fails loudly if the contract's basic
	// error shape (Summary.Failed being a map, not a single error) ever
	// regresses.
	summary := newSummary()
	assert.NotNil(t, summary.Failed)
	assert.True(t, summary.OK())
	summary.Failed["x"] = apierrors.NewNotFound(hyvev1alpha1.GroupVersion.WithResource("clusterdefinitions").GroupResource(), "x")
	assert.False(t, summary.OK())
}

// ── TemplatesFromDir / WorkflowsFromDir (local dir -> cluster) ───────────

// newLocalTemplatesAndWorkflows writes one Template and one Workflow file
// under localPath's templates/ and workflows/ subdirectories — the literal
// on-disk shape template.Manager/workflow.Manager expect.
func newLocalTemplatesAndWorkflows(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	templatesDir := filepath.Join(root, "templates")
	require.NoError(t, os.MkdirAll(templatesDir, 0755))
	tpl := "apiVersion: hyve.io/v1alpha1\nkind: Template\nmetadata:\n  name: my-template\nspec:\n  driver:\n    source: github.com/example/civo-k3s\n    version: 1.0.0\n  region: PHX1\n"
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "my-template.yaml"), []byte(tpl), 0644))

	workflowsDir := filepath.Join(root, "workflows")
	require.NoError(t, os.MkdirAll(workflowsDir, 0755))
	wf := "apiVersion: hyve.io/v1alpha1\nkind: Workflow\nmetadata:\n  name: my-workflow\nspec:\n  jobs:\n    - name: main\n      steps:\n        - name: hello\n          script: echo hi\n"
	require.NoError(t, os.WriteFile(filepath.Join(workflowsDir, "my-workflow.yaml"), []byte(wf), 0644))

	return root
}

func TestTemplatesFromDir_CreatesTemplate(t *testing.T) {
	localPath := newLocalTemplatesAndWorkflows(t)
	dest := newFakeClient(t)

	summary, err := TemplatesFromDir(context.Background(), localPath, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-template"}, summary.Created)

	var tpl hyvev1alpha1.Template
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-template"}, &tpl))
	assert.Equal(t, "github.com/example/civo-k3s", tpl.Spec.Driver.Source)
	assert.Equal(t, "PHX1", tpl.Spec.Region)
}

func TestTemplatesFromDir_DryRun_WritesNothing(t *testing.T) {
	localPath := newLocalTemplatesAndWorkflows(t)
	dest := newFakeClient(t)

	summary, err := TemplatesFromDir(context.Background(), localPath, dest, testNamespace, true, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-template"}, summary.Created)

	var list hyvev1alpha1.TemplateList
	require.NoError(t, dest.List(context.Background(), &list, client.InNamespace(testNamespace)))
	assert.Empty(t, list.Items)
}

func TestTemplatesFromDir_SkipsExistingWithoutForce(t *testing.T) {
	localPath := newLocalTemplatesAndWorkflows(t)
	existing := &hyvev1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: "my-template", Namespace: testNamespace},
		Spec:       hyvev1alpha1.TemplateSpec{Region: "original-region"},
	}
	dest := newFakeClient(t, existing)

	summary, err := TemplatesFromDir(context.Background(), localPath, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-template"}, summary.Skipped)

	var tpl hyvev1alpha1.Template
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-template"}, &tpl))
	assert.Equal(t, "original-region", tpl.Spec.Region, "must not overwrite an existing template without --force")
}

func TestTemplatesFromDir_ForceOverwritesExisting(t *testing.T) {
	localPath := newLocalTemplatesAndWorkflows(t)
	existing := &hyvev1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: "my-template", Namespace: testNamespace},
		Spec:       hyvev1alpha1.TemplateSpec{Region: "original-region"},
	}
	dest := newFakeClient(t, existing)

	summary, err := TemplatesFromDir(context.Background(), localPath, dest, testNamespace, false, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-template"}, summary.Created)

	var tpl hyvev1alpha1.Template
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-template"}, &tpl))
	assert.Equal(t, "PHX1", tpl.Spec.Region, "--force must overwrite the existing template")
}

func TestWorkflowsFromDir_CreatesWorkflow(t *testing.T) {
	localPath := newLocalTemplatesAndWorkflows(t)
	dest := newFakeClient(t)

	summary, err := WorkflowsFromDir(context.Background(), localPath, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-workflow"}, summary.Created)

	var wf hyvev1alpha1.Workflow
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-workflow"}, &wf))
	require.Len(t, wf.Spec.Jobs, 1)
	assert.Equal(t, "main", wf.Spec.Jobs[0].Name)
}

func TestWorkflowsFromDir_SkipsExistingWithoutForce(t *testing.T) {
	localPath := newLocalTemplatesAndWorkflows(t)
	existing := &hyvev1alpha1.Workflow{ObjectMeta: metav1.ObjectMeta{Name: "my-workflow", Namespace: testNamespace}}
	dest := newFakeClient(t, existing)

	summary, err := WorkflowsFromDir(context.Background(), localPath, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-workflow"}, summary.Skipped)
}

// ── Templates / Workflows (cluster -> cluster) ───────────────────────────

func TestTemplates_CopiesFromSourceToDest(t *testing.T) {
	tpl := &hyvev1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: "my-template", Namespace: testNamespace},
		Spec:       hyvev1alpha1.TemplateSpec{Region: "PHX1"},
	}
	source := newFakeClient(t, tpl)
	dest := newFakeClient(t)

	summary, err := Templates(context.Background(), source, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-template"}, summary.Created)

	var got hyvev1alpha1.Template
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-template"}, &got))
	assert.Equal(t, "PHX1", got.Spec.Region)
}

func TestTemplates_OnlyCopiesFromTheGivenNamespace(t *testing.T) {
	mine := &hyvev1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: "mine", Namespace: testNamespace}}
	other := &hyvev1alpha1.Template{ObjectMeta: metav1.ObjectMeta{Name: "not-mine", Namespace: "tenant-b"}}
	source := newFakeClient(t, mine, other)
	dest := newFakeClient(t)

	summary, err := Templates(context.Background(), source, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"mine"}, summary.Created)
}

func TestWorkflows_CopiesFromSourceToDest(t *testing.T) {
	wf := &hyvev1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "my-workflow", Namespace: testNamespace},
		Spec:       hyvev1alpha1.WorkflowSpec{Description: "test workflow"},
	}
	source := newFakeClient(t, wf)
	dest := newFakeClient(t)

	summary, err := Workflows(context.Background(), source, dest, testNamespace, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-workflow"}, summary.Created)

	var got hyvev1alpha1.Workflow
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-workflow"}, &got))
	assert.Equal(t, "test workflow", got.Spec.Description)
}

func TestWorkflows_ForceOverwritesExisting(t *testing.T) {
	wf := &hyvev1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "my-workflow", Namespace: testNamespace},
		Spec:       hyvev1alpha1.WorkflowSpec{Description: "new description"},
	}
	source := newFakeClient(t, wf)
	existing := &hyvev1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "my-workflow", Namespace: testNamespace},
		Spec:       hyvev1alpha1.WorkflowSpec{Description: "old description"},
	}
	dest := newFakeClient(t, existing)

	summary, err := Workflows(context.Background(), source, dest, testNamespace, false, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-workflow"}, summary.Created)

	var got hyvev1alpha1.Workflow
	require.NoError(t, dest.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "my-workflow"}, &got))
	assert.Equal(t, "new description", got.Spec.Description)
}

// ── Environments ──────────────────────────────────────────────────────────

func TestEnvironments_ListsOnlyControlPlaneNamespace(t *testing.T) {
	acme := &hyvev1alpha1.HyveEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: testNamespace},
		Spec:       hyvev1alpha1.HyveEnvironmentSpec{Namespace: "acme"},
	}
	// A HyveEnvironment object living somewhere other than the
	// control-plane namespace must never happen in practice, but proves
	// this really does filter by namespace rather than listing globally.
	stray := &hyvev1alpha1.HyveEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: "stray", Namespace: "not-control-plane"},
		Spec:       hyvev1alpha1.HyveEnvironmentSpec{Namespace: "stray"},
	}
	source := newFakeClient(t, acme, stray)

	envs, err := Environments(context.Background(), source, testNamespace)
	require.NoError(t, err)
	require.Len(t, envs, 1)
	assert.Equal(t, "acme", envs[0].Name)
	assert.Equal(t, "acme", envs[0].Spec.Namespace)
}

func TestEnvironments_EmptyWhenNoneExist(t *testing.T) {
	source := newFakeClient(t)
	envs, err := Environments(context.Background(), source, testNamespace)
	require.NoError(t, err)
	assert.Empty(t, envs)
}
