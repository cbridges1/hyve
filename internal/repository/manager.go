package repository

import (
	"database/sql"
	"fmt"
	"regexp"
	"time"

	"github.com/cbridges1/hyve/internal/database"
)

// Repository represents one registered environment. An environment is
// either a local directory (LocalPath set) hyve reads/writes cluster
// definitions from, or a cluster-mode API URL (APIURL set) pre-registered
// for `hyve env login` to target later — the two are independent kinds of
// entry in the same registry, not the same row wearing two hats. A local
// directory and a cluster-mode *session* (the actual credential, as
// opposed to just the URL) used to be the same row here — that conflation
// is what made an expired/logged-out session silently fall back to
// whatever local files happened to be sitting in the current directory.
// APIURL only ever remembers where to point `hyve env login` at; it carries no
// credential of its own — see internal/session for `hyve env login`'s
// separate, machine-wide session storage, which is what actually
// authenticates. The repositories table's own legacy
// session_token/session_expires_at columns still physically exist
// (dropping columns is an awkward SQLite migration for zero benefit) but
// nothing in this package reads or writes them — only api_url is reused.
type Repository struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	RepoURL   string `json:"repo_url"`
	LocalPath string `json:"local_path"`
	APIURL    string `json:"api_url,omitempty"`
	// APICACert is a PEM-encoded CA certificate to trust in addition to
	// the system trust store when talking to APIURL — see
	// database.ensureRepositoryCredentialColumns' own doc comment. Empty
	// means "use the system trust store as-is," correct for the common
	// case of a publicly-trusted certificate. Set via 'hyve env create
	// --ca-cert'/'hyve env login --ca-cert', read by cmd/shared's HTTP
	// client construction.
	APICACert string    `json:"api_ca_cert,omitempty"`
	IsCurrent bool      `json:"is_current"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const repositoryColumns = `id, name, repo_url, local_path, is_current, api_url, api_ca_cert, created_at, updated_at`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanRepository reads one row in repositoryColumns' exact order.
func scanRepository(s scanner) (*Repository, error) {
	repo := &Repository{}
	var createdAt, updatedAt string
	var apiURL, apiCACert sql.NullString

	if err := s.Scan(&repo.ID, &repo.Name, &repo.RepoURL, &repo.LocalPath, &repo.IsCurrent,
		&apiURL, &apiCACert, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	repo.APIURL = apiURL.String
	repo.APICACert = apiCACert.String

	var err error
	if repo.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt); err != nil {
		repo.CreatedAt = time.Now()
	}
	if repo.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt); err != nil {
		repo.UpdatedAt = time.Now()
	}

	return repo, nil
}

// Manager handles repository configurations using the unified database
type Manager struct {
	db     *database.DB
	dbPath string
}

// NewManager creates a new repository manager
func NewManager() (*Manager, error) {
	db, err := database.GetDB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	return &Manager{
		db:     db,
		dbPath: db.Path(),
	}, nil
}

// NewManagerWithDB creates a new repository manager with a specific database (for testing)
func NewManagerWithDB(db *database.DB) *Manager {
	return &Manager{
		db:     db,
		dbPath: db.Path(),
	}
}

// Close is a no-op for repository manager since the database is managed centrally
func (m *Manager) Close() error {
	// Database is managed by the database package, don't close it here
	return nil
}

// AddRepository adds a new repository configuration. apiURL may be empty
// (a plain local-directory environment) — pass it non-empty to register a
// cluster environment instead, optionally alongside a local directory too.
func (m *Manager) AddRepository(name, repoURL, localPath, apiURL string) (*Repository, error) {
	// Check if repository with this name already exists
	if exists, err := m.repositoryExists(name); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("repository '%s' already exists", name)
	}

	// If this is the first repository, make it current
	isFirst, err := m.isFirstRepository()
	if err != nil {
		return nil, err
	}

	// If making this current, unset other current repositories
	if isFirst {
		if err := m.unsetCurrentRepository(); err != nil {
			return nil, err
		}
	}

	insertSQL := `
	INSERT INTO repositories (name, repo_url, local_path, is_current, api_url)
	VALUES (?, ?, ?, ?, ?)
	`

	result, err := m.db.Conn().Exec(insertSQL, name, repoURL, localPath, isFirst, nullableString(apiURL))
	if err != nil {
		return nil, fmt.Errorf("failed to insert repository: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert ID: %w", err)
	}

	return m.GetRepositoryByID(int(id))
}

// nullableString converts "" to a SQL NULL so api_url round-trips through
// sql.NullString the same way whether it was never set or explicitly
// cleared — an empty string and "not registered" should read identically.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// UpdateRepository updates an existing repository configuration
func (m *Manager) UpdateRepository(name, repoURL, localPath, apiURL string) (*Repository, error) {
	updateSQL := `
	UPDATE repositories
	SET repo_url = ?, local_path = ?, api_url = ?, updated_at = CURRENT_TIMESTAMP
	WHERE name = ?
	`

	result, err := m.db.Conn().Exec(updateSQL, repoURL, localPath, nullableString(apiURL), name)
	if err != nil {
		return nil, fmt.Errorf("failed to update repository: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return nil, fmt.Errorf("repository '%s' not found", name)
	}

	return m.GetRepositoryByName(name)
}

// DeleteRepository removes a repository configuration
func (m *Manager) DeleteRepository(name string) error {
	// Check if this is the current repository
	current, err := m.GetCurrentRepository()
	if err == nil && current != nil && current.Name == name {
		// If deleting current repository, find another one to make current
		repos, err := m.ListRepositories()
		if err != nil {
			return err
		}

		for _, repo := range repos {
			if repo.Name != name {
				if err := m.SetCurrentRepository(repo.Name); err != nil {
					return fmt.Errorf("failed to set new current repository: %w", err)
				}
				break
			}
		}
	}

	// No FK cascade in this schema — delete the environment's secrets
	// explicitly before the repositories row itself, so removing an
	// environment doesn't leave orphaned secrets behind.
	if repo, err := m.GetRepositoryByName(name); err == nil {
		if _, err := m.db.Conn().Exec(`DELETE FROM environment_secrets WHERE repository_id = ?`, repo.ID); err != nil {
			return fmt.Errorf("failed to remove secrets for '%s': %w", name, err)
		}
	}

	deleteSQL := `DELETE FROM repositories WHERE name = ?`
	result, err := m.db.Conn().Exec(deleteSQL, name)
	if err != nil {
		return fmt.Errorf("failed to delete repository: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("repository '%s' not found", name)
	}

	return nil
}

// ListRepositories returns all repository configurations
func (m *Manager) ListRepositories() ([]*Repository, error) {
	selectSQL := `
	SELECT ` + repositoryColumns + `
	FROM repositories
	ORDER BY is_current DESC, name ASC
	`

	rows, err := m.db.Conn().Query(selectSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to query repositories: %w", err)
	}
	defer rows.Close()

	var repositories []*Repository
	for rows.Next() {
		repo, err := scanRepository(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan repository: %w", err)
		}
		repositories = append(repositories, repo)
	}

	return repositories, nil
}

// SetAPICACert stores caCertPEM (PEM-encoded, or "" to clear) as the named
// repository's trusted CA for its api_url — see Repository.APICACert's own
// doc comment. Separate from AddRepository/UpdateRepository (which every
// existing caller already invokes without this) rather than folded into
// either, matching SetSecret/UnsetSecret's own precedent for a narrow,
// single-field mutator.
func (m *Manager) SetAPICACert(name, caCertPEM string) error {
	result, err := m.db.Conn().Exec(`UPDATE repositories SET api_ca_cert = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?`,
		nullableString(caCertPEM), name)
	if err != nil {
		return fmt.Errorf("failed to set api_ca_cert: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("repository '%s' not found", name)
	}
	return nil
}

// GetRepositoryByAPIURL returns the repository registered against apiURL
// (exact match — callers should already have trimmed a trailing slash the
// same way AddRepository/'hyve env login' do), or an error if none is
// registered yet. Used to resolve a per-environment trusted CA (see
// Repository.APICACert) from just a URL, when no environment name is
// available at the call site (cmd/shared's session-refresh/logout
// requests, which only carry session.Session.APIURL).
func (m *Manager) GetRepositoryByAPIURL(apiURL string) (*Repository, error) {
	selectSQL := `
	SELECT ` + repositoryColumns + `
	FROM repositories
	WHERE api_url = ?
	`

	repo, err := scanRepository(m.db.Conn().QueryRow(selectSQL, apiURL))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no environment registered for api_url %q", apiURL)
		}
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}
	return repo, nil
}

// GetRepositoryByName returns a repository by name
func (m *Manager) GetRepositoryByName(name string) (*Repository, error) {
	selectSQL := `
	SELECT ` + repositoryColumns + `
	FROM repositories
	WHERE name = ?
	`

	repo, err := scanRepository(m.db.Conn().QueryRow(selectSQL, name))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("repository '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}
	return repo, nil
}

// GetRepositoryByID returns a repository by ID
func (m *Manager) GetRepositoryByID(id int) (*Repository, error) {
	selectSQL := `
	SELECT ` + repositoryColumns + `
	FROM repositories
	WHERE id = ?
	`

	repo, err := scanRepository(m.db.Conn().QueryRow(selectSQL, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("repository with ID %d not found", id)
		}
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}
	return repo, nil
}

// GetCurrentRepository returns the currently selected repository
func (m *Manager) GetCurrentRepository() (*Repository, error) {
	selectSQL := `
	SELECT ` + repositoryColumns + `
	FROM repositories
	WHERE is_current = TRUE
	LIMIT 1
	`

	repo, err := scanRepository(m.db.Conn().QueryRow(selectSQL))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no current repository configured")
		}
		return nil, fmt.Errorf("failed to get current repository: %w", err)
	}
	return repo, nil
}

// SetCurrentRepository sets a repository as the current one
func (m *Manager) SetCurrentRepository(name string) error {
	// First, unset all current repositories
	if err := m.unsetCurrentRepository(); err != nil {
		return err
	}

	// Set the specified repository as current
	updateSQL := `
	UPDATE repositories
	SET is_current = TRUE, updated_at = CURRENT_TIMESTAMP
	WHERE name = ?
	`

	result, err := m.db.Conn().Exec(updateSQL, name)
	if err != nil {
		return fmt.Errorf("failed to set current repository: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("repository '%s' not found", name)
	}

	return nil
}

// HasRepositories checks if any repositories are configured
func (m *Manager) HasRepositories() (bool, error) {
	countSQL := `SELECT COUNT(*) FROM repositories`
	var count int
	err := m.db.Conn().QueryRow(countSQL).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to count repositories: %w", err)
	}
	return count > 0, nil
}

// repositoryExists checks if a repository with the given name exists
func (m *Manager) repositoryExists(name string) (bool, error) {
	countSQL := `SELECT COUNT(*) FROM repositories WHERE name = ?`
	var count int
	err := m.db.Conn().QueryRow(countSQL, name).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check repository existence: %w", err)
	}
	return count > 0, nil
}

// isFirstRepository checks if this would be the first repository
func (m *Manager) isFirstRepository() (bool, error) {
	hasRepos, err := m.HasRepositories()
	if err != nil {
		return false, err
	}
	return !hasRepos, nil
}

// unsetCurrentRepository unsets the current repository flag for all repositories
func (m *Manager) unsetCurrentRepository() error {
	updateSQL := `UPDATE repositories SET is_current = FALSE`
	_, err := m.db.Conn().Exec(updateSQL)
	if err != nil {
		return fmt.Errorf("failed to unset current repository: %w", err)
	}
	return nil
}

// secretKeyPattern matches valid environment variable names — checked up
// front in SetSecret so a bad key fails clearly here rather than silently
// producing something os.Setenv/downstream tooling can't use.
var secretKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ListSecrets returns every KEY=VALUE pair attached to repositoryID.
func (m *Manager) ListSecrets(repositoryID int) (map[string]string, error) {
	rows, err := m.db.Conn().Query(`SELECT key, value FROM environment_secrets WHERE repository_id = ? ORDER BY key`, repositoryID)
	if err != nil {
		return nil, fmt.Errorf("failed to query secrets: %w", err)
	}
	defer rows.Close()

	vars := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("failed to scan secret: %w", err)
		}
		vars[key] = value
	}
	return vars, nil
}

// GetSecret returns a single key's value, and whether it was set at all.
func (m *Manager) GetSecret(repositoryID int, key string) (string, bool, error) {
	var value string
	err := m.db.Conn().QueryRow(`SELECT value FROM environment_secrets WHERE repository_id = ? AND key = ?`, repositoryID, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to get secret: %w", err)
	}
	return value, true, nil
}

// SetSecret adds or updates a single key on repositoryID.
func (m *Manager) SetSecret(repositoryID int, key, value string) error {
	if !secretKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid variable name %q: must match %s", key, secretKeyPattern.String())
	}
	upsertSQL := `
	INSERT INTO environment_secrets (repository_id, key, value)
	VALUES (?, ?, ?)
	ON CONFLICT(repository_id, key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`
	if _, err := m.db.Conn().Exec(upsertSQL, repositoryID, key, value); err != nil {
		return fmt.Errorf("failed to set secret: %w", err)
	}
	return nil
}

// UnsetSecret removes a single key from repositoryID. Idempotent — removing
// a key that isn't present is not an error.
func (m *Manager) UnsetSecret(repositoryID int, key string) error {
	if _, err := m.db.Conn().Exec(`DELETE FROM environment_secrets WHERE repository_id = ? AND key = ?`, repositoryID, key); err != nil {
		return fmt.Errorf("failed to unset secret: %w", err)
	}
	return nil
}
