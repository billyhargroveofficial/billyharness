package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestReadCodexAuthFileParsesCodexCLIAuthJSON(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	accessJWT := testkit.JWT(t, map[string]any{
		"exp": exp,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id":         "acct_123",
			"chatgpt_account_is_fedramp": true,
		},
	})
	path := writeJSONFile(t, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  accessJWT,
			"refresh_token": "refresh-old",
		},
		"last_refresh": time.Now().UTC().Format(time.RFC3339),
	})

	auth, err := readCodexAuthFile(context.Background(), config.AuthSettings{}, http.DefaultClient, path)
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccessToken != accessJWT {
		t.Fatalf("AccessToken = %q", auth.AccessToken)
	}
	if auth.RefreshToken != "refresh-old" {
		t.Fatalf("RefreshToken = %q", auth.RefreshToken)
	}
	if auth.AccountID != "acct_123" {
		t.Fatalf("AccountID = %q", auth.AccountID)
	}
	if !auth.FedRAMP {
		t.Fatalf("FedRAMP = false")
	}
	if auth.ExpiresAt.Unix() != exp {
		t.Fatalf("ExpiresAt = %v", auth.ExpiresAt)
	}
	if auth.AuthFile != path {
		t.Fatalf("AuthFile = %q", auth.AuthFile)
	}
}

func TestReadCodexAuthFileParsesPersonalAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer at-test" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"chatgpt_account_id":"acct_pat","chatgpt_account_is_fedramp":false}`))
	}))
	t.Cleanup(server.Close)
	path := writeJSONFile(t, map[string]any{
		"auth_mode":             "personalAccessToken",
		"personal_access_token": "at-test",
	})

	auth, err := readCodexAuthFile(context.Background(), config.AuthSettings{CodexAuthAPIBaseURL: server.URL}, http.DefaultClient, path)
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccessToken != "at-test" {
		t.Fatalf("AccessToken = %q", auth.AccessToken)
	}
	if auth.RefreshToken != "" {
		t.Fatalf("RefreshToken = %q", auth.RefreshToken)
	}
	if auth.AccountID != "acct_pat" {
		t.Fatalf("AccountID = %q", auth.AccountID)
	}
	if !auth.PAT {
		t.Fatalf("PAT = false")
	}
	if !auth.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt = %v", auth.ExpiresAt)
	}
}

func TestLoadCodexAuthRefreshesExpiredAuthJSONAndPersistsTokens(t *testing.T) {
	t.Setenv("CODEX_ACCESS_TOKEN", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("FAST_AGENT_ENV_FILE", emptyEnvFile(t))

	expiredJWT := testkit.JWT(t, map[string]any{
		"exp": time.Now().Add(-time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_old",
		},
	})
	newAccessJWT := testkit.JWT(t, map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_new",
		},
	})
	idJWT := testkit.JWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id":         "acct_new",
			"chatgpt_account_is_fedramp": true,
		},
	})
	path := writeJSONFile(t, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  expiredJWT,
			"refresh_token": "refresh-old",
			"account_id":    "acct_old",
		},
		"last_refresh": time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
	})

	var sawRefresh bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRefresh = true
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["grant_type"] != "refresh_token" {
			t.Fatalf("grant_type = %q", body["grant_type"])
		}
		if body["refresh_token"] != "refresh-old" {
			t.Fatalf("refresh_token = %q", body["refresh_token"])
		}
		if body["client_id"] != "client-test" {
			t.Fatalf("client_id = %q", body["client_id"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  newAccessJWT,
			"refresh_token": "refresh-new",
			"id_token":      idJWT,
			"expires_in":    3600,
		})
	}))
	t.Cleanup(server.Close)

	auth, err := loadCodexAuth(context.Background(), config.AuthSettings{
		CodexAuthFile:   path,
		CodexRefreshURL: server.URL,
		CodexClientID:   "client-test",
	}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if !sawRefresh {
		t.Fatal("refresh endpoint was not called")
	}
	if auth.AccessToken != newAccessJWT {
		t.Fatalf("AccessToken = %q", auth.AccessToken)
	}
	if auth.RefreshToken != "refresh-new" {
		t.Fatalf("RefreshToken = %q", auth.RefreshToken)
	}
	if auth.AccountID != "acct_new" {
		t.Fatalf("AccountID = %q", auth.AccountID)
	}
	if !auth.FedRAMP {
		t.Fatalf("FedRAMP = false")
	}

	var disk map[string]any
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	tokens := disk["tokens"].(map[string]any)
	if tokens["access_token"] != newAccessJWT {
		t.Fatalf("disk access_token = %#v", tokens["access_token"])
	}
	if tokens["refresh_token"] != "refresh-new" {
		t.Fatalf("disk refresh_token = %#v", tokens["refresh_token"])
	}
	if _, err := time.Parse(time.RFC3339, disk["last_refresh"].(string)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestLoadCodexAuthHydratesPATFromEnv(t *testing.T) {
	t.Setenv("CODEX_ACCESS_TOKEN", "at-env")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("FAST_AGENT_ENV_FILE", emptyEnvFile(t))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer at-env" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"chatgpt_account_id":"acct_env","chatgpt_account_is_fedramp":true}`))
	}))
	t.Cleanup(server.Close)

	auth, err := loadCodexAuth(context.Background(), config.AuthSettings{CodexAuthAPIBaseURL: server.URL}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccountID != "acct_env" || !auth.FedRAMP || !auth.PAT {
		t.Fatalf("auth = %#v", auth)
	}
}

