package jobstore

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestOwnershipExclusiveAndCloseIdempotent(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	first, err := acquireOwnership(root)
	if err != nil {
		t.Fatalf("first acquireOwnership: %v", err)
	}
	second, err := acquireOwnership(root)
	if second != nil {
		second.Close()
		t.Fatal("second acquireOwnership unexpectedly succeeded")
	}
	if !errors.Is(err, ErrOwnership) {
		t.Fatalf("second acquireOwnership error = %v, want ErrOwnership", err)
	}
	var typed *OwnershipError
	if !errors.As(err, &typed) {
		t.Fatalf("second acquireOwnership error type = %T, want *OwnershipError", err)
	}

	const closers = 8
	var wait sync.WaitGroup
	errs := make(chan error, closers)
	for range closers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- first.Close()
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("idempotent Close: %v", err)
		}
	}

	reopened, err := acquireOwnership(root)
	if err != nil {
		t.Fatalf("reacquire after Close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, root, 0o700)
	assertMode(t, filepath.Join(root, ownershipFileName), 0o600)
}

func TestOwnershipUsesCrossProcessAdvisoryLock(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	lock, err := acquireOwnership(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	command := exec.Command(os.Args[0], "-test.run=^TestOwnershipHelperProcess$")
	command.Env = append(os.Environ(),
		"JOBSTORE_OWNERSHIP_HELPER=try",
		"JOBSTORE_OWNERSHIP_ROOT="+root,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}
}

func TestOwnershipAdvisoryLockReleasesAfterProcessExit(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	command := exec.Command(os.Args[0], "-test.run=^TestOwnershipHelperProcess$")
	command.Env = append(os.Environ(),
		"JOBSTORE_OWNERSHIP_HELPER=acquire-and-exit",
		"JOBSTORE_OWNERSHIP_ROOT="+root,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}

	lock, err := acquireOwnership(root)
	if err != nil {
		t.Fatalf("acquire after helper exit: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnershipRejectsSymlinkLockFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation generally requires elevated Windows privileges")
	}
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ownershipFileName)); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireOwnership(root)
	if lock != nil {
		lock.Close()
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("acquireOwnership error = %v, want ErrCorrupt", err)
	}
}

func TestOwnershipHelperProcess(t *testing.T) {
	mode := os.Getenv("JOBSTORE_OWNERSHIP_HELPER")
	if mode == "" {
		return
	}
	root := os.Getenv("JOBSTORE_OWNERSHIP_ROOT")
	switch mode {
	case "try":
		lock, err := acquireOwnership(root)
		if lock != nil {
			lock.Close()
			t.Fatal("cross-process acquire unexpectedly succeeded")
		}
		if !errors.Is(err, ErrOwnership) {
			t.Fatalf("cross-process acquire error = %v, want ErrOwnership", err)
		}
	case "acquire-and-exit":
		lock, err := acquireOwnership(root)
		if err != nil {
			t.Fatal(err)
		}
		if lock == nil {
			t.Fatal("nil ownership lock")
		}
		// Deliberately do not Close: process exit must release the OS lock.
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func TestOwnershipFileContainsCurrentPID(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "jobs")
	lock, err := acquireOwnership(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	file, err := os.Open(filepath.Join(root, ownershipFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line == "" {
		t.Fatal("ownership file is empty")
	}
}

func TestOwnershipRejectsFilesystemRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FileStore is fail-closed on Windows")
	}
	root := string(filepath.Separator)
	before, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := acquireOwnership(root)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("filesystem root unexpectedly acquired")
	}
	if !errors.Is(err, ErrOwnership) {
		t.Fatalf("error = %v, want ErrOwnership", err)
	}
	after, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("filesystem root mode changed from %o to %o", before.Mode().Perm(), after.Mode().Perm())
	}
}

func TestOwnershipRejectsUnmarkedNonEmptyDirectoryWithoutMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-store")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireOwnership(root)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("unmarked non-empty directory unexpectedly acquired")
	}
	if !errors.Is(err, ErrOwnership) {
		t.Fatalf("error = %v, want ErrOwnership", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("root mode mutated to %o", info.Mode().Perm())
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "keep" {
		t.Fatalf("sentinel mutated: %q, %v", body, err)
	}
}

func TestOwnershipRejectsHardLinkedLockWithoutMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hard-link semantics differ and FileStore is fail-closed on Windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "jobs")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(target, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, filepath.Join(root, ownershipFileName)); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireOwnership(root)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("hard-linked ownership file unexpectedly acquired")
	}
	if err == nil {
		t.Fatal("expected ownership error")
	}
	body, readErr := os.ReadFile(target)
	if readErr != nil || string(body) != "do-not-touch" {
		t.Fatalf("outside hard-link target mutated: %q, %v", body, readErr)
	}
}
