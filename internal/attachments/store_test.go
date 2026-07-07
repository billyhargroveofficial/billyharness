package attachments

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestImportLocalImageStoresMetadataAndPrivateFile(t *testing.T) {
	root := realAttachmentTestTempDir(t)
	store := NewStore(filepath.Join(root, "attachments"))
	source := filepath.Join(root, "screen.png")
	writePNG(t, source, 2, 3)

	ref, err := store.ImportLocalImage(source, protocol.AttachmentDetailHigh)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID == "" || ref.Kind != protocol.AttachmentKindImage || ref.StorageRef == "" ||
		ref.FileName != "screen.png" || ref.MIMEType != "image/png" ||
		ref.SizeBytes <= 0 || ref.Width != 2 || ref.Height != 3 ||
		len(ref.SHA256) != 64 || ref.Detail != protocol.AttachmentDetailHigh {
		t.Fatalf("ref = %#v", ref)
	}
	resolved, err := store.Resolve(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resolved.Path, store.Root) {
		t.Fatalf("resolved path %q outside store %q", resolved.Path, store.Root)
	}
	assertMode(t, store.Root, 0o700)
	assertMode(t, resolved.Path, 0o600)

	body, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "base64") || strings.Contains(string(body), "data:image") {
		t.Fatalf("ref leaked raw image bytes: %s", body)
	}
}

func TestStoreImageBytesUsesStableAttachmentID(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "attachments"))
	data := pngBytes(t, 1, 1)
	first, err := store.StoreImageBytes("first.png", data, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.StoreImageBytes("second.png", data, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.StorageRef != second.StorageRef || first.SHA256 != second.SHA256 {
		t.Fatalf("ids should be stable for identical bytes: first=%#v second=%#v", first, second)
	}
}

func TestImportLocalImageRejectsSymlink(t *testing.T) {
	root := realAttachmentTestTempDir(t)
	source := filepath.Join(root, "screen.png")
	writePNG(t, source, 1, 1)
	link := filepath.Join(root, "linked.png")
	if err := os.Symlink(source, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := NewStore(filepath.Join(root, "attachments")).ImportLocalImage(link, "")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func realAttachmentTestTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return realDir
}

func TestStoreRejectsUnsupportedImageMIME(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "attachments"))
	_, err := store.StoreImageBytes("note.txt", []byte("hello"), "")
	if err == nil || !strings.Contains(err.Error(), "unsupported image MIME type") {
		t.Fatalf("unsupported error = %v", err)
	}
}

func TestStoreRejectsDimensionCaps(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "attachments"))
	store.MaxImageWidth = 1
	_, err := store.StoreImageBytes("wide.png", pngBytes(t, 2, 1), "")
	if err == nil || !strings.Contains(err.Error(), "width") {
		t.Fatalf("dimension error = %v", err)
	}
}

func TestResolveRejectsTraversalAndStaleAttachment(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "attachments"))
	ref, err := store.StoreImageBytes("screen.png", pngBytes(t, 1, 1), "")
	if err != nil {
		t.Fatal(err)
	}
	ref.StorageRef = "../screen.png"
	if _, err := store.Resolve(ref); err == nil || !strings.Contains(err.Error(), "invalid attachment storage_ref") {
		t.Fatalf("traversal error = %v", err)
	}

	ref, err = store.StoreImageBytes("screen.png", pngBytes(t, 1, 1), "")
	if err != nil {
		t.Fatal(err)
	}
	ref.SizeBytes++
	if _, err := store.Resolve(ref); err == nil || !strings.Contains(err.Error(), "size changed") {
		t.Fatalf("stale size error = %v", err)
	}
}

func TestPruneRemovesOldFiles(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "attachments"))
	oldPath := writeStoreFile(t, store.Root, "old.png", "old")
	newPath := writeStoreFile(t, store.Root, "new.png", "new")
	tmpPath := writeStoreFile(t, store.Root, ".tmp-attachment-keep", "tmp")
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	removed, removedBytes, err := store.Prune(24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || removedBytes != 3 {
		t.Fatalf("removed=%d removedBytes=%d, want 1/3", removed, removedBytes)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old file still exists or unexpected stat error: %v", err)
	}
	for _, path := range []string{newPath, tmpPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept file %s: %v", path, err)
		}
	}
	count, bytes, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || bytes != 3 {
		t.Fatalf("usage=%d/%d, want new file only", count, bytes)
	}
}

func TestPruneEnforcesMaxBytesOldestFirst(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "attachments"))
	oldest := writeStoreFile(t, store.Root, "oldest.png", "1111")
	middle := writeStoreFile(t, store.Root, "middle.png", "2222")
	newest := writeStoreFile(t, store.Root, "newest.png", "3333")
	base := time.Now().Add(-3 * time.Hour)
	for i, path := range []string{oldest, middle, newest} {
		ts := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(path, ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	removed, removedBytes, err := store.Prune(0, 8)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || removedBytes != 4 {
		t.Fatalf("removed=%d removedBytes=%d, want 1/4", removed, removedBytes)
	}
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatalf("oldest file still exists or unexpected stat error: %v", err)
	}
	for _, path := range []string{middle, newest} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept file %s: %v", path, err)
		}
	}
	count, bytes, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || bytes != 8 {
		t.Fatalf("usage=%d/%d, want 2/8", count, bytes)
	}
}

func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	if err := os.WriteFile(path, pngBytes(t, width, height), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeStoreFile(t *testing.T, root, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode(%s) = %#o, want %#o", path, got, want)
	}
}
