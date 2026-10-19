//go:build ignore

package cmd

import (
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/migrate"
)

// migrateCmd groups the commands that move hyve's state between clusters.
// Both talk to their clusters' Kubernetes APIs directly. (Pushing local
// files through hyve-api, the bare 'hyve migrate', went with hyve-api's
// login.)
var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Move hyve's state onto a cluster, or from one cluster to another",
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}

func printMigrateSummary(kind string, s *migrate.Summary, dryRun bool) {
	verb := "created"
	if dryRun {
		verb = "would create"
	}
	for _, name := range s.Created {
		log.Printf("[%s] %s — %s", kind, name, verb)
	}
	for _, name := range s.Skipped {
		log.Printf("[%s] %s — already exists, skipped (use --force to overwrite)", kind, name)
	}
	for name, err := range s.Failed {
		log.Printf("[%s] %s — FAILED: %v", kind, name, err)
	}
	log.Printf("%s: %d created, %d skipped, %d failed", kind, len(s.Created), len(s.Skipped), len(s.Failed))
}
