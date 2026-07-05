package config

import "testing"

func TestConfigKeySpecsProjectConfigSpecs(t *testing.T) {
	specs := configSpecs()
	docs := ConfigKeySpecs()
	if len(docs) != len(specs) {
		t.Fatalf("ConfigKeySpecs length = %d, want %d", len(docs), len(specs))
	}
	for i, doc := range docs {
		spec := specs[i]
		if doc.Key != spec.Key {
			t.Fatalf("doc %d key = %s, want %s", i, doc.Key, spec.Key)
		}
		if doc.Type == "" {
			t.Fatalf("%s has empty type", doc.Key)
		}
		if doc.Description == "" {
			t.Fatalf("%s has empty description", doc.Key)
		}
	}
}

func TestConfigKeySpecEnvAliasesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, spec := range ConfigKeySpecs() {
		for _, env := range spec.Env {
			if previous := seen[env]; previous != "" {
				t.Fatalf("env alias %s used by both %s and %s", env, previous, spec.Key)
			}
			seen[env] = spec.Key
		}
	}
}
