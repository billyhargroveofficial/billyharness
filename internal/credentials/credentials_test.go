package credentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
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
	manager := NewManagerFromAuthSettings(config.AuthSettings{APIKeyEnv: "CUSTOM_DEEPSEEK_KEY"})

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

func TestNewManagerFromAuthSettingsUsesProjection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "1")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PROJECTED_DEEPSEEK_KEY=sk-projected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(root, "auth", "projected-codex.json")
	manager := NewManagerFromAuthSettings(config.AuthSettings{
		APIKeyEnv:     "PROJECTED_DEEPSEEK_KEY",
		CodexAuthFile: authFile,
	})

	secret, err := manager.ResolveDeepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if secret.Value != "sk-projected" || secret.EnvVar != "PROJECTED_DEEPSEEK_KEY" {
		t.Fatalf("secret = %#v", secret)
	}
	if got := manager.CodexAuthFilePath(); got != authFile {
		t.Fatalf("CodexAuthFilePath = %q", got)
	}
}

func TestManagerSaveDeepSeekAPIKeyUsesConfiguredEnvName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "1")
	manager := NewManagerFromAuthSettings(config.AuthSettings{APIKeyEnv: "CUSTOM_DEEPSEEK_KEY"})

	status, err := manager.SaveDeepSeekAPIKey("sk-custom-save")
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
	if strings.TrimSpace(string(body)) != "CUSTOM_DEEPSEEK_KEY=sk-custom-save" {
		t.Fatalf(".env = %q", body)
	}
	secret, err := manager.ResolveDeepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if secret.Value != "sk-custom-save" || secret.EnvVar != "CUSTOM_DEEPSEEK_KEY" {
		t.Fatalf("secret = %#v", secret)
	}
}

func TestManagerResolvesCredentialFileSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("CODEX_ACCESS_TOKEN", "")
	t.Setenv("CODEX_CHATGPT_ACCOUNT_ID", "")
	t.Setenv("FAST_AGENT_ENV_FILE", filepath.Join(root, "missing.env"))
	credentialFile := filepath.Join(root, "auth", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(credentialFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(credentialFile, map[string]any{
		"deepseek_api_key":   "sk-from-credential-file",
		"codex_access_token": "codex-token-from-file",
		"codex_account_id":   "acct_file",
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManagerFromAuthSettings(config.AuthSettings{CredentialFile: credentialFile})

	deepseek, err := manager.ResolveDeepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if deepseek.Value != "sk-from-credential-file" || deepseek.Source != credentialFile || deepseek.Path != credentialFile {
		t.Fatalf("deepseek secret = %#v", deepseek)
	}
	resolved := manager.ResolveCodexAuth()
	if resolved.AccessToken.Value != "codex-token-from-file" || resolved.AccessToken.Source != credentialFile || resolved.AccountID.Value != "acct_file" {
		t.Fatalf("codex resolution = %#v", resolved)
	}
	status := manager.CodexStatus()
	if !status.Configured || status.Source != credentialFile || status.AccountID != "acct_file" {
		t.Fatalf("codex status = %#v", status)
	}
	if strings.Contains(status.Source, "codex-token-from-file") || strings.Contains(status.Path, "codex-token-from-file") {
		t.Fatalf("status leaked credential file token: %#v", status)
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
			"access_token":  testkit.JWT(t, map[string]any{"exp": exp, "chatgpt_account_id": "acct_test"}),
			"refresh_token": "refresh-secret",
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)

	status, err := ImportCodexAuthFromAuthSettings(config.AuthSettings{}, "")
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
	_, err := SaveCodexAuthJSONFromAuthSettings(config.AuthSettings{}, json.RawMessage(`{"auth_mode":"chatgpt"}`))
	if err == nil || !strings.Contains(err.Error(), "does not contain OAuth tokens") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexStatusSeesEnvAccessToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	exp := time.Now().Add(time.Hour).Unix()
	t.Setenv("CODEX_ACCESS_TOKEN", testkit.JWT(t, map[string]any{"exp": exp, "chatgpt_account_id": "acct_env"}))

	status := CodexStatusFromAuthSettings(config.AuthSettings{})
	if !status.Configured || status.Source != "env:CODEX_ACCESS_TOKEN" || status.AccountID != "acct_env" || status.Mode != "accessToken" || status.Refresh != "fresh" {
		t.Fatalf("status = %#v", status)
	}
	if status.ExpiresAt == "" {
		t.Fatalf("expires_at missing: %#v", status)
	}
}

func TestManagerResolveCodexAuthUsesSharedSources(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("CODEX_ACCESS_TOKEN", "")
	t.Setenv("CODEX_CHATGPT_ACCOUNT_ID", "")
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("CODEX_ACCESS_TOKEN=token-from-dotenv\nCODEX_CHATGPT_ACCOUNT_ID=acct_dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(root, "custom-auth.json")
	manager := NewManagerFromAuthSettings(config.AuthSettings{CodexAuthFile: authPath})

	resolved := manager.ResolveCodexAuth()
	if resolved.AccessToken.Value != "token-from-dotenv" || resolved.AccessToken.Source != ".env" || resolved.AccessToken.EnvVar != CodexAccessTokenEnv {
		t.Fatalf("access token source = %#v", resolved.AccessToken)
	}
	if resolved.AccountID.Value != "acct_dotenv" || resolved.AccountID.EnvVar != CodexAccountIDEnv {
		t.Fatalf("account source = %#v", resolved.AccountID)
	}
	if resolved.AuthFile != authPath {
		t.Fatalf("auth file = %q", resolved.AuthFile)
	}
}

func TestCodexStatusShowsRefreshStateForAuthFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", filepath.Join(root, "missing.env"))
	path := filepath.Join(root, "auth", "codex.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  testkit.JWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
			"refresh_token": "refresh-secret",
		},
	}); err != nil {
		t.Fatal(err)
	}

	status := CodexStatusFromAuthSettings(config.AuthSettings{})
	if !status.Configured || status.Refresh != "refresh_required" {
		t.Fatalf("status = %#v", status)
	}
	if strings.Contains(status.Source, "refresh-secret") || strings.Contains(status.Path, "refresh-secret") {
		t.Fatalf("status leaked refresh token: %#v", status)
	}
}

func TestCodexStatusPATDoesNotNeedRefresh(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("CODEX_ACCESS_TOKEN", "at-secret")

	status := CodexStatusFromAuthSettings(config.AuthSettings{})
	if !status.Configured || status.Mode != "personalAccessToken" || status.Refresh != "not_required" {
		t.Fatalf("status = %#v", status)
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

	status := CodexStatusFromAuthSettings(config.AuthSettings{})
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
