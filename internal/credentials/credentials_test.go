package credentials

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestSaveDeepSeekAPIKeyWritesBillyDotenv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)

	status, err := SaveDeepSeekAPIKey("sk-test-value")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.Path != filepath.Join(root, ".env") {
		t.Fatalf("status = %#v", status)
	}
	body, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "DEEPSEEK_API_KEY=sk-test-value" {
		t.Fatalf(".env = %q", body)
	}
	if got := DeepSeekStatus(); !got.Configured || got.Source != filepath.Join(root, ".env") {
		t.Fatalf("DeepSeekStatus = %#v", got)
	}
}

func TestManagerResolveDeepSeekAPIKeyUsesConfiguredEnvName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "1")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("CUSTOM_DEEPSEEK_KEY=sk-custom-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(config.Config{APIKeyEnv: "CUSTOM_DEEPSEEK_KEY"})

	secret, err := manager.ResolveDeepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if secret.Value != "sk-custom-value" || secret.EnvVar != "CUSTOM_DEEPSEEK_KEY" || secret.Path != filepath.Join(root, ".env") {
		t.Fatalf("secret = %#v", secret)
	}
	status := manager.DeepSeekStatus()
	if !status.Configured || status.Source != filepath.Join(root, ".env") || status.Path != filepath.Join(root, ".env") {
		t.Fatalf("status = %#v", status)
	}
}

func TestImportCodexAuthCopiesOAuthJSONToBillyHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	codexHome := filepath.Join(root, "codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).Unix()
	source := filepath.Join(codexHome, "auth.json")
	if err := writeJSON(source, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  testJWT(t, map[string]any{"exp": exp, "chatgpt_account_id": "acct_test"}),
			"refresh_token": "refresh-secret",
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)

	status, err := ImportCodexAuth(config.Default(), "")
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, "auth", "codex.json")
	if !status.Configured || status.Path != wantPath || status.Source != source || status.AccountID != "acct_test" {
		t.Fatalf("status = %#v", status)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestSaveCodexAuthJSONRejectsMissingTokens(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	_, err := SaveCodexAuthJSON(config.Default(), json.RawMessage(`{"auth_mode":"chatgpt"}`))
	if err == nil || !strings.Contains(err.Error(), "does not contain OAuth tokens") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexStatusSeesEnvAccessToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	exp := time.Now().Add(time.Hour).Unix()
	t.Setenv("CODEX_ACCESS_TOKEN", testJWT(t, map[string]any{"exp": exp, "chatgpt_account_id": "acct_env"}))

	status := CodexStatus(config.Default())
	if !status.Configured || status.Source != "env:CODEX_ACCESS_TOKEN" || status.AccountID != "acct_env" || status.Mode != "accessToken" {
		t.Fatalf("status = %#v", status)
	}
	if status.ExpiresAt == "" {
		t.Fatalf("expires_at missing: %#v", status)
	}
}

func TestCodexStatusDoesNotConfigureEmptyAuthFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", filepath.Join(root, "missing.env"))
	path := filepath.Join(root, "auth", "codex.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, map[string]any{"auth_mode": "chatgpt"}); err != nil {
		t.Fatal(err)
	}

	status := CodexStatus(config.Default())
	if status.Configured || status.Source != "" {
		t.Fatalf("empty auth file should not be configured: %#v", status)
	}
	if status.Path != path || status.Mode != "chatgpt" {
		t.Fatalf("status should still report path/mode: %#v", status)
	}
}

func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return encode(map[string]string{"alg": "none", "typ": "JWT"}) + "." + encode(claims) + ".sig"
}
