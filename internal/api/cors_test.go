package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCorsMiddleware_ReflectsOriginAndVaries(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	corsMiddleware(okHandler()).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://localhost:5173", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", rec.Header().Get("Vary"))
}

func TestCorsMiddleware_NoOriginHeaderSetWhenRequestHasNone(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	rec := httptest.NewRecorder()

	corsMiddleware(okHandler()).ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCorsMiddleware_PreflightShortCircuitsBeforeNextHandler(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/clusters", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	rec := httptest.NewRecorder()

	corsMiddleware(next).ServeHTTP(rec, req)

	assert.False(t, called, "preflight must never reach the wrapped handler")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "Authorization, Content-Type", rec.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "GET, POST, PUT, PATCH, DELETE, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
}

func TestCorsMiddleware_PreflightDefaultsAllowHeadersWhenRequestOmitsIt(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/clusters", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	corsMiddleware(okHandler()).ServeHTTP(rec, req)

	assert.Equal(t, "Content-Type, Authorization", rec.Header().Get("Access-Control-Allow-Headers"))
}

func TestCorsMiddleware_NeverSetsAllowCredentials(t *testing.T) {
	// This API is bearer-token-only, never cookie-based — reflecting Origin
	// is only safe as long as that stays true. A future edit adding
	// Access-Control-Allow-Credentials without re-deriving that argument
	// would silently reopen the exact risk the doc comment warns about.
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	corsMiddleware(okHandler()).ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
}
