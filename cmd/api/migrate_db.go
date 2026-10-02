package api

import (
	"context"
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/cbridges1/hyve/internal/orgdb"
)

var (
	migrateDBFromDriver string
	migrateDBFromDSN    string
	migrateDBToDriver   string
	migrateDBToDSN      string
)

var migrateDBCmd = &cobra.Command{
	Use:   "migrate-db",
	Short: "One-shot dump/restore of hyve-api's own organization datastore between SQLite and Postgres",
	Long: `Copies every row — reconciling clusters, organizations, environments,
bindings, the signing key, sessions — from --from-driver/--from-dsn into
--to-driver/--to-dsn, in FK dependency order, preserving every row's own
id (see internal/orgdb.Migrate's own doc comment for exactly what is and
isn't preserved — created_at timestamps are reset to migration time, ids
and every other column are not).

This is a one-shot tool, not a live sync: stop hyve-api (and hyve-controller,
if running in --reconciling-cluster-id mode against this same database)
before running it, and point every process at the new database only after
it completes successfully. The destination must already exist and be
completely empty — this refuses to run otherwise, to avoid silently
duplicating or partially overwriting data.

The common direction is sqlite -> postgres: SQLite has no story for the
concurrent, cross-process access that horizontal API scaling or any use of
per-organization reconciling clusters assumes, but nothing here is
sqlite-specific — postgres -> sqlite (e.g. for a local reproduction of a
production issue) works identically.

Example:
  hyve cluster-config api migrate-db \
    --from-driver sqlite --from-dsn /data/orgdb.sqlite \
    --to-driver postgres --to-dsn postgres://user:pass@host:5432/hyve`,
	Run: func(cmd *cobra.Command, args []string) {
		runMigrateDB()
	},
}

func init() {
	migrateDBCmd.Flags().StringVar(&migrateDBFromDriver, "from-driver", "sqlite", "Source database driver: sqlite or postgres")
	migrateDBCmd.Flags().StringVar(&migrateDBFromDSN, "from-dsn", "/data/orgdb.sqlite", "Source data source name — a file path for sqlite, a connection string for postgres")
	migrateDBCmd.Flags().StringVar(&migrateDBToDriver, "to-driver", "postgres", "Destination database driver: sqlite or postgres")
	migrateDBCmd.Flags().StringVar(&migrateDBToDSN, "to-dsn", "", "Destination data source name (required) — must already exist and be completely empty")
	Cmd.AddCommand(migrateDBCmd)
}

func runMigrateDB() {
	if migrateDBToDSN == "" {
		log.Fatal("--to-dsn is required")
	}

	source, err := orgdb.Open(migrateDBFromDriver, migrateDBFromDSN)
	if err != nil {
		log.Fatalf("Failed to open source --from-driver=%s database at %q: %v", migrateDBFromDriver, migrateDBFromDSN, err)
	}
	defer source.Close()

	dest, err := orgdb.Open(migrateDBToDriver, migrateDBToDSN)
	if err != nil {
		log.Fatalf("Failed to open destination --to-driver=%s database at %q: %v", migrateDBToDriver, migrateDBToDSN, err)
	}
	defer dest.Close()

	summary, err := orgdb.Migrate(context.Background(), source, dest)
	if err != nil {
		log.Fatalf("❌ Migration failed: %v", err)
	}

	fmt.Printf("✅ Migrated %d reconciling cluster(s), %d organization(s), %d environment(s), %d binding(s), %d signing key(s), %d session(s)\n",
		summary.ReconcilingClusters, summary.Organizations, summary.Environments, summary.Bindings, summary.SigningKeys, summary.Sessions)
	fmt.Println("Point hyve-api (and hyve-controller, if applicable) at the new database before starting them back up.")
}
