package jobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestArtifactStorePutOpenVerified(t *testing.T) {
	t.Parallel()

	root, jobID := newArtifactTestRoot(t)
	store := newArtifactStore(root, 1<<20)
	body := []byte("durable artifact\n")

	ref, err := store.Put(context.Background(), jobID, "report-1", "text/plain", "attempt-1", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	sum := sha256.Sum256(body)
	if ref.ID != "report-1" ||
		ref.URI != "job://job-1/artifacts/report-1" ||
		ref.SHA256 != hex.EncodeToString(sum[:]) ||
		ref.MediaType != "text/plain" ||
		ref.CreatedByAttemptID != "attempt-1" {
		t.Fatalf("unexpected artifact ref: %#v", ref)
	}
	if strings.Contains(ref.URI, root) {
		t.Fatalf("logical artifact URI leaks storage root: %q", ref.URI)
	}

	reader, openedRef, err := store.Open(context.Background(), jobID, "report-1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	openedBody, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close: %v / %v", readErr, closeErr)
	}
	if !bytes.Equal(openedBody, body) {
		t.Fatalf("opened body = %q, want %q", openedBody, body)
	}
	if openedRef != ref {
		t.Fatalf("opened ref = %#v, want %#v", openedRef, ref)
	}

	artifactDir := filepath.Join(root, jobID, "artifacts", "report-1")
	assertMode(t, artifactDir, 0o700)
	assertMode(t, filepath.Join(artifactDir, artifactDataName), 0o600)
	assertMode(t, filepath.Join(artifactDir, artifactMetadataName), 0o600)

	metadataBody, err := os.ReadFile(filepath.Join(artifactDir, artifactMetadataName))
	if err != nil {
		t.Fatal(err)
	}
	var metadata artifactMetadata
	if err := json.Unmarshal(metadataBody, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Version != artifactMetadataVersion || metadata.Bytes != int64(len(body)) {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

func TestArtifactStoreRefusesOverwriteAndPreservesOriginal(t *testing.T) {
	t.Parallel()

	root, jobID := newArtifactTestRoot(t)
	store := newArtifactStore(root, 1024)
	original := []byte("original")
	if _, err := store.Put(context.Background(), jobID, "answer", "text/plain", "", bytes.NewReader(original)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), jobID, "answer", "text/plain", "", strings.NewReader("replacement")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second Put error = %v, want ErrAlreadyExists", err)
	}
	reader, _, err := store.Open(context.Background(), jobID, "answer")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("artifact was overwritten: %q", got)
	}
}

func TestArtifactStoreConcurrentPublicationHasOneWinner(t *testing.T) {
	t.Parallel()

	root, jobID := newArtifactTestRoot(t)
	store := newArtifactStore(root, 1024)
	const writers = 8
	results := make(chan error, writers)
	var wait sync.WaitGroup
	for index := range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.Put(
				context.Background(),
				jobID,
				"shared",
				"text/plain",
				"",
				strings.NewReader(string(rune('a'+index))),
			)
			switch {
			case err == nil:
				results <- nil
			case errors.Is(err, ErrAlreadyExists):
				results <- ErrAlreadyExists
			default:
				results <- err
			}
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	alreadyExists := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyExists):
			alreadyExists++
		default:
			t.Fatalf("unexpected Put error: %v", err)
		}
	}
	if successes != 1 || alreadyExists != writers-1 {
		t.Fatalf("successes=%d already_exists=%d", successes, alreadyExists)
	}
}

func TestArtifactStoreStreamingLimitCleansTemporaryData(t *testing.T) {
	t.Parallel()

	root, jobID := newArtifactTestRoot(t)
	store := newArtifactStore(root, 5)
	source := &countingReader{reader: strings.NewReader(strings.Repeat("x", 100))}
	if _, err := store.Put(context.Background(), jobID, "too-big", "", "", source); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put error = %v, want ErrTooLarge", err)
	}
	if source.bytesRead > 6 {
		t.Fatalf("bounded writer consumed %d source bytes, want at most max+1", source.bytesRead)
	}
	artifactsDir := filepath.Join(root, jobID, "artifacts")
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized Put left entries: %v", entries)
	}
}

func TestArtifactStoreDetectsDataTampering(t *testing.T) {
	t.Parallel()

	root, jobID := newArtifactTestRoot(t)
	store := newArtifactStore(root, 1024)
	if _, err := store.Put(context.Background(), jobID, "evidence", "", "", strings.NewReader("trusted")); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(root, jobID, "artifacts", "evidence", artifactDataName)
	if err := os.WriteFile(dataPath, []byte("altered"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, _, err := store.Open(context.Background(), jobID, "evidence")
	if reader != nil {
		reader.Close()
	}
	if !errors.Is(err, ErrTampered) {
		t.Fatalf("Open error = %v, want ErrTampered", err)
	}
}

func TestArtifactStoreDetectsMetadataTampering(t *testing.T) {
	t.Parallel()

	root, jobID := newArtifactTestRoot(t)
	store := newArtifactStore(root, 1024)
	if _, err := store.Put(context.Background(), jobID, "evidence", "", "", strings.NewReader("trusted")); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(root, jobID, "artifacts", "evidence", artifactMetadataName)
	body, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.Replace(body, []byte(`"version":1`), []byte(`"version":2`), 1)
	if err := os.WriteFile(metadataPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	reader, _, err := store.Open(context.Background(), jobID, "evidence")
	if reader != nil {
		reader.Close()
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open error = %v, want ErrCorrupt", err)
	}
}

func TestArtifactStoreRejectsInvalidIDsAndMissingJob(t *testing.T) {
	t.Parallel()

	root, jobID := newArtifactTestRoot(t)
	store := newArtifactStore(root, 1024)
	for _, id := range []string{"", ".", "../escape", "a/b", `a\b`, "/absolute", " leading"} {
		if _, err := store.Put(context.Background(), jobID, id, "", "", strings.NewReader("x")); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Put artifact ID %q error = %v, want ErrInvalidID", id, err)
		}
	}
	if _, err := store.Put(context.Background(), "missing", "artifact", "", "", strings.NewReader("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Put missing job error = %v, want ErrNotFound", err)
	}
	if _, _, err := store.Open(context.Background(), jobID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open missing artifact error = %v, want ErrNotFound", err)
	}
}

func TestArtifactStoreHonorsCancelledContext(t *testing.T) {
	t.Parallel()

	root, jobID := newArtifactTestRoot(t)
	store := newArtifactStore(root, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Put(ctx, jobID, "cancelled", "", "", strings.NewReader("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put error = %v, want context.Canceled", err)
	}
	if _, _, err := store.Open(ctx, jobID, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", err)
	}
}

func newArtifactTestRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	jobID := "job-1"
	jobDir := filepath.Join(root, jobID)
	if err := os.Mkdir(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return root, jobID
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

type countingReader struct {
	reader    io.Reader
	bytesRead int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	read, err := r.reader.Read(buffer)
	r.bytesRead += read
	return read, err
}
