package shared

import (
	"os"

	"github.com/joho/godotenv"

	"github.com/cbridges1/hyve/internal/repository"
	"github.com/cbridges1/hyve/internal/session"
	"github.com/cbridges1/hyve/internal/state"
)

// LoadEnvironmentSecrets loads every secret source into the process
// environment, most-specific first — additive throughout (a variable
// already set always wins, the same precedent godotenv.Load itself
// follows), so layers compose rather than shadow each other except on an
// actual same-key collision. Highest to lowest precedence: cluster-mode
// secrets (if logged in) → the active local environment's DB-backed
// secrets → the legacy repo-relative hyve.yaml env.file. Every layer is
// best-effort: not logged in, a DB read failure, an unreachable API
// server, or a session that can't be refreshed is silently a no-op rather
// than aborting the CLI invocation — this runs unconditionally from
// rootCmd's PersistentPreRunE, before every single command, including ones
// with nothing to do with cluster mode at all (`hyve whoami`, `hyve env
// list`...). Deliberately calls EnsureValidSession (which attempts a
// silent refresh) rather than UseClusterMode: that function intentionally
// hard-fails when a session can't be made to work (see its own doc
// comment) for the real mode-dispatch decision points (cmd/cluster,
// cmd/workflow, etc.) — this call site is not one of those, and must never
// abort a command that doesn't even touch cluster mode just because the
// session happens to be unrefreshable.
func LoadEnvironmentSecrets() {
	if sess, err := EnsureValidSession(); err == nil && sess != nil {
		loadClusterSecrets(sess)
	}
	loadLocalEnvironmentSecrets()
}

// loadClusterSecrets loads the logged-in hyve-api server's shared
// `hyve-cli-secrets` values (see internal/api/secrets.go) into the process
// environment. A read-only session gets a 403 from the values endpoint —
// swallowed here just like every other failure mode, so a read-only caller
// can still run commands that don't happen to need a cluster secret.
func loadClusterSecrets(sess *session.Session) {
	vars, err := NewAPIClient(sess).ListSecretValues()
	if err != nil {
		return
	}
	for key, value := range vars {
		if _, alreadySet := os.LookupEnv(key); !alreadySet {
			os.Setenv(key, value)
		}
	}
}

// loadLocalEnvironmentSecrets loads the currently-active environment's
// DB-backed secrets (see 'hyve env secrets' local mode).
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
// env.file, defaulting to ".env") into the process environment — the
// original bootstrap mechanism (previously inline in main.go, before
// --home/the DB-backed environment registry could be resolved), now
// deliberately run after LoadEnvironmentSecrets so the newer, centrally-
// managed stores take precedence when both set the same key. Additive only
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
