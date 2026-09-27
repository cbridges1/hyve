package orgdb

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

// migrate applies every not-yet-applied migration file for driver, in
// filename order, tracking progress in a schema_migrations table so a
// second call against an already-current database is a no-op. Each file
// is split into individual statements and executed one at a time inside
// a single transaction per file — database/sql's Exec doesn't reliably
// support multiple ;-separated statements in one call against every
// driver (notably pgx's default extended-protocol mode), so this sidesteps
// that rather than depending on it.
//
// Everything runs on one dedicated connection. For SQLite, that connection
// has foreign-key enforcement switched off for the duration, the one
// documented way to rebuild a table other tables reference (SQLite can't
// drop a column's inline UNIQUE constraint in place, see
// 0006_reconciling_cluster_ownership.sql) — foreign_keys can't be toggled
// inside a transaction, and it's per-connection, hence the dedicated
// connection rather than the pool. PRAGMA foreign_key_check runs before
// each migration's commit instead, so a migration that would leave a
// dangling reference still fails rather than committing silently.
func migrate(db *sql.DB, driver string) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	if driver == "sqlite" {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return fmt.Errorf("disable sqlite foreign_keys for migration: %w", err)
		}
		defer conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`) //nolint:errcheck // best-effort restore before the connection returns to the pool
	}

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	fsys, dir := sqliteMigrations, "migrations/sqlite"
	if driver == "postgres" {
		fsys, dir = postgresMigrations, "migrations/postgres"
	}

	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")

		var already int
		checkQuery := rebind(driver, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`)
		if err := conn.QueryRowContext(ctx, checkQuery, version).Scan(&already); err != nil {
			return fmt.Errorf("check migration %s applied: %w", version, err)
		}
		if already > 0 {
			continue
		}

		content, err := fsys.ReadFile(path.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		for _, stmt := range splitStatements(string(content)) {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("apply migration %s: %w", version, err)
			}
		}
		if driver == "sqlite" {
			if err := checkSQLiteForeignKeys(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("apply migration %s: %w", version, err)
			}
		}
		insertQuery := rebind(driver, `INSERT INTO schema_migrations (version) VALUES (?)`)
		if _, err := tx.Exec(insertQuery, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}

	return nil
}

// checkSQLiteForeignKeys stands in for the enforcement migrate switches off:
// PRAGMA foreign_key_check returns one row per violating reference, so any
// row at all means the migration left the schema inconsistent.
func checkSQLiteForeignKeys(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("foreign_key_check: %w", err)
		}
		return fmt.Errorf("foreign key violation: %s row %d references a missing %s row", table, rowid.Int64, parent)
	}
	return rows.Err()
}

// splitStatements splits a migration file's content into individual SQL
// statements on ';' boundaries, dropping comment-only and blank lines
// first. Naive by design — sufficient for this package's own hand-written
// migration files, which never embed a literal semicolon inside a string
// literal or comment; not intended as a general SQL parser.
func splitStatements(content string) []string {
	var cleaned strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		cleaned.WriteString(line)
		cleaned.WriteByte('\n')
	}

	var stmts []string
	for _, raw := range strings.Split(cleaned.String(), ";") {
		if s := strings.TrimSpace(raw); s != "" {
			stmts = append(stmts, s)
		}
	}
	return stmts
}
