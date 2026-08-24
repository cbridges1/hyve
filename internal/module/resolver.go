package module

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// ResolvedModule points to the local directory containing the module files.
type ResolvedModule struct {
	Dir      string
	SHA256   string
	Resolved string
	Runner   LockedRunner
	Version  string // canonical resolved version (e.g. "v1.2.3"); empty for local paths
}

// IsLocalSource reports whether a module source string names a local path
// ("./...", "../...", or an absolute path) rather than a remote Git
// reference. "../" is recognized alongside "./" so a module can live in a
// sibling checkout outside the consuming repo (e.g. "../../hyve-some-
// module") without needing to be a remote Git source at all — useful when
// the module's real repo is private and reconcile always runs from a
// machine that already has it checked out locally.
func IsLocalSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "/")
}

// githubToken returns a personal access token for authenticating to
// GitHub, read from GITHUB_TOKEN — needed to resolve a Git-sourced module
// hosted in a private repo, since the plain archive URL resolveGit uses by
// default only works for public ones. Empty (no token) preserves the
// original public-only behavior exactly.
func githubToken() string {
	return os.Getenv("GITHUB_TOKEN")
}

// resolveGitHubToken prefers an explicit token (cluster mode's live
// hyve-cli-secrets fetch, threaded in per-reconcile via
// ResolveWithToken/ResolveRefWithToken — see internal/controller/
// reconciler.go's resolveModuleIfNeeded) over the process-wide GITHUB_TOKEN
// env var, falling back to the latter when explicit is empty. Explicit,
// not os.Setenv: MaxConcurrentReconciles already permits concurrently
// reconciling different ClusterDefinitions in one process, and a
// per-reconcile token mutated into a global env var would race.
func resolveGitHubToken(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return githubToken()
}

// ResolveToken is resolveGitHubToken, exported so other packages that fetch
// from a private git host (internal/workflowref.FetchRepoArchive) apply the
// identical explicit-wins-over-GITHUB_TOKEN-env-var rule, without each
// duplicating the env var name themselves.
func ResolveToken(explicit string) string {
	return resolveGitHubToken(explicit)
}

// Resolve fetches and caches a module, returning its local directory.
// For local paths (starting with "./" or absolute): returns the dir directly, no caching.
// For Git sources: downloads, hashes, and caches under ~/.hyve/module-cache/{sha256}/.
func Resolve(source, version string, locked *LockedModule, repoRoot string) (*ResolvedModule, error) {
	return ResolveWithToken(source, version, locked, repoRoot, "")
}

// ResolveWithToken is Resolve, but with an explicit GitHub token to use
// instead of reading GITHUB_TOKEN from the process environment — see
// resolveGitHubToken. An empty token falls back to GITHUB_TOKEN exactly as
// Resolve does, so every other existing caller is unaffected.
func ResolveWithToken(source, version string, locked *LockedModule, repoRoot, token string) (*ResolvedModule, error) {
	if IsLocalSource(source) {
		return resolveLocal(source, repoRoot)
	}
	return resolveGit(source, version, locked, token)
}

func resolveLocal(source, repoRoot string) (*ResolvedModule, error) {
	var dir string
	if filepath.IsAbs(source) {
		dir = source
	} else {
		dir = filepath.Join(repoRoot, source)
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("local module path not found: %s", dir)
	}
	return &ResolvedModule{Dir: dir}, nil
}

