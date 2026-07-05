package docsgen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTargetFingerprintsMatchGeneratedFooters(t *testing.T) {
	for _, target := range Targets() {
		body, err := target.Generate()
		if err != nil {
			t.Fatalf("generate %s: %v", target.Name, err)
		}
		footerHash, ok := parseSourceHash(body)
		if !ok {
			t.Fatalf("generated %s missing source hash footer", target.Name)
		}
		fingerprint, err := target.Fingerprint()
		if err != nil {
			t.Fatalf("fingerprint %s: %v", target.Name, err)
		}
		if fingerprint != footerHash {
			t.Fatalf("fingerprint %s = %s, footer = %s", target.Name, fingerprint, footerHash)
		}
	}
}

func TestVerifyAgainstReportsStaleGeneratedFile(t *testing.T) {
	dir := t.TempDir()
	targets := Targets()
	for _, target := range targets {
		body, err := target.Generate()
		if err != nil {
			t.Fatalf("generate %s: %v", target.Name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, target.Filename), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first := targets[0]
	path := filepath.Join(dir, first.Filename)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash, ok := parseSourceHash(body)
	if !ok {
		t.Fatalf("%s missing source hash", first.Filename)
	}
	stale := bytes.Replace(body, []byte(hash), []byte(strings.Repeat("0", len(hash))), 1)
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	statuses := VerifyAgainst(dir)
	for _, status := range statuses {
		if status.Name == first.Name {
			if status.Status != TargetStatusStale {
				t.Fatalf("status for %s = %#v, want stale", first.Name, status)
			}
			return
		}
	}
	t.Fatalf("status for %s missing: %#v", first.Name, statuses)
}
