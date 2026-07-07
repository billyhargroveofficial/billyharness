package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultAgentClubConfigFilesUsesBillyharnessHomeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	if files := DefaultAgentClubConfigFiles(); len(files) != 0 {
		t.Fatalf("files before create = %#v", files)
	}
	path := DefaultAgentClubConfigFile()
	if path != filepath.Join(home, "agentclub.config.json") {
		t.Fatalf("default path = %q", path)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	files := DefaultAgentClubConfigFiles()
	if len(files) != 1 || files[0] != path {
		t.Fatalf("files after create = %#v", files)
	}
}

func TestAgentClubConfigFilesEnvAliasesResolve(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("BILLYHARNESS_AGENTCLUB_CONFIG_FILES", "one.json,two.json")
	resolved, err := ResolveStrict()
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Config.AgentClubConfigFiles) != 2 ||
		resolved.Config.AgentClubConfigFiles[0] != "one.json" ||
		resolved.Config.AgentClubConfigFiles[1] != "two.json" {
		t.Fatalf("agentclub config files = %#v", resolved.Config.AgentClubConfigFiles)
	}
	if value, ok := resolved.Value("agentclub_config_files"); !ok || value.Source != SourceEnvironment {
		t.Fatalf("resolved value = %#v ok=%t", value, ok)
	}
}
