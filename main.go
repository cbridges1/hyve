package main

import (
	"os"

	"github.com/joho/godotenv"

	"github.com/cbridges1/hyve/cmd"
	"github.com/cbridges1/hyve/internal/state"
)

func main() {
	// hyve.yaml's env.file was previously ignored entirely — this always
	// called godotenv.Load() with no argument, which only ever loads a
	// literal ".env" in the current directory regardless of what env.file
	// says. Repos naming their dotenv file anything else (e.g. hyve.env)
	// silently had it never loaded at all; any command depending on a
	// var from it would fail with "missing required environment
	// variable(s)" even though the file existed right next to hyve.yaml.
	// os.Getwd() as repoRoot matches every other repo-root assumption in
	// this codebase — hyve is always run from the repository root, the
	// same place hyve.yaml/hyve.env both sit.
	repoRoot, err := os.Getwd()
	if err != nil {
		_ = godotenv.Load()
	} else {
		_ = godotenv.Load(state.ResolveEnvFile(repoRoot))
	}
	cmd.Execute()
}
