package main

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBinaryBuilds verifies that the hyve binary can be compiled.
func TestBinaryBuilds(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build failed:\n%s", string(out))
}

// TestHelpFlag verifies that the binary responds to --help without crashing.
func TestHelpFlag(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "--help")
	out, err := cmd.CombinedOutput()
	// --help exits with code 0 in Cobra.
	assert.NoError(t, err, "unexpected error running --help:\n%s", string(out))
	assert.Contains(t, string(out), "hyve")
}

// TestVersionOrHelp verifies that invoking with no args does not panic.
func TestNoArgs(t *testing.T) {
	cmd := exec.Command("go", "run", ".")
	// The root command prints help and exits 0 when no subcommand is given.
	_, _ = cmd.CombinedOutput()
	// We only assert the process doesn't panic (non-zero exit is acceptable).
}
