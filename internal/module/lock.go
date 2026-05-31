package module

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const lockFileName = "hyve.lock"

func LoadLockFile(repoDir string) (*LockFile, error) {
	path := filepath.Join(repoDir, lockFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &LockFile{Version: 1, Modules: map[string]*LockedModule{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var lf LockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, err
	}
	if lf.Modules == nil {
		lf.Modules = map[string]*LockedModule{}
	}
	return &lf, nil
}

func SaveLockFile(repoDir string, lf *LockFile) error {
	data, err := yaml.Marshal(lf)
	if err != nil {
		return err
	}
	tmp := filepath.Join(repoDir, lockFileName+".tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(repoDir, lockFileName))
}

func LockKey(source, version string) string {
	return source + "@" + version
}

func (lf *LockFile) GetLocked(source, version string) *LockedModule {
	if lf.Modules == nil {
		return nil
	}
	return lf.Modules[LockKey(source, version)]
}

func (lf *LockFile) SetLocked(source, version string, m *LockedModule) {
	if lf.Modules == nil {
		lf.Modules = map[string]*LockedModule{}
	}
	lf.Modules[LockKey(source, version)] = m
}

func (lf *LockFile) RemoveLocked(source, version string) {
	delete(lf.Modules, LockKey(source, version))
}
