// Package helm embeds hyve's chart, so `hyve install` and local mode can
// install it without Helm (internal/install renders it).
package helm

import "embed"

// Chart is deploy/helm/hyve.
//
//go:embed all:hyve
var Chart embed.FS
