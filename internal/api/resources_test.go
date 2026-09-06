package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// No resources_test.go existed before this file — this covers only the new
// PATCH /resources/<name> handler, mirroring workflows_test.go's helpers
// and style rather than backfilling full coverage for the rest of
// resources.go, which is out of scope here.

func newResourceMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.registerResourceRoutes(mux)
	return mux
}

func doResourceRequest(t *testing.T, s *Server, role, method, path string, body interface{}) *httptest.ResponseRecorder {
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
	newResourceMux(s).ServeHTTP(rec, req)
	return rec
}

func newResourceDef(name string) *hyvev1alpha1.Resource {
	return &hyvev1alpha1.Resource{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       hyvev1alpha1.ResourceSpec{Manifest: "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: example\n"},
	}
}

func TestHandleUpdateResource_AdminAllowed(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newResourceDef("r1")), Namespace: testNamespace}

	rec := doResourceRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/resources/r1", updateResourceRequest{
		Spec: hyvev1alpha1.ResourceSpec{Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: updated\n"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var dto resourceDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.Spec)
	assert.Contains(t, dto.Spec.Manifest, "updated")
}

func TestHandleUpdateResource_ReadOnlyForbidden(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newResourceDef("r1")), Namespace: testNamespace}
	rec := doResourceRequest(t, s, hyvev1alpha1.RoleReadOnly, http.MethodPatch, "/resources/r1", updateResourceRequest{})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleUpdateResource_NotFound(t *testing.T) {
	s := &Server{Client: newFakeClient(t), Namespace: testNamespace}
	rec := doResourceRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/resources/missing", updateResourceRequest{})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A git-ref-backed resource has no real Resource CR named "gitref" — only a
// ResourceRefStatus mirrored under a derived metadata.name — so PATCH must
// 404 rather than finding and overwriting anything, the same way DELETE
// already behaves for this case today.
func TestHandleUpdateResource_GitRefBacked_NotFound(t *testing.T) {
	refStatus := &hyvev1alpha1.ResourceRefStatus{
		ObjectMeta: metav1.ObjectMeta{Name: "derived-slug", Namespace: testNamespace},
		Spec:       hyvev1alpha1.ResourceRefStatusSpec{Name: "gitref", Source: "github.com/example/repo//resources/gitref"},
	}
	s := &Server{Client: newFakeClient(t, refStatus), Namespace: testNamespace}
	rec := doResourceRequest(t, s, hyvev1alpha1.RoleAdmin, http.MethodPatch, "/resources/gitref", updateResourceRequest{})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
