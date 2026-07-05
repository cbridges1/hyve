package server

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cbridges1/hyve/internal/git"
	"github.com/cbridges1/hyve/internal/state"
)

// RepoContext is the single repository hyve-server is bound to for its
// entire process lifetime (resolved once from --path at startup). Unlike the
// CLI, the server never consults internal/repository's sqlite multi-repo
// registry — it always operates on exactly this one already-checked-out
// local path, matching "operates on the single repository it was started
// against" in the design doc.
type RepoContext struct {
	Path string
}

// NewRepoContext resolves path to an absolute directory and verifies it
// exists.
func NewRepoContext(path string) (*RepoContext, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repo path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("repo path %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repo path %s is not a directory", abs)
	}
	return &RepoContext{Path: abs}, nil
}

// gitToken returns the git credential token the CLI's cmd/shared.GetAuthCredentials
// uses for the same purpose — HYVE_GIT_TOKEN, with no username (system git
// credential helper / GIT_ASKPASS handles the rest).
func gitToken() string {
	return os.Getenv("HYVE_GIT_TOKEN")
}

// StateManager builds a git-backed state.Manager for this repo. repoURL is
// left empty — Clone() is a no-op when the path already contains a .git
// directory (which --path is required to, since the server never clones a
// fresh repository itself), and Pull/Commit/Push all operate against the
// existing "origin" remote already configured in that checkout.
func (rc *RepoContext) StateManager() (*state.Manager, error) {
	return state.NewManager("", rc.Path, "", gitToken())
}

// GitBackend builds a git.SystemBackend for this repo, for the /git/status
// and /git/sync handlers.
func (rc *RepoContext) GitBackend() (*git.SystemBackend, error) {
	return git.NewBackend("", rc.Path, "", gitToken())
}
