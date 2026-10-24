package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	h := Handler()
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	rec := get("/clusters/dev/web")
	if rec.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("missing security headers: %v", rec.Header())
	}
	if !Built() {
		// A plain `go build`: the placeholder says how to build the UI.
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "task ui") {
			t.Errorf("unbuilt UI: got %d %q", rec.Code, rec.Body.String())
		}
		return
	}
	// The app's own routes get index.html; missing assets don't.
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<div id="root">`) {
		t.Errorf("app route: got %d", rec.Code)
	}
	if rec := get("/assets/nope.js"); rec.Code != http.StatusNotFound {
		t.Errorf("missing asset: got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: got %d", rec.Code)
	}
}
