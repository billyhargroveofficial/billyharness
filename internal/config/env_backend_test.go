package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebBackendConfigAndDiagnosticsDoNotLeakKeys(t *testing.T) {
	home := t.TempDir()
	hermesEnv := filepath.Join(home, "hermes.env")
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	if err := os.WriteFile(hermesEnv, []byte("TAVILY_API_KEY=tvly-secret-value\nEXA_API_KEY=exa-secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`
web_search_backend = "exa"
web_extract_backend = "tavily"
web_hermes_env_files = ["`+strings.ReplaceAll(hermesEnv, `\`, `\\`)+`"]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	if cfg.WebSearchBackend != "exa" || cfg.WebExtractBackend != "tavily" || len(cfg.WebHermesEnvFiles) != 1 {
		t.Fatalf("web backend config = %#v", cfg)
	}
	runtime := cfg.DiagnosticSnapshot().RuntimeTool
	if !runtime.WebTavilyAPIKeyPresent || runtime.WebTavilyAPIKeySource != "configured_env_file" ||
		!runtime.WebExaAPIKeyPresent || runtime.WebExaAPIKeySource != "configured_env_file" {
		t.Fatalf("runtime web key diagnostics = %#v", runtime)
	}
	body, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "tvly-secret-value") || strings.Contains(string(body), "exa-secret-value") {
		t.Fatalf("diagnostics leaked key value: %s", string(body))
	}
}

func TestLookupEnvOrDotenvFallsBackToBillyharnessHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("FROM_BILLY_ENV=dotenv-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := LookupEnvOrDotenv("FROM_BILLY_ENV")
	if !ok || got != "dotenv-value" {
		t.Fatalf("LookupEnvOrDotenv = %q %v", got, ok)
	}
}

func TestLookupEnvOrDotenvPrefersBillyharnessHomeOverCWD(t *testing.T) {
	root := t.TempDir()
	billyHome := filepath.Join(root, "billyhome")
	workdir := filepath.Join(root, "work")
	if err := os.MkdirAll(billyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billyHome, ".env"), []byte("PREFERRED_ENV=from-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".env"), []byte("PREFERRED_ENV=from-cwd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")

	got, ok := LookupEnvOrDotenv("PREFERRED_ENV")
	if !ok || got != "from-home" {
		t.Fatalf("LookupEnvOrDotenv = %q %v", got, ok)
	}
}

func TestLookupEnvOrDotenvExplicitFileOverridesByDefault(t *testing.T) {
	root := t.TempDir()
	billyHome := filepath.Join(root, "billyhome")
	explicit := filepath.Join(root, "explicit.env")
	if err := os.MkdirAll(billyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billyHome, ".env"), []byte("EXPLICIT_ENV=from-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicit, []byte("EXPLICIT_ENV=from-explicit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	t.Setenv("FAST_AGENT_ENV_FILE", explicit)
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")

	got, ok := LookupEnvOrDotenv("EXPLICIT_ENV")
	if !ok || got != "from-explicit" {
		t.Fatalf("LookupEnvOrDotenv = %q %v", got, ok)
	}
}

func TestLookupEnvOrDotenvCanRestrictToBillyharnessHome(t *testing.T) {
	root := t.TempDir()
	billyHome := filepath.Join(root, "billyhome")
	workdir := filepath.Join(root, "work")
	outsideEnv := filepath.Join(root, "outside.env")
	if err := os.MkdirAll(billyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(billyHome, ".env"), []byte("RESTRICTED_ENV=from-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".env"), []byte("RESTRICTED_ENV=from-cwd\nCWD_ONLY=blocked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideEnv, []byte("RESTRICTED_ENV=from-explicit\nEXPLICIT_ONLY=blocked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	t.Setenv("FAST_AGENT_ENV_FILE", outsideEnv)
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "true")

	got, ok := LookupEnvOrDotenv("RESTRICTED_ENV")
	if !ok || got != "from-home" {
		t.Fatalf("LookupEnvOrDotenv = %q %v", got, ok)
	}
	if got, ok := LookupEnvOrDotenv("CWD_ONLY"); ok || got != "" {
		t.Fatalf("CWD dotenv should be blocked in home-only mode, got %q %v", got, ok)
	}
	if got, ok := LookupEnvOrDotenv("EXPLICIT_ONLY"); ok || got != "" {
		t.Fatalf("explicit dotenv should be blocked in home-only mode, got %q %v", got, ok)
	}
}
