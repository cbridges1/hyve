package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleWhoami_ReturnsResolvedIdentity(t *testing.T) {
	mux := http.NewServeMux()
	s := &Server{}
	s.registerWhoamiRoute(mux)

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req = req.WithContext(contextWithUsername(req.Context(), "cedric"))
	req = req.WithContext(contextWithRole(req.Context(), hyvev1alpha1.RoleAdmin))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp whoamiResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "cedric", resp.Username)
	assert.Equal(t, hyvev1alpha1.RoleAdmin, resp.Role)
}

func TestWhoamiRoute_RequiresAuthAndRole(t *testing.T) {
	s := &Server{Client: newFakeClient(t), SigningKey: []byte("key")}

	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
