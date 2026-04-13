package kubeconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/cbridges1/hyve/internal/database"
)

// Kubeconfig represents stored kubeconfig data
type Kubeconfig struct {
	ID              int       `json:"id"`
	ClusterName     string    `json:"cluster_name"`
	RepositoryName  string    `json:"repository_name"`
	EncryptedConfig string    `json:"-"` // Not serialized for security
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	manager         *Manager  `json:"-"` // Reference to manager for decryption
}

// GetConfig returns the decrypted kubeconfig for this cluster
func (k *Kubeconfig) GetConfig() (string, error) {
	if k.manager == nil {
		return "", fmt.Errorf("kubeconfig manager not available for config decryption")
	}
	return k.manager.decryptConfig(k.EncryptedConfig)
}

// Manager handles kubeconfig storage using the unified database with encryption
type Manager struct {
	db             *database.DB
	dbPath         string
	repositoryName string
}

// LocalRepoName is the sentinel repository name used to store kubeconfigs for
// clusters that are not managed by Hyve (imported via "hyve kubeconfig add-external").
const LocalRepoName = "_local"

// NewLocalManager creates a kubeconfig manager for locally-imported external
// clusters. No Git repository needs to be configured to use this manager.
func NewLocalManager() (*Manager, error) {
	return NewManager(LocalRepoName)
}

// NewManager creates a new kubeconfig manager for a specific repository
func NewManager(repositoryName string) (*Manager, error) {
	db, err := database.GetDB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	return &Manager{
		db:             db,
		dbPath:         db.Path(),
		repositoryName: repositoryName,
	}, nil
}

// NewManagerWithDB creates a new kubeconfig manager with a specific database (for testing)
func NewManagerWithDB(db *database.DB, repositoryName string) *Manager {
	return &Manager{
		db:             db,
		dbPath:         db.Path(),
		repositoryName: repositoryName,
	}
}

// Close is a no-op for kubeconfig manager since the database is managed centrally
func (m *Manager) Close() error {
	// Database is managed by the database package, don't close it here
	return nil
}

// StoreKubeconfig stores or updates a kubeconfig for a cluster
func (m *Manager) StoreKubeconfig(clusterName, kubeconfig string) (*Kubeconfig, error) {
	if clusterName == "" || kubeconfig == "" {
		return nil, fmt.Errorf("cluster name and kubeconfig are required")
	}

	// Encrypt the kubeconfig
	encryptedConfig, err := m.encryptConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt kubeconfig: %w", err)
	}

	// Check if kubeconfig already exists
	existing, _ := m.GetKubeconfig(clusterName)
	if existing != nil {
		// Update existing kubeconfig
		updateSQL := `
		UPDATE kubeconfigs
		SET encrypted_config = ?, updated_at = CURRENT_TIMESTAMP
		WHERE cluster_name = ? AND repository_name = ?
		`
		_, err := m.db.Conn().Exec(updateSQL, encryptedConfig, clusterName, m.repositoryName)
		if err != nil {
			return nil, fmt.Errorf("failed to update kubeconfig: %w", err)
		}
	} else {
		// Insert new kubeconfig
		insertSQL := `
		INSERT INTO kubeconfigs (cluster_name, repository_name, encrypted_config)
		VALUES (?, ?, ?)
		`
		_, err := m.db.Conn().Exec(insertSQL, clusterName, m.repositoryName, encryptedConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to insert kubeconfig: %w", err)
		}
	}

	return m.GetKubeconfig(clusterName)
}

// GetKubeconfig retrieves a kubeconfig for a specific cluster
func (m *Manager) GetKubeconfig(clusterName string) (*Kubeconfig, error) {
	selectSQL := `
	SELECT id, cluster_name, repository_name, encrypted_config, created_at, updated_at
	FROM kubeconfigs
	WHERE cluster_name = ? AND repository_name = ?
	`

	kc := &Kubeconfig{}
	var createdAt, updatedAt string

	err := m.db.Conn().QueryRow(selectSQL, clusterName, m.repositoryName).Scan(&kc.ID, &kc.ClusterName,
		&kc.RepositoryName, &kc.EncryptedConfig, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No kubeconfig stored
		}
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Parse timestamps
	if kc.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt); err != nil {
		kc.CreatedAt = time.Now()
	}
	if kc.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt); err != nil {
		kc.UpdatedAt = time.Now()
	}

	// Set manager reference for config decryption
	kc.manager = m
	return kc, nil
}

