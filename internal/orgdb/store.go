// Package orgdb is hyve-api's own organization/environment/RBAC datastore
// — Postgres or SQLite, chosen at startup, same schema either way. See
// HYVE-ORGANIZATION-MODEL-PROPOSAL.md (nexus-config/docs) for the design
// this implements and HYVE-ORGANIZATION-MODEL-IMPLEMENTATION-PLAN.md for
// the milestone this package is part of (Milestone 1: schema + type
// scaffolding — inert; nothing in internal/api reads or writes through
// this yet).
package orgdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	_ "modernc.org/sqlite"             // registers the "sqlite" database/sql driver

	"github.com/google/uuid"
)

// Store is hyve-api's own handle onto the organization/environment/RBAC
// database. A thin wrapper over *sql.DB rather than an interface with
// multiple implementations — SQLite and Postgres are both reached through
// the one database/sql surface (see dialect.go's rebind), so there's
// nothing a second implementation would need to satisfy.
type Store struct {
	db     *sql.DB
	driver string // "sqlite" or "postgres" — governs rebind's placeholder rewriting
}

// Open opens (creating and migrating, if necessary) the organization
// datastore. driver is "sqlite" or "postgres"; dsn is a file path for
// sqlite, a standard Postgres connection string for postgres.
func Open(driver, dsn string) (*Store, error) {
	sqlDriverName := driver
	if driver == "postgres" {
		sqlDriverName = "pgx"
	}

	if driver == "sqlite" {
		// dsn is a file path here — mirrors internal/database's own
		// os.MkdirAll(configDir) precedent for the CLI's local store, so a
		// fresh install (whose PVC mount exists but is otherwise empty)
		// doesn't fail just because the file's own parent directory has
		// never been created.
		if dir := filepath.Dir(dsn); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create sqlite database directory %s: %w", dir, err)
			}
		}
	}

	db, err := sql.Open(sqlDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", driver, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s database: %w", driver, err)
	}

	if driver == "sqlite" {
		// Foreign keys are off by default per SQLite connection — this
		// package's schema relies on them being enforced (an environment
		// row pointing at a deleted organization is a real bug, not
		// something to silently allow).
		if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable sqlite foreign_keys: %w", err)
		}
	}

	if err := migrate(db, driver); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s database: %w", driver, err)
	}

	return &Store{db: db, driver: driver}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, rebind(s.driver, query), args...)
}

func (s *Store) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, rebind(s.driver, query), args...)
}

func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, rebind(s.driver, query), args...)
}

// newID generates a fresh row identifier — a plain UUIDv4 string, the
// same "app-generated TEXT primary key" convention the migration files'
// own header comments describe.
func newID() string {
	return uuid.NewString()
}
