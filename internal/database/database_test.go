package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDBWithDir(t *testing.T) {
	db, err := GetDBWithDir(t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	require.NotNil(t, db)
}

func TestConn(t *testing.T) {
	db, err := GetDBWithDir(t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	conn := db.Conn()
	require.NotNil(t, conn)

	// Verify the connection is usable
	err = conn.Ping()
	assert.NoError(t, err)
}

func TestPath(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := GetDBWithDir(tmpDir)
	require.NoError(t, err)
	defer db.Close()

	assert.Equal(t, filepath.Join(tmpDir, DatabaseFileName), db.Path())
}

func TestConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := GetDBWithDir(tmpDir)
	require.NoError(t, err)
	defer db.Close()

	assert.Equal(t, tmpDir, db.ConfigDir())
}

func TestClose(t *testing.T) {
	db, err := GetDBWithDir(t.TempDir())
	require.NoError(t, err)

	err = db.Close()
	assert.NoError(t, err)
}

func TestDatabaseFileName(t *testing.T) {
	assert.Equal(t, "hyve.db", DatabaseFileName)
}

func TestTablesCreated(t *testing.T) {
	db, err := GetDBWithDir(t.TempDir())
	require.NoError(t, err)
	defer db.Close()

	tables := []string{"credentials", "secrets", "repositories", "kubeconfigs"}
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			var name string
			err := db.Conn().QueryRow(
				"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
			).Scan(&name)
			require.NoError(t, err)
			assert.Equal(t, table, name)
		})
	}
}

func TestResetSingleton(t *testing.T) {
	t.Cleanup(ResetSingleton)

	// Prime the singleton
	db1, err := GetDB()
	require.NoError(t, err)
	require.NotNil(t, db1)

	// Reset and get a new instance
	ResetSingleton()
	db2, err := GetDB()
	require.NoError(t, err)
	require.NotNil(t, db2)

	// The two instances are different objects
	assert.NotSame(t, db1, db2)

	t.Cleanup(func() { db2.Close() })
}

func TestMultipleInstancesIndependent(t *testing.T) {
	db1, err := GetDBWithDir(t.TempDir())
	require.NoError(t, err)
	defer db1.Close()

	db2, err := GetDBWithDir(t.TempDir())
	require.NoError(t, err)
	defer db2.Close()

	// Each instance has its own path
	assert.NotEqual(t, db1.Path(), db2.Path())
}
