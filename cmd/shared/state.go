package shared

import (
	gocontext "context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cbridges1/hyve/internal/credentials"
	"github.com/cbridges1/hyve/internal/reconcile"
	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
	"github.com/cbridges1/hyve/internal/types"
)

// SyncRepoState performs a git pull to synchronise the local repository with
// the remote before any operation that reads or writes repository state.
func SyncRepoState(ctx gocontext.Context) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Printf("⚠️  Skipping git sync: failed to open repository manager: %v", err)
		return
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		// No repository configured — nothing to sync.
		return
	}

	authUsername, authToken := GetAuthCredentials(currentRepo)
	stateMgr, err := state.NewManager(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Printf("⚠️  Skipping git sync: failed to create state manager: %v", err)
		return
	}

	if err := stateMgr.InitializeGitRepo(ctx); err != nil {
		log.Printf("⚠️  Skipping git sync: failed to initialise git repo: %v", err)
		return
	}

	if err := stateMgr.SyncWithRemote(ctx); err != nil {
		log.Printf("⚠️  Skipping git sync: failed to sync with remote: %v", err)
		return
	}

	log.Println("🔄 Repository synchronised")
}

// CreateStateManager creates state manager from current repository
func CreateStateManager(ctx gocontext.Context) (*state.Manager, string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatalf("❌ No Git repository configured. Hyve requires a Git repository for state management.\n\n" +
			"Add a Git repository with: hyve git add <name> --repo-url <url>")
	}
	log.Printf("Using Git repository: %s", currentRepo.RepoURL)

	credsMgr, err := credentials.NewManager()
	var authToken string
	var authUsername = currentRepo.Username
	if err == nil {
		defer credsMgr.Close()
		if creds, _ := credsMgr.GetCredentials(); creds != nil {
			if password, err := creds.GetPassword(); err == nil && password != "" {
				authToken = password
				if authUsername == "" {
					authUsername = creds.Username
				}
			}
		}
	}
	if authToken == "" {
		authToken = os.Getenv("HYVE_GIT_TOKEN")
	}

	stateMgr, err := state.NewManager(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create state manager: %v", err)
	}
	if err := stateMgr.InitializeGitRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize Git repository: %v", err)
	}
	if err := stateMgr.SyncWithRemote(ctx); err != nil {
		log.Fatalf("Failed to sync with remote repository: %v", err)
	}
	log.Println("Git repository synchronized")
	stateDir := filepath.Join(currentRepo.LocalPath, "clusters")
	return stateMgr, stateDir
}

// CommitStateChanges commits changes to Git repository and pushes to remote
func CommitStateChanges(ctx gocontext.Context, stateMgr *state.Manager, message string) {
	log.Println("📝 Committing and pushing changes to Git repository...")

	if err := stateMgr.CommitAndPush(ctx, message); err != nil {
		log.Printf("❌ Failed to commit and push: %v", err)

		if strings.Contains(err.Error(), "failed to push") {
			log.Println("💡 Changes were committed locally but push failed")
			log.Println("💡 Check your Git credentials and network connection")
			log.Println("💡 You can manually push with: cd <repo-path> && git push")
		} else if strings.Contains(err.Error(), "failed to commit") {
			log.Println("💡 Commit operation failed - changes may still be in working directory")
		}
		return
	}

	log.Println("✅ Changes committed and pushed to remote repository successfully")
}

// ParseNodeGroup parses a node group spec string into a types.NodeGroup.
// Format: name=workers,type=t3.medium,count=3[,min=1,max=5,disk=50,spot=true,mode=System]
func ParseNodeGroup(s string) (types.NodeGroup, error) {
	ng := types.NodeGroup{}
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		switch k {
		case "name":
			ng.Name = v
		case "type", "instanceType":
			ng.InstanceType = v
		case "count":
			n, err := strconv.Atoi(v)
			if err != nil {
				return ng, fmt.Errorf("invalid count '%s': %w", v, err)
			}
			ng.Count = n
		case "min":
			n, err := strconv.Atoi(v)
			if err != nil {
				return ng, fmt.Errorf("invalid min '%s': %w", v, err)
			}
			ng.MinCount = n
		case "max":
			n, err := strconv.Atoi(v)
			if err != nil {
				return ng, fmt.Errorf("invalid max '%s': %w", v, err)
			}
			ng.MaxCount = n
		case "disk":
			n, err := strconv.Atoi(v)
			if err != nil {
				return ng, fmt.Errorf("invalid disk '%s': %w", v, err)
			}
			ng.DiskSize = n
		case "spot":
			ng.Spot = strings.EqualFold(v, "true")
		case "mode":
			ng.Mode = v
		}
	}
	if ng.Name == "" {
		return ng, fmt.Errorf("node group must have a name (name=<value>)")
	}
	if ng.InstanceType == "" {
		return ng, fmt.Errorf("node group '%s' must have a type (type=<value>)", ng.Name)
	}
	if ng.Count < 1 {
		ng.Count = 1
	}
	return ng, nil
}

