package server

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openAPISpec []byte

// ServeOpenAPI handles GET /openapi.json — always unauthenticated, so
// clients can generate typed API clients without a token.
func ServeOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(openAPISpec)
}
