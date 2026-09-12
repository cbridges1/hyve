package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newClusterDef(name string) *hyvev1alpha1.ClusterDefinition {
	return &hyvev1alpha1.ClusterDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "./modules/civo", Version: "latest"}},
		Status: hyvev1alpha1.ClusterDefinitionStatus{
			DriverOutputs: map[string]string{"HYVE_SECRET_TOKEN": "should-never-appear-in-dto"},
		},
	}
}

// newTestMux builds a request handler equivalent to what Server.Routes
// produces for /api/clusters, pre-authenticated as an already-role-
// resolved caller (bypassing requireAuth/requireRole, which have their own
// dedicated tests) so these tests focus purely on the handlers' behavior.
func newTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerClusterRoutes(mux)
	return mux
}

func doRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(contextWithRole(req.Context(), role))
	rec := httptest.NewRecorder()
	newTestMux(s).ServeHTTP(rec, req)
	return rec
}

func TestHandleListClusters_ExcludesDriverOutputs(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "should-never-appear-in-dto")

	var dtos []clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dtos))
	require.Len(t, dtos, 1)
	assert.Equal(t, "prod", dtos[0].Name)
}

// TestHandleListClusters_ScopedToNamespace is the direct regression test
// for a real bug caught live: the original handler called s.Client.List
// with no namespace restriction — a genuine cluster-wide list attempt.
// hyve-api's own RBAC Role (deploy/helm/hyve-api/templates/rbac.yaml) is
// deliberately namespace-scoped, not a ClusterRole, so that unscoped call
// always 500'd against real RBAC (never caught by earlier tests, since the
// fake client used here has no RBAC layer to violate at all) — confirmed
// live: "clusterdefinitions.hyve.io is forbidden ... at the cluster
// scope". This test can't reproduce the RBAC rejection itself without a
// real API server, but it does prove the fix's actual effect: a
// ClusterDefinition in a different namespace must never appear in the
// response, which only holds if the List call is namespace-scoped.
func TestHandleListClusters_ScopedToNamespace(t *testing.T) {
	other := newClusterDef("other-ns-cluster")
	other.Namespace = "some-other-namespace"
	s := &Server{Client: newFakeClient(t, newClusterDef("prod"), other), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dtos []clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dtos))
	require.Len(t, dtos, 1)
	assert.Equal(t, "prod", dtos[0].Name)
}

func TestHandleGetCluster_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetCluster_Found(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/prod", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "prod", dto.Name)
	assert.NotContains(t, rec.Body.String(), "should-never-appear-in-dto")
}

// TestHandleGetCluster_SurfacesAgent is milestone 7's own regression test:
// spec.access.agent/status.agent were previously entirely absent from the
// DTO, leaving `hyve cluster auth`/`show` with no way to know a cluster
// even had hyve-agent configured.
func TestHandleGetCluster_SurfacesAgent(t *testing.T) {
	cd := newClusterDef("prod")
	cd.Spec.Access.Agent = &hyvev1alpha1.AgentSpec{Enabled: true, Proxy: true}
	cd.Status.Agent = hyvev1alpha1.AgentStatus{Connected: true, Version: "dev", LastConnectedAt: "2026-01-01T00:00:00Z"}
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/prod", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.Agent)
	assert.True(t, dto.Agent.Enabled)
	assert.True(t, dto.Agent.Proxy)
	assert.True(t, dto.AgentStatus.Connected)
	assert.Equal(t, "dev", dto.AgentStatus.Version)
}

// TestHandleGetCluster_PendingDeletion proves the DTO surfaces a pending
// delete — confirmed live this was previously invisible: DELETE
// /clusters/<name> only ever sets metadata.deletionTimestamp
// (ClusterDefinitionFinalizer keeps the object around for the controller's
// own OnDelete/driver-delete/AfterDelete sequence), but nothing in the API
// response said so until this field existed.
func TestHandleGetCluster_PendingDeletion(t *testing.T) {
	cd := newClusterDef("doomed")
	cd.Finalizers = []string{hyvev1alpha1.ClusterDefinitionFinalizer}
	now := metav1.Now()
	cd.DeletionTimestamp = &now
	s := &Server{Client: newFakeClient(t, cd), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/doomed", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.True(t, dto.PendingDeletion)
}

func TestHandleGetCluster_NotPendingDeletion(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("healthy")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/healthy", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.False(t, dto.PendingDeletion)
}

func TestHandleCreateCluster_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPost, "/clusters", createClusterRequest{Name: "new-cluster"})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleCreateCluster_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name: "new-cluster",
		Spec: hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "./modules/civo", Version: "latest"}},
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	rec2 := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/new-cluster", nil)
	assert.Equal(t, http.StatusOK, rec2.Code)
}

