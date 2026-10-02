// Package webui embeds hyve's own web console (web/) directly into the
// hyve binary, so hyve-api can serve it itself — one image, one process,
// one port, rather than a second nginx-fronted image/Deployment/Service
// only ever meaningful deployed alongside the first (see this session's
// own decision to merge them: the UI's every API call is already a bare
// relative path — web/src/lib/api/client.ts — so it always had to share
// hyve-api's own origin; a separate container never bought any real
// independence, just extra moving parts).
//
// dist/ here holds a committed placeholder (internal/webui/dist/index.html)
// so `go build`/`go test` succeed for anyone who hasn't run `npm run
// build` — go:embed requires something to exist at compile time.
// deploy/Dockerfile.dev overwrites this placeholder with web/'s real
// `npm run build` output before compiling the Go binary, in the same
// image build — see that file's own comment for the exact copy step.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded UI's static files, rooted at dist/ (dropping
// the dist/ prefix itself, so callers see index.html/assets/... directly,
// matching what a real `npm run build` output looks like).
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Can't happen: dist/ is always embedded, even as just the
		// placeholder — fs.Sub only fails if the prefix itself doesn't
		// exist in the embedded tree, which go:embed already guarantees.
		panic(err)
	}
	return sub
}
