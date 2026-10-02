package workflowref

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cbridges1/hyve/internal/module"
)

// Resolve fetches (or serves from cache) every workflow YAML file referenced
// by a remote source string.
//
//   - File-kind sources (path ends in .yaml/.yml) resolve to exactly one
//     ResolvedWorkflowFile. If lf is non-nil and has a matching locked entry
//     whose SHA256 is cached, the fetch is skipped entirely — no network —
//     mirroring module.Resolve's cache short-circuit.
//   - Directory-kind sources (empty path, or a path ending "/") always
//     perform one network fetch (the repo archive at the resolved ref) and
//     expand into N ResolvedWorkflowFile values, one per top-level *.yaml
//     file (non-recursive). lf is not consulted to skip this fetch — there
//     is no single lock key for a directory reference.
//
// pathOverride is a `path:` field ("" when not set); it takes precedence
// over an inline "//path" in rawSource per ApplyPathOverride. token is an
// explicit GitHub-style access token to use instead of reading GITHUB_TOKEN
// from the process environment — see module.resolveGitHubToken; empty
// falls back to that env var exactly as before, so every existing caller
// passing "" is unaffected.
func Resolve(rawSource, pathOverride string, lf *module.LockFile, token string) ([]*ResolvedWorkflowFile, error) {
	ps, err := ParseSource(rawSource)
	if err != nil {
		return nil, err
	}
	ps, _ = ApplyPathOverride(ps, pathOverride)

	kind, err := ClassifyPath(ps.Path)
	if err != nil {
		return nil, err
	}

	if kind == PathKindFile {
		f, err := resolveFile(ps, lf, token)
		if err != nil {
			return nil, err
		}
		return []*ResolvedWorkflowFile{f}, nil
	}
	return resolveDir(ps, token)
}

// resolveFile resolves a single-file source, short-circuiting on a cache hit.
func resolveFile(ps ParsedSource, lf *module.LockFile, token string) (*ResolvedWorkflowFile, error) {
	canonicalSource := ps.CanonicalSource()

	var locked *module.LockedWorkflow
	if lf != nil {
		locked = lf.GetLockedWorkflow(canonicalSource, ps.Version)
	}

	if locked != nil && locked.SHA256 != "" && IsCached(locked.SHA256) {
		data, err := ReadCached(locked.SHA256)
		if err != nil {
			return nil, fmt.Errorf("failed to read cached workflow %s: %w", canonicalSource, err)
		}
		return &ResolvedWorkflowFile{
			CanonicalSource: canonicalSource,
			RawVersion:      ps.Version,
			Name:            extractWorkflowName(data, path.Base(ps.Path)),
			Resolved:        locked.Resolved,
			SHA256:          locked.SHA256,
			Data:            data,
		}, nil
	}

	ref, err := module.ResolveRefWithToken(ps.Host, ps.Org, ps.Repo, ps.Version, token)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve version %q for %s: %w", ps.Version, canonicalSource, err)
	}

	archiveDir, cleanup, err := FetchRepoArchive(ps.Host, ps.Org, ps.Repo, ref, token)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	f, err := extractFileFromArchiveDir(archiveDir, ps, ref)
	if err != nil {
		return nil, err
	}

	if locked != nil && locked.SHA256 != "" && f.SHA256 != locked.SHA256 {
		return nil, fmt.Errorf("digest mismatch for %s: expected %s, got %s", canonicalSource, locked.SHA256, f.SHA256)
	}

	if err := StoreInCache(f.SHA256, f.Data); err != nil {
		return nil, fmt.Errorf("failed to cache workflow %s: %w", canonicalSource, err)
	}

	return f, nil
}

// resolveDir resolves a directory-form source: always fetches, always
// expands into N files — no cache short-circuit is possible for a directory
// reference since there is no single lock key for it.
func resolveDir(ps ParsedSource, token string) ([]*ResolvedWorkflowFile, error) {
	ref, err := module.ResolveRefWithToken(ps.Host, ps.Org, ps.Repo, ps.Version, token)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve version %q for %s: %w", ps.Version, ps.RepoSource(), err)
	}

	archiveDir, cleanup, err := FetchRepoArchive(ps.Host, ps.Org, ps.Repo, ref, token)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	files, err := extractDirFromArchiveDir(archiveDir, ps, ref)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		if err := StoreInCache(f.SHA256, f.Data); err != nil {
			return nil, fmt.Errorf("failed to cache workflow %s: %w", f.CanonicalSource, err)
		}
	}
	return files, nil
}

