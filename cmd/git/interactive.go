package git

import (
	"fmt"

	"github.com/charmbracelet/huh"

	"hyve/cmd/shared"
)

// RunInteractive runs the interactive git menu.
func RunInteractive() error {
	return runInteractiveGit()
}

func runInteractiveGit() error {
	for {
		var category string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Git — what would you like to do?").
					Options(
						huh.NewOption("Repository management", "repo"),
						huh.NewOption("Branch management", "branch"),
						huh.NewOption("Pull / push / sync", "sync"),
						huh.NewOption("← Back", "back"),
					).
					Value(&category),
			),
		).Run()
		if err != nil {
			return err
		}

		switch category {
		case "back":
			return shared.ErrBack
		case "repo":
			if err := interactiveGitRepo(); err != nil && err != shared.ErrBack {
				return err
			}
		case "branch":
			if err := interactiveGitBranch(); err != nil && err != shared.ErrBack {
				return err
			}
		case "sync":
			if err := interactiveGitSync(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

// ── Repository ───────────────────────────────────────────────────────────────

func interactiveGitRepo() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Repository — action").
					Options(
						huh.NewOption("Add repository", "add"),
						huh.NewOption("List repositories", "list"),
						huh.NewOption("Use (switch to) repository", "use"),
						huh.NewOption("Show status", "status"),
						huh.NewOption("Remove repository", "remove"),
						huh.NewOption("← Back", "back"),
					).
					Value(&action),
			),
		).Run()
		if err != nil {
			return err
		}

		switch action {
		case "back":
			return shared.ErrBack
		case "list":
			listGitRepositories()
		case "status":
			showGitStatus()
		case "add":
			if err := interactiveGitRepoAdd(); err != nil && err != shared.ErrBack {
				return err
			}
		case "use":
			if err := interactiveGitRepoUse(); err != nil && err != shared.ErrBack {
				return err
			}
		case "remove":
			if err := interactiveGitRepoRemove(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveGitRepoAdd() error {
	var (
		name       string
		repoURL    string
		username   string
		setCurrent bool
	)

	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Repository alias").Placeholder("production").Validate(shared.RequireNotEmpty).Value(&name),
			huh.NewInput().Title("Repository URL").Placeholder("https://github.com/org/hyve-state.git").Validate(shared.RequireNotEmpty).Value(&repoURL),
			huh.NewInput().Title("Git username (optional)").Value(&username),
			huh.NewConfirm().
				Title("Set as current active repository?").
				Affirmative("Yes").
				Negative("No").
				Value(&setCurrent),
		),
	).Run()
	if err != nil {
		return err
	}

	addGitRepository(name, repoURL, username, setCurrent)
	return nil
}

func interactiveGitRepoUse() error {
	name := ""
	if err := shared.SelectFromList("Repository to switch to", shared.FetchGitRepoNames(), &name); err != nil {
		return err
	}
	switchToRepository(name)
	return nil
}

func interactiveGitRepoRemove() error {
	name := ""
	if err := shared.SelectFromList("Repository to remove", shared.FetchGitRepoNames(), &name); err != nil {
		return err
	}

	var confirm bool
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Remove repository '%s'?", name)).
				Affirmative("Yes, remove").
				Negative("Cancel").
				Value(&confirm),
		),
	).Run()
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	removeGitRepository(name)
	return nil
}

// ── Branch ───────────────────────────────────────────────────────────────────

func interactiveGitBranch() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Branch — action").
					Options(
						huh.NewOption("List branches", "list"),
						huh.NewOption("Create branch", "create"),
						huh.NewOption("Switch branch", "switch"),
						huh.NewOption("Delete branch", "delete"),
						huh.NewOption("← Back", "back"),
					).
					Value(&action),
			),
		).Run()
		if err != nil {
			return err
		}

		switch action {
		case "back":
			return shared.ErrBack
		case "list":
			listGitBranches()
		case "create":
			if err := interactiveGitBranchCreate(); err != nil && err != shared.ErrBack {
				return err
			}
		case "switch":
			if err := interactiveGitBranchSwitch(); err != nil && err != shared.ErrBack {
				return err
			}
		case "delete":
			if err := interactiveGitBranchDelete(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveGitBranchCreate() error {
	var (
		branchName   string
		switchBranch bool
		push         bool
	)

	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("New branch name").Placeholder("feature/my-feature").Validate(shared.RequireNotEmpty).Value(&branchName),
			huh.NewConfirm().
				Title("Switch to new branch after creating?").
				Affirmative("Yes").
				Negative("No").
				Value(&switchBranch),
			huh.NewConfirm().
				Title("Push to remote?").
				Affirmative("Yes").
				Negative("No").
				Value(&push),
		),
	).Run()
	if err != nil {
		return err
	}

	createGitBranch(branchName, switchBranch, push)
	return nil
}

func interactiveGitBranchSwitch() error {
	var (
		branchName string
		pull       bool
	)

	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Branch name to switch to").Validate(shared.RequireNotEmpty).Value(&branchName),
			huh.NewConfirm().
				Title("Pull latest changes after switching?").
				Affirmative("Yes").
				Negative("No").
				Value(&pull),
		),
	).Run()
	if err != nil {
		return err
	}

	switchGitBranch(branchName, pull)
	return nil
}

func interactiveGitBranchDelete() error {
	var (
		branchName string
		force      bool
	)

	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Branch name to delete").Validate(shared.RequireNotEmpty).Value(&branchName),
			huh.NewConfirm().
				Title("Force delete (even if not merged)?").
				Affirmative("Force delete").
				Negative("Safe delete").
				Value(&force),
		),
	).Run()
	if err != nil {
		return err
	}

	var confirm bool
	err = shared.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete branch '%s'?", branchName)).
				Affirmative("Yes, delete").
				Negative("Cancel").
				Value(&confirm),
		),
	).Run()
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	deleteGitBranch(branchName, force)
	return nil
}

// ── Sync ─────────────────────────────────────────────────────────────────────

func interactiveGitSync() error {
	for {
		var action string
		err := shared.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Sync — action").
					Options(
						huh.NewOption("Pull latest changes", "pull"),
						huh.NewOption("Push changes", "push"),
						huh.NewOption("Sync (pull then push)", "sync"),
						huh.NewOption("← Back", "back"),
					).
					Value(&action),
			),
		).Run()
		if err != nil {
			return err
		}

		switch action {
		case "back":
			return shared.ErrBack
		case "pull":
			pullGitChanges()
		case "push":
			if err := interactiveGitPush(); err != nil && err != shared.ErrBack {
				return err
			}
		case "sync":
			if err := interactiveGitSyncChanges(); err != nil && err != shared.ErrBack {
				return err
			}
		}
	}
}

func interactiveGitPush() error {
	var message string
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Commit message").Placeholder("Update cluster config").Validate(shared.RequireNotEmpty).Value(&message),
		),
	).Run()
	if err != nil {
		return err
	}
	pushGitChanges(message)
	return nil
}

func interactiveGitSyncChanges() error {
	var message string
	err := shared.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Commit message").Placeholder("Update cluster config").Validate(shared.RequireNotEmpty).Value(&message),
		),
	).Run()
	if err != nil {
		return err
	}
	syncGitChanges(message)
	return nil
}
