package module

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLockedWorkflow_RoundTripYAML(t *testing.T) {
	lf := &LockFile{
		Version: 1,
		Modules: map[string]*LockedModule{},
		Workflows: map[string]*LockedWorkflow{
			"github.com/org/repo//a.yaml@v1.0.0": {
				Name:     "a",
				Source:   "github.com/org/repo//a.yaml",
				Resolved: "https://raw.githubusercontent.com/org/repo/v1.0.0/a.yaml",
				SHA256:   "deadbeef",
			},
		},
	}

	data, err := yaml.Marshal(lf)
	require.NoError(t, err)

	var got LockFile
	require.NoError(t, yaml.Unmarshal(data, &got))
	require.Contains(t, got.Workflows, "github.com/org/repo//a.yaml@v1.0.0")
	assert.Equal(t, lf.Workflows["github.com/org/repo//a.yaml@v1.0.0"], got.Workflows["github.com/org/repo//a.yaml@v1.0.0"])
}

func TestGetSetRemoveLockedWorkflow(t *testing.T) {
	lf := &LockFile{Version: 1}

	assert.Nil(t, lf.GetLockedWorkflow("github.com/org/repo//a.yaml", "v1.0.0"))

	w := &LockedWorkflow{Name: "a", Source: "github.com/org/repo//a.yaml", SHA256: "abc"}
	lf.SetLockedWorkflow("github.com/org/repo//a.yaml", "v1.0.0", w)
	assert.Equal(t, w, lf.GetLockedWorkflow("github.com/org/repo//a.yaml", "v1.0.0"))

	lf.RemoveLockedWorkflow("github.com/org/repo//a.yaml", "v1.0.0")
	assert.Nil(t, lf.GetLockedWorkflow("github.com/org/repo//a.yaml", "v1.0.0"))
}

func TestSplitLockKey(t *testing.T) {
	tests := []struct {
		key        string
		wantSource string
		wantVer    string
	}{
		{"github.com/org/repo//a.yaml@v1.0.0", "github.com/org/repo//a.yaml", "v1.0.0"},
		{"github.com/org/repo//a.yaml@", "github.com/org/repo//a.yaml", ""},
		{"no-at-sign", "no-at-sign", ""},
	}
	for _, tt := range tests {
		source, version := splitLockKey(tt.key)
		assert.Equal(t, tt.wantSource, source)
		assert.Equal(t, tt.wantVer, version)
	}
}

func TestFindLockedWorkflowsByName(t *testing.T) {
	lf := &LockFile{
		Version: 1,
		Workflows: map[string]*LockedWorkflow{
			"github.com/org/repo//a.yaml@v1.0.0":  {Name: "setup", Source: "github.com/org/repo//a.yaml"},
			"github.com/org/other//b.yaml@v2.0.0": {Name: "setup", Source: "github.com/org/other//b.yaml"},
			"github.com/org/repo//c.yaml@v1.0.0":  {Name: "deploy", Source: "github.com/org/repo//c.yaml"},
		},
	}

	t.Run("no match", func(t *testing.T) {
		assert.Empty(t, lf.FindLockedWorkflowsByName("missing"))
	})

	t.Run("one match", func(t *testing.T) {
		matches := lf.FindLockedWorkflowsByName("deploy")
		require.Len(t, matches, 1)
		assert.Equal(t, "github.com/org/repo//c.yaml", matches[0].Source)
		assert.Equal(t, "v1.0.0", matches[0].Version)
	})

	t.Run("ambiguous", func(t *testing.T) {
		matches := lf.FindLockedWorkflowsByName("setup")
		assert.Len(t, matches, 2)
	})
}
