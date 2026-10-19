package shared

import (
	"os"
	"path/filepath"
)

// HomeFlagValue is bound to the root command's --home persistent flag (see
// cmd/root.go's init) — lives here, not in the top-level cmd package,
// specifically so cmd/cluster (and any other subpackage) can resolve the
// same home directory without importing cmd itself, which would be a
// cycle: cmd already imports cmd/cluster.
var HomeFlagValue string

// HyveHome returns the effective Hyve home directory. It respects (in order):
//  1. --home flag (HomeFlagValue)
//  2. HYVE_HOME environment variable
//  3. ~/.hyve (default)
func HyveHome() string {
	if home := resolvedHyveHome(); home != "" {
		return home
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".hyve")
}

func resolvedHyveHome() string {
	if HomeFlagValue != "" {
		return HomeFlagValue
	}
	return os.Getenv("HYVE_HOME")
}

// ExplicitHome returns the hyve home when one was chosen with --home or
// HYVE_HOME, or "" for the default; kubeconfigs pass it to `hyve token`.
func ExplicitHome() string {
	if resolvedHyveHome() == "" {
		return ""
	}
	return HyveHome()
}
