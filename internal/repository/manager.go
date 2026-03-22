package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/cbridges1/hyve/internal/database"
)

// Repository represents a Git repository configuration
type Repository struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	RepoURL   string    `json:"repo_url"`
	LocalPath string    `json:"local_path"`
	Username  string    `json:"username"`
	Token     string    `json:"-"` // Not serialized for security (legacy)
	IsCurrent bool      `json:"is_current"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// AddRepository adds a new repository configuration
func (m *Manager) AddRepository(name, repoURL, localPath, username string) (*Repository, error) {
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
	INSERT INTO repositories (name, repo_url, local_path, username, is_current)
	VALUES (?, ?, ?, ?, ?)
	`

	result, err := m.db.Conn().Exec(insertSQL, name, repoURL, localPath, username, isFirst)
	if err != nil {
		return nil, fmt.Errorf("failed to insert repository: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert ID: %w", err)
	}

	return m.GetRepositoryByID(int(id))
}

// UpdateRepository updates an existing repository configuration
func (m *Manager) UpdateRepository(name, repoURL, localPath, username string) (*Repository, error) {
	updateSQL := `
	UPDATE repositories
	SET repo_url = ?, local_path = ?, username = ?, updated_at = CURRENT_TIMESTAMP
	WHERE name = ?
	`

	result, err := m.db.Conn().Exec(updateSQL, repoURL, localPath, username, name)
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
	SELECT id, name, repo_url, local_path, username, is_current, created_at, updated_at
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
		repo := &Repository{}
		var createdAt, updatedAt string

		err := rows.Scan(&repo.ID, &repo.Name, &repo.RepoURL, &repo.LocalPath,
			&repo.Username, &repo.IsCurrent, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan repository: %w", err)
		}

		// Parse timestamps
		if repo.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt); err != nil {
			repo.CreatedAt = time.Now()
		}
		if repo.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt); err != nil {
			repo.UpdatedAt = time.Now()
		}

		repositories = append(repositories, repo)
	}

	return repositories, nil
}

// GetRepositoryByName returns a repository by name
func (m *Manager) GetRepositoryByName(name string) (*Repository, error) {
	selectSQL := `
	SELECT id, name, repo_url, local_path, username, is_current, created_at, updated_at
	FROM repositories
	WHERE name = ?
	`

	repo := &Repository{}
	var createdAt, updatedAt string

	err := m.db.Conn().QueryRow(selectSQL, name).Scan(&repo.ID, &repo.Name, &repo.RepoURL,
		&repo.LocalPath, &repo.Username, &repo.IsCurrent, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("repository '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	// Parse timestamps
	if repo.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt); err != nil {
		repo.CreatedAt = time.Now()
	}
	if repo.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt); err != nil {
		repo.UpdatedAt = time.Now()
	}

	return repo, nil
}

// GetRepositoryByID returns a repository by ID
func (m *Manager) GetRepositoryByID(id int) (*Repository, error) {
	selectSQL := `
	SELECT id, name, repo_url, local_path, username, is_current, created_at, updated_at
	FROM repositories
	WHERE id = ?
	`

	repo := &Repository{}
	var createdAt, updatedAt string

	err := m.db.Conn().QueryRow(selectSQL, id).Scan(&repo.ID, &repo.Name, &repo.RepoURL,
		&repo.LocalPath, &repo.Username, &repo.IsCurrent, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("repository with ID %d not found", id)
		}
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	// Parse timestamps
	if repo.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt); err != nil {
		repo.CreatedAt = time.Now()
	}
	if repo.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt); err != nil {
		repo.UpdatedAt = time.Now()
	}

	return repo, nil
}

// GetCurrentRepository returns the currently selected repository
func (m *Manager) GetCurrentRepository() (*Repository, error) {
	selectSQL := `
	SELECT id, name, repo_url, local_path, username, is_current, created_at, updated_at
	FROM repositories
	WHERE is_current = TRUE
	LIMIT 1
	`

	repo := &Repository{}
	var createdAt, updatedAt string

	err := m.db.Conn().QueryRow(selectSQL).Scan(&repo.ID, &repo.Name, &repo.RepoURL,
		&repo.LocalPath, &repo.Username, &repo.IsCurrent, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no current repository configured")
		}
		return nil, fmt.Errorf("failed to get current repository: %w", err)
	}

	// Parse timestamps
	if repo.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt); err != nil {
		repo.CreatedAt = time.Now()
	}
	if repo.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt); err != nil {
		repo.UpdatedAt = time.Now()
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
