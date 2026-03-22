package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BranchInfo holds information about a Git branch
type BranchInfo struct {
	Name      string
	IsCurrent bool
	Hash      string
}

// SystemBackend handles Git repository operations using system git command
type SystemBackend struct {
	repoURL   string
	localPath string
	username  string
	token     string
}

// NewSystemBackend creates a new Git repository manager using system git
func NewSystemBackend(repoURL, localPath, username, token string) *SystemBackend {
	return &SystemBackend{
		repoURL:   repoURL,
		localPath: localPath,
		username:  username,
		token:     token,
	}
}

// runGitCommand executes a git command in the repository directory
func (m *SystemBackend) runGitCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = m.localPath

	// Configure git credential helper for authentication
	if m.username != "" && m.token != "" {
		// Set up authentication URL
		authURL := m.repoURL
		if strings.Contains(authURL, "://") {
			parts := strings.SplitN(authURL, "://", 2)
			authURL = fmt.Sprintf("%s://%s:%s@%s", parts[0], m.username, m.token, parts[1])
		}
		cmd.Env = append(os.Environ(), fmt.Sprintf("GIT_ASKPASS=echo"), fmt.Sprintf("GIT_USERNAME=%s", m.username), fmt.Sprintf("GIT_PASSWORD=%s", m.token))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w\nOutput: %s", strings.Join(args, " "), err, string(output))
	}
	return nil
}

// runGitCommandOutput executes a git command and returns its output
func (m *SystemBackend) runGitCommandOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = m.localPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w\nOutput: %s", strings.Join(args, " "), err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

// Clone clones the repository to the local path
func (m *SystemBackend) Clone(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(m.localPath, ".git")); err == nil {
		// Repository already exists, just open it
		return nil
	}

	// Create parent directories if they don't exist
	if err := os.MkdirAll(filepath.Dir(m.localPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directories: %w", err)
	}

	// Build clone URL with authentication
	cloneURL := m.repoURL
	if m.username != "" && m.token != "" {
		if strings.Contains(cloneURL, "://") {
			parts := strings.SplitN(cloneURL, "://", 2)
			cloneURL = fmt.Sprintf("%s://%s:%s@%s", parts[0], m.username, m.token, parts[1])
		}
	}

	cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, m.localPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If repository is empty, initialize a new one
		if strings.Contains(string(output), "empty") || strings.Contains(string(output), "not found") {
			return m.createNewRepo(ctx)
		}
		return fmt.Errorf("failed to clone repository: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// Pull pulls the latest changes from the remote repository
func (m *SystemBackend) Pull(ctx context.Context) error {
	// Check if .git exists
	if _, err := os.Stat(filepath.Join(m.localPath, ".git")); err != nil {
		return fmt.Errorf("repository not initialized")
	}

	err := m.runGitCommand(ctx, "pull", "origin", "HEAD")
	if err != nil {
		// Ignore "already up to date" errors
		if strings.Contains(err.Error(), "Already up to date") || strings.Contains(err.Error(), "up-to-date") {
			return nil
		}
		return err
	}
	return nil
}

// Commit commits changes to the repository
func (m *SystemBackend) Commit(ctx context.Context, message string) error {
	// Check if .git exists
	if _, err := os.Stat(filepath.Join(m.localPath, ".git")); err != nil {
		return fmt.Errorf("repository not initialized")
	}

	// Add all changes
	if err := m.runGitCommand(ctx, "add", "."); err != nil {
		return fmt.Errorf("failed to add changes: %w", err)
	}

	// Configure git user if not set
	if err := m.runGitCommand(ctx, "config", "user.name", "Hyve CLI"); err == nil {
		m.runGitCommand(ctx, "config", "user.email", "cli@hyve.local")
	}

	// Commit changes
	if err := m.runGitCommand(ctx, "commit", "-m", message); err != nil {
		// Ignore "nothing to commit" errors
		if strings.Contains(err.Error(), "nothing to commit") {
			return nil
		}
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	return nil
}

// Push pushes changes to the remote repository
func (m *SystemBackend) Push(ctx context.Context) error {
	// Check if .git exists
	if _, err := os.Stat(filepath.Join(m.localPath, ".git")); err != nil {
		return fmt.Errorf("repository not initialized")
	}

	// Get current branch
	branch, err := m.GetCurrentBranch(ctx)
	if err != nil {
		return err
	}

	// Push with authentication
	err = m.runGitCommand(ctx, "push", "origin", branch)
	if err != nil {
		// Ignore "already up to date" errors
		if strings.Contains(err.Error(), "up-to-date") || strings.Contains(err.Error(), "Everything up-to-date") {
			return nil
		}
		return err
	}

	return nil
}

// GetStateDir returns the path to the clusters directory within the repository
func (m *SystemBackend) GetStateDir() string {
	return filepath.Join(m.localPath, "clusters")
}

// EnsureStateDir ensures the clusters directory exists in the repository
func (m *SystemBackend) EnsureStateDir() error {
	stateDir := m.GetStateDir()
	return os.MkdirAll(stateDir, 0755)
}

// InitializeRepo initializes a new repository if it doesn't exist
func (m *SystemBackend) InitializeRepo(ctx context.Context) error {
	// Try to clone first
	err := m.Clone(ctx)
	if err == nil {
		return m.EnsureStateDir()
	}

	// If clone fails, create a new repository
	return m.createNewRepo(ctx)
}

// createNewRepo creates a new local repository
func (m *SystemBackend) createNewRepo(ctx context.Context) error {
	if err := os.MkdirAll(m.localPath, 0755); err != nil {
		return fmt.Errorf("failed to create local path: %w", err)
	}

	// Initialize repository
	if err := m.runGitCommand(ctx, "init"); err != nil {
		return fmt.Errorf("failed to initialize repository: %w", err)
	}

	// Add remote origin if URL is provided
	if m.repoURL != "" {
		if err := m.runGitCommand(ctx, "remote", "add", "origin", m.repoURL); err != nil {
			// Ignore if remote already exists
			if !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("failed to add remote origin: %w", err)
			}
		}
	}

	// Ensure state directory exists
	if err := m.EnsureStateDir(); err != nil {
		return err
	}

	// Create initial commit with README
	readmePath := filepath.Join(m.localPath, "README.md")
	readmeContent := `# Hyve State Repository

This repository contains cluster state definitions for Hyve.
All cluster YAML files are stored in the clusters/ directory.
`
	if err := os.WriteFile(readmePath, []byte(readmeContent), 0644); err != nil {
		return fmt.Errorf("failed to create README: %w", err)
	}

	// Create initial commit
	return m.Commit(ctx, "Initialize Hyve state repository")
}

// ListBranches lists all branches in the repository
func (m *SystemBackend) ListBranches(ctx context.Context) ([]BranchInfo, error) {
	// Get current branch
	currentBranch, err := m.GetCurrentBranch(ctx)
	if err != nil {
		return nil, err
	}

	// List all branches
	output, err := m.runGitCommandOutput(ctx, "branch", "-v", "--no-abbrev")
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}

	var branchInfos []BranchInfo
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		isCurrent := strings.HasPrefix(line, "*")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimSpace(line)

		parts := strings.Fields(line)
		if len(parts) >= 2 {
			branchName := parts[0]
			hash := parts[1]
			if len(hash) > 8 {
				hash = hash[:8]
			}

			branchInfos = append(branchInfos, BranchInfo{
				Name:      branchName,
				IsCurrent: isCurrent || branchName == currentBranch,
				Hash:      hash,
			})
		}
	}

	return branchInfos, nil
}

