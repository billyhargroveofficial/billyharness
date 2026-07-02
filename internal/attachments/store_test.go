package attachments

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestImportLocalImageStoresMetadataAndPrivateFile(t *testing.T) {
	root := t.TempDir()
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
	root := t.TempDir()
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

func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	if err := os.WriteFile(path, pngBytes(t, width, height), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode(%s) = %#o, want %#o", path, got, want)
	}
}
