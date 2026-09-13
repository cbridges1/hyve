package orgdb

import (
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
func migrate(db *sql.DB, driver string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
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
		if err := db.QueryRow(checkQuery, version).Scan(&already); err != nil {
			return fmt.Errorf("check migration %s applied: %w", version, err)
		}
		if already > 0 {
			continue
		}

		content, err := fsys.ReadFile(path.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		for _, stmt := range splitStatements(string(content)) {
			if _, err := tx.Exec(stmt); err != nil {
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
