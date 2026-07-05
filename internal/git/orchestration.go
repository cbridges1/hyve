package git

import (
	"context"
	"fmt"
)

// StatusReport is the structured result of Status.
type StatusReport struct {
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"` // set when Connected is false
}

// Status verifies connectivity to the remote for an already-constructed
// backend. Clone is a no-op (and so does not actually test connectivity) when
// the local path already contains a .git directory — this mirrors the
// existing `hyve git status` behavior exactly.
func Status(ctx context.Context, backend *SystemBackend) StatusReport {
	if err := backend.Clone(ctx); err != nil {
		return StatusReport{Connected: false, Error: err.Error()}
	}
	return StatusReport{Connected: true}
}

// SyncReport is the structured result of Sync.
type SyncReport struct {
	Branch        string `json:"branch"`
	PullWarning   string `json:"pullWarning,omitempty"` // set if the pull step failed (non-fatal — sync continues)
	HadChanges    bool   `json:"hadChanges"`
	StatusSummary string `json:"statusSummary,omitempty"` // human-readable summary of local changes, set when HadChanges
	Committed     bool   `json:"committed"`
	Pushed        bool   `json:"pushed"`
}

// Sync pulls the latest changes, and if there are local uncommitted changes,
// commits and pushes them. message defaults to "Update repository state" when
// empty.
func Sync(ctx context.Context, backend *SystemBackend, message string) (*SyncReport, error) {
	if err := backend.InitializeRepo(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize repository: %w", err)
	}

	currentBranch, err := backend.GetCurrentBranch(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current branch: %w", err)
	}

	report := &SyncReport{Branch: currentBranch}

	if err := backend.Pull(ctx); err != nil {
		report.PullWarning = err.Error()
	}

	hasChanges, err := backend.HasUncommittedChanges(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check for changes: %w", err)
	}
	if !hasChanges {
		return report, nil
	}
	report.HadChanges = true

	statusSummary, err := backend.GetStatusSummary(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}
	report.StatusSummary = statusSummary

	if message == "" {
		message = "Update repository state"
	}

	if err := backend.Commit(ctx, message); err != nil {
		return nil, fmt.Errorf("failed to commit changes: %w", err)
	}
	report.Committed = true

	if err := backend.Push(ctx); err != nil {
		return nil, fmt.Errorf("failed to push changes: %w", err)
	}
	report.Pushed = true

	return report, nil
}