// GetAuthCredentials retrieves authentication credentials for git operations
func GetAuthCredentials(currentRepo *repository.Repository) (username, token string) {
	username = currentRepo.Username

	credsMgr, err := credentials.NewManager()
	if err == nil {
		defer credsMgr.Close()
		if creds, _ := credsMgr.GetCredentials(); creds != nil {
			if password, err := creds.GetPassword(); err == nil && password != "" {
				token = password
				if username == "" {
					username = creds.Username
				}
			}
		}
	}

	if token == "" {
		token = os.Getenv("HYVE_GIT_TOKEN")
	}

	return username, token
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

// GetLocalPath syncs the repo state and returns the local path of the current repository.
func GetLocalPath() string {
	SyncRepoState(gocontext.Background())

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()
	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatal("No Git repository configured. Use 'hyve git add' to configure a repository")
	}
	return currentRepo.LocalPath
}

// RunReconciliation runs reconciliation, optionally from a specific local path.
func RunReconciliation(repoPath string) {
	ctx := gocontext.Background()

	var stateMgr *state.Manager

	if repoPath != "" {
		absPath, err := filepath.Abs(repoPath)
		if err != nil {
			log.Fatalf("Invalid path %q: %v", repoPath, err)
		}
		log.Printf("Using local repository path: %s", absPath)
		stateMgr = CreateStateManagerFromPath(absPath)
		RunLocalReconciliation(ctx, stateMgr)
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
		log.Println("Pushing desired state to repository...")
		if err := stateMgr.CommitAndPush(ctx, "Update desired cluster state"); err != nil {
			log.Printf("❌ Failed to push state: %v", err)
			if strings.Contains(err.Error(), "failed to push") {
				log.Println("💡 Changes were committed locally but push failed")
				log.Println("💡 Check your Git credentials and network connection")
			}
		} else {
			log.Println("✅ Desired state pushed to repository. The CI/CD pipeline will reconcile.")
		}
		return
	}

	RunLocalReconciliation(ctx, stateMgr)
}

// RunLocalReconciliation performs full local reconciliation.
func RunLocalReconciliation(ctx gocontext.Context, stateMgr *state.Manager) {
	clusterDefs, err := stateMgr.LoadClusterDefinitions()
	if err != nil {
		log.Fatalf("Failed to load cluster definitions: %v", err)
	}

	if err = stateMgr.ValidateClusterDefinitions(clusterDefs); err != nil {
		log.Fatalf("Invalid cluster configuration: %v", err)
	}

	clusterDefs = stateMgr.OrderClusters(clusterDefs)

	reconciler := reconcile.NewReconciler(stateMgr)
	if err = reconciler.ReconcileAll(ctx, clusterDefs); err != nil {
		log.Fatalf("Reconciliation failed: %v", err)
	}

	log.Println("Cluster reconciliation completed")
}

// CreateStateManagerFromPath builds a state manager from an already-checked-out local repository.
func CreateStateManagerFromPath(absPath string) *state.Manager {
	stateMgr, err := state.NewManager("", absPath, "", "")
	if err != nil {
		log.Fatalf("Failed to create state manager from path %q: %v", absPath, err)
	}
	return stateMgr
}

// CreateStateManagerFromRepository creates state manager from current repository configuration
func CreateStateManagerFromRepository(ctx gocontext.Context) (*state.Manager, string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Fatalf("❌ No Git repository configured. Hyve requires a Git repository for state management.\n\n" +
			"Add a Git repository with: hyve git add <name> --repo-url <url>")
	}
	log.Printf("Using Git repository: %s", currentRepo.RepoURL)

	credsMgr, err := credentials.NewManager()
	var authToken string
	var authUsername = currentRepo.Username

	if err == nil {
		defer credsMgr.Close()
		if creds, _ := credsMgr.GetCredentials(); creds != nil {
			if password, err := creds.GetPassword(); err == nil && password != "" {
				authToken = password
				if authUsername == "" {
					authUsername = creds.Username
				}
			}
		}
	}

	if authToken == "" {
		authToken = os.Getenv("HYVE_GIT_TOKEN")
	}

	stateMgr, err := state.NewManager(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create state manager: %v", err)
	}

	if err := stateMgr.InitializeGitRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize Git repository: %v", err)
	}

	if err := stateMgr.SyncWithRemote(ctx); err != nil {
		log.Fatalf("Failed to sync with remote repository: %v", err)
	}

	log.Println("Git repository synchronized")
	return stateMgr, currentRepo.LocalPath
}
