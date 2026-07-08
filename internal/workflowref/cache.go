package workflowref

import (
	"os"
	"path/filepath"
)

// CacheDir returns ~/.hyve/workflow-cache, creating it if necessary. Unlike
// module's directory-based cache, a resolved workflow is always exactly one
// file, so entries here are individual files, not directories.
func CacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".hyve", "workflow-cache")
	return dir, os.MkdirAll(dir, 0755)
}

// CachePath returns the cache file path for a given content sha256.
func CachePath(sha256 string) (string, error) {
	base, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, sha256), nil
}

// IsCached reports whether a file with the given content sha256 is cached.
func IsCached(sha256 string) bool {
	if sha256 == "" {
		return false
	}
	p, err := CachePath(sha256)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// StoreInCache writes data to the cache under its content sha256, atomically
// (write to a temp file, then rename).
func StoreInCache(sha256 string, data []byte) error {
	dst, err := CachePath(sha256)
	if err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, "workflow-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dst)
}

// ReadCached reads a cached file's bytes by content sha256.
func ReadCached(sha256 string) ([]byte, error) {
	p, err := CachePath(sha256)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}
