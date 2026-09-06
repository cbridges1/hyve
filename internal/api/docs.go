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
  <style>
    /* Swagger UI 5 has no built-in dark theme, so this uses the standard
       invert-then-reinvert trick: flip the whole page's colors, then flip
       images/embeds back so they don't render as photo negatives. Cheap
       and imperfect (a few third-party badge icons can look slightly off)
       but correct for the actual UI chrome and code blocks, which is what
       this page is for.

       Three states, matching the main console's own light/dark/system
       toggle: no data-theme attribute (default) follows the OS/browser
       preference via the media query below; an explicit data-theme
       attribute (set by the toggle button's script, persisted in
       localStorage) overrides it either way. The dark rule appears twice
       (once gated by the media query for "system says dark and nothing
       overrides it", once unconditional for "explicitly forced dark") —
       :not([data-theme="light"]) in the first is what lets an explicit
       light override win even when the OS is dark. */
    #hyve-theme-toggle {
      position: fixed; top: 10px; right: 16px; z-index: 10000;
      font: 13px -apple-system, BlinkMacSystemFont, sans-serif;
      background: #fafafa; border: 1px solid #d4d4d4; border-radius: 8px;
      padding: 2px; display: flex; gap: 2px;
    }
    #hyve-theme-toggle button {
      font: inherit; padding: 3px 9px; border: none; border-radius: 6px;
      background: transparent; color: #555; cursor: pointer;
    }
    #hyve-theme-toggle button[aria-pressed="true"] { background: #1a1a1a; color: #fff; }

    @media (prefers-color-scheme: dark) {
      html:not([data-theme="light"]) { filter: invert(1) hue-rotate(180deg); background: #fff; }
      html:not([data-theme="light"]) img,
      html:not([data-theme="light"]) svg,
      html:not([data-theme="light"]) picture,
      html:not([data-theme="light"]) video,
      html:not([data-theme="light"]) iframe,
      html:not([data-theme="light"]) embed,
      html:not([data-theme="light"]) object,
      html:not([data-theme="light"]) #hyve-theme-toggle { filter: invert(1) hue-rotate(180deg); }
    }
    html[data-theme="dark"] { filter: invert(1) hue-rotate(180deg); background: #fff; }
    html[data-theme="dark"] img,
    html[data-theme="dark"] svg,
    html[data-theme="dark"] picture,
    html[data-theme="dark"] video,
    html[data-theme="dark"] iframe,
    html[data-theme="dark"] embed,
    html[data-theme="dark"] object,
    html[data-theme="dark"] #hyve-theme-toggle { filter: invert(1) hue-rotate(180deg); }
    /* A CSS filter on an ancestor (html, above) applies to its whole
       rendered subtree as a compositing effect — a descendant can't opt
       out with filter: none, only cancel it back out with the same
       filter applied a second time (confirmed while writing this: the
       first version of this file tried filter: none here and the toggle
       stayed visibly inverted). Applying the identical invert+hue-rotate
       to #hyve-theme-toggle above is what makes it render in its normal
       authored colors even while the rest of the page is being flipped. */
  </style>
  <script>
    // Applied before body paint (this script runs synchronously in <head>,
    // before Swagger UI's own bundle loads) so there's no flash of the
    // wrong theme on load.
    (() => {
      const saved = localStorage.getItem('hyve-docs-theme');
      if (saved === 'light' || saved === 'dark') document.documentElement.setAttribute('data-theme', saved);
    })();
  </script>
</head>
<body>
  <div id="hyve-theme-toggle" role="group" aria-label="Theme">
    <button type="button" data-theme-choice="light">Light</button>
    <button type="button" data-theme-choice="system">System</button>
    <button type="button" data-theme-choice="dark">Dark</button>
  </div>
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

    function applyTheme(choice) {
      if (choice === 'system') {
        document.documentElement.removeAttribute('data-theme');
        localStorage.removeItem('hyve-docs-theme');
      } else {
        document.documentElement.setAttribute('data-theme', choice);
        localStorage.setItem('hyve-docs-theme', choice);
      }
      for (const btn of document.querySelectorAll('#hyve-theme-toggle button')) {
        btn.setAttribute('aria-pressed', String(btn.dataset.themeChoice === choice));
      }
    }
    applyTheme(localStorage.getItem('hyve-docs-theme') || 'system');
    for (const btn of document.querySelectorAll('#hyve-theme-toggle button')) {
      btn.addEventListener('click', () => applyTheme(btn.dataset.themeChoice));
    }
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
