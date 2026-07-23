package jobstore

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

const ownershipFileName = ".owner.lock"

var ownershipRegistry = struct {
	sync.Mutex
	roots map[string]struct{}
}{
	roots: make(map[string]struct{}),
}

type ownershipLock struct {
	root string
	file *os.File

	closeOnce sync.Once
	closeErr  error
}

func acquireOwnership(root string) (*ownershipLock, error) {
	absolute, err := prepareStoreRoot(root)
	if err != nil {
		return nil, err
	}

	ownershipRegistry.Lock()
	if _, exists := ownershipRegistry.roots[absolute]; exists {
		ownershipRegistry.Unlock()
		return nil, &OwnershipError{
			Root: absolute,
			Err:  errors.New("already open in this process"),
		}
	}
	ownershipRegistry.roots[absolute] = struct{}{}
	ownershipRegistry.Unlock()
	releaseRegistry := true
	defer func() {
		if releaseRegistry {
			releaseOwnedRoot(absolute)
		}
	}()

	lockPath, err := containedJoin(absolute, ownershipFileName)
	if err != nil {
		return nil, err
	}
	file, err := openOwnershipFile(lockPath)
	if err != nil {
		return nil, &OwnershipError{Root: absolute, Err: err}
	}
	locked := false
	fail := func(err error) (*ownershipLock, error) {
		if locked {
			_ = unlockOwnershipFile(file)
			locked = false
		}
		_ = file.Close()
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		return fail(fmt.Errorf("stat ownership file: %w", err))
	}
	current, err := os.Lstat(lockPath)
	if err != nil {
		return fail(fmt.Errorf("restat ownership file: %w", err))
	}
	if !opened.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		return fail(fmt.Errorf("%w: ownership file changed while opening", ErrCorrupt))
	}
	if err := lockOwnershipFile(file); err != nil {
		return fail(&OwnershipError{Root: absolute, Err: err})
	}
	locked = true

	if err := file.Truncate(0); err != nil {
		return fail(fmt.Errorf("truncate ownership file: %w", err))
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fail(fmt.Errorf("rewind ownership file: %w", err))
	}
	if _, err := fmt.Fprintf(file, "pid=%d\n", os.Getpid()); err != nil {
		return fail(fmt.Errorf("write ownership file: %w", err))
	}
	if err := file.Sync(); err != nil {
		return fail(fmt.Errorf("sync ownership file: %w", err))
	}
	if err := syncDirectory(absolute); err != nil {
		return fail(fmt.Errorf("sync job store root: %w", err))
	}

	lock := &ownershipLock{root: absolute, file: file}
	locked = false
	releaseRegistry = false
	return lock, nil
}

func (lock *ownershipLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.closeOnce.Do(func() {
		var errs []error
		if lock.file != nil {
			if err := unlockOwnershipFile(lock.file); err != nil {
				errs = append(errs, fmt.Errorf("unlock job store root: %w", err))
			}
			if err := lock.file.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close ownership file: %w", err))
			}
			lock.file = nil
		}
		releaseOwnedRoot(lock.root)
		lock.closeErr = errors.Join(errs...)
	})
	return lock.closeErr
}

func releaseOwnedRoot(root string) {
	ownershipRegistry.Lock()
	delete(ownershipRegistry.roots, root)
	ownershipRegistry.Unlock()
}
