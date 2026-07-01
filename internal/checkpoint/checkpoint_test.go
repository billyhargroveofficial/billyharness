package checkpoint

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func rawArgs(value map[string]any) json.RawMessage {
	body, _ := json.Marshal(value)
	return body
}
