package jobstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// cleanupAbandonedStaging runs only after exclusive root ownership is held.
// It removes the exact temporary prefixes created by this package and refuses
// links or unexpected file types rather than traversing ambiguous paths.
func cleanupAbandonedStaging(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	rootChanged := false
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if strings.HasPrefix(entry.Name(), ".creating-") {
			if err := removeStagingDirectory(path); err != nil {
				return err
			}
			rootChanged = true
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() || ValidatePortableID(entry.Name()) != nil {
			continue
		}
		if err := cleanupJobStaging(path); err != nil {
			return err
		}
	}
	if rootChanged {
		return syncDirectory(root)
	}
	return nil
}

func cleanupJobStaging(jobDir string) error {
	info, err := os.Lstat(jobDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: refusing staging cleanup through %s", ErrCorrupt, jobDir)
	}
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		return err
	}
	changed := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "."+snapshotFileName+".tmp-") {
			if err := removeStagingFile(filepath.Join(jobDir, entry.Name())); err != nil {
				return err
			}
			changed = true
		}
	}
	artifactsDir := filepath.Join(jobDir, artifactsDirectory)
	if err := cleanupArtifactStaging(artifactsDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if changed {
		return syncDirectory(jobDir)
	}
	return nil
}

func cleanupArtifactStaging(artifactsDir string) error {
	info, err := os.Lstat(artifactsDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: refusing staging cleanup through %s", ErrCorrupt, artifactsDir)
	}
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		return err
	}
	changed := false
	for _, entry := range entries {
		path := filepath.Join(artifactsDir, entry.Name())
		if strings.HasPrefix(entry.Name(), ".tmp-artifact-") {
			if err := removeStagingDirectory(path); err != nil {
				return err
			}
			changed = true
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() {
			continue
		}
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		artifactChanged := false
		for _, child := range children {
			if !strings.HasPrefix(child.Name(), ".verified-read-") {
				continue
			}
			if err := removeStagingFile(filepath.Join(path, child.Name())); err != nil {
				return err
			}
			artifactChanged = true
			changed = true
		}
		if artifactChanged {
			if err := syncDirectory(path); err != nil {
				return err
			}
		}
	}
	if changed {
		return syncDirectory(artifactsDir)
	}
	return nil
}

func removeStagingDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: staging directory has unexpected type: %s", ErrCorrupt, path)
	}
	return os.RemoveAll(path)
}

func removeStagingFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: staging file has unexpected type: %s", ErrCorrupt, path)
	}
	return os.Remove(path)
}
