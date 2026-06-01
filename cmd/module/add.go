package module

import (
	"context"
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/cmd/shared"
	mod "github.com/cbridges1/hyve/internal/module"
)

var addCmd = &cobra.Command{
	Use:   "add <source>[@<version>]",
	Short: "Add a module to hyve.lock",
	Long: `Add a module to hyve.lock.

Version is optional. When omitted, the latest semver tag is resolved automatically.

Examples:
  hyve module add github.com/hyve-modules/civo
  hyve module add github.com/hyve-modules/civo@v1.0.0
  hyve module add github.com/hyve-modules/civo@~> 1.0`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		source, version := parseSourceVersion(args)
		ctx := context.Background()
		stateMgr, _ := shared.CreateStateManager(ctx)
		repoPath := stateMgr.LocalPath()

		lf, err := mod.LoadLockFile(repoPath)
		if err != nil {
			log.Fatalf("Failed to load lock file: %v", err)
		}

		log.Printf("Resolving %s@%s...", source, version)
		resolved, err := mod.Resolve(source, version, nil, repoPath)
		if err != nil {
			log.Fatalf("Failed to resolve module: %v", err)
		}

		// Use the canonical resolved version (e.g. "v1.2.3") as the lock key,
		// not the user-supplied version string (which may be "latest" or a constraint).
		lockVersion := resolved.Version
		if lockVersion == "" {
			lockVersion = version
		}

		if lf.GetLocked(source, lockVersion) != nil {
			log.Printf("Already locked: %s@%s", source, lockVersion)
			return
		}

		lf.SetLocked(source, lockVersion, &mod.LockedModule{
			Source:   source,
			Resolved: resolved.Resolved,
			SHA256:   resolved.SHA256,
			Runner:   resolved.Runner,
		})
		if err := mod.SaveLockFile(repoPath, lf); err != nil {
			log.Fatalf("Failed to save lock file: %v", err)
		}
		log.Printf("Locked %s@%s (sha256: %s)", source, lockVersion, resolved.SHA256)
		shared.CommitStateChanges(ctx, stateMgr, "chore: add module "+source+"@"+lockVersion)
	},
}

// parseSourceVersion extracts source and version from the command args.
// Accepts "source@version" as a single arg, or source and version as two args.
// Defaults to "latest" when no version is provided.
func parseSourceVersion(args []string) (source, version string) {
	if len(args) == 2 {
		return args[0], args[1]
	}
	// Single arg: check for "@version" suffix
	if idx := strings.LastIndex(args[0], "@"); idx > 0 {
		return args[0][:idx], args[0][idx+1:]
	}
	return args[0], "latest"
}