// FetchRepoArchive clones the WHOLE repo at ref into a temp dir
// (module.CloneAndExtract with subdir=""), returning the dir and a cleanup
// func the caller must defer. Exported so internal/resourceref can reuse
// this fetch primitive rather than reimplementing it a third time.
//
// Uses a real `git clone` + `git checkout`, not an HTTP archive download —
// deliberately git-host-agnostic: works identically against GitHub,
// GitLab, Bitbucket, or a self-hosted server, using whatever git
// credential mechanism is already configured (SSH key, credential
// helper), the same reasoning module.resolveGit already established for
// module resolution. token is resolved via module.ResolveToken (explicit
// wins, falling back to the process's own GITHUB_TOKEN env var — so every
// existing caller passing "" keeps working via the env var exactly as
// before this function grew a token param at all). When non-empty, it's
// embedded in the clone URL as a bare-username HTTPS credential
// (https://TOKEN@host/org/repo.git) — broadly supported across git hosts,
// and deliberately NOT gated to host == "github.com" the way
// module.resolveGit's own token-embedding currently is; that's a narrower,
// pre-existing choice made there, not one to propagate into new code.
func FetchRepoArchive(host, org, repo, ref, token string) (dir string, cleanup func(), err error) {
	token = module.ResolveToken(token)
	cloneURL := fmt.Sprintf("https://%s/%s/%s.git", host, org, repo)
	if token != "" {
		cloneURL = fmt.Sprintf("https://%s@%s/%s/%s.git", token, host, org, repo)
	}
	tmpDir, err := os.MkdirTemp("", "hyve-workflowref-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(tmpDir) }

	if err := module.CloneAndExtract(cloneURL, ref, "", tmpDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to fetch workflow source from %s/%s/%s@%s: %w", host, org, repo, ref, err)
	}
	return tmpDir, cleanup, nil
}

// extractFileFromArchiveDir reads exactly ps.Path out of an already-extracted
// archive dir and builds a ResolvedWorkflowFile. Pure — no network.
func extractFileFromArchiveDir(archiveDir string, ps ParsedSource, ref string) (*ResolvedWorkflowFile, error) {
	fullPath := filepath.Join(archiveDir, filepath.FromSlash(ps.Path))
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("workflow file %q not found in %s@%s: %w", ps.Path, ps.RepoSource(), ref, err)
	}

	sum := sha256.Sum256(data)
	return &ResolvedWorkflowFile{
		CanonicalSource: ps.CanonicalSource(),
		RawVersion:      ps.Version,
		ResolvedVersion: ref,
		Name:            extractWorkflowName(data, path.Base(ps.Path)),
		Resolved:        rawFileURL(ps.Host, ps.Org, ps.Repo, ref, ps.Path),
		SHA256:          fmt.Sprintf("%x", sum[:]),
		Data:            data,
	}, nil
}

// extractDirFromArchiveDir shallow-lists ps.Path (os.ReadDir — naturally
// non-recursive) inside an already-extracted archive dir, keeping only
// entries whose name has a literal ".yaml" suffix (deliberately not ".yml"
// — directory listing is stricter than the single-file suffix rule), and
// builds one ResolvedWorkflowFile per match. Pure — no network.
func extractDirFromArchiveDir(archiveDir string, ps ParsedSource, ref string) ([]*ResolvedWorkflowFile, error) {
	dirPath := filepath.Join(archiveDir, filepath.FromSlash(ps.Path))
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("workflow directory %q not found in %s@%s: %w", ps.Path, ps.RepoSource(), ref, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	if len(names) == 0 {
		return nil, fmt.Errorf("no *.yaml files found directly in %q of %s@%s", ps.Path, ps.RepoSource(), ref)
	}

	var out []*ResolvedWorkflowFile
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dirPath, n))
		if err != nil {
			return nil, err
		}
		filePath := path.Join(ps.Path, n)
		sum := sha256.Sum256(data)
		out = append(out, &ResolvedWorkflowFile{
			CanonicalSource: ps.RepoSource() + "//" + filePath,
			RawVersion:      ps.Version,
			ResolvedVersion: ref,
			Name:            extractWorkflowName(data, n),
			Resolved:        rawFileURL(ps.Host, ps.Org, ps.Repo, ref, filePath),
			SHA256:          fmt.Sprintf("%x", sum[:]),
			Data:            data,
		})
	}
	return out, nil
}

// rawFileURL builds a human-openable URL for a resolved file. GitHub-specific
// (raw.githubusercontent.com); for a non-github.com host this falls back to
// a best-effort documentation string — the fetch mechanism itself (via
// module.DownloadAndExtract) works for any host serving
// .../archive/<ref>.tar.gz, only this display URL is GitHub-specific.
func rawFileURL(host, org, repo, ref, filePath string) string {
	if host != "github.com" {
		return fmt.Sprintf("https://%s/%s/%s/archive/%s.tar.gz#%s", host, org, repo, ref, filePath)
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, repo, ref, filePath)
}

// extractWorkflowName best-effort-parses {metadata: {name: ...}} out of
// data; falls back to the filename stem (no extension) on parse error or an
// empty name. Deliberately does not import internal/workflow — only needs
// metadata.name, keeping this package decoupled from the full
// Workflow/WorkflowSpec surface.
func extractWorkflowName(data []byte, filename string) string {
	var partial struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(data, &partial); err == nil && partial.Metadata.Name != "" {
		return partial.Metadata.Name
	}
	base := path.Base(filename)
	return strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
}