func resolveGit(source, version string, locked *LockedModule, token string) (*ResolvedModule, error) {
	host, org, repo, subdir, err := parseGitSource(source)
	if err != nil {
		return nil, err
	}

	// Cache hit: if locked and cached, return immediately
	if locked != nil && locked.SHA256 != "" && IsCached(locked.SHA256) {
		cacheDir, err := CachePath(locked.SHA256)
		if err != nil {
			return nil, err
		}
		runner, _ := readRunnerFromManifest(cacheDir)
		return &ResolvedModule{
			Dir:      cacheDir,
			SHA256:   locked.SHA256,
			Resolved: locked.Resolved,
			Runner:   runner,
		}, nil
	}

	// Resolve version to a concrete ref (tag or HEAD)
	ref, err := ResolveRefWithToken(host, org, repo, version, token)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve version %q for %s: %w", version, source, err)
	}

	// Fetch via `git clone` + `git checkout`, not a plain archive download —
	// this is what actually lets a private module resolve using whatever
	// git authentication is already configured on the machine (a
	// credential helper from `gh auth setup-git`, a cached HTTPS
	// credential, an SSH key — the exact same mechanism ResolveRef's own
	// `git ls-remote` above already relies on), with zero extra setup
	// needed for the common case. A plain archive download can't do this:
	// GitHub's web-facing archive URL (github.com/org/repo/archive/
	// ref.tar.gz) 404s for a private repo regardless of credentials (it
	// isn't part of the authenticated REST API surface at all), and the
	// REST API's own tarball endpoint only accepts a bearer token, not a
	// credential-helper-backed request — so either path would need a
	// GITHUB_TOKEN even when git itself already works. GITHUB_TOKEN is
	// still supported here (embedded in the clone URL the same way
	// ResolveRef does), purely as a fallback for environments with no
	// other git credential mechanism configured at all, e.g. a bare CI
	// runner — not something most setups need to add.
	// cleanRepoURL (no embedded credentials) is what gets recorded in
	// hyve.lock's `resolved` field below — hyve.lock is a git-tracked
	// file, so the token-bearing cloneURL must never be the thing stored
	// there.
	cleanRepoURL := fmt.Sprintf("https://%s/%s/%s.git", host, org, repo)
	cloneURL := cleanRepoURL
	if t := resolveGitHubToken(token); t != "" && host == "github.com" {
		cloneURL = fmt.Sprintf("https://x-access-token:%s@%s/%s/%s.git", t, host, org, repo)
	}
	tmpDir, err := os.MkdirTemp("", "hyve-module-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	if err := CloneAndExtract(cloneURL, ref, subdir, tmpDir); err != nil {
		return nil, fmt.Errorf("failed to fetch module from %s@%s: %w", cleanRepoURL, ref, err)
	}

	// Compute SHA256 of directory contents
	digest, err := hashDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("failed to hash module directory: %w", err)
	}

	// Verify digest if locked
	if locked != nil && locked.SHA256 != "" && digest != locked.SHA256 {
		return nil, fmt.Errorf("digest mismatch for %s: expected %s, got %s", source, locked.SHA256, digest)
	}

	// Store in cache (StoreInCache may move tmpDir)
	if err := StoreInCache(digest, tmpDir); err != nil {
		return nil, fmt.Errorf("failed to cache module: %w", err)
	}

	cacheDir, err := CachePath(digest)
	if err != nil {
		return nil, err
	}

	runner, _ := readRunnerFromManifest(cacheDir)
	return &ResolvedModule{
		Dir:      cacheDir,
		SHA256:   digest,
		Resolved: fmt.Sprintf("%s@%s", cleanRepoURL, ref),
		Runner:   runner,
		Version:  ref,
	}, nil
}

// parseGitSource parses "github.com/org/repo//subdir" into components.
func parseGitSource(source string) (host, org, repo, subdir string, err error) {
	// Check for subdir separator
	parts := strings.SplitN(source, "//", 2)
	if len(parts) == 2 {
		subdir = parts[1]
		source = parts[0]
	}

	segments := strings.Split(source, "/")
	if len(segments) < 3 {
		return "", "", "", "", fmt.Errorf("invalid module source %q: expected host/org/repo[//subdir]", source)
	}
	host = segments[0]
	org = segments[1]
	repo = segments[2]
	return host, org, repo, subdir, nil
}

// ResolveRef resolves a version string to a git ref.
// - "" or "latest": picks the highest semver tag; falls back to "HEAD" if no tags exist.
// - semver constraint (e.g. "~> 1.2", ">= 1.0"): picks the highest matching tag.
// - anything else: treated as an exact tag or commit ref.
func ResolveRef(host, org, repo, version string) (string, error) {
	return ResolveRefWithToken(host, org, repo, version, "")
}

// ResolveRefWithToken is ResolveRef, but with an explicit GitHub token — see
// resolveGitHubToken/ResolveWithToken.
func ResolveRefWithToken(host, org, repo, version, token string) (string, error) {
	repoURL := fmt.Sprintf("https://%s/%s/%s.git", host, org, repo)
	// `git ls-remote` below has no credential prompt to fall back on in a
	// non-interactive reconcile run — a private repo needs the token
	// embedded directly in the URL (GitHub's documented HTTPS PAT format)
	// rather than relying on a credential helper being configured.
	if t := resolveGitHubToken(token); t != "" && host == "github.com" {
		repoURL = fmt.Sprintf("https://x-access-token:%s@%s/%s/%s.git", t, host, org, repo)
	}

	if version == "" || version == "latest" {
		tags, err := listRemoteTags(repoURL)
		if err != nil || len(tags) == 0 {
			return "HEAD", nil
		}
		return latestSemverTag(tags, ""), nil
	}

	// Try to parse as semver constraint
	constraint, err := semver.NewConstraint(version)
	if err != nil {
		// Not a constraint — treat as exact ref (tag or commit)
		return version, nil
	}

	tags, err := listRemoteTags(repoURL)
	if err != nil {
		return "", fmt.Errorf("failed to list tags for %s: %w", repoURL, err)
	}

	best := latestSemverTag(tags, version)
	if best == "" {
		return "", fmt.Errorf("no tag satisfies constraint %q", version)
	}
	// Verify the chosen tag satisfies the constraint (latestSemverTag with a
	// constraint arg already filters, but double-check for the error case).
	v, err := semver.NewVersion(best)
	if err != nil || !constraint.Check(v) {
		return "", fmt.Errorf("no tag satisfies constraint %q", version)
	}
	return best, nil
}

