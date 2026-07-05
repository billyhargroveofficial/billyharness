package credentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	if secret.Value != "sk-custom-value" || secret.EnvVar != "CUSTOM_DEEPSEEK_KEY" || secret.Path != filepath.Join(root, ".env") || secret.Provenance != "dotenv" {
		t.Fatalf("secret = %#v", secret)
	}
	status := manager.DeepSeekStatus()
	if !status.Configured || status.Source != filepath.Join(root, ".env") || status.Path != filepath.Join(root, ".env") || status.Provenance != "dotenv" {
		t.Fatalf("status = %#v", status)
	}
}

func TestAuthStatusClassifiesAndRedactsCredentials(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "1")
	t.Setenv("DEEPSEEK_API_KEY", "sk-classified-secret")
	t.Setenv("CODEX_ACCESS_TOKEN", "at-codex-secret")

	status := CurrentStatusForRuntime(config.AuthSettings{}, "deepseek", "gpt-5.5")
	if status.ActiveProvider != "openai-codex" || status.ActiveModel != "gpt-5.5" || status.CostMode != "subscription" {
		t.Fatalf("runtime status = %#v", status)
	}
	if status.DeepSeek.Provider != "deepseek" || status.DeepSeek.AuthType != "api-key" ||
		status.DeepSeek.Status != "configured" || status.DeepSeek.Credential != "redacted" ||
		status.DeepSeek.Provenance != "env" {
		t.Fatalf("deepseek classified status = %#v", status.DeepSeek)
	}
	if status.Codex.Provider != "codex" || status.Codex.AuthType != "codex-oauth" ||
		status.Codex.Status != "configured" || status.Codex.Credential != "redacted" ||
		status.Codex.Mode != "personalAccessToken" {
		t.Fatalf("codex classified status = %#v", status.Codex)
	}
	text := FormatStatusText(status)
	for _, leak := range []string{"sk-classified-secret", "at-codex-secret"} {
		if strings.Contains(text, leak) {
			t.Fatalf("formatted status leaked %q:\n%s", leak, text)
		}
	}
	for _, want := range []string{"runtime: provider=openai-codex model=gpt-5.5 cost_mode=subscription", "auth=api-key", "auth=codex-oauth", "credential=redacted", "provenance=env"} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatted status missing %q:\n%s", want, text)
		}
	}

	deepseekRuntime := CurrentStatusForRuntime(config.AuthSettings{}, "openai-codex", "deepseek-v4-flash")
	if deepseekRuntime.ActiveProvider != "deepseek" || deepseekRuntime.CostMode != "metered" {
		t.Fatalf("deepseek runtime status = %#v", deepseekRuntime)
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
	if secret.Value != "sk-projected" || secret.EnvVar != "PROJECTED_DEEPSEEK_KEY" || secret.Provenance != "dotenv" {
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

func TestManagerSaveDeepSeekAPIKeyWritesExplicitEnvFile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	explicit := filepath.Join(root, "runtime.env")
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", explicit)
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")
	manager := NewManagerFromAuthSettings(config.AuthSettings{})

	status, err := manager.SaveDeepSeekAPIKey("sk-explicit-save")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.Path != explicit || status.Source != explicit || status.Provenance != "dotenv" {
		t.Fatalf("status = %#v", status)
	}
	body, err := os.ReadFile(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "DEEPSEEK_API_KEY=sk-explicit-save" {
		t.Fatalf("explicit env = %q", body)
	}
	if _, err := os.Stat(filepath.Join(home, ".env")); !os.IsNotExist(err) {
		t.Fatalf("home .env should not be written, err=%v", err)
	}
	secret, err := manager.ResolveDeepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if secret.Value != "sk-explicit-save" || secret.Path != explicit || secret.Provenance != "dotenv" {
		t.Fatalf("secret = %#v", secret)
	}
}

func TestManagerSaveDeepSeekAPIKeyCreatesMissingExplicitEnvFile(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "missing", "runtime.env")
	t.Setenv("BILLYHARNESS_HOME", filepath.Join(root, "home"))
	t.Setenv("FAST_AGENT_ENV_FILE", explicit)
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")
	manager := NewManagerFromAuthSettings(config.AuthSettings{})

	status, err := manager.SaveDeepSeekAPIKey("sk-missing-explicit-save")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.Path != explicit {
		t.Fatalf("status = %#v", status)
	}
	body, err := os.ReadFile(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "DEEPSEEK_API_KEY=sk-missing-explicit-save" {
		t.Fatalf("explicit env = %q", body)
	}
}

func TestManagerSaveDeepSeekAPIKeyFailsForReadOnlyExplicitEnvFile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	explicit := filepath.Join(root, "runtime.env")
	t.Setenv("BILLYHARNESS_HOME", home)
	t.Setenv("FAST_AGENT_ENV_FILE", explicit)
	t.Setenv("BILLYHARNESS_DOTENV_HOME_ONLY", "")
	if err := os.WriteFile(explicit, []byte("OTHER=value\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	manager := NewManagerFromAuthSettings(config.AuthSettings{})

	_, err := manager.SaveDeepSeekAPIKey("sk-read-only-save")
	if err == nil || !strings.Contains(err.Error(), explicit) || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v", err)
	}
	body, readErr := os.ReadFile(explicit)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(body), "sk-read-only-save") || strings.Contains(string(body), "DEEPSEEK_API_KEY") {
		t.Fatalf("read-only env was modified: %q", body)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".env")); !os.IsNotExist(statErr) {
		t.Fatalf("home .env should not be fallback-written, err=%v", statErr)
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
	if deepseek.Value != "sk-from-credential-file" || deepseek.Source != credentialFile || deepseek.Path != credentialFile || deepseek.Provenance != "credential_file" {
		t.Fatalf("deepseek secret = %#v", deepseek)
	}
	resolved := manager.ResolveCodexAuth()
	if resolved.AccessToken.Value != "codex-token-from-file" || resolved.AccessToken.Source != credentialFile || resolved.AccessToken.Provenance != "credential_file" || resolved.AccountID.Value != "acct_file" {
		t.Fatalf("codex resolution = %#v", resolved)
	}
	status := manager.CodexStatus()
	if !status.Configured || status.Source != credentialFile || status.Provenance != "credential_file" || status.AccountID != "acct_file" {
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
	if !fileModeMatches(info.Mode().Perm(), 0o600) {
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
	if resolved.AccessToken.Value != "token-from-dotenv" || resolved.AccessToken.Source != envPath || resolved.AccessToken.Provenance != "dotenv" || resolved.AccessToken.EnvVar != CodexAccessTokenEnv {
		t.Fatalf("access token source = %#v", resolved.AccessToken)
	}
	if resolved.AccountID.Value != "acct_dotenv" || resolved.AccountID.Source != envPath || resolved.AccountID.Provenance != "dotenv" || resolved.AccountID.EnvVar != CodexAccountIDEnv {
		t.Fatalf("account source = %#v", resolved.AccountID)
	}
	if resolved.AuthFile != authPath {
		t.Fatalf("auth file = %q", resolved.AuthFile)
	}
}

func TestManagerSecretLookupUsesSharedDotenvPrecedence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", root)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("CODEX_ACCESS_TOKEN", "")
	t.Setenv("CODEX_CHATGPT_ACCOUNT_ID", "")
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("DEEPSEEK_API_KEY=sk-from-dotenv\nCODEX_ACCESS_TOKEN=codex-from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialFile := filepath.Join(root, "auth", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(credentialFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(credentialFile, map[string]any{
		"deepseek_api_key":   "sk-from-credential-file",
		"codex_access_token": "codex-from-credential-file",
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManagerFromAuthSettings(config.AuthSettings{CredentialFile: credentialFile})

	deepseek, err := manager.ResolveDeepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if deepseek.Value != "sk-from-dotenv" || deepseek.Source != envPath || deepseek.Provenance != "dotenv" {
		t.Fatalf("deepseek shared lookup = %#v", deepseek)
	}
	codex := manager.ResolveCodexAuth()
	if codex.AccessToken.Value != "codex-from-dotenv" || codex.AccessToken.Source != envPath || codex.AccessToken.Provenance != "dotenv" {
		t.Fatalf("codex shared lookup = %#v", codex)
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

func fileModeMatches(got, want os.FileMode) bool {
	return got == want || runtime.GOOS == "windows"
}
