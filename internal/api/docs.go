package api

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.yaml
var openAPISpec []byte

// swaggerUIPage loads Swagger UI from a CDN rather than vendoring/embedding
// the ~2-3MB dist bundle into this repo and binary — this is a development
// aid (GET /docs, unauthenticated, same as /healthz), not something a real
// deployment depends on, so requiring internet access for the page's own
// JS/CSS is an acceptable trade — no hyve traffic or credentials go
// through the CDN, it only serves static assets.
const swaggerUIPage = `<!DOCTYPE html>
<html>
<head>
  <title>hyve API docs</title>
  <meta charset="utf-8"/>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => {
      window.ui = SwaggerUIBundle({
        url: "/openapi.yaml",
        dom_id: "#swagger-ui",
        presets: [SwaggerUIBundle.presets.apis],
      });
    };
  </script>
</body>
</html>`

// registerDocsRoutes wires the (unauthenticated, like /healthz) API
// documentation endpoints — a development aid, see Server.Routes.
func (s *Server) registerDocsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerUIPage))
	})
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec)
	})
}
