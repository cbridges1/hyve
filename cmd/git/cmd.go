package git

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/credentials"
	internalgit "github.com/cbridges1/hyve/internal/git"
	"github.com/cbridges1/hyve/internal/repository"
)

// HyveHome returns the effective Hyve home directory.
// We import this from the parent but to avoid circular imports we accept it as a function.
var hyveHomeFunc func() string

// SetHyveHomeFunc sets the function to retrieve the hyve home directory.
func SetHyveHomeFunc(f func() string) {
	hyveHomeFunc = f
}

func hyveHome() string {
	if hyveHomeFunc != nil {
		return hyveHomeFunc()
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".hyve")
}

// Cmd is the git command.
var Cmd = &cobra.Command{
	Use:   "git",
	Short: "Manage Git repositories",
	Long:  "Configure and manage multiple Git repositories for state management",
}

var gitAddCmd = &cobra.Command{
	Use:   "add [repository-name]",
	Short: "Add a new Git repository",
	Long: `Add a new Git repository configuration for state management.
The repository name is used as a friendly identifier for switching between repositories.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repoName := args[0]
		repoURL, _ := cmd.Flags().GetString("repo-url")
		setCurrent, _ := cmd.Flags().GetBool("set-current")

		if repoURL == "" {
			log.Fatal("Repository URL is required. Use --repo-url flag.")
		}

		addGitRepository(repoName, repoURL, setCurrent)
	},
}

var gitListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured Git repositories",
	Long:  "Display all configured Git repositories and their status",
	Run: func(cmd *cobra.Command, args []string) {
		listGitRepositories()
	},
}

var gitUseCmd = &cobra.Command{
	Use:   "use [repository-name]",
	Short: "Switch to a different Git repository",
	Long:  "Set the specified repository as the current active repository",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repoName := args[0]
		switchToRepository(repoName)
	},
}

var gitStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current Git configuration status",
	Long:  "Display the current Git repository configuration and connection status",
	Run: func(cmd *cobra.Command, args []string) {
		showGitStatus()
	},
}

var gitRemoveCmd = &cobra.Command{
	Use:   "remove [repository-name]",
	Short: "Remove a Git repository configuration",
	Long:  "Remove the specified repository configuration from storage",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repoName := args[0]
		removeGitRepository(repoName)
	},
}

var gitResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset to local state management",
	Long:  "Remove all Git repository configurations and revert to local state directory",
	Run: func(cmd *cobra.Command, args []string) {
		resetGitConfiguration()
	},
}

var gitBranchCmd = &cobra.Command{
	Use:   "branch",
	Short: "Manage Git branches",
	Long:  "Create, list, delete, and switch between Git branches in the current repository",
}

var gitBranchListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all branches",
	Long:  "List all branches in the current Git repository",
	Run: func(cmd *cobra.Command, args []string) {
		listGitBranches()
	},
}

var gitBranchCreateCmd = &cobra.Command{
	Use:   "create [branch-name]",
	Short: "Create a new branch",
	Long:  "Create a new branch from the current HEAD",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		branchName := args[0]
		switchToBranch, _ := cmd.Flags().GetBool("switch")
		push, _ := cmd.Flags().GetBool("push")
		createGitBranch(branchName, switchToBranch, push)
	},
}

var gitBranchDeleteCmd = &cobra.Command{
	Use:   "delete [branch-name]",
	Short: "Delete a branch",
	Long:  "Delete a branch from the local repository",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		branchName := args[0]
		force, _ := cmd.Flags().GetBool("force")
		deleteGitBranch(branchName, force)
	},
}

var gitBranchSwitchCmd = &cobra.Command{
	Use:   "switch [branch-name]",
	Short: "Switch to a branch",
	Long:  "Switch to a different branch (git checkout)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		branchName := args[0]
		pull, _ := cmd.Flags().GetBool("pull")
		switchGitBranch(branchName, pull)
	},
}

var gitPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull changes from remote",
	Long:  "Pull the latest changes from the remote repository for the current branch",
	Run: func(cmd *cobra.Command, args []string) {
		pullGitChanges()
	},
}

var gitPushCmd = &cobra.Command{
	Use:   "push [commit-message]",
	Short: "Stage, commit, and push changes",
	Long: `Stage all changes, commit with a message, and push to remote.

This is a convenience command that combines:
  - git add .
  - git commit -m "message"
  - git push

If no commit message is provided, a default message based on the changes will be used.`,
	Run: func(cmd *cobra.Command, args []string) {
		var message string
		if len(args) > 0 {
			message = args[0]
		}
		pushGitChanges(message)
	},
}

var gitSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync with remote (pull and push)",
	Long: `Pull latest changes from remote and optionally commit and push local changes.

This command:
  1. Pulls latest changes from remote
  2. If --message is provided, commits local changes and pushes to remote`,
	Run: func(cmd *cobra.Command, args []string) {
		message, _ := cmd.Flags().GetString("message")
		syncGitChanges(message)
	},
}

func init() {
	gitAddCmd.Flags().StringP("repo-url", "r", "", "Git repository URL (required)")
	gitAddCmd.Flags().BoolP("set-current", "c", false, "Set this repository as current after adding")

	gitSyncCmd.Flags().StringP("message", "m", "", "Commit message for local changes before pushing")

	gitBranchCreateCmd.Flags().BoolP("switch", "s", false, "Switch to the new branch after creating it")
	gitBranchCreateCmd.Flags().BoolP("push", "p", false, "Push the branch to remote after creating it")

	gitBranchDeleteCmd.Flags().BoolP("force", "f", false, "Force delete the branch")

	gitBranchSwitchCmd.Flags().BoolP("pull", "p", false, "Pull latest changes after switching")

	gitBranchCmd.AddCommand(gitBranchListCmd)
	gitBranchCmd.AddCommand(gitBranchCreateCmd)
	gitBranchCmd.AddCommand(gitBranchDeleteCmd)
	gitBranchCmd.AddCommand(gitBranchSwitchCmd)

	Cmd.AddCommand(gitAddCmd)
	Cmd.AddCommand(gitListCmd)
	Cmd.AddCommand(gitUseCmd)
	Cmd.AddCommand(gitStatusCmd)
	Cmd.AddCommand(gitRemoveCmd)
	Cmd.AddCommand(gitResetCmd)
	Cmd.AddCommand(gitBranchCmd)
	Cmd.AddCommand(gitPullCmd)
	Cmd.AddCommand(gitPushCmd)
	Cmd.AddCommand(gitSyncCmd)
}

func addGitRepository(name, repoURL string, setCurrent bool) {
	repositoriesDir := filepath.Join(hyveHome(), "repositories")
	localPath := filepath.Join(repositoriesDir, strings.ToLower(name))

	if err := os.MkdirAll(repositoriesDir, 0755); err != nil {
		log.Printf("Warning: Failed to create repositories directory: %v", err)
	}

	log.Printf("Adding Git repository '%s': %s", name, repoURL)

	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	repo, err := repoMgr.AddRepository(name, repoURL, localPath)
	if err != nil {
		log.Fatalf("Failed to add repository: %v", err)
	}

	if setCurrent {
		if err := repoMgr.SetCurrentRepository(name); err != nil {
			log.Fatalf("Failed to set current repository: %v", err)
		}
		repo.IsCurrent = true
	}

	log.Println("Testing Git repository connection...")

	credsMgr, err := credentials.NewManager()
	if err == nil {
		defer credsMgr.Close()
	}

	var authToken string
	var authUsername string

	if credsMgr != nil {
		if creds, err := credsMgr.GetCredentials(); err == nil && creds != nil {
			if password, err := creds.GetPassword(); err == nil && password != "" {
				authToken = password
				authUsername = creds.Username
			}
		}
	}

	if authToken == "" {
		authToken = os.Getenv("HYVE_GIT_TOKEN")
	}

	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(repoURL, localPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.InitializeRepo(ctx); err != nil {
		log.Printf("⚠️  Failed to connect to Git repository: %v", err)
		log.Println("Repository added but connection failed. Check your credentials and network.")
	} else {
		log.Println("✅ Git repository connected successfully!")
	}

	log.Printf("Repository '%s' added successfully!", name)
	log.Printf("Repository URL: %s", repoURL)
	log.Printf("Local path: %s", localPath)
	if credsMgr != nil {
		if creds, _ := credsMgr.GetCredentials(); creds != nil {
			log.Printf("Authentication: ✅ Global credentials configured")
		}
	}
	if repo.IsCurrent {
		log.Println("✅ This repository is now current")
	}

	log.Println("\n💡 Tips:")
	log.Println("  - Use 'hyve git list' to see all repositories")
	log.Println("  - Use 'hyve git use <name>' to switch repositories")
	log.Println("  - Set HYVE_GIT_TOKEN env var for authentication")
}

func listGitRepositories() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	repos, err := repoMgr.ListRepositories()
	if err != nil {
		log.Fatalf("Failed to list repositories: %v", err)
	}

	if len(repos) == 0 {
		log.Println("❌ No Git repositories configured")
		log.Println("\nHyve requires at least one Git repository for state management.")
		log.Println("To add a Git repository, use:")
		log.Println("  hyve git add <name> --repo-url <repository-url>")
		return
	}

	log.Printf("📁 Configured Git repositories (%d):\n", len(repos))

	for _, repo := range repos {
		status := ""
		if repo.IsCurrent {
			status = " (current) ⭐"
		}

		log.Printf("  %s%s", repo.Name, status)
		log.Printf("    URL: %s", repo.RepoURL)
		log.Printf("    Local: %s", repo.LocalPath)
		log.Printf("    Added: %s", repo.CreatedAt.Format("2006-01-02 15:04"))
		log.Println()
	}

	credsMgr, err := credentials.NewManager()
	if err == nil {
		defer credsMgr.Close()
		if creds, _ := credsMgr.GetCredentials(); creds != nil {
			log.Printf("🔑 Authentication: ✅ Global credentials configured (%s)", creds.Username)
		} else {
			log.Println("🔑 Authentication: ⚠️  No global credentials stored")
		}
	}

	hasToken := os.Getenv("HYVE_GIT_TOKEN") != ""
	if hasToken {
		log.Println("🔑 Environment Fallback: ✅ HYVE_GIT_TOKEN configured")
	} else {
		log.Println("🔑 Environment Fallback: ⚠️  HYVE_GIT_TOKEN not set")
	}
}

func switchToRepository(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if err := repoMgr.SetCurrentRepository(name); err != nil {
		log.Fatalf("Failed to switch repository: %v", err)
	}

	log.Printf("✅ Switched to repository '%s'", name)

	repo, err := repoMgr.GetRepositoryByName(name)
	if err != nil {
		log.Fatalf("Failed to get repository details: %v", err)
	}

	log.Printf("Repository URL: %s", repo.RepoURL)
	log.Printf("Local path: %s", repo.LocalPath)
}

func showGitStatus() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		log.Println("\nHyve requires a Git repository for state management.")
		log.Println("To add a Git repository, use:")
		log.Println("  hyve git add <name> --repo-url <repository-url>")
		return
	}

	log.Printf("✅ Current repository: %s", currentRepo.Name)
	log.Printf("Repository URL: %s", currentRepo.RepoURL)
	log.Printf("Local path: %s", currentRepo.LocalPath)

	credsMgr, err := credentials.NewManager()
	var globalCreds *credentials.Credentials
	if err == nil {
		defer credsMgr.Close()
		globalCreds, _ = credsMgr.GetCredentials()
	}

	envToken := os.Getenv("HYVE_GIT_TOKEN")

	if globalCreds != nil {
		log.Printf("Authentication: ✅ Global credentials configured (%s)", globalCreds.Username)
	} else if envToken != "" {
		log.Println("Authentication: ✅ Environment token configured")
	} else {
		log.Println("Authentication: ⚠️  No authentication configured")
	}

	log.Println("\nTesting connection...")
	var authToken string
	var authUsername string

	if globalCreds != nil {
		if password, err := globalCreds.GetPassword(); err == nil && password != "" {
			authToken = password
			authUsername = globalCreds.Username
		}
	}

	if authToken == "" {
		authToken = envToken
	}
	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.Clone(ctx); err != nil {
		log.Printf("❌ Connection failed: %v", err)
	} else {
		log.Println("✅ Connection successful")
	}
}

func removeGitRepository(name string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	if err := repoMgr.DeleteRepository(name); err != nil {
		log.Fatalf("Failed to remove repository: %v", err)
	}

	log.Printf("✅ Repository '%s' removed successfully", name)

	repos, err := repoMgr.ListRepositories()
	if err == nil && len(repos) > 0 {
		current, err := repoMgr.GetCurrentRepository()
		if err == nil {
			log.Printf("Current repository is now: %s", current.Name)
		}
	} else {
		log.Println("No repositories remaining. Add a new repository to continue using Hyve.")
	}
}

func resetGitConfiguration() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	repos, err := repoMgr.ListRepositories()
	if err != nil {
		log.Fatalf("Failed to list repositories: %v", err)
	}

	if len(repos) == 0 {
		log.Println("No Git repositories configured to reset")
		return
	}

	for _, repo := range repos {
		if err := repoMgr.DeleteRepository(repo.Name); err != nil {
			log.Printf("Failed to remove repository '%s': %v", repo.Name, err)
		}
	}

	log.Println("✅ All Git configurations reset")
	log.Println("Add a Git repository to continue using Hyve: hyve git add <name> --repo-url <url>")
}

func listGitBranches() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		log.Println("Add a Git repository first: hyve git add <name> --repo-url <url>")
		return
	}

	authToken, authUsername := getGitAuth(currentRepo)

	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.InitializeRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize repository: %v", err)
	}

	branches, err := gitMgr.ListBranches(ctx)
	if err != nil {
		log.Fatalf("Failed to list branches: %v", err)
	}

	if len(branches) == 0 {
		log.Println("No branches found in repository")
		return
	}

	log.Printf("🌿 Branches in repository '%s':\n", currentRepo.Name)
	for _, branch := range branches {
		marker := "  "
		if branch.IsCurrent {
			marker = "* "
		}
		log.Printf("%s%s (%s)", marker, branch.Name, branch.Hash)
	}

	log.Println("\n💡 Commands:")
	log.Println("  hyve git branch create <name>    # Create new branch")
	log.Println("  hyve git branch switch <name>    # Switch to branch")
	log.Println("  hyve git branch delete <name>    # Delete branch")
}

func createGitBranch(branchName string, switchToBranch, push bool) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	authToken, authUsername := getGitAuth(currentRepo)

	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.InitializeRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize repository: %v", err)
	}

	currentBranch, err := gitMgr.GetCurrentBranch(ctx)
	if err != nil {
		log.Fatalf("Failed to get current branch: %v", err)
	}

	log.Printf("Creating branch '%s' from '%s'...", branchName, currentBranch)
	if err := gitMgr.CreateBranch(ctx, branchName); err != nil {
		log.Fatalf("Failed to create branch: %v", err)
	}

	log.Printf("✅ Branch '%s' created successfully", branchName)

	if switchToBranch {
		if err := gitMgr.SwitchBranch(ctx, branchName); err != nil {
			log.Fatalf("Failed to switch to branch: %v", err)
		}
		log.Printf("✅ Switched to branch '%s'", branchName)
	}

	if push {
		log.Printf("Pushing branch '%s' to remote...", branchName)
		if err := gitMgr.PushBranch(ctx, branchName); err != nil {
			log.Printf("⚠️  Failed to push branch: %v", err)
		} else {
			log.Printf("✅ Branch '%s' pushed to remote", branchName)
		}
	}

	if !switchToBranch {
		log.Printf("\n💡 Switch to this branch with: hyve git branch switch %s", branchName)
	}
}

func deleteGitBranch(branchName string, force bool) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	authToken, authUsername := getGitAuth(currentRepo)

	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.InitializeRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize repository: %v", err)
	}

	log.Printf("Deleting branch '%s'...", branchName)
	if err := gitMgr.DeleteBranch(ctx, branchName, force); err != nil {
		log.Fatalf("Failed to delete branch: %v", err)
	}

	log.Printf("✅ Branch '%s' deleted successfully", branchName)
	log.Println("\n💡 The branch has been deleted locally.")
	log.Println("💡 To delete from remote, use: git push origin --delete " + branchName)
}

func switchGitBranch(branchName string, pull bool) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	authToken, authUsername := getGitAuth(currentRepo)

	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.InitializeRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize repository: %v", err)
	}

	currentBranch, err := gitMgr.GetCurrentBranch(ctx)
	if err != nil {
		log.Fatalf("Failed to get current branch: %v", err)
	}

	if currentBranch == branchName {
		log.Printf("Already on branch '%s'", branchName)
		if pull {
			log.Println("Pulling latest changes...")
			if err := gitMgr.Pull(ctx); err != nil {
				log.Printf("⚠️  Failed to pull: %v", err)
			} else {
				log.Println("✅ Pulled latest changes")
			}
		}
		return
	}

	log.Printf("Switching from '%s' to '%s'...", currentBranch, branchName)
	if err := gitMgr.SwitchBranch(ctx, branchName); err != nil {
		log.Fatalf("Failed to switch branch: %v", err)
	}

	log.Printf("✅ Switched to branch '%s'", branchName)

	if pull {
		log.Println("Pulling latest changes...")
		if err := gitMgr.Pull(ctx); err != nil {
			log.Printf("⚠️  Failed to pull: %v", err)
		} else {
			log.Println("✅ Pulled latest changes")
		}
	}

	log.Println("\n💡 Your working directory now reflects the '" + branchName + "' branch")
	log.Println("💡 Changes made will be tracked on this branch")
}

func getGitAuth(repo *repository.Repository) (token, username string) {
	credsMgr, err := credentials.NewManager()
	if err == nil {
		defer credsMgr.Close()
		if creds, err := credsMgr.GetCredentials(); err == nil && creds != nil {
			if password, err := creds.GetPassword(); err == nil && password != "" {
				token = password
				username = creds.Username
			}
		}
	}

	if token == "" {
		token = os.Getenv("HYVE_GIT_TOKEN")
	}

	return token, username
}

func pullGitChanges() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	authToken, authUsername := getGitAuth(currentRepo)

	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.InitializeRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize repository: %v", err)
	}

	currentBranch, err := gitMgr.GetCurrentBranch(ctx)
	if err != nil {
		log.Fatalf("Failed to get current branch: %v", err)
	}

	log.Printf("Pulling latest changes from '%s'...", currentBranch)

	if err := gitMgr.Pull(ctx); err != nil {
		log.Fatalf("Failed to pull changes: %v", err)
	}

	log.Println("✅ Successfully pulled latest changes")
	log.Println("\n💡 Your local branch is now up to date with remote")
}

func pushGitChanges(message string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	authToken, authUsername := getGitAuth(currentRepo)

	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.InitializeRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize repository: %v", err)
	}

	hasChanges, err := gitMgr.HasUncommittedChanges(ctx)
	if err != nil {
		log.Fatalf("Failed to check for changes: %v", err)
	}

	if !hasChanges {
		log.Println("No changes to commit")
		log.Println("\n💡 Working tree is clean")
		return
	}

	statusSummary, err := gitMgr.GetStatusSummary(ctx)
	if err != nil {
		log.Fatalf("Failed to get status: %v", err)
	}

	log.Printf("📝 Changes detected: %s", statusSummary)

	if message == "" {
		message = fmt.Sprintf("Update: %s", statusSummary)
		log.Printf("Using default commit message: %s", message)
	}

	currentBranch, err := gitMgr.GetCurrentBranch(ctx)
	if err != nil {
		log.Fatalf("Failed to get current branch: %v", err)
	}

	log.Printf("Committing changes to '%s'...", currentBranch)
	if err := gitMgr.Commit(ctx, message); err != nil {
		log.Fatalf("Failed to commit changes: %v", err)
	}

	log.Println("✅ Changes committed successfully")

	log.Printf("Pushing to remote '%s'...", currentBranch)
	if err := gitMgr.Push(ctx); err != nil {
		log.Fatalf("Failed to push changes: %v", err)
	}

	log.Println("✅ Changes pushed successfully")
	log.Printf("\n💡 Branch '%s' is now synchronized with remote", currentBranch)
}

func syncGitChanges(message string) {
	repoMgr, err := repository.NewManager()
	if err != nil {
		log.Fatalf("Failed to create repository manager: %v", err)
	}
	defer repoMgr.Close()

	currentRepo, err := repoMgr.GetCurrentRepository()
	if err != nil {
		log.Println("❌ No Git repository configured")
		return
	}

	authToken, authUsername := getGitAuth(currentRepo)

	ctx := context.Background()
	gitMgr, err := internalgit.NewBackend(currentRepo.RepoURL, currentRepo.LocalPath, authUsername, authToken)
	if err != nil {
		log.Fatalf("Failed to create git backend: %v", err)
	}

	if err := gitMgr.InitializeRepo(ctx); err != nil {
		log.Fatalf("Failed to initialize repository: %v", err)
	}

	currentBranch, err := gitMgr.GetCurrentBranch(ctx)
	if err != nil {
		log.Fatalf("Failed to get current branch: %v", err)
	}

	log.Printf("🔄 Syncing branch '%s' with remote...", currentBranch)

	log.Println("1. Pulling latest changes from remote...")
	if err := gitMgr.Pull(ctx); err != nil {
		log.Printf("⚠️  Failed to pull changes: %v", err)
	} else {
		log.Println("✅ Pulled latest changes")
	}

	hasChanges, err := gitMgr.HasUncommittedChanges(ctx)
	if err != nil {
		log.Fatalf("Failed to check for changes: %v", err)
	}

	if !hasChanges {
		log.Println("\n✅ Repository is synchronized")
		log.Println("💡 No local changes to push")
		return
	}

	statusSummary, err := gitMgr.GetStatusSummary(ctx)
	if err != nil {
		log.Fatalf("Failed to get status: %v", err)
	}

	log.Printf("\n2. Local changes detected: %s", statusSummary)

	if message == "" {
		message = "Update repository state"
	}

	log.Println("3. Committing local changes...")
	if err := gitMgr.Commit(ctx, message); err != nil {
		log.Fatalf("Failed to commit changes: %v", err)
	}
	log.Println("✅ Changes committed")

	log.Println("4. Pushing to remote...")
	if err := gitMgr.Push(ctx); err != nil {
		log.Fatalf("Failed to push changes: %v", err)
	}
	log.Println("✅ Changes pushed")

	log.Printf("\n✅ Branch '%s' is now fully synchronized", currentBranch)
}
