//go:build ignore

package shared

import (
	"os"

	"github.com/joho/godotenv"

	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/state"
)

// LoadEnvironmentSecrets loads every secret source into the process
// environment, most-specific first — additive throughout (a variable
// already set always wins, the same precedent godotenv.Load itself
// follows). Highest to lowest precedence: the active local context's
// DB-backed secrets → the legacy repo-relative hyve.yaml env.file. Every
// layer is best-effort: a DB read failure is silently a no-op rather than
// aborting the CLI invocation, since this runs before every command.
// (hyve-api's shared secrets were the top layer until hyve-api's login was
// removed; a leftover session file is no longer read.)
func LoadEnvironmentSecrets() {
	loadLocalEnvironmentSecrets()
}

// loadLocalEnvironmentSecrets loads the currently-active environment's
// DB-backed secrets (see 'hyve context secrets' local mode).
func loadLocalEnvironmentSecrets() {
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
// env.file, defaulting to hyve.env) into the process environment — run
// after LoadEnvironmentSecrets so the DB-backed stores take precedence
// when both set the same key. Checked in two places, in order: the
// current working directory, then the active context's own directory (so
// `hyve cluster auth x` works from anywhere, not only from inside the
// context's checkout). Additive only (godotenv.Load, not .Overload): a
// variable already set always wins, so the working directory's file beats
// the context's on a same-key collision — see internal/state.EnvConfig's
// own doc comment.
func LoadLegacyRepoEnvFile() {
	loaded := map[string]bool{}
	load := func(dir string) {
		path := state.ResolveEnvFile(dir)
		if loaded[path] {
			return
		}
		loaded[path] = true
		_ = godotenv.Load(path)
	}

	if cwd, err := os.Getwd(); err == nil {
		load(cwd)
	} else {
		_ = godotenv.Load()
	}

	repoMgr, err := repository.NewManager()
	if err != nil {
		return
	}
	defer repoMgr.Close()
	if current, err := repoMgr.GetCurrentRepository(); err == nil && current.LocalPath != "" {
		load(current.LocalPath)
	}
}
