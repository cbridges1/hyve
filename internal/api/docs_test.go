package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDocsRoutes_Unauthenticated(t *testing.T) {
	s := &Server{Client: newFakeClient(t), SigningKey: []byte("key")}

	for _, path := range []string{"/docs", "/openapi.yaml"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "GET %s should not require a session", path)
		assert.NotEmpty(t, rec.Body.String())
	}
}

func TestOpenAPISpec_NotEmpty(t *testing.T) {
	assert.NotEmpty(t, openAPISpec)
	assert.Contains(t, string(openAPISpec), "openapi: 3.0.3")
}
