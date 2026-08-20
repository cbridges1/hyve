package shared

import (
	gocontext "context"
	"log"
	"path/filepath"

	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
)

// CreateStateManager creates state manager from current repository
func CreateStateManager(_ gocontext.Context) (*state.Manager, string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatalf("❌ No repository configured. Hyve requires a registered directory for state management.\n\n" +
			"Register one with: hyve env create [--path <dir>]")
	}
	logCurrentRepo(currentRepo)

	stateDir := filepath.Join(currentRepo.LocalPath, "clusters")
	stateMgr := state.NewManagerFromPath(stateDir)
	return stateMgr, stateDir
}

// CommitStateChanges no longer commits or pushes anything — git sync is not
// a native hyve capability (see internal/reconcile.StateProvider). State
// changes were already written to disk by the caller; this just reminds the
// user (or whatever workflow they've wired up) that persisting them to the
// remote is now their job, e.g. `git push` or a git-sync workflow.
func CommitStateChanges(_ gocontext.Context, _ *state.Manager, message string) {
	log.Printf("📝 State changes written locally (%q) — not pushed automatically.", message)
	log.Println("💡 Push them yourself when ready: git add -A && git commit -m ... && git push")
}

// GetRepoPath returns the local path of the current repository (no sync).
func GetRepoPath() string {
	repoMgr, err := repository.NewManager()
	if err != nil {
		return ""
	}
	defer repoMgr.Close()
	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		return ""
	}
	return currentRepo.LocalPath
}

// GetLocalPath returns the local path of the current repository.
func GetLocalPath() string {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()
	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatal("No repository configured. Use 'hyve env create' to configure one")
	}
	return currentRepo.LocalPath
}

// logCurrentRepo prints which registered directory is in use — phrased for
// both git-backed (has a RepoURL) and plain local (via `hyve env create`)
// entries, since both are equally valid sources of truth.
func logCurrentRepo(r *repository.Repository) {
	if r.RepoURL != "" {
		log.Printf("Using Git repository: %s", r.RepoURL)
	} else {
		log.Printf("Using local state directory: %s", r.LocalPath)
	}
}

// RunReconciliation runs reconciliation, optionally from a specific local path.
// When dryRun is true, the entire cycle is read-only — see
// reconcile.Reconciler.ReconcileAll for exactly what's gated.
func RunReconciliation(repoPath string, dryRun bool) {
	ctx := gocontext.Background()

	var stateMgr *state.Manager

	if repoPath != "" {
		absPath, err := filepath.Abs(repoPath)
		if err != nil {
			log.Fatalf("Invalid path %q: %v", repoPath, err)
		}
		log.Printf("Using local repository path: %s", absPath)
		stateMgr = CreateStateManagerFromPath(absPath)
		RunLocalReconciliation(ctx, stateMgr, dryRun)
		return
	}

	stateMgr, _ = CreateStateManagerFromRepository(ctx)

	clusterDefs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		log.Fatalf("Failed to load cluster definitions: %v", err)
	}

	if err = stateMgr.ValidateClusterDefinitions(clusterDefs); err != nil {
		log.Fatalf("Invalid cluster configuration: %v", err)
	}

	repoCfg, err := stateMgr.LoadRepoConfig()
	if err != nil {
		log.Printf("Warning: Could not load hyve.yaml: %v. Defaulting to local mode.", err)
		repoCfg = &state.RepoConfig{Reconcile: state.ReconcileConfig{Mode: state.ReconcileModeLocal}}
	}

	if repoCfg.Reconcile.Mode == state.ReconcileModeCICD {
		log.Println("Reconcile mode: cicd")
		log.Println("Skipping local reconciliation — cluster provisioning will be handled by the CI/CD pipeline.")
		if dryRun {
			log.Println("DRY RUN: nothing to push — skipping (this branch doesn't reconcile locally either way)")
			return
		}
		log.Println("💡 Push the desired state yourself so the CI/CD pipeline picks it up: git add -A && git commit -m ... && git push")
		return
	}

	RunLocalReconciliation(ctx, stateMgr, dryRun)
}

// RunLocalReconciliation performs full local reconciliation. When dryRun is
// true, nothing is mutated — see reconcile.Reconciler.ReconcileAll.
func RunLocalReconciliation(ctx gocontext.Context, stateMgr *state.Manager, dryRun bool) {
	clusterDefs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		log.Fatalf("Failed to load cluster definitions: %v", err)
	}

	if err = stateMgr.ValidateClusterDefinitions(clusterDefs); err != nil {
		log.Fatalf("Invalid cluster configuration: %v", err)
	}

	clusterDefs = stateMgr.OrderClusters(clusterDefs)

	reconciler := reconcile.NewReconciler(stateMgr)
	if err = reconciler.ReconcileAll(ctx, clusterDefs, dryRun); err != nil {
		log.Fatalf("Reconciliation failed: %v", err)
	}

	log.Println("Cluster reconciliation completed")
}

// CreateStateManagerFromPath builds a state manager from an already-checked-out local repository.
func CreateStateManagerFromPath(absPath string) *state.Manager {
	return state.NewManagerFromPath(filepath.Join(absPath, "clusters"))
}

// CreateStateManagerFromRepository creates state manager from current repository configuration
func CreateStateManagerFromRepository(_ gocontext.Context) (*state.Manager, string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatalf("❌ No repository configured. Hyve requires a registered directory for state management.\n\n" +
			"Register one with: hyve env create [--path <dir>]")
	}
	logCurrentRepo(currentRepo)

	stateMgr := state.NewManagerFromPath(filepath.Join(currentRepo.LocalPath, "clusters"))
	return stateMgr, currentRepo.LocalPath
}
