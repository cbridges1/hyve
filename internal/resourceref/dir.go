package resourceref

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cbridges1/hyve/internal/module"
	"github.com/cbridges1/hyve/internal/workflowref"
)

// IsLocalSource reports whether source is a filesystem path rather than a
// remote git reference — "./", "../", or absolute, matching module sources.
func IsLocalSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "/")
}

// IsDirSource reports whether source names a directory rather than a single
// file — the same explicit rule workflow references use (see
// workflowref.ClassifyPath): a path ending in "/" is a directory; there is no
// file-existence fallback. For a remote source the rule applies to the path
// after "//" (an empty path, the repository root, is also a directory).
func IsDirSource(source string) bool {
	if IsLocalSource(source) {
		return strings.HasSuffix(source, "/")
	}
	ps, err := workflowref.ParseSource(source)
	if err != nil {
		return false
	}
	kind, err := workflowref.ClassifyPath(ps.Path)
	return err == nil && kind == workflowref.PathKindDir
}

// DirEntry is one file expanded from a directory source.
type DirEntry struct {
	// Stem is the file name without its extension — combined with the
	// directory resource's own name as "<name>/<stem>" to name the file's
	// own tracked resource.
	Stem     string
	Resolved *ResolvedResource
}

// ResolveDir expands a directory source into its manifest files: a shallow
// listing (not recursive), only files ending in ".yaml", sorted by name — the
// same rule as workflow directory references, so file names control apply
// order (e.g. 00-namespace.yaml, 10-crds.yaml). A remote directory is always
// fetched fresh — there's no single lock key for "whatever this directory
// holds", the same as a workflow directory reference.
func ResolveDir(source, repoRoot, token string) ([]DirEntry, error) {
	if IsLocalSource(source) {
		dir := source
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(repoRoot, source)
		}
		// source already ends in "/" (IsDirSource); concatenating keeps it
		// exactly as written — path.Join would drop a leading "./".
		return readManifestDir(dir, func(name string) string { return source + name })
	}

	ps, err := workflowref.ParseSource(source)
	if err != nil {
		return nil, err
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

	entries, err := readManifestDir(filepath.Join(archiveDir, filepath.FromSlash(ps.Path)), func(name string) string {
		return ps.RepoSource() + "//" + path.Join(ps.Path, name)
	})
	if err != nil {
		return nil, fmt.Errorf("%s@%s: %w", ps.RepoSource(), ref, err)
	}
	for i := range entries {
		entries[i].Resolved.RawVersion = ps.Version
		entries[i].Resolved.Resolved = rawFileURL(ps.Host, ps.Org, ps.Repo, ref, path.Join(ps.Path, entries[i].Stem+".yaml"))
	}
	return entries, nil
}

// readManifestDir lists dir's *.yaml files in name order. canonical builds
// each file's CanonicalSource from its base name.
func readManifestDir(dir string, canonical func(name string) string) ([]DirEntry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("resource directory %q not found: %w", dir, err)
	}
	var names []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".yaml") {
			names = append(names, f.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no *.yaml files found directly in resource directory %q", dir)
	}

	out := make([]DirEntry, 0, len(names))
	for _, name := range names {
		full := filepath.Join(dir, name)
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", full, err)
		}
		out = append(out, DirEntry{
			Stem: strings.TrimSuffix(name, ".yaml"),
			Resolved: &ResolvedResource{
				CanonicalSource: canonical(name),
				Resolved:        full,
				SHA256:          sha256Hex(data),
				Data:            data,
			},
		})
	}
	return out, nil
}
