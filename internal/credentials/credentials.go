package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/codexauth"
	"github.com/billyhargroveofficial/billyharness/internal/config"
)

const deepSeekKeyEnv = "DEEPSEEK_API_KEY"

const (
	CodexAccessTokenEnv = "CODEX_ACCESS_TOKEN"
	CodexAccountIDEnv   = "CODEX_CHATGPT_ACCOUNT_ID"
)

type ProviderStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	Path       string `json:"path,omitempty"`
	AccountID  string `json:"account_id,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Refresh    string `json:"refresh_status,omitempty"`
}

type Status struct {
	DeepSeek ProviderStatus `json:"deepseek"`
	Codex    ProviderStatus `json:"codex"`
}

type SecretValue struct {
	Value  string
	Source string
	Path   string
	EnvVar string
}

type CodexAuthResolution struct {
	AccessToken SecretValue
	AccountID   SecretValue
	AuthFile    string
}

type Manager struct {
	auth config.AuthSettings
}

func NewManagerFromAuthSettings(auth config.AuthSettings) Manager {
	if strings.TrimSpace(auth.APIKeyEnv) == "" {
		auth.APIKeyEnv = deepSeekKeyEnv
	}
	if strings.TrimSpace(auth.CredentialFile) == "" {
		auth.CredentialFile = config.DefaultCredentialFile()
	}
	if strings.TrimSpace(auth.CodexAuthFile) == "" {
		auth.CodexAuthFile = config.DefaultCodexAuthFile()
	}
	return Manager{auth: auth}
}

func CurrentStatusFromAuthSettings(auth config.AuthSettings) Status {
	return NewManagerFromAuthSettings(auth).Status()
}

func (m Manager) Status() Status {
	return Status{
		DeepSeek: m.DeepSeekStatus(),
		Codex:    m.CodexStatus(),
	}
}

func DeepSeekStatus() ProviderStatus {
	return NewManagerFromAuthSettings(config.AuthSettings{APIKeyEnv: deepSeekKeyEnv}).DeepSeekStatus()
}

func (m Manager) DeepSeekStatus() ProviderStatus {
	secret, err := m.ResolveDeepSeekAPIKey()
	if err != nil {
		return ProviderStatus{Path: BillyDotenvPath()}
	}
	return ProviderStatus{Configured: true, Source: secret.Source, Path: secret.Path}
}

func (m Manager) ResolveDeepSeekAPIKey() (SecretValue, error) {
	envKey := strings.TrimSpace(m.auth.APIKeyEnv)
	if envKey == "" {
		envKey = deepSeekKeyEnv
	}
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return SecretValue{Value: value, Source: "env:" + envKey, EnvVar: envKey}, nil
	}
	path := BillyDotenvPath()
	if value, ok := dotenvValue(path, envKey); ok && strings.TrimSpace(value) != "" {
		return SecretValue{Value: strings.TrimSpace(value), Source: path, Path: path, EnvVar: envKey}, nil
	}
	if secret := m.lookupCredentialFileSecret(envKey, "deepseek_api_key"); strings.TrimSpace(secret.Value) != "" {
		return secret, nil
	}
	if value, ok := config.LookupEnvOrDotenv(envKey); ok && strings.TrimSpace(value) != "" {
		return SecretValue{Value: strings.TrimSpace(value), Source: ".env", EnvVar: envKey}, nil
	}
	return SecretValue{Path: path, EnvVar: envKey}, fmt.Errorf("missing API key env var %s", envKey)
}

func (m Manager) ResolveCodexAuth() CodexAuthResolution {
	return CodexAuthResolution{
		AccessToken: m.lookupSecret(CodexAccessTokenEnv, "codex_access_token"),
		AccountID:   m.lookupSecret(CodexAccountIDEnv, "codex_account_id"),
		AuthFile:    m.CodexAuthFilePath(),
	}
}

func (m Manager) CodexAuthFilePath() string {
	if path := strings.TrimSpace(m.auth.CodexAuthFile); path != "" {
		return path
	}
	return config.DefaultCodexAuthFile()
}

func (m Manager) CredentialFilePath() string {
	if path := strings.TrimSpace(m.auth.CredentialFile); path != "" {
		return path
	}
	return config.DefaultCredentialFile()
}

func SaveDeepSeekAPIKey(apiKey string) (ProviderStatus, error) {
	return NewManagerFromAuthSettings(config.AuthSettings{APIKeyEnv: deepSeekKeyEnv}).SaveDeepSeekAPIKey(apiKey)
}

func (m Manager) SaveDeepSeekAPIKey(apiKey string) (ProviderStatus, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ProviderStatus{}, fmt.Errorf("DeepSeek API key is empty")
	}
	if !strings.HasPrefix(apiKey, "sk-") {
		return ProviderStatus{}, fmt.Errorf("DeepSeek API key should start with sk-")
	}
	envKey := strings.TrimSpace(m.auth.APIKeyEnv)
	if envKey == "" {
		envKey = deepSeekKeyEnv
	}
	path := BillyDotenvPath()
	if err := upsertDotenvValue(path, envKey, apiKey); err != nil {
		return ProviderStatus{}, err
	}
	return ProviderStatus{Configured: true, Source: path, Path: path}, nil
}

func CodexStatusFromAuthSettings(auth config.AuthSettings) ProviderStatus {
	return NewManagerFromAuthSettings(auth).CodexStatus()
}

func (m Manager) CodexStatus() ProviderStatus {
	resolved := m.ResolveCodexAuth()
	path := resolved.AuthFile
	status := ProviderStatus{Path: path}
	if token := strings.TrimSpace(resolved.AccessToken.Value); token != "" {
		return codexEnvStatus(token, strings.TrimSpace(resolved.AccountID.Value), resolved.AccessToken.Source, path)
	}
	payload, err := readAuthPayload(path)
	if err != nil {
		return status
	}
	status.Mode = codexauth.StringField(payload, "auth_mode")
	if token := codexauth.StringField(payload, "personal_access_token"); token != "" {
		status.Configured = true
		status.Source = path
		status.Mode = "personalAccessToken"
		status.AccountID = codexauth.StringField(payload, "chatgpt_account_id")
		status.Refresh = "not_required"
		return status
	}
	tokens, _ := payload["tokens"].(map[string]any)
	if tokens == nil {
		return status
	}
	accessToken := codexauth.StringField(tokens, "access_token")
	refreshToken := codexauth.StringField(tokens, "refresh_token")
	if accessToken == "" && refreshToken == "" {
		return status
	}
	status.Configured = true
	status.Source = path
	if status.AccountID = codexauth.StringField(tokens, "account_id"); status.AccountID == "" {
		status.AccountID = codexauth.AccountIDFromJWT(codexauth.StringField(tokens, "id_token"))
	}
	if status.AccountID == "" {
		status.AccountID = codexauth.AccountIDFromJWT(accessToken)
	}
	exp := codexauth.ExpirationFromJWT(accessToken)
	status.Refresh = codexauth.RefreshStatus(accessToken, refreshToken, exp, false)
	if !exp.IsZero() {
		status.ExpiresAt = exp.UTC().Format(time.RFC3339)
	}
	return status
}

func (m Manager) lookupSecret(envKey, fileKey string) SecretValue {
	envKey = strings.TrimSpace(envKey)
	if envKey == "" {
		return SecretValue{}
	}
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return SecretValue{Value: value, Source: "env:" + envKey, EnvVar: envKey}
	}
	if secret := m.lookupCredentialFileSecret(envKey, fileKey); strings.TrimSpace(secret.Value) != "" {
		return secret
	}
	if value, ok := config.LookupEnvOrDotenv(envKey); ok && strings.TrimSpace(value) != "" {
		return SecretValue{Value: strings.TrimSpace(value), Source: ".env", EnvVar: envKey}
	}
	return SecretValue{EnvVar: envKey}
}

func (m Manager) lookupCredentialFileSecret(envKey, fileKey string) SecretValue {
	path := m.CredentialFilePath()
	value, ok := credentialFileValue(path, fileKey, envKey)
	if !ok || strings.TrimSpace(value) == "" {
		return SecretValue{Path: path, EnvVar: envKey}
	}
	return SecretValue{Value: strings.TrimSpace(value), Source: path, Path: path, EnvVar: envKey}
}

func codexEnvStatus(token, accountID, source, path string) ProviderStatus {
	status := ProviderStatus{Configured: true, Source: source, Path: path, AccountID: accountID}
	if strings.HasPrefix(strings.TrimSpace(token), "at-") {
		status.Mode = "personalAccessToken"
		status.Refresh = "not_required"
		return status
	}
	status.Mode = "accessToken"
	if status.AccountID == "" {
		status.AccountID = codexauth.AccountIDFromJWT(token)
	}
	exp := codexauth.ExpirationFromJWT(token)
	status.Refresh = codexauth.RefreshStatus(token, "", exp, false)
	if !exp.IsZero() {
		status.ExpiresAt = exp.UTC().Format(time.RFC3339)
	}
	return status
}

func ImportCodexAuthFromAuthSettings(auth config.AuthSettings, sourcePath string) (ProviderStatus, error) {
	return NewManagerFromAuthSettings(auth).ImportCodexAuth(sourcePath)
}

func (m Manager) ImportCodexAuth(sourcePath string) (ProviderStatus, error) {
	dest := m.auth.CodexAuthFile
	if strings.TrimSpace(dest) == "" {
		dest = config.DefaultCodexAuthFile()
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		var candidates []string
		if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
			candidates = append(candidates, filepath.Join(codexHome, "auth.json"))
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates, filepath.Join(home, ".codex", "auth.json"))
		}
		for _, candidate := range candidates {
			if samePath(candidate, dest) {
				continue
			}
			if _, err := os.Stat(candidate); err == nil {
				sourcePath = candidate
				break
			}
		}
	}
	if sourcePath == "" {
		return ProviderStatus{Path: dest}, fmt.Errorf("Codex OAuth auth.json not found; run `codex login` first or pass source_path")
	}
	payload, err := readAuthPayload(sourcePath)
	if err != nil {
		return ProviderStatus{}, err
	}
	if !codexauth.HasTokens(payload) {
		return ProviderStatus{}, fmt.Errorf("Codex auth file %s does not contain OAuth tokens", sourcePath)
	}
	if err := writeAuthPayload(dest, payload); err != nil {
		return ProviderStatus{}, err
	}
	status := NewManagerFromAuthSettings(config.AuthSettings{CodexAuthFile: dest}).CodexStatus()
	status.Source = sourcePath
	status.Path = dest
	return status, nil
}

func SaveCodexAuthJSONFromAuthSettings(auth config.AuthSettings, raw json.RawMessage) (ProviderStatus, error) {
	return NewManagerFromAuthSettings(auth).SaveCodexAuthJSON(raw)
}

func (m Manager) SaveCodexAuthJSON(raw json.RawMessage) (ProviderStatus, error) {
	dest := m.auth.CodexAuthFile
	if strings.TrimSpace(dest) == "" {
		dest = config.DefaultCodexAuthFile()
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ProviderStatus{}, fmt.Errorf("invalid Codex auth JSON: %w", err)
	}
	if !codexauth.HasTokens(payload) {
		return ProviderStatus{}, fmt.Errorf("Codex auth JSON does not contain OAuth tokens")
	}
	if err := writeAuthPayload(dest, payload); err != nil {
		return ProviderStatus{}, err
	}
	return NewManagerFromAuthSettings(config.AuthSettings{CodexAuthFile: dest}).CodexStatus(), nil
}

func BillyDotenvPath() string {
	return filepath.Join(config.BillyHomeDir(), ".env")
}

func upsertDotenvValue(path, key, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var lines []string
	if body, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	}
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, _, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(name) == key {
			lines[i] = key + "=" + shellQuoteEnv(value)
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, key+"="+shellQuoteEnv(value))
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func shellQuoteEnv(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\r\"'#$`\\") {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
	}
	return value
}

func dotenvValue(path, key string) (string, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`), true
	}
	return "", false
}

func credentialFileValue(path string, keys ...string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", false
	}
	for _, key := range keys {
		for _, candidate := range []string{key, strings.ToLower(key), strings.ToUpper(key)} {
			if value := codexauth.StringField(payload, candidate); value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func readAuthPayload(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Codex auth file %s: %w", path, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse Codex auth file %s: %w", path, err)
	}
	return payload, nil
}

func writeAuthPayload(path string, payload map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

func samePath(a, b string) bool {
	aa, err := filepath.Abs(a)
	if err != nil {
		aa = a
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		bb = b
	}
	return aa == bb
}
