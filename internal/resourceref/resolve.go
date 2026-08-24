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
	RawVersion      string // version as originally written ("" = latest); the lock-key version component. Empty for a local path.
	Resolved        string // absolute local path, or a human-openable URL for remote
	SHA256          string // sha256 of Data
	Data            []byte // raw manifest bytes (may be multi-document YAML)
}

// Resolve resolves a ResourceRef.Source string to manifest bytes + hash.
//
// Local paths ("./..." or "/...") are read directly relative to repoRoot, no
// caching, lf/token ignored entirely. Remote sources
// ("host/org/repo[//path][@version]") are parsed via workflowref.ParseSource
// + workflowref.ClassifyPath (reusing the exact same parsing modules/
// workflows use) and must classify as PathKindFile — a resource source is
// always a single file, never a directory.
//
// A remote source is install-required, matching Workflows: if lf is
// non-nil and has a matching locked entry whose SHA256 is cached (see
// internal/resourceref.IsCached), the fetch is skipped entirely — no
// network — mirroring workflowref.resolveFile's own cache short-circuit
// exactly. Only when nothing is cached yet does this fetch live (and then
// cache the result for next time). token is an explicit GitHub-style
// access token for a private source; empty falls back to the process's own
// GITHUB_TOKEN env var (see module.resolveGitHubToken).
func Resolve(source, repoRoot string, lf *module.LockFile, token string) (*ResolvedResource, error) {
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "/") {
		return resolveLocal(source, repoRoot)
	}
	return resolveRemote(source, lf, token)
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

func resolveRemote(source string, lf *module.LockFile, token string) (*ResolvedResource, error) {
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

	canonicalSource := ps.CanonicalSource()
	var locked *module.LockedResource
	if lf != nil {
		locked = lf.GetLockedResource(canonicalSource, ps.Version)
	}
	if locked != nil && locked.SHA256 != "" && IsCached(locked.SHA256) {
		data, err := ReadCached(locked.SHA256)
		if err != nil {
			return nil, fmt.Errorf("failed to read cached resource %s: %w", canonicalSource, err)
		}
		return &ResolvedResource{
			CanonicalSource: canonicalSource,
			RawVersion:      ps.Version,
			Resolved:        locked.Resolved,
			SHA256:          locked.SHA256,
			Data:            data,
		}, nil
	}

	ref, err := module.ResolveRefWithToken(ps.Host, ps.Org, ps.Repo, ps.Version, token)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve version %q for %s: %w", ps.Version, ps.RepoSource(), err)
	}

	archiveDir, cleanup, err := workflowref.FetchRepoArchive(ps.Host, ps.Org, ps.Repo, ref, token)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	f, err := extractFileFromArchiveDir(archiveDir, ps, ref)
	if err != nil {
		return nil, err
	}
	f.RawVersion = ps.Version

	if locked != nil && locked.SHA256 != "" && f.SHA256 != locked.SHA256 {
		return nil, fmt.Errorf("digest mismatch for %s: expected %s, got %s", canonicalSource, locked.SHA256, f.SHA256)
	}

	if err := StoreInCache(f.SHA256, f.Data); err != nil {
		return nil, fmt.Errorf("failed to cache resource %s: %w", canonicalSource, err)
	}

	return f, nil
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
