package main

import (
	"github.com/cbridges1/hyve/cmd"
)

func main() {
	// Env-file/secret loading (both the DB-backed 'hyve env secrets' store
	// and the legacy repo-relative hyve.yaml env.file) happens inside
	// cmd/root.go's PersistentPreRunE, not here — it needs --home resolved
	// first (to know which environment's secrets to load), which isn't
	// available until cobra has parsed flags. See
	// cmd/shared.LoadEnvironmentSecrets/LoadLegacyRepoEnvFile.
	cmd.Execute()
}