func TestHandleCreateCluster_FromTemplate(t *testing.T) {
	tpl := newTemplateDef("t1")
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name: "from-tpl",
		Template: &createClusterFromTemplateRef{
			Name:   "t1",
			Region: "NYC1",
			Params: map[string]string{"node_size": "large"},
		},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "from-tpl", dto.Name)
	assert.Equal(t, tpl.Spec.Driver.Source, dto.Driver)

	var created hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: "from-tpl"}, &created))
	assert.Equal(t, "NYC1", created.Spec.Region)
	assert.Equal(t, "large", created.Spec.Params["node_size"])
}

// TestHandleCreateCluster_FromTemplate_WithSchedule_SetsExpiresAt is the
// regression test for a real bug: RenderClusterDefinitionSpec never looks
// at Template.Spec.Schedule (it can't — internal/template, where
// CronNextOccurrence lives, already imports this package the other way),
// so a cluster created here from a schedule-having template got no
// spec.expiresAt at all. internal/reconcile's expiry check
// (ReconcileOne: `if def.Spec.ExpiresAt != ""`) had nothing to act on, so
// scheduled deletion silently never happened for any cluster created via
// this endpoint — confirmed live against a real k3d deployment before this
// fix, where the equivalent local-mode path (cmd/cluster/create.go) has
// always computed this correctly.
func TestHandleCreateCluster_FromTemplate_WithSchedule_SetsExpiresAt(t *testing.T) {
	tpl := newTemplateDef("scheduled")
	tpl.Spec.Schedule = "0 0 * * *" // every day at midnight — just needs to resolve to *some* future time
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name:     "from-scheduled-tpl",
		Template: &createClusterFromTemplateRef{Name: "scheduled"},
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: "from-scheduled-tpl"}, &created))
	assert.NotEmpty(t, created.Spec.ExpiresAt, "a cluster created from a schedule-having template must get spec.expiresAt set, or expiry can never fire")
}

func TestHandleCreateCluster_FromTemplate_NoSchedule_NoExpiresAt(t *testing.T) {
	tpl := newTemplateDef("unscheduled")
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name:     "from-unscheduled-tpl",
		Template: &createClusterFromTemplateRef{Name: "unscheduled"},
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var created hyvev1alpha1.ClusterDefinition
	require.NoError(t, s.Client.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: "from-unscheduled-tpl"}, &created))
	assert.Empty(t, created.Spec.ExpiresAt)
}

func TestHandleCreateCluster_FromTemplate_InvalidSchedule_400(t *testing.T) {
	tpl := newTemplateDef("bad-schedule")
	tpl.Spec.Schedule = "not a cron expression"
	s := &Server{Client: newFakeClient(t, tpl), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name:     "from-bad-schedule-tpl",
		Template: &createClusterFromTemplateRef{Name: "bad-schedule"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleCreateCluster_FromTemplate_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{
		Name:     "from-tpl",
		Template: &createClusterFromTemplateRef{Name: "missing"},
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleCreateCluster_MissingName(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleCreateCluster_AlreadyExists(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPost, "/clusters", createClusterRequest{Name: "prod"})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestHandleDeleteCluster_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodDelete, "/clusters/prod", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleDeleteCluster_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("prod")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/clusters/prod", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec2 := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/prod", nil)
	assert.Equal(t, http.StatusNotFound, rec2.Code)
}

func TestHandleDeleteCluster_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodDelete, "/clusters/missing", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetClusterResources_ReadOnlyAllowed(t *testing.T) {
	def := newClusterDef("c1")
	def.Spec.Resources = []hyvev1alpha1.ResourceRef{{Name: "podinfo", Source: "./resource-files/podinfo.yaml"}}
	def.Status.AppliedResources = map[string]*hyvev1alpha1.AppliedResource{
		"podinfo": {SourceSHA256: "abc123", AppliedAt: "2026-01-01T00:00:00Z"},
	}
	s := &Server{Client: newFakeClient(t, def), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/c1/resources", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterResourcesDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Len(t, dto.Resources, 1)
	assert.Equal(t, "podinfo", dto.Resources[0].Name)
	require.Contains(t, dto.AppliedResources, "podinfo")
	assert.Equal(t, "abc123", dto.AppliedResources["podinfo"].SourceSHA256)
}

func TestHandleGetClusterResources_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/missing/resources", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleGetClusterResources_Empty(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/c1/resources", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterResourcesDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Empty(t, dto.Resources)
	assert.Empty(t, dto.AppliedResources)
}

func TestHandleUpdateCluster_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/clusters/c1", updateClusterRequest{
		Spec: hyvev1alpha1.ClusterDefinitionSpec{Driver: hyvev1alpha1.DriverRef{Source: "./modules/updated", Version: "v2"}},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "./modules/updated", dto.Driver)

	rec2 := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodGet, "/clusters/c1", nil)
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &dto))
	require.NotNil(t, dto.Spec)
	assert.Equal(t, "v2", dto.Spec.Driver.Version)
}

