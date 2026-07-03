package workflowref

// ParsedSource is the decomposed form of
// "github.com/<org>/<repo>[//<path>][@<version>]".
type ParsedSource struct {
	Host    string
	Org     string
	Repo    string
	Path    string // "" (repo root), a directory ("foo/bar/"), or a file ("foo/bar.yaml")
	Version string // raw version as written; "" means "latest"
}

// RepoSource returns "host/org/repo" with no path or version component.
func (p ParsedSource) RepoSource() string { return p.Host + "/" + p.Org + "/" + p.Repo }

// CanonicalSource returns "host/org/repo//path" (never includes version —
// version is a separate lock-key component, joined via module.LockKey).
// Only meaningful once p.Path names a concrete file.
func (p ParsedSource) CanonicalSource() string { return p.RepoSource() + "//" + p.Path }

// PathKind is the result of classifying a source's path component.
type PathKind int

const (
	// PathKindFile means the path names exactly one workflow YAML file.
	PathKindFile PathKind = iota
	// PathKindDir means the path names a directory to shallow-list for *.yaml files.
	PathKindDir
)

// ResolvedWorkflowFile is one fetched, content-hashed workflow YAML file —
// the unit that becomes exactly one hyve.lock `workflows` entry.
type ResolvedWorkflowFile struct {
	CanonicalSource string // e.g. "github.com/org/repo//platform/workflows/create.yaml"
	RawVersion      string // version as originally written ("" = latest); the lock-key version component
	ResolvedVersion string // concrete resolved ref (tag/branch/HEAD); empty when served from cache
	Name            string // workflow's metadata.name (best-effort parse; falls back to filename stem)
	Resolved        string // https://raw.githubusercontent.com/<org>/<repo>/<ref>/<path> — a URL a human can open
	SHA256          string // sha256 of this file's raw bytes only
	Data            []byte // the file's raw YAML bytes
}