// GetCurrentBranch returns the current branch name
func (m *SystemBackend) GetCurrentBranch(ctx context.Context) (string, error) {
	output, err := m.runGitCommandOutput(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}
	return output, nil
}

// CreateBranch creates a new branch from the current HEAD
func (m *SystemBackend) CreateBranch(ctx context.Context, branchName string) error {
	if err := m.runGitCommand(ctx, "branch", branchName); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}
	return nil
}

// SwitchBranch switches to a different branch (checkout)
func (m *SystemBackend) SwitchBranch(ctx context.Context, branchName string) error {
	if err := m.runGitCommand(ctx, "checkout", branchName); err != nil {
		return fmt.Errorf("failed to switch to branch: %w", err)
	}
	return nil
}

// DeleteBranch deletes a branch
func (m *SystemBackend) DeleteBranch(ctx context.Context, branchName string, force bool) error {
	// Check if trying to delete current branch
	currentBranch, err := m.GetCurrentBranch(ctx)
	if err != nil {
		return err
	}

	if currentBranch == branchName {
		return fmt.Errorf("cannot delete current branch '%s'; switch to another branch first", branchName)
	}

	// Delete the branch
	deleteFlag := "-d"
	if force {
		deleteFlag = "-D"
	}

	if err := m.runGitCommand(ctx, "branch", deleteFlag, branchName); err != nil {
		return fmt.Errorf("failed to delete branch: %w", err)
	}

	return nil
}

// PushBranch pushes a specific branch to remote
func (m *SystemBackend) PushBranch(ctx context.Context, branchName string) error {
	if err := m.runGitCommand(ctx, "push", "-u", "origin", branchName); err != nil {
		// Ignore "already up to date" errors
		if strings.Contains(err.Error(), "up-to-date") || strings.Contains(err.Error(), "Everything up-to-date") {
			return nil
		}
		return fmt.Errorf("failed to push branch: %w", err)
	}

	return nil
}

// HasUncommittedChanges checks if there are uncommitted changes in the repository
func (m *SystemBackend) HasUncommittedChanges(ctx context.Context) (bool, error) {
	output, err := m.runGitCommandOutput(ctx, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("failed to get status: %w", err)
	}

	return strings.TrimSpace(output) != "", nil
}

// GetStatusSummary returns a summary of uncommitted changes
func (m *SystemBackend) GetStatusSummary(ctx context.Context) (string, error) {
	output, err := m.runGitCommandOutput(ctx, "status", "--short")
	if err != nil {
		return "", fmt.Errorf("failed to get status: %w", err)
	}

	if strings.TrimSpace(output) == "" {
		return "Working tree clean", nil
	}

	lines := strings.Split(output, "\n")
	modified := 0
	added := 0
	deleted := 0
	untracked := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if len(line) >= 2 {
			status := line[:2]
			if strings.Contains(status, "M") {
				modified++
			} else if strings.Contains(status, "A") {
				added++
			} else if strings.Contains(status, "D") {
				deleted++
			} else if strings.Contains(status, "?") {
				untracked++
			}
		}
	}

	var summary string
	if modified > 0 {
		summary += fmt.Sprintf("%d modified, ", modified)
	}
	if added > 0 {
		summary += fmt.Sprintf("%d added, ", added)
	}
	if deleted > 0 {
		summary += fmt.Sprintf("%d deleted, ", deleted)
	}
	if untracked > 0 {
		summary += fmt.Sprintf("%d untracked, ", untracked)
	}

	// Remove trailing comma and space
	if len(summary) > 2 {
		summary = summary[:len(summary)-2]
	}

	return summary, nil
}