func TestHandleUpdateCluster_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPatch, "/clusters/c1", updateClusterRequest{})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUpdateCluster_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/clusters/missing", updateClusterRequest{})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// newTestEvent builds a real corev1.Event involving ClusterDefinition
// clusterName, seq minutes in the past — a distinct LastSeen per event
// lets tests assert on ordering deterministically.
func newTestEvent(name, clusterName string, seq int) *corev1.Event {
	when := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(seq) * time.Minute))
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		InvolvedObject: corev1.ObjectReference{Kind: "ClusterDefinition", Name: clusterName},
		Type:           "Normal",
		Reason:         fmt.Sprintf("Reason%d", seq),
		Message:        fmt.Sprintf("message %d", seq),
		LastTimestamp:  when,
	}
}

func TestHandleGetClusterEvents_DefaultsAndOrdersNewestFirst(t *testing.T) {
	clientset := fake.NewClientset(
		newTestEvent("ev-0", "c1", 0),
		newTestEvent("ev-1", "c1", 1),
		newTestEvent("ev-2", "c1", 2),
	)
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Clientset: clientset, Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/c1/events", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterActivityDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, 3, dto.TotalEvents)
	require.Len(t, dto.Events, 3)
	assert.Equal(t, "Reason2", dto.Events[0].Reason, "newest (seq 2) must come first")
	assert.Equal(t, "Reason1", dto.Events[1].Reason)
	assert.Equal(t, "Reason0", dto.Events[2].Reason, "oldest (seq 0) must come last")
}

func TestHandleGetClusterEvents_LimitAndOffsetPaginate(t *testing.T) {
	clientset := fake.NewClientset(
		newTestEvent("ev-0", "c1", 0),
		newTestEvent("ev-1", "c1", 1),
		newTestEvent("ev-2", "c1", 2),
		newTestEvent("ev-3", "c1", 3),
		newTestEvent("ev-4", "c1", 4),
	)
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Clientset: clientset, Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/c1/events?limit=2&offset=1", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterActivityDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, 5, dto.TotalEvents, "total must reflect the full set, not just this page")
	require.Len(t, dto.Events, 2, "page size must be exactly ?limit=")
	// Newest-first order: seq 4,3,2,1,0 — offset=1,limit=2 is seq 3,2.
	assert.Equal(t, "Reason3", dto.Events[0].Reason)
	assert.Equal(t, "Reason2", dto.Events[1].Reason)
}

func TestHandleGetClusterEvents_OffsetPastEnd_ReturnsEmptyNotError(t *testing.T) {
	clientset := fake.NewClientset(newTestEvent("ev-0", "c1", 0))
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Clientset: clientset, Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, "/clusters/c1/events?offset=50", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto clusterActivityDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, 1, dto.TotalEvents)
	assert.Empty(t, dto.Events)
}

func TestHandleGetClusterEvents_LimitClampedToMax(t *testing.T) {
	clientset := fake.NewClientset(newTestEvent("ev-0", "c1", 0))
	s := &Server{Client: newFakeClient(t, newClusterDef("c1")), Clientset: clientset, Namespace: testNamespace}

	rec := doRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodGet, fmt.Sprintf("/clusters/c1/events?limit=%d", maxClusterEventsLimit+1000), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	// Only one event exists, so a successful 200 with it present is enough
	// to prove the (deliberately unexported) clamp didn't error the request.
	var dto clusterActivityDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Len(t, dto.Events, 1)
}