// ListKubeconfigs lists all kubeconfigs for the current repository
func (m *Manager) ListKubeconfigs() ([]*Kubeconfig, error) {
	selectSQL := `
	SELECT id, cluster_name, repository_name, encrypted_config, created_at, updated_at
	FROM kubeconfigs
	WHERE repository_name = ?
	ORDER BY cluster_name
	`

	rows, err := m.db.Conn().Query(selectSQL, m.repositoryName)
	if err != nil {
		return nil, fmt.Errorf("failed to list kubeconfigs: %w", err)
	}
	defer rows.Close()

	var kubeconfigs []*Kubeconfig
	for rows.Next() {
		kc := &Kubeconfig{}
		var createdAt, updatedAt string

		err := rows.Scan(&kc.ID, &kc.ClusterName, &kc.RepositoryName,
			&kc.EncryptedConfig, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan kubeconfig: %w", err)
		}

		// Parse timestamps
		if kc.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAt); err != nil {
			kc.CreatedAt = time.Now()
		}
		if kc.UpdatedAt, err = time.Parse("2006-01-02 15:04:05", updatedAt); err != nil {
			kc.UpdatedAt = time.Now()
		}

		// Set manager reference for config decryption
		kc.manager = m
		kubeconfigs = append(kubeconfigs, kc)
	}

	return kubeconfigs, nil
}

// DeleteKubeconfig removes a kubeconfig for a specific cluster
func (m *Manager) DeleteKubeconfig(clusterName string) error {
	deleteSQL := `DELETE FROM kubeconfigs WHERE cluster_name = ? AND repository_name = ?`
	_, err := m.db.Conn().Exec(deleteSQL, clusterName, m.repositoryName)
	if err != nil {
		return fmt.Errorf("failed to delete kubeconfig: %w", err)
	}
	return nil
}

// CleanupOrphanedKubeconfigs removes kubeconfigs that don't have corresponding cluster definitions
func (m *Manager) CleanupOrphanedKubeconfigs(activeClusterNames []string) error {
	// First, find orphaned kubeconfigs to log what will be removed
	var orphanedNames []string

	if len(activeClusterNames) == 0 {
		// If no active clusters, all kubeconfigs are orphaned
		rows, err := m.db.Conn().Query(`SELECT cluster_name FROM kubeconfigs WHERE repository_name = ?`, m.repositoryName)
		if err != nil {
			return fmt.Errorf("failed to query kubeconfigs: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				orphanedNames = append(orphanedNames, name)
			}
		}

		if len(orphanedNames) > 0 {
			deleteSQL := `DELETE FROM kubeconfigs WHERE repository_name = ?`
			_, err := m.db.Conn().Exec(deleteSQL, m.repositoryName)
			if err != nil {
				return fmt.Errorf("failed to cleanup all kubeconfigs: %w", err)
			}
			for _, name := range orphanedNames {
				log.Printf("🗑️  Removed orphaned kubeconfig: %s", name)
			}
		}
		return nil
	}

	// Build a map for quick lookup of active cluster names
	activeSet := make(map[string]bool)
	for _, name := range activeClusterNames {
		activeSet[name] = true
	}

	// Query all kubeconfigs for this repository to find orphans
	rows, err := m.db.Conn().Query(`SELECT cluster_name FROM kubeconfigs WHERE repository_name = ?`, m.repositoryName)
	if err != nil {
		return fmt.Errorf("failed to query kubeconfigs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			if !activeSet[name] {
				orphanedNames = append(orphanedNames, name)
			}
		}
	}

	// Delete orphaned kubeconfigs
	for _, orphanName := range orphanedNames {
		deleteSQL := `DELETE FROM kubeconfigs WHERE repository_name = ? AND cluster_name = ?`
		_, err := m.db.Conn().Exec(deleteSQL, m.repositoryName, orphanName)
		if err != nil {
			log.Printf("⚠️  Failed to remove orphaned kubeconfig %s: %v", orphanName, err)
			continue
		}
		log.Printf("🗑️  Removed orphaned kubeconfig: %s", orphanName)
	}

	return nil
}