func TestLoadCodexAuthRefreshHTTPErrorDoesNotExposeRefreshToken(t *testing.T) {
	t.Setenv("CODEX_ACCESS_TOKEN", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("FAST_AGENT_ENV_FILE", emptyEnvFile(t))
	path := writeJSONFile(t, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  testkit.JWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
			"refresh_token": "refresh-secret",
		},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad refresh-secret", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	_, err := loadCodexAuth(context.Background(), config.AuthSettings{
		CodexAuthFile:   path,
		CodexRefreshURL: server.URL,
		CodexClientID:   "client-test",
	}, http.DefaultClient)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "refresh-secret") {
		t.Fatalf("refresh token leaked: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("redaction marker missing: %v", err)
	}
}

func TestReadCodexAuthFilePATMetadataHTTPErrorDoesNotExposePAT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad at-secret", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	path := writeJSONFile(t, map[string]any{
		"auth_mode":             "personalAccessToken",
		"personal_access_token": "at-secret",
	})
	_, err := readCodexAuthFile(context.Background(), config.AuthSettings{CodexAuthAPIBaseURL: server.URL}, http.DefaultClient, path)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "at-secret") {
		t.Fatalf("PAT leaked: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("redaction marker missing: %v", err)
	}
}

func TestLoadCodexAuthUsesEnvTokenBeforeAuthFileAndEnvAccountID(t *testing.T) {
	t.Setenv("FAST_AGENT_ENV_FILE", emptyEnvFile(t))
	envJWT := testkit.JWT(t, map[string]any{
		"exp":                         time.Now().Add(time.Hour).Unix(),
		"chatgpt_account_id":          "acct_claim",
		"chatgpt_account_is_fedramp":  true,
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct_nested"},
	})
	t.Setenv("CODEX_ACCESS_TOKEN", envJWT)
	t.Setenv("CODEX_CHATGPT_ACCOUNT_ID", "acct_env")
	path := writeJSONFile(t, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  testkit.JWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
			"refresh_token": "refresh-file",
		},
	})
	auth, err := loadCodexAuth(context.Background(), config.AuthSettings{CodexAuthFile: path}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccessToken != envJWT {
		t.Fatalf("AccessToken came from file")
	}
	if auth.AccountID != "acct_env" {
		t.Fatalf("AccountID = %q", auth.AccountID)
	}
	if !auth.FedRAMP {
		t.Fatalf("FedRAMP = false")
	}
}

func TestLoadCodexAuthReadsDotenvCodexToken(t *testing.T) {
	root := t.TempDir()
	envJWT := testkit.JWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "chatgpt_account_id": "acct_dotenv"})
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("CODEX_ACCESS_TOKEN="+envJWT+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_ACCESS_TOKEN", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("FAST_AGENT_ENV_FILE", envPath)
	auth, err := loadCodexAuth(context.Background(), config.AuthSettings{}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccessToken != envJWT || auth.AccountID != "acct_dotenv" {
		t.Fatalf("auth = %#v", auth)
	}
}

func TestCodexAuthPathPrefersConfiguredFileThenBillyharnessHome(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "configured.json")
	billyHome := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	if got := codexAuthPath(config.AuthSettings{CodexAuthFile: cfgPath}); got != cfgPath {
		t.Fatalf("configured path = %q", got)
	}
	if got := codexAuthPath(config.AuthSettings{}); got != filepath.Join(billyHome, "auth", "codex.json") {
		t.Fatalf("billyharness auth path = %q", got)
	}
}

func TestReadCodexAuthFileRejectsUnsupportedAuthModes(t *testing.T) {
	for _, payload := range []map[string]any{
		{"auth_mode": "apikey", "OPENAI_API_KEY": "sk-test"},
		{"auth_mode": "bedrockApiKey", "bedrock_api_key": "bedrock"},
		{"auth_mode": "agentIdentity", "agent_identity": map[string]any{}},
	} {
		_, err := readCodexAuthFile(context.Background(), config.AuthSettings{}, http.DefaultClient, writeJSONFile(t, payload))
		if err == nil {
			t.Fatalf("expected unsupported auth mode error for %#v", payload)
		}
	}
}

func TestCodexAuthNeedsRefreshBoundaries(t *testing.T) {
	now := time.Now()
	if (&codexAuth{PAT: true, AccessToken: "at-test"}).needsRefresh(now) {
		t.Fatal("PAT should not need refresh")
	}
	if !(&codexAuth{AccessToken: "tok", ExpiresAt: now.Add(4 * time.Minute)}).needsRefresh(now) {
		t.Fatal("token expiring inside 5 minutes should refresh")
	}
	if (&codexAuth{AccessToken: "tok", ExpiresAt: now.Add(6 * time.Minute)}).needsRefresh(now) {
		t.Fatal("token expiring after 6 minutes should not refresh")
	}
	if !(&codexAuth{AccessToken: "tok", LastRefresh: now.Add(-9 * 24 * time.Hour)}).needsRefresh(now) {
		t.Fatal("unknown expiry with old refresh should refresh")
	}
	if (&codexAuth{AccessToken: "tok", LastRefresh: now.Add(-2 * 24 * time.Hour)}).needsRefresh(now) {
		t.Fatal("unknown expiry with recent refresh should not refresh")
	}
}

func writeJSONFile(t *testing.T, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func emptyEnvFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(strings.Repeat("# empty\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