// latestSemverTag returns the highest semver tag from the list. When constraint
// is non-empty it is applied as a filter. Returns "" if no matching tag is found.
func latestSemverTag(tags []string, constraintStr string) string {
	var c *semver.Constraints
	if constraintStr != "" {
		var err error
		c, err = semver.NewConstraint(constraintStr)
		if err != nil {
			c = nil
		}
	}
	var best *semver.Version
	var bestTag string
	for _, tag := range tags {
		v, err := semver.NewVersion(tag)
		if err != nil {
			continue
		}
		if c != nil && !c.Check(v) {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
			bestTag = tag
		}
	}
	return bestTag
}

func listRemoteTags(repoURL string) ([]string, error) {
	cmd := exec.Command("git", "ls-remote", "--tags", repoURL)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		ref := parts[1]
		if strings.HasSuffix(ref, "^{}") {
			continue
		}
		if tag, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

// CloneAndExtract fetches a repo by shelling out to `git clone` + `git
// checkout` rather than downloading an archive — see resolveGit's own
// comment for why: this is what lets a private repo resolve using
// whatever git authentication the environment already has configured
// (SSH key, credential helper, or a token embedded in cloneURL), the same
// way for any git host — GitHub, GitLab, Bitbucket, self-hosted — since
// `git` itself doesn't care which one it's talking to. Exported so
// internal/workflowref.FetchRepoArchive can reuse it directly (shared by
// both Workflow and Resource remote resolution) instead of each package
// reimplementing its own fetch transport. cloneURL may embed credentials;
// callers must not log or persist it anywhere — see resolveGit's
// cleanRepoURL, which is what actually gets recorded in hyve.lock.
//
// destDir ends up containing the repo's tree at ref (or, if subdir is set,
// just that subdirectory's contents) with .git removed.
func CloneAndExtract(cloneURL, ref, subdir, destDir string) error {
	cloneCmd := exec.Command("git", "clone", "--quiet", cloneURL, destDir)
	cloneCmd.Stderr = os.Stderr
	if err := cloneCmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	if ref != "" && ref != "HEAD" {
		checkoutCmd := exec.Command("git", "-C", destDir, "checkout", "--quiet", ref)
		checkoutCmd.Stderr = os.Stderr
		if err := checkoutCmd.Run(); err != nil {
			return fmt.Errorf("git checkout %s: %w", ref, err)
		}
	}

	if err := os.RemoveAll(filepath.Join(destDir, ".git")); err != nil {
		return fmt.Errorf("remove .git: %w", err)
	}

	if subdir == "" {
		return nil
	}

	// Move subdir's contents up to destDir's root via a copy (not a
	// rename): destDir is about to be wiped and replaced with exactly
	// subdir's contents, and copying first means a failure partway
	// through leaves the original clone intact rather than a half-deleted
	// destDir.
	srcDir := filepath.Join(destDir, subdir)
	staging, err := os.MkdirTemp(filepath.Dir(destDir), "hyve-module-subdir-*")
	if err != nil {
		return fmt.Errorf("stage subdir extraction: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := copyTree(srcDir, staging); err != nil {
		return fmt.Errorf("copy subdir %s: %w", subdir, err)
	}
	if err := os.RemoveAll(destDir); err != nil {
		return err
	}
	return os.Rename(staging, destDir)
}

// copyTree recursively copies src's contents into dst (both must already
// make sense as directories; dst is created if missing).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// hashDir computes a deterministic SHA256 over all files in a directory.
func hashDir(dir string) (string, error) {
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(h, "%s\n", rel)
		h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func readRunnerFromManifest(dir string) (LockedRunner, error) {
	data, err := os.ReadFile(filepath.Join(dir, "module.yaml"))
	if err != nil {
		return LockedRunner{}, err
	}
	var m ModuleManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return LockedRunner{}, err
	}
	return LockedRunner{Image: m.Spec.Runner.Image}, nil
}

// LoadManifestForSource resolves a module's directory and reads its module.yaml.
// For local paths the directory is read directly; for Git sources the cached
// directory (looked up via lf) is used. Returns nil without error when the
// module cannot be found locally (not yet installed).
func LoadManifestForSource(source, version, repoRoot string, lf *LockFile) (*ModuleManifest, error) {
	var dir string

	if IsLocalSource(source) {
		resolved, err := resolveLocal(source, repoRoot)
		if err != nil {
			return nil, nil // not available locally
		}
		dir = resolved.Dir
	} else {
		if lf == nil {
			return nil, nil
		}
		locked := lf.GetLocked(source, version)
		if locked == nil || locked.SHA256 == "" || !IsCached(locked.SHA256) {
			return nil, nil
		}
		cacheDir, err := CachePath(locked.SHA256)
		if err != nil {
			return nil, err
		}
		dir = cacheDir
	}

	data, err := os.ReadFile(filepath.Join(dir, "module.yaml"))
	if err != nil {
		return nil, nil
	}
	var m ModuleManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse module.yaml for %s: %w", source, err)
	}
	return &m, nil
}
