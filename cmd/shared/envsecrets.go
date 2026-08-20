package shared

import (
	"os"

	"github.com/joho/godotenv"

	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
)

// LoadEnvironmentSecrets loads the currently-active environment's DB-backed
// secrets (see 'hyve env secrets') into the process environment, additive
// only — a variable already set (the real process/CI environment) always
// wins, the same precedent godotenv.Load itself follows. Best-effort: no
// active environment, or a DB read failure, is silently a no-op rather than
// aborting the CLI invocation, matching the legacy env-file load this runs
// ahead of (see LoadLegacyRepoEnvFile).
func LoadEnvironmentSecrets() {
	repoMgr, err := repository.NewManager()
	if err != nil {
		return
	}
	defer repoMgr.Close()

	current, err := repoMgr.GetCurrentRepository()
	if err != nil {
		return
	}

	vars, err := repoMgr.ListSecrets(current.ID)
	if err != nil {
		return
	}

	for key, value := range vars {
		if _, alreadySet := os.LookupEnv(key); !alreadySet {
			os.Setenv(key, value)
		}
	}
}

// LoadLegacyRepoEnvFile loads a repo-relative dotenv file (hyve.yaml's
// env.file, defaulting to ".env") into the process environment — the
// original bootstrap mechanism (previously inline in main.go, before
// --home/the DB-backed environment registry could be resolved), now
// deliberately run after LoadEnvironmentSecrets so the newer, centrally-
// managed store takes precedence when both set the same key. Additive only
// (godotenv.Load, not .Overload) — see internal/state.EnvConfig's own doc
// comment.
func LoadLegacyRepoEnvFile() {
	repoRoot, err := os.Getwd()
	if err != nil {
		_ = godotenv.Load()
		return
	}
	_ = godotenv.Load(state.ResolveEnvFile(repoRoot))
}
