package local

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/cbridges1/hyve/internal/install"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.0.0-rc.13", "1.0.0-rc.12", true},
		{"1.0.0-rc.12", "1.0.0-rc.13", false},
		{"1.0.0", "1.0.0-rc.13", true},
		{"v1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// TestUpgradeSkips covers the cases that leave the local cluster alone,
// which return before touching Docker.
func TestUpgradeSkips(t *testing.T) {
	defer func(v string) { install.Version = v }(install.Version)
	install.Version = "1.0.0-rc.13"
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	var said []string
	logf := func(f string, _ ...any) { said = append(said, f) }

	// Set up by an older CLI: no settings, so only advice.
	if err := upgrade(context.Background(), home, logf); err != nil || len(said) != 1 || !strings.Contains(said[0], "hyve local up") {
		t.Fatalf("no settings: err %v, said %q", err, said)
	}
	for name, s := range map[string]settings{
		"same release": {Version: "1.0.0-rc.13"},
		"chosen image": {Image: "hyve:dev"},
		"newer":        {Version: "1.0.0-rc.14"},
	} {
		said = nil
		if err := saveSettings(home, Options{}, s.Image); err != nil {
			t.Fatal(err)
		}
		if s.Version != "" {
			install.Version = s.Version
			_ = saveSettings(home, Options{}, "")
			install.Version = "1.0.0-rc.13"
		}
		if err := upgrade(context.Background(), home, logf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == "newer" != (len(said) == 1) {
			t.Errorf("%s: said %q", name, said)
		}
	}
	install.Version = "1.0.0-rc.14"
	_ = saveSettings(home, Options{}, "")
	install.Version = "1.0.0-rc.13"
	if err := checkDowngrade(home); err == nil {
		t.Error("checkDowngrade allowed rc.13 over rc.14")
	}
	install.Version = "dev"
	if err := checkDowngrade(home); err != nil {
		t.Errorf("a build from source: %v", err)
	}
}
