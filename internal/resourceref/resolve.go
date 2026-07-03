package resourceref

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/workflowref"
)

// ResolvedResource is one fetched, content-hashed resource manifest file.
type ResolvedResource struct {
	CanonicalSource string // local path as given, or "host/org/repo//path" for remote
	Resolved        string // absolute local path, or a human-openable URL for remote
	SHA256          string // sha256 of Data
	Data            []byte // raw manifest bytes (may be multi-document YAML)
}

// Resolve resolves a ResourceRef.Source string to manifest bytes + hash.
//
// Local paths ("./..." or "/...") are read directly relative to repoRoot, no
// caching. Remote sources ("host/org/repo[//path][@version]") are parsed via
// workflowref.ParseSource + workflowref.ClassifyPath (reusing the exact same
// parsing modules/workflows use) and must classify as PathKindFile — a
// resource source is always a single file, never a directory.
//
// Deliberately no persistent cross-run cache and no hyve.lock participation,
// unlike modules/workflows (which are pinned once via an install step):
// resources are meant to be freshly checked every reconcile cycle by design,
// so every remote Resolve call re-fetches from the network.
func Resolve(source, repoRoot string) (*ResolvedResource, error) {
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "/") {
		return resolveLocal(source, repoRoot)
	}
	return resolveRemote(source)
}

func resolveLocal(source, repoRoot string) (*ResolvedResource, error) {
	full := source
	if !filepath.IsAbs(source) {
		full = filepath.Join(repoRoot, source)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("resource source %q not found: %w", source, err)
	}
	return &ResolvedResource{
		CanonicalSource: source,
		Resolved:        full,
		SHA256:          sha256Hex(data),
		Data:            data,
	}, nil
}

func resolveRemote(source string) (*ResolvedResource, error) {
	ps, err := workflowref.ParseSource(source)
	if err != nil {
		return nil, err
	}
	kind, err := workflowref.ClassifyPath(ps.Path)
	if err != nil {
		return nil, err
	}
	if kind == workflowref.PathKindDir {
		return nil, fmt.Errorf(
			"resource source %q resolves to a directory — a resource source must name a single file (no directory-expansion form)", source)
	}

	ref, err := module.ResolveRef(ps.Host, ps.Org, ps.Repo, ps.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve version %q for %s: %w", ps.Version, ps.RepoSource(), err)
	}

	archiveDir, cleanup, err := workflowref.FetchRepoArchive(ps.Host, ps.Org, ps.Repo, ref)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return extractFileFromArchiveDir(archiveDir, ps, ref)
}

// extractFileFromArchiveDir reads exactly ps.Path out of an already-extracted
// archive dir and hashes it. Pure — no network. The only new low-level thing
// resourceref needs beyond workflowref's exported primitives: a resource has
// no metadata.name to extract (unlike ResolvedWorkflowFile), just raw bytes.
func extractFileFromArchiveDir(archiveDir string, ps workflowref.ParsedSource, ref string) (*ResolvedResource, error) {
	fullPath := filepath.Join(archiveDir, filepath.FromSlash(ps.Path))
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("resource file %q not found in %s@%s: %w", ps.Path, ps.RepoSource(), ref, err)
	}
	return &ResolvedResource{
		CanonicalSource: ps.RepoSource() + "//" + ps.Path,
		Resolved:        rawFileURL(ps.Host, ps.Org, ps.Repo, ref, ps.Path),
		SHA256:          sha256Hex(data),
		Data:            data,
	}, nil
}

// rawFileURL builds a human-openable URL for a resolved file. Deliberately a
// small local copy of workflowref's unexported rawFileURL rather than a
// second export off workflowref — this is a pure display-string helper, not
// fetch/parse logic, so duplicating a few lines is preferable to growing
// workflowref's public surface further for a cosmetic value.
func rawFileURL(host, org, repo, ref, filePath string) string {
	if host != "github.com" {
		return fmt.Sprintf("https://%s/%s/%s/archive/%s.tar.gz#%s", host, org, repo, ref, filePath)
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, repo, ref, filePath)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}
