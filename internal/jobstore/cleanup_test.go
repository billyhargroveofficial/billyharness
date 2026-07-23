package jobstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreCleansOnlyKnownAbandonedStaging(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jobs")
	store := openFileStoreForTest(t, root, Options{})
	spec := fileStoreTestSpec(t, "job-cleanup")
	if _, err := store.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	closeFileStoreForTest(t, store)

	rootTemp := filepath.Join(root, ".creating-abandoned")
	snapshotTemp := filepath.Join(root, spec.ID, ".snapshot.json.tmp-abandoned")
	artifactTemp := filepath.Join(root, spec.ID, artifactsDirectory, ".tmp-artifact-abandoned")
	artifactDir := filepath.Join(root, spec.ID, artifactsDirectory, "artifact-1")
	verifiedTemp := filepath.Join(artifactDir, ".verified-read-abandoned")
	for _, directory := range []string{rootTemp, artifactTemp, artifactDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{snapshotTemp, verifiedTemp} {
		if err := os.WriteFile(file, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	reopened := openFileStoreForTest(t, root, Options{})
	defer closeFileStoreForTest(t, reopened)
	for _, path := range []string{rootTemp, snapshotTemp, artifactTemp, verifiedTemp} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("abandoned staging %s still exists, err=%v", path, err)
		}
	}
	if _, err := reopened.Load(context.Background(), spec.ID); err != nil {
		t.Fatalf("canonical job damaged by cleanup: %v", err)
	}
}
