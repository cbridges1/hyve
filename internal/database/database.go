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

	// Credentials table (Git credentials)
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS credentials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			encrypted_password TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create credentials table: %w", err)
	}

	// Secrets table (generic named secret storage)
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS secrets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL DEFAULT '',
			encrypted_value TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create secrets table: %w", err)
	}

	// Add type column if upgrading from a version without it (ignore error if already exists)
	tx.Exec(`ALTER TABLE secrets ADD COLUMN type TEXT NOT NULL DEFAULT ''`)

	// Migrate from old api_tokens table if it exists
	_, err = tx.Exec(`
		INSERT OR IGNORE INTO secrets (name, type, encrypted_value, created_at, updated_at)
		SELECT provider, provider, encrypted_token, created_at, updated_at FROM api_tokens
	`)
	// Ignore error - old table might not exist

	// Repositories table
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS repositories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			repo_url TEXT NOT NULL,
			local_path TEXT NOT NULL,
			is_current BOOLEAN DEFAULT FALSE,
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
