package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/cbridges1/hyve/internal/install"
)

// settings is what 'hyve local up' was given and which hyve it installed,
// kept in ~/.hyve/local so the local cluster follows the CLI: a newer
// release upgrades it with the same settings at its first local-mode
// command.
type settings struct {
	// Version is the hyve release installed, empty for a build from
	// source or a chosen --image, which the CLI doesn't upgrade.
	Version      string   `json:"version,omitempty"`
	Image        string   `json:"image,omitempty"`
	Port         int      `json:"port"`
	K3sImage     string   `json:"k3sImage"`
	PublicURL    string   `json:"publicURL,omitempty"`
	GOGC         string   `json:"gogc,omitempty"`
	Full         bool     `json:"full,omitempty"`
	APIServerArg []string `json:"apiServerArgs,omitempty"`
}

func settingsFile(home string) string { return filepath.Join(Dir(home), "settings.json") }

// loadSettings returns nil when the local cluster was set up by a CLI
// from before settings were kept.
func loadSettings(home string) (*settings, error) {
	b, err := os.ReadFile(settingsFile(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s settings
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", settingsFile(home), err)
	}
	return &s, nil
}

func saveSettings(home string, o Options, image string) error {
	s := settings{Image: image, Port: o.Port, K3sImage: o.K3sImage, PublicURL: o.PublicURL,
		GOGC: o.GOGC, Full: o.Full, APIServerArg: o.APIServerArg}
	if image == "" && !install.IsDevBuild() {
		s.Version = install.Version
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsFile(home), append(b, '\n'), 0o600)
}

// Version is the hyve release the local cluster runs, empty when it isn't
// known (a build from source, a chosen image, or set up by an older CLI).
func Version(home string) string {
	if s, _ := loadSettings(home); s != nil {
		return s.Version
	}
	return ""
}

// newer says whether release a is newer than b.
func newer(a, b string) bool {
	return semver.Compare("v"+strings.TrimPrefix(a, "v"), "v"+strings.TrimPrefix(b, "v")) > 0
}

// checkDowngrade refuses to install an older hyve over a newer one, whose
// CRDs and stored objects it may not understand.
func checkDowngrade(home string) error {
	if install.IsDevBuild() {
		return nil
	}
	s, err := loadSettings(home)
	if err != nil || s == nil || s.Version == "" {
		return err
	}
	if newer(s.Version, install.Version) {
		return fmt.Errorf("local mode runs hyve %s, newer than this CLI (%s): upgrade the CLI, or 'hyve local down' first", s.Version, install.Version)
	}
	return nil
}

// upgrade brings the local cluster to this CLI's release, with the
// settings it was set up with. A build from source, a chosen image or a
// newer install are left alone.
func upgrade(ctx context.Context, home string, logf func(string, ...any)) error {
	if install.IsDevBuild() {
		return nil
	}
	s, err := loadSettings(home)
	if err != nil {
		return err
	}
	switch {
	case s == nil:
		logf("Local mode was set up by an older CLI: run 'hyve local up' (with the flags you used then) to upgrade it to hyve %s", install.Version)
		return nil
	case s.Image != "" || s.Version == install.Version:
		return nil
	case s.Version != "" && newer(s.Version, install.Version):
		logf("Local mode runs hyve %s, newer than this CLI (%s); upgrade the CLI", s.Version, install.Version)
		return nil
	}
	from := s.Version
	if from == "" {
		from = "a build from source"
	}
	logf("Upgrading local mode from %s to hyve %s", from, install.Version)
	return Up(ctx, Options{Home: home, Port: s.Port, K3sImage: s.K3sImage, PublicURL: s.PublicURL,
		GOGC: s.GOGC, Full: s.Full, APIServerArg: s.APIServerArg, Log: logf})
}
