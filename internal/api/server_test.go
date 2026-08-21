package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireAuth_MissingHeader_401(t *testing.T) {
	s := &Server{SigningKey: []byte("key")}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	rec := httptest.NewRecorder()

	s.requireAuth(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_MalformedHeader_401(t *testing.T) {
	s := &Server{SigningKey: []byte("key")}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req.Header.Set("Authorization", "not-bearer-format")
	rec := httptest.NewRecorder()

	s.requireAuth(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_InvalidToken_401(t *testing.T) {
	s := &Server{SigningKey: []byte("key")}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()

	s.requireAuth(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_ValidToken_PassesUsernameToContext(t *testing.T) {
	key := []byte("key")
	s := &Server{SigningKey: key}
	token, err := IssueAccessToken(key, "cedric")
	require.NoError(t, err)

	var gotUsername string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUsername, _ = UsernameFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	s.requireAuth(next).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "cedric", gotUsername)
}

func TestRequireRole_UnboundIdentity_403(t *testing.T) {
	s := &Server{Client: newFakeClient(t)}

	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req = req.WithContext(contextWithUsername(req.Context(), "nobody"))
	rec := httptest.NewRecorder()

	s.requireRole(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireRole_BoundIdentity_PassesRoleToContext(t *testing.T) {
	s := &Server{Client: newFakeClient(t, newBinding("cedric-admin", "cedric", hyvev1alpha1.RoleAdmin))}

	var gotRole string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRole, _ = RoleFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req = req.WithContext(contextWithUsername(req.Context(), "cedric"))
	rec := httptest.NewRecorder()

	s.requireRole(next).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, gotRole)
}

func TestRequireAuth_Then_RequireRole_UnauthenticatedNeverReachesRoleCheck(t *testing.T) {
	// requireRole run standalone (as it would be if requireAuth were
	// somehow skipped) must reject rather than silently proceeding —
	// belt-and-suspenders against a future refactor reordering the chain.
	s := &Server{Client: newFakeClient(t)}
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	rec := httptest.NewRecorder()

	s.requireRole(okHandler()).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireRole_DistinguishesAdminFromReadOnly(t *testing.T) {
	for _, tc := range []struct {
		role     string
		allowed  []string
		expectOK bool
	}{
		{hyvev1alpha1.RoleAdmin, []string{hyvev1alpha1.RoleAdmin}, true},
		{hyvev1alpha1.RoleReadOnly, []string{hyvev1alpha1.RoleAdmin}, false},
		{hyvev1alpha1.RoleReadOnly, []string{hyvev1alpha1.RoleReadOnly, hyvev1alpha1.RoleAdmin}, true},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
		req = req.WithContext(contextWithRole(req.Context(), tc.role))
		rec := httptest.NewRecorder()

		ok := RequireRole(rec, req, tc.allowed...)
		assert.Equal(t, tc.expectOK, ok, "role=%s allowed=%v", tc.role, tc.allowed)
		if !tc.expectOK {
			assert.Equal(t, http.StatusForbidden, rec.Code)
		}
	}
}
