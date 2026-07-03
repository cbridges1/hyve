package workflowref

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSource(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		want    ParsedSource
		wantErr bool
	}{
		{
			name:   "repo root, no version",
			source: "github.com/org/repo",
			want:   ParsedSource{Host: "github.com", Org: "org", Repo: "repo"},
		},
		{
			name:   "with version",
			source: "github.com/org/repo@v1.2.0",
			want:   ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Version: "v1.2.0"},
		},
		{
			name:   "with file path",
			source: "github.com/org/repo//foo/bar.yaml",
			want:   ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "foo/bar.yaml"},
		},
		{
			name:   "with file path and version",
			source: "github.com/org/repo//foo/bar.yaml@v2",
			want:   ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "foo/bar.yaml", Version: "v2"},
		},
		{
			name:   "with directory path (trailing slash)",
			source: "github.com/org/repo//foo/",
			want:   ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "foo/"},
		},
		{
			name:   "branch as version",
			source: "github.com/org/repo//foo/@main",
			want:   ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "foo/", Version: "main"},
		},
		{
			name:    "too few segments",
			source:  "github.com/org",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSource(tt.source)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestClassifyPath(t *testing.T) {
	tests := []struct {
		path    string
		want    PathKind
		wantErr bool
	}{
		{path: "", want: PathKindDir},
		{path: "a/b/", want: PathKindDir},
		{path: "a/b.yaml", want: PathKindFile},
		{path: "a/b.yml", want: PathKindFile},
		{path: "a/b", wantErr: true},
		{path: "a/b.YAML", wantErr: true}, // case-sensitive, no fallback
		{path: "a.txt", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := ClassifyPath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestApplyPathOverride(t *testing.T) {
	base := ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "inline.yaml"}

	t.Run("no override", func(t *testing.T) {
		out, warned := ApplyPathOverride(base, "")
		assert.Equal(t, base, out)
		assert.False(t, warned)
	})

	t.Run("override on empty inline path", func(t *testing.T) {
		noInline := ParsedSource{Host: "github.com", Org: "org", Repo: "repo"}
		out, warned := ApplyPathOverride(noInline, "override.yaml")
		assert.Equal(t, "override.yaml", out.Path)
		assert.False(t, warned)
	})

	t.Run("override wins over inline, warns", func(t *testing.T) {
		out, warned := ApplyPathOverride(base, "override.yaml")
		assert.Equal(t, "override.yaml", out.Path)
		assert.True(t, warned)
	})
}

func TestParsedSourceHelpers(t *testing.T) {
	ps := ParsedSource{Host: "github.com", Org: "org", Repo: "repo", Path: "foo/bar.yaml"}
	assert.Equal(t, "github.com/org/repo", ps.RepoSource())
	assert.Equal(t, "github.com/org/repo//foo/bar.yaml", ps.CanonicalSource())
}
