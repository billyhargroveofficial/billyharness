package tooloutput

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestStoreWritesPrivateRefWithMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	ref, err := Store(StoreRequest{
		Parts:                 []string{"web_fetch", "https://example.com/a/b?secret=x"},
		Content:               "  important body  ",
		TrimSpace:             true,
		EnsureTrailingNewline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Path == "" || ref.ID == "" || !strings.HasPrefix(ref.Path, filepath.Join(home, "tool-output")) {
		t.Fatalf("ref = %#v", ref)
	}
	if !strings.Contains(filepath.Base(ref.Path), "web_fetch-example.com_a_b") {
		t.Fatalf("unsafe filename was not normalized: %s", filepath.Base(ref.Path))
	}
	body, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "important body\n" {
		t.Fatalf("body = %q", body)
	}
	sum := sha256.Sum256(body)
	if ref.Bytes != int64(len(body)) || ref.SHA256 != hex.EncodeToString(sum[:]) ||
		ref.Permissions != "0600" || !ref.Plaintext {
		t.Fatalf("metadata = %#v", ref)
	}
	assertMode(t, filepath.Join(config.BillyHomeDir(), "tool-output"), 0o700)
	assertMode(t, filepath.Dir(ref.Path), 0o700)
	assertMode(t, ref.Path, 0o600)

	stat, err := Stat(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Path != ref.Path || stat.ID != ref.ID || stat.Bytes != ref.Bytes ||
		stat.SHA256 != ref.SHA256 || stat.Permissions != "0600" || !stat.Plaintext {
		t.Fatalf("stat = %#v want %#v", stat, ref)
	}
	metadata := map[string]any{}
	stat.AddMetadata(metadata)
	if metadata[MetadataOutputRef] != ref.Path ||
		metadata[MetadataOutputRefID] != ref.ID ||
		metadata[MetadataOutputRefBytes] != ref.Bytes ||
		metadata[MetadataOutputRefSHA256] != ref.SHA256 ||
		metadata[MetadataOutputRefPermissions] != "0600" ||
		metadata[MetadataOutputRefPlaintext] != true {
		t.Fatalf("metadata map = %#v", metadata)
	}
}

func TestArtifactMetadataMapAndAttach(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	ref, err := Store(StoreRequest{
		Parts:   []string{"shell_exec", "call_1"},
		Content: "full output",
	})
	if err != nil {
		t.Fatal(err)
	}

	typed, err := StatMetadata(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	if typed.OutputRef != ref.Path ||
		typed.OutputRefID != ref.ID ||
		typed.OutputRefBytes != ref.Bytes ||
		typed.OutputRefSHA256 != ref.SHA256 ||
		typed.OutputRefPermissions != "0600" ||
		!typed.OutputRefPlaintext {
		t.Fatalf("typed metadata = %#v want ref %#v", typed, ref)
	}
	asMap := typed.Map()
	if asMap[MetadataOutputRef] != ref.Path ||
		asMap[MetadataOutputRefPermissions] != "0600" ||
		asMap[MetadataOutputRefPlaintext] != true {
		t.Fatalf("typed metadata map = %#v", asMap)
	}

	attached := map[string]any{}
	if err := AddMetadataForPath(attached, ref.Path); err != nil {
		t.Fatal(err)
	}
	if attached[MetadataOutputRefSHA256] != ref.SHA256 ||
		attached[MetadataOutputRefBytes] != ref.Bytes {
		t.Fatalf("attached metadata = %#v", attached)
	}

	missing := filepath.Join(t.TempDir(), "missing.txt")
	missingMeta := map[string]any{}
	if err := AddMetadataForPath(missingMeta, missing); err == nil {
		t.Fatal("missing artifact should report an error")
	}
	if missingMeta[MetadataOutputRefHashError] == "" {
		t.Fatalf("missing metadata should record hash/stat error: %#v", missingMeta)
	}
}

func TestStoreEmptyAndExists(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	ref, err := Store(StoreRequest{Content: "   ", TrimSpace: true})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Path != "" {
		t.Fatalf("empty trimmed content should not create ref: %#v", ref)
	}
	if !Exists("") {
		t.Fatal("empty ref should count as existing")
	}
	missing := filepath.Join(t.TempDir(), "missing.txt")
	if Exists(missing) {
		t.Fatal("missing ref should not exist")
	}
	dir := t.TempDir()
	if Exists(dir) {
		t.Fatal("directory ref should not count as existing")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
