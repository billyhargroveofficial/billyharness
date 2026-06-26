package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAPIKeyFallsBackToDotenv(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TEST_API_KEY=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_API_KEY", "")
	t.Setenv("FAST_AGENT_ENV_FILE", "")

	cfg := Default()
	cfg.APIKeyEnv = "TEST_API_KEY"
	if got := cfg.APIKey(); got != "from-dotenv" {
		t.Fatalf("APIKey() = %q", got)
	}
}
