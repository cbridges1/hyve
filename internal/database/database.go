package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

const (
	DatabaseFileName = "hyve.db"
)

var (
	instance          *DB
	once              sync.Once
	initErr           error
	configDirOverride string
)

// SetConfigDir overrides the config directory used by the singleton database.
// Must be called before the first GetDB() call (e.g. from a PersistentPreRun hook).
func SetConfigDir(dir string) {
	configDirOverride = dir
}

// DB represents the unified database connection
type DB struct {
	db        *sql.DB
	dbPath    string
	configDir string
}

// GetDB returns the singleton database instance
func GetDB() (*DB, error) {
	once.Do(func() {
		instance, initErr = newDB(configDirOverride)
	})
	return instance, initErr
}

// GetDBWithDir returns a database instance with a custom config directory (for testing)
func GetDBWithDir(configDir string) (*DB, error) {
	return newDB(configDir)
}

// newDB creates a new database connection
func newDB(configDir string) (*DB, error) {
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = "."
		}
		configDir = filepath.Join(homeDir, ".hyve")
	}

	dbPath := filepath.Join(configDir, DatabaseFileName)

	// Ensure config directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	d := &DB{
		db:        db,
		dbPath:    dbPath,
		configDir: configDir,
	}

	if err := d.initialize(); err != nil {
		db.Close()
		return nil, err
	}

	// Additive column migration for pre-existing databases created before
	// the repositories table grew its api_url/session_token/
	// session_expires_at columns — CREATE TABLE IF NOT EXISTS above is a
	// no-op against an already-existing table, so a real ALTER TABLE step
	// is needed for anyone upgrading from an older hyve.db. No-op against a
	// freshly-created database, since the CREATE TABLE above already
	// includes these columns.
	if err := d.ensureRepositoryCredentialColumns(); err != nil {
		db.Close()
		return nil, err
	}

	// Run migrations from old databases
	if err := d.migrateFromOldDatabases(); err != nil {
		// Log but don't fail - migration is best-effort
		log.Printf("Note: Could not migrate from old databases: %v\n", err)
	}

	return d, nil
}

// initialize creates all tables
func (d *DB) initialize() error {
	// Create all tables in a single transaction
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Repositories table — each row is a "environment": a local directory
	// (see internal/repository, cmd/env) plus, optionally, cluster-mode
	// login credentials (api_url/session_token/session_expires_at) attached
	// by `hyve login`. One is_current flag switches both halves together.
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS repositories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			repo_url TEXT NOT NULL,
			local_path TEXT NOT NULL,
			is_current BOOLEAN DEFAULT FALSE,
			api_url TEXT,
			session_token TEXT,
			session_expires_at TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_repositories_name ON repositories(name);
		CREATE INDEX IF NOT EXISTS idx_repositories_current ON repositories(is_current)
	`)
	if err != nil {
		return fmt.Errorf("failed to create repositories table: %w", err)
	}

	// Kubeconfigs table
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS kubeconfigs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cluster_name TEXT NOT NULL,
			repository_name TEXT NOT NULL,
			encrypted_config TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(cluster_name, repository_name)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create kubeconfigs table: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ensureRepositoryCredentialColumns adds any of api_url/session_token/
// session_expires_at missing from an existing repositories table — see the
// call site in newDB for why this can't just live in the CREATE TABLE
// statement alone.
func (d *DB) ensureRepositoryCredentialColumns() error {
	rows, err := d.db.Query(`PRAGMA table_info(repositories)`)
	if err != nil {
		return fmt.Errorf("failed to inspect repositories table: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("failed to read repositories column info: %w", err)
		}
		existing[name] = true
	}
	rows.Close()

	for _, col := range []string{"api_url", "session_token", "session_expires_at"} {
		if existing[col] {
			continue
		}
		if _, err := d.db.Exec(fmt.Sprintf(`ALTER TABLE repositories ADD COLUMN %s TEXT`, col)); err != nil {
			return fmt.Errorf("failed to add %s column to repositories: %w", col, err)
		}
	}
	return nil
}

// migrateFromOldDatabases migrates data from the old separate databases
func (d *DB) migrateFromOldDatabases() error {
	// Migrate from credentials.db
	if err := d.migrateFromCredentialsDB(); err != nil {
		return err
	}

	// Migrate from repositories.db
	if err := d.migrateFromRepositoriesDB(); err != nil {
		return err
	}

	// Migrate from kubeconfigs.db
	if err := d.migrateFromKubeconfigsDB(); err != nil {
		return err
	}

	return nil
}

// migrateFromCredentialsDB migrates data from the old credentials.db
// NOTE: Encrypted credentials and tokens are NOT migrated because the encryption
// keys are derived differently in the new unified database. Users will need to
// re-enter their credentials and tokens.
func (d *DB) migrateFromCredentialsDB() error {
	// We intentionally do NOT migrate encrypted credentials or tokens
	// because the encryption keys have changed with the database consolidation.
	// Users will need to re-enter their credentials with:
	//   hyve config civo token set --org <org-name>
	//   hyve config set-credentials
	return nil
}

// migrateFromRepositoriesDB migrates data from the old repositories.db
func (d *DB) migrateFromRepositoriesDB() error {
	oldDBPath := filepath.Join(d.configDir, "repositories.db")
	if _, err := os.Stat(oldDBPath); os.IsNotExist(err) {
		return nil // No old database to migrate
	}

	oldDB, err := sql.Open("sqlite", oldDBPath)
	if err != nil {
		return fmt.Errorf("failed to open old repositories database: %w", err)
	}
	defer oldDB.Close()

	rows, err := oldDB.Query(`SELECT name, repo_url, local_path, is_current, created_at, updated_at FROM repositories`)
	if err != nil {
		return nil // Table might not exist
	}
	defer rows.Close()

	for rows.Next() {
		var name, repoURL, localPath, createdAt, updatedAt string
		var isCurrent bool
		if err := rows.Scan(&name, &repoURL, &localPath, &isCurrent, &createdAt, &updatedAt); err != nil {
			continue
		}
		// Check if already exists
		var count int
		d.db.QueryRow(`SELECT COUNT(*) FROM repositories WHERE name = ?`, name).Scan(&count)
		if count == 0 {
			d.db.Exec(`INSERT INTO repositories (name, repo_url, local_path, is_current, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
				name, repoURL, localPath, isCurrent, createdAt, updatedAt)
		}
	}

	return nil
}

// migrateFromKubeconfigsDB migrates data from the old kubeconfigs.db
// NOTE: Encrypted kubeconfigs are NOT migrated because the encryption
// keys are derived differently in the new unified database. Kubeconfigs
// will be re-fetched when clusters are synced.
func (d *DB) migrateFromKubeconfigsDB() error {
	// We intentionally do NOT migrate encrypted kubeconfigs
	// because the encryption keys have changed with the database consolidation.
	// Kubeconfigs will be re-fetched when clusters are synced with:
	//   hyve cluster sync
	return nil
}

// DB returns the underlying sql.DB connection
func (d *DB) Conn() *sql.DB {
	return d.db
}

// Path returns the database file path
func (d *DB) Path() string {
	return d.dbPath
}

// ConfigDir returns the config directory
func (d *DB) ConfigDir() string {
	return d.configDir
}

// Close closes the database connection
func (d *DB) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// ResetSingleton resets the singleton instance (for testing)
func ResetSingleton() {
	once = sync.Once{}
	if instance != nil {
		instance.Close()
		instance = nil
	}
	initErr = nil
}
