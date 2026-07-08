package module

import (
	"io"
	"os"
	"path/filepath"
)

func CacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".hyve", "module-cache")
	return dir, os.MkdirAll(dir, 0755)
}

func CachePath(sha256 string) (string, error) {
	base, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, sha256), nil
}

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

func StoreInCache(sha256, srcDir string) error {
	dst, err := CachePath(sha256)
	if err != nil {
		return err
	}
	// os.Rename fails across devices; copy instead
	if err := os.Rename(srcDir, dst); err != nil {
		if err2 := copyDir(srcDir, dst); err2 != nil {
			return err2
		}
		os.RemoveAll(srcDir)
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
