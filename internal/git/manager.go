package git

import (
	"fmt"
	"os/exec"
)

// NewBackend creates a new Git backend using system git
func NewBackend(repoURL, localPath, username, token string) (*SystemBackend, error) {
	if err := checkSystemGit(); err != nil {
		return nil, fmt.Errorf("system git not available: %w", err)
	}
	return NewSystemBackend(repoURL, localPath, username, token), nil
}

// checkSystemGit verifies that system git is available
func checkSystemGit() error {
	cmd := exec.Command("git", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git command not found in PATH: %w", err)
	}
	return nil
}
