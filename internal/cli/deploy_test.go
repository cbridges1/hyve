package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/resources"
)

// patched runs update's source flags over src.
func patched(t *testing.T, src resources.Entry, args ...string) (resources.Entry, error) {
	t.Helper()
	var f deployFlags
	cmd := &cobra.Command{}
	f.add(cmd, true)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	err := f.patchSource(cmd, &src)
	return src, err
}

func TestPatchSourceHelm(t *testing.T) {
	src := resources.Entry{Name: "web", HelmOp: map[string]any{
		"defaultNamespace": "web",
		"helm": map[string]any{"chart": "web", "repo": "https://charts.example", "version": "1.0.0",
			"values": map[string]any{"image": map[string]any{"repository": "ghcr.io/me/web", "tag": "a1"}, "replicas": 2}},
		"diff": map[string]any{"comparePatches": []any{}},
	}}
	got, err := patched(t, src, "--version", "1.1.0", "--set", "image.tag=b2")
	if err != nil {
		t.Fatal(err)
	}
	h := got.HelmOp["helm"].(map[string]any)
	want := map[string]any{"image": map[string]any{"repository": "ghcr.io/me/web", "tag": "b2"}, "replicas": 2}
	if h["version"] != "1.1.0" || h["chart"] != "web" || h["repo"] != "https://charts.example" || !reflect.DeepEqual(h["values"], want) {
		t.Fatalf("helm: %v", h)
	}
	if got.HelmOp["defaultNamespace"] != "web" || got.HelmOp["diff"] == nil {
		t.Fatalf("the rest of the source changed: %v", got.HelmOp)
	}
	if _, err := patched(t, src, "--revision", "v2"); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("--revision on a chart: %v", err)
	}
}

func TestPatchSourceGit(t *testing.T) {
	src := resources.Entry{Name: "config", GitRepo: map[string]any{"repo": "https://git.example/config", "branch": "main", "paths": []any{"apps"}}}
	got, err := patched(t, src, "--revision", "v1.4.3")
	if err != nil {
		t.Fatal(err)
	}
	if got.GitRepo["revision"] != "v1.4.3" || got.GitRepo["branch"] != "main" || !reflect.DeepEqual(got.GitRepo["paths"], []any{"apps"}) {
		t.Fatalf("--revision: %v", got.GitRepo)
	}
	got, err = patched(t, got, "--branch", "release")
	if err != nil {
		t.Fatal(err)
	}
	if got.GitRepo["branch"] != "release" || got.GitRepo["revision"] != nil {
		t.Fatalf("--branch keeps the pinned revision: %v", got.GitRepo)
	}
	if _, err := patched(t, src, "--version", "2"); err == nil {
		t.Fatal("--version on a git source passed")
	}
}

func TestMergeValues(t *testing.T) {
	old := map[string]any{"a": map[string]any{"b": 1, "c": 2}, "list": []any{1}}
	got := mergeValues(old, map[string]any{"a": map[string]any{"b": 3}, "list": []any{2}})
	want := map[string]any{"a": map[string]any{"b": 3, "c": 2}, "list": []any{2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if old["a"].(map[string]any)["b"] != 1 {
		t.Fatal("old values changed")
	}
}
