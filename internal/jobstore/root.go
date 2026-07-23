package jobstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	rootMarkerFileName = ".jobstore.json"
	rootMarkerVersion  = 1
)

type rootMarker struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
}

func prepareStoreRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("job store root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve job store root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if filepath.Dir(absolute) == absolute {
		return "", &OwnershipError{Root: absolute, Err: errors.New("filesystem root cannot be a job store")}
	}

	created := false
	info, err := os.Lstat(absolute)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		parent := filepath.Dir(absolute)
		parentInfo, parentErr := os.Lstat(parent)
		if parentErr != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
			if parentErr == nil {
				parentErr = errors.New("parent is not a real directory")
			}
			return "", &OwnershipError{
				Root: absolute,
				Err:  fmt.Errorf("job store parent must already exist: %w", parentErr),
			}
		}
		if err := os.Mkdir(absolute, 0o700); err != nil {
			return "", fmt.Errorf("create job store root: %w", err)
		}
		created = true
		if err := syncDirectory(parent); err != nil {
			return "", fmt.Errorf("sync job store parent: %w", err)
		}
	case err != nil:
		return "", fmt.Errorf("inspect job store root: %w", err)
	case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
		return "", &OwnershipError{Root: absolute, Err: errors.New("root must be a real directory, not a link or file")}
	}

	evaluated, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve job store root links: %w", err)
	}
	absolute = filepath.Clean(evaluated)
	if filepath.Dir(absolute) == absolute {
		return "", &OwnershipError{Root: absolute, Err: errors.New("filesystem root cannot be a job store")}
	}

	entries, err := os.ReadDir(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect job store root contents: %w", err)
	}
	markerPath := filepath.Join(absolute, rootMarkerFileName)
	markerPresent := false
	for _, entry := range entries {
		if entry.Name() == rootMarkerFileName {
			markerPresent = true
		}
		if entry.Name() == ownershipFileName {
			lockInfo, statErr := os.Lstat(filepath.Join(absolute, ownershipFileName))
			if statErr != nil {
				return "", statErr
			}
			if lockInfo.Mode()&os.ModeSymlink != 0 || !lockInfo.Mode().IsRegular() {
				return "", fmt.Errorf("%w: refusing non-regular ownership file", ErrCorrupt)
			}
		}
	}

	if markerPresent {
		if err := validateRootMarker(markerPath); err != nil {
			return "", err
		}
	} else {
		if !created && len(entries) != 0 {
			return "", &OwnershipError{
				Root: absolute,
				Err:  errors.New("existing non-empty directory is not a marked job store"),
			}
		}
		if err := os.Chmod(absolute, 0o700); err != nil {
			return "", fmt.Errorf("secure new job store root: %w", err)
		}
		marker := rootMarker{SchemaVersion: rootMarkerVersion, Kind: "billyharness-job-store"}
		if err := writeRootMarker(markerPath, marker); err != nil {
			return "", err
		}
		if err := syncDirectory(absolute); err != nil {
			return "", fmt.Errorf("sync new job store marker: %w", err)
		}
	}
	if err := requirePrivateDirectory(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

func writeRootMarker(path string, marker rootMarker) error {
	body, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create job store marker: %w", err)
	}
	writeErr := writeAll(file, body)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write job store marker: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close job store marker: %w", closeErr)
	}
	return nil
}

func validateRootMarker(path string) error {
	file, err := openRegularRead(path)
	if err != nil {
		return fmt.Errorf("read job store marker: %w", err)
	}
	defer file.Close()
	body, err := ioReadAllBounded(file, 4096)
	if err != nil {
		return fmt.Errorf("read job store marker: %w", err)
	}
	var marker rootMarker
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return fmt.Errorf("%w: decode job store marker: %v", ErrCorrupt, err)
	}
	canonical, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(body, canonical) || marker.SchemaVersion != rootMarkerVersion || marker.Kind != "billyharness-job-store" {
		return fmt.Errorf("%w: invalid job store marker", ErrCorrupt)
	}
	return nil
}

func ioReadAllBounded(file *os.File, limit int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("%w: file size %d exceeds marker bound", ErrCorrupt, info.Size())
	}
	body := make([]byte, info.Size())
	read, err := file.Read(body)
	if err != nil && read != len(body) {
		return nil, err
	}
	if read != len(body) {
		return nil, fmt.Errorf("%w: short marker read", ErrCorrupt)
	}
	return body, nil
}