// getEncryptionKey generates a deterministic encryption key based on repository name
func (m *Manager) getEncryptionKey() []byte {
	// Use a combination of fixed prefix and repository name for key derivation
	// Note: This is portable across machines and database locations
	keyMaterial := fmt.Sprintf("hyve-kubeconfig-v1:%s", m.repositoryName)
	hash := sha256.Sum256([]byte(keyMaterial))
	return hash[:]
}

// getEncryptionKeyWithHostname generates the old encryption key that included hostname
// This is used for migrating data encrypted with the old key format
func (m *Manager) getEncryptionKeyWithHostname(hostname string) []byte {
	keyMaterial := fmt.Sprintf("%s:%s:%s", m.dbPath, m.repositoryName, hostname)
	hash := sha256.Sum256([]byte(keyMaterial))
	return hash[:]
}

// encryptConfig encrypts a kubeconfig using AES-GCM
func (m *Manager) encryptConfig(kubeconfig string) (string, error) {
	if kubeconfig == "" {
		return "", nil
	}

	key := m.getEncryptionKey()

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(kubeconfig), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptConfig decrypts a kubeconfig using AES-GCM
func (m *Manager) decryptConfig(encryptedConfig string) (string, error) {
	if encryptedConfig == "" {
		return "", nil
	}

	key := m.getEncryptionKey()

	ciphertext, err := base64.StdEncoding.DecodeString(encryptedConfig)
	if err != nil {
		return "", fmt.Errorf("failed to decode encrypted kubeconfig: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt kubeconfig: %w", err)
	}

	return string(plaintext), nil
}

// decryptConfigWithHostname decrypts a kubeconfig using the old hostname-based key
func (m *Manager) decryptConfigWithHostname(encryptedConfig string, hostname string) (string, error) {
	if encryptedConfig == "" {
		return "", nil
	}

	key := m.getEncryptionKeyWithHostname(hostname)

	ciphertext, err := base64.StdEncoding.DecodeString(encryptedConfig)
	if err != nil {
		return "", fmt.Errorf("failed to decode encrypted kubeconfig: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt kubeconfig with hostname: %w", err)
	}

	return string(plaintext), nil
}

// MigrateEncryption migrates all kubeconfigs from hostname-based encryption to portable encryption
func (m *Manager) MigrateEncryption(oldHostname string) error {
	// Get all kubeconfigs for this repository
	rows, err := m.db.Conn().Query(`
		SELECT id, cluster_name, encrypted_config
		FROM kubeconfigs
		WHERE repository_name = ?
	`, m.repositoryName)
	if err != nil {
		return fmt.Errorf("failed to query kubeconfigs: %w", err)
	}
	defer rows.Close()

	type kubeconfigRecord struct {
		id              int
		clusterName     string
		encryptedConfig string
	}

	var kubeconfigs []kubeconfigRecord
	for rows.Next() {
		var kc kubeconfigRecord
		if err := rows.Scan(&kc.id, &kc.clusterName, &kc.encryptedConfig); err != nil {
			return fmt.Errorf("failed to scan kubeconfig: %w", err)
		}
		kubeconfigs = append(kubeconfigs, kc)
	}

	if len(kubeconfigs) == 0 {
		return fmt.Errorf("no kubeconfigs found for repository %s", m.repositoryName)
	}

	// Migrate each kubeconfig
	for _, kc := range kubeconfigs {
		// Decrypt with old hostname-based key
		plaintext, err := m.decryptConfigWithHostname(kc.encryptedConfig, oldHostname)
		if err != nil {
			return fmt.Errorf("failed to decrypt kubeconfig for cluster %s: %w", kc.clusterName, err)
		}

		// Re-encrypt with new portable key
		newEncrypted, err := m.encryptConfig(plaintext)
		if err != nil {
			return fmt.Errorf("failed to re-encrypt kubeconfig for cluster %s: %w", kc.clusterName, err)
		}

		// Update the database
		_, err = m.db.Conn().Exec(`
			UPDATE kubeconfigs
			SET encrypted_config = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, newEncrypted, kc.id)
		if err != nil {
			return fmt.Errorf("failed to update kubeconfig for cluster %s: %w", kc.clusterName, err)
		}
	}

	return nil
}
