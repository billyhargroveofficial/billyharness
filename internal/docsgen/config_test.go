package docsgen

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestConfigReferenceCoversEveryKey(t *testing.T) {
	output, err := GenerateConfig()
	if err != nil {
		t.Fatal(err)
	}
	keySection := bytes.Split(output, []byte("\n## settings.json\n"))[0]
	for _, spec := range config.ConfigKeySpecs() {
		if count := bytes.Count(keySection, []byte("| "+spec.Key+" ")); count != 1 {
			t.Fatalf("config key %s appears %d times", spec.Key, count)
		}
	}
}

func TestConfigReferenceCoversBillySettingsFields(t *testing.T) {
	output, err := GenerateConfig()
	if err != nil {
		t.Fatal(err)
	}
	parts := bytes.Split(output, []byte("\n## settings.json\n"))
	if len(parts) != 2 {
		t.Fatal("config reference missing settings.json section")
	}
	settingsSection := bytes.Split(parts[1], []byte("\n## Outside The Key Table\n"))[0]
	for _, field := range config.BillySettingsFieldSpecs() {
		if count := bytes.Count(settingsSection, []byte("| "+field.JSON+" ")); count != 1 {
			t.Fatalf("settings key %s appears %d times", field.JSON, count)
		}
	}
}

func TestConfigSourceDocsMatchKnownSources(t *testing.T) {
	docs := configSourceDocs()
	if len(docs) != 10 {
		t.Fatalf("config source docs count = %d, want 10", len(docs))
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		if doc.Layer == "" || doc.Description == "" {
			t.Fatalf("empty config source doc: %#v", doc)
		}
		seen[doc.Layer] = true
	}
	for _, source := range []string{
		config.SourceBuiltIn,
		config.SourceHomeConfig,
		config.SourceProject,
		config.SourceSettings,
		config.SourceProfile,
		config.SourceDotenv,
		config.SourceEnvironment,
		config.SourceCLI,
		config.SourceGateway,
		config.SourceDerived,
	} {
		if !seen[source] {
			t.Fatalf("missing config source %s", source)
		}
	}
}

func TestConfigReferenceFooterHashesInput(t *testing.T) {
	data := configReferenceInput()
	hash, err := sourceHash(data)
	if err != nil {
		t.Fatal(err)
	}
	output, err := GenerateConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("<!-- source-hash: %s -->", hash)
	if !bytes.Contains(output, []byte(want)) {
		t.Fatalf("config output missing source hash %s", hash)
	}
}
