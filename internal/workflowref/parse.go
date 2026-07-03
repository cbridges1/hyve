package workflowref

import (
	"fmt"
	"strings"
)

// ParseSource parses "github.com/org/repo[//path][@version]". Splits off
// version first (last "@"), then path ("//" separator, matching
// module.parseGitSource's convention), then host/org/repo. Does NOT
// classify or validate Path — call ClassifyPath separately.
func ParseSource(source string) (ParsedSource, error) {
	var version string
	if idx := strings.LastIndex(source, "@"); idx > 0 {
		version, source = source[idx+1:], source[:idx]
	}

	var path string
	if parts := strings.SplitN(source, "//", 2); len(parts) == 2 {
		path, source = parts[1], parts[0]
	}

	segments := strings.Split(source, "/")
	if len(segments) < 3 {
		return ParsedSource{}, fmt.Errorf("invalid workflow source %q: expected host/org/repo[//path][@version]", source)
	}

	return ParsedSource{
		Host:    segments[0],
		Org:     segments[1],
		Repo:    segments[2],
		Path:    path,
		Version: version,
	}, nil
}

// ApplyPathOverride merges a `path:` field onto ps. When both an inline
// "//path" and pathOverride are non-empty, pathOverride wins and warned is
// true — the caller is responsible for logging the warning.
func ApplyPathOverride(ps ParsedSource, pathOverride string) (out ParsedSource, warned bool) {
	out = ps
	if pathOverride == "" {
		return out, false
	}
	warned = ps.Path != ""
	out.Path = pathOverride
	return out, warned
}

// ClassifyPath implements the design's explicit, non-inferred rule:
//   - "" or a path ending in "/"         -> PathKindDir  (shallow listing)
//   - a path ending in ".yaml" or ".yml" -> PathKindFile
//   - anything else                      -> error (ambiguous; must disambiguate)
//
// There is deliberately no file-existence fallback.
func ClassifyPath(path string) (PathKind, error) {
	switch {
	case path == "" || strings.HasSuffix(path, "/"):
		return PathKindDir, nil
	case strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml"):
		return PathKindFile, nil
	default:
		return 0, fmt.Errorf(
			"ambiguous workflow path %q: must end in \".yaml\"/\".yml\" (a single file) or \"/\" (a directory) — there is no file-existence fallback",
			path)
	}
}
