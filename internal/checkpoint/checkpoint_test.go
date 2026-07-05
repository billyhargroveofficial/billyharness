package checkpoint

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCheckpointRestorePreservesDirtyPreRunContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("user dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker, tracked, err := Begin(DefaultOptions([]string{root}), "fs_write_file", rawArgs(map[string]any{"path": "file.txt"}))
	if err != nil || !tracked {
		t.Fatalf("begin tracked=%v err=%v", tracked, err)
	}
	if err := os.WriteFile(path, []byte("agent edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, changed, err := tracker.Complete("turn-001", "step-001", "call-001", "attempt-001")
	if err != nil || !changed {
		t.Fatalf("complete changed=%v err=%v", changed, err)
	}
	if record.Stats.Modified != 1 || record.Files[0].Change != ChangeModified {
		t.Fatalf("record = %#v", record)
	}
	result, err := Restore(record)
	if err != nil {
		t.Fatalf("restore: %v conflicts=%v", err, result.Conflicts)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "user dirty\n" {
		t.Fatalf("restored content = %q", got)
	}
}

func TestCheckpointRedoRestoresAfterStateAndDetectsConflicts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker, tracked, err := Begin(DefaultOptions([]string{root}), "fs_write_file", rawArgs(map[string]any{"path": "file.txt"}))
	if err != nil || !tracked {
		t.Fatalf("begin tracked=%v err=%v", tracked, err)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, changed, err := tracker.Complete("turn-001", "step-001", "call-001", "attempt-001")
	if err != nil || !changed {
		t.Fatalf("complete changed=%v err=%v", changed, err)
	}
	if _, err := Restore(record); err != nil {
		t.Fatalf("restore: %v", err)
	}
	result, err := Redo(record)
	if err != nil {
		t.Fatalf("redo: %v conflicts=%v", err, result.Conflicts)
	}
	if got := readCheckpointFile(t, path); got != "after\n" {
		t.Fatalf("redone content = %q", got)
	}
	if _, err := Restore(record); err != nil {
		t.Fatalf("restore second time: %v", err)
	}
	if err := os.WriteFile(path, []byte("user after undo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = Redo(record)
	if !errors.Is(err, ErrConflict) || len(result.Conflicts) == 0 {
		t.Fatalf("redo err=%v result=%#v", err, result)
	}
	if got := readCheckpointFile(t, path); got != "user after undo\n" {
		t.Fatalf("redo conflict mutated file: %q", got)
	}
}

func TestCheckpointPreviewWritesNothing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "new.txt")
	tracker, tracked, err := Begin(DefaultOptions([]string{root}), "fs_write_file", rawArgs(map[string]any{"path": "new.txt"}))
	if err != nil || !tracked {
		t.Fatalf("begin tracked=%v err=%v", tracked, err)
	}
	if err := os.WriteFile(path, []byte("agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, changed, err := tracker.Complete("turn-001", "step-001", "call-001", "attempt-001")
	if err != nil || !changed {
		t.Fatalf("complete changed=%v err=%v", changed, err)
	}
	preview, truncated := Preview(record, 4096)
	if truncated || !strings.Contains(preview, "+agent") {
		t.Fatalf("preview truncated=%v text=%q", truncated, preview)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "agent\n" {
		t.Fatalf("preview mutated file: %q", got)
	}
}

func TestCheckpointRestoreConflictPreventsPartialRestore(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	if err := os.WriteFile(a, []byte("a0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker, tracked, err := Begin(DefaultOptions([]string{root}), "shell_exec", rawArgs(map[string]any{"cwd": "."}))
	if err != nil || !tracked {
		t.Fatalf("begin tracked=%v err=%v", tracked, err)
	}
	if err := os.WriteFile(a, []byte("a1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, changed, err := tracker.Complete("turn-001", "step-001", "call-001", "attempt-001")
	if err != nil || !changed {
		t.Fatalf("complete changed=%v err=%v", changed, err)
	}
	if err := os.WriteFile(a, []byte("user-after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Restore(record)
	if !errors.Is(err, ErrConflict) || len(result.Conflicts) == 0 {
		t.Fatalf("restore err=%v result=%#v", err, result)
	}
	gotB, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotB) != "b1\n" {
		t.Fatalf("restore should not partially modify b.txt, got %q", gotB)
	}
}

func TestCheckpointRestoreOneRejectsSymlinkFileTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "file.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	file := FilePatch{
		Path: link,
		Before: &FileState{
			Exists:        true,
			Kind:          KindFile,
			Mode:          0o644,
			ContentBase64: base64.StdEncoding.EncodeToString([]byte("restore\n")),
		},
	}
	if err := restoreOne(file, false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink restore rejection, got %v", err)
	}
	if got := readCheckpointFile(t, outside); got != "outside\n" {
		t.Fatalf("outside target mutated: %q", got)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink should remain in place: %v", err)
	}
}

func TestCheckpointRestoreOneRejectsSymlinkDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	link := filepath.Join(root, "dir")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	file := FilePatch{
		Path: link,
		Before: &FileState{
			Exists: true,
			Kind:   KindDir,
			Mode:   0o755,
		},
	}
	if err := restoreOne(file, false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink directory restore rejection, got %v", err)
	}
}

func TestCheckpointLoadVerifiedRejectsTamperedMovedAndSymlinkArtifacts(t *testing.T) {
	root := t.TempDir()
	record := PatchRecord{
		SchemaVersion: SchemaVersion,
		ChangeID:      "change-test",
		Files: []FilePatch{{
			Path:   filepath.Join(root, "file.txt"),
			Change: ChangeModified,
			Kind:   KindFile,
			Before: checkpointFileState("before\n"),
			After:  checkpointFileState("after\n"),
		}},
	}
	artifact := filepath.Join(root, "patch.json")
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	wantSHA := hex.EncodeToString(sum[:])
	if _, err := LoadVerified(artifact, wantSHA); err != nil {
		t.Fatalf("verified load: %v", err)
	}
	if err := os.WriteFile(artifact, append(body, []byte("\n ")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVerified(artifact, wantSHA); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha mismatch, got %v", err)
	}
	moved := filepath.Join(root, "moved.json")
	if err := os.Rename(artifact, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVerified(artifact, wantSHA); err == nil {
		t.Fatal("expected moved artifact path to fail")
	}
	link := filepath.Join(root, "patch-link.json")
	if err := os.Symlink(moved, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := LoadVerified(link, ""); err == nil || !strings.Contains(err.Error(), "artifact symlink") {
		t.Fatalf("expected artifact symlink rejection, got %v", err)
	}
}

func TestCheckpointLoadAndRedoWithOptionsRemoveDeletedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "delete.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record := PatchRecord{
		SchemaVersion: SchemaVersion,
		ChangeID:      "change-delete",
		Files: []FilePatch{{
			Path:       path,
			Change:     ChangeDeleted,
			Kind:       KindFile,
			Before:     checkpointFileState("before\n"),
			After:      &FileState{Exists: false},
			Reversible: true,
		}},
	}
	artifact := filepath.Join(root, "patch.json")
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(artifact)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	result, err := RedoWithOptions(loaded, RestoreOptions{WorkspaceRoots: []string{root}})
	if err != nil {
		t.Fatalf("redo with options: %v result=%#v", err, result)
	}
	if len(result.RestoredFiles) != 1 || result.RestoredFiles[0] != path {
		t.Fatalf("restored files = %#v", result.RestoredFiles)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("redo should remove deleted file, stat err=%v", err)
	}
}

func TestCheckpointRestoreWithOptionsRequiresWorkspaceRootsAndRejectsOutOfRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record := PatchRecord{
		SchemaVersion: SchemaVersion,
		ChangeID:      "change-outside",
		Files: []FilePatch{{
			Path:   outside,
			Change: ChangeModified,
			Kind:   KindFile,
			Before: checkpointFileState("before\n"),
			After:  checkpointFileState("after\n"),
		}},
	}
	if _, err := RestoreWithOptions(record, RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "requires workspace roots") {
		t.Fatalf("expected missing roots error, got %v", err)
	}
	if _, err := RestoreWithOptions(record, RestoreOptions{WorkspaceRoots: []string{root}}); err == nil || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("expected out-of-root restore rejection, got %v", err)
	}
	if got := readCheckpointFile(t, outside); got != "after\n" {
		t.Fatalf("out-of-root restore mutated file: %q", got)
	}
}

func TestCheckpointRestoreWithOptionsRejectsSymlinkParentEscapingRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "file.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	record := PatchRecord{
		SchemaVersion: SchemaVersion,
		ChangeID:      "change-symlink-parent",
		Files: []FilePatch{{
			Path:   filepath.Join(link, "file.txt"),
			Change: ChangeModified,
			Kind:   KindFile,
			Before: checkpointFileState("before\n"),
			After:  checkpointFileState("after\n"),
		}},
	}
	if _, err := RestoreWithOptions(record, RestoreOptions{WorkspaceRoots: []string{root}}); err == nil || !strings.Contains(err.Error(), "symlink outside workspace roots") {
		t.Fatalf("expected symlink-parent restore rejection, got %v", err)
	}
	if got := readCheckpointFile(t, filepath.Join(outsideDir, "file.txt")); got != "after\n" {
		t.Fatalf("outside symlink target mutated: %q", got)
	}
}

func TestCheckpointShellChangedDetectsCreatedModifiedDeletedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "delete.txt"), []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker, tracked, err := Begin(DefaultOptions([]string{root}), "shell_exec", rawArgs(map[string]any{"cwd": "."}))
	if err != nil || !tracked {
		t.Fatalf("begin tracked=%v err=%v", tracked, err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("created\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, changed, err := tracker.Complete("turn-001", "step-001", "call-001", "attempt-001")
	if err != nil || !changed {
		t.Fatalf("complete changed=%v err=%v", changed, err)
	}
	if record.Stats.Added != 1 || record.Stats.Modified != 1 || record.Stats.Deleted != 1 {
		t.Fatalf("stats = %#v files=%#v", record.Stats, record.Files)
	}
}

func TestCheckpointShellCompleteReadsOnlyChangedFiles(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 2000; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("unchanged-%04d.txt", i)), []byte("same\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	modified := filepath.Join(root, "modified.txt")
	deleted := filepath.Join(root, "deleted.txt")
	if err := os.WriteFile(modified, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deleted, []byte("delete me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := DefaultOptions([]string{root})
	opts.MaxScanEntries = 3000
	tracker, tracked, err := Begin(opts, "shell_exec", rawArgs(map[string]any{"cwd": "."}))
	if err != nil || !tracked {
		t.Fatalf("begin tracked=%v err=%v", tracked, err)
	}

	if err := os.WriteFile(modified, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(modified, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("created\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fullAfter, err := snapshotTargets(opts, tracker.targets)
	if err != nil {
		t.Fatal(err)
	}
	expected := diffSnapshots(opts, tracker.before, fullAfter)

	originalReadFile := snapshotReadFile
	readCount := 0
	snapshotReadFile = func(path string) ([]byte, error) {
		readCount++
		return originalReadFile(path)
	}
	defer func() {
		snapshotReadFile = originalReadFile
	}()

	record, changed, err := tracker.Complete("turn-001", "step-001", "call-001", "attempt-001")
	if err != nil || !changed {
		t.Fatalf("complete changed=%v err=%v", changed, err)
	}
	if !reflect.DeepEqual(record.Files, expected.Files) || record.Stats != expected.Stats {
		t.Fatalf("fast record mismatch\nfast=%#v\nfull=%#v", record.Files, expected.Files)
	}
	if readCount > 2 {
		t.Fatalf("complete full reads = %d, want <= 2", readCount)
	}
}

func readCheckpointFile(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes)
}

func rawArgs(value map[string]any) json.RawMessage {
	body, _ := json.Marshal(value)
	return body
}

func checkpointFileState(content string) *FileState {
	sum := sha256.Sum256([]byte(content))
	return &FileState{
		Exists:        true,
		Kind:          KindFile,
		Mode:          0o644,
		Size:          int64(len(content)),
		SHA256:        hex.EncodeToString(sum[:]),
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
	}
}
