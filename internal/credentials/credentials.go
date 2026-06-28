package credentials

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	cfg config.Config
}

func NewManager(cfg config.Config) Manager {
	if strings.TrimSpace(cfg.APIKeyEnv) == "" {
		cfg.APIKeyEnv = deepSeekKeyEnv
	}
	if strings.TrimSpace(cfg.CredentialFile) == "" {
		cfg.CredentialFile = config.DefaultCredentialFile()
	}
	if strings.TrimSpace(cfg.CodexAuthFile) == "" {
		cfg.CodexAuthFile = config.DefaultCodexAuthFile()
	}
	return Manager{cfg: cfg}
}

func CurrentStatus(cfg config.Config) Status {
	return NewManager(cfg).Status()
}

func (m Manager) Status() Status {
	return Status{
		DeepSeek: m.DeepSeekStatus(),
		Codex:    m.CodexStatus(),
	}
}

func DeepSeekStatus() ProviderStatus {
	return NewManager(config.Config{APIKeyEnv: deepSeekKeyEnv}).DeepSeekStatus()
}

func (m Manager) DeepSeekStatus() ProviderStatus {
	secret, err := m.ResolveDeepSeekAPIKey()
	if err != nil {
		return ProviderStatus{Path: BillyDotenvPath()}
	}
	return ProviderStatus{Configured: true, Source: secret.Source, Path: secret.Path}
}

func (m Manager) ResolveDeepSeekAPIKey() (SecretValue, error) {
	envKey := strings.TrimSpace(m.cfg.APIKeyEnv)
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
	if path := strings.TrimSpace(m.cfg.CodexAuthFile); path != "" {
		return path
	}
	return config.DefaultCodexAuthFile()
}

func (m Manager) CredentialFilePath() string {
	if path := strings.TrimSpace(m.cfg.CredentialFile); path != "" {
		return path
	}
	return config.DefaultCredentialFile()
}

func SaveDeepSeekAPIKey(apiKey string) (ProviderStatus, error) {
	return NewManager(config.Config{APIKeyEnv: deepSeekKeyEnv}).SaveDeepSeekAPIKey(apiKey)
}

func (m Manager) SaveDeepSeekAPIKey(apiKey string) (ProviderStatus, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ProviderStatus{}, fmt.Errorf("DeepSeek API key is empty")
	}
	if !strings.HasPrefix(apiKey, "sk-") {
		return ProviderStatus{}, fmt.Errorf("DeepSeek API key should start with sk-")
	}
	path := BillyDotenvPath()
	if err := upsertDotenvValue(path, deepSeekKeyEnv, apiKey); err != nil {
		return ProviderStatus{}, err
	}
	return ProviderStatus{Configured: true, Source: path, Path: path}, nil
}

func CodexStatus(cfg config.Config) ProviderStatus {
	return NewManager(cfg).CodexStatus()
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
	status.Mode = stringField(payload, "auth_mode")
	if token := stringField(payload, "personal_access_token"); token != "" {
		status.Configured = true
		status.Source = path
		status.Mode = "personalAccessToken"
		status.AccountID = stringField(payload, "chatgpt_account_id")
		status.Refresh = "not_required"
		return status
	}
	tokens, _ := payload["tokens"].(map[string]any)
	if tokens == nil {
		return status
	}
	accessToken := stringField(tokens, "access_token")
	refreshToken := stringField(tokens, "refresh_token")
	if accessToken == "" && refreshToken == "" {
		return status
	}
	status.Configured = true
	status.Source = path
	if status.AccountID = stringField(tokens, "account_id"); status.AccountID == "" {
		status.AccountID = accountIDFromJWT(stringField(tokens, "id_token"))
	}
	if status.AccountID == "" {
		status.AccountID = accountIDFromJWT(accessToken)
	}
	exp := expirationFromJWT(accessToken)
	status.Refresh = codexRefreshStatus(accessToken, refreshToken, exp, false)
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
		status.AccountID = accountIDFromJWT(token)
	}
	exp := expirationFromJWT(token)
	status.Refresh = codexRefreshStatus(token, "", exp, false)
	if !exp.IsZero() {
		status.ExpiresAt = exp.UTC().Format(time.RFC3339)
	}
	return status
}

func codexRefreshStatus(accessToken, refreshToken string, expiresAt time.Time, personalAccessToken bool) string {
	if personalAccessToken || strings.HasPrefix(strings.TrimSpace(accessToken), "at-") {
		return "not_required"
	}
	hasAccess := strings.TrimSpace(accessToken) != ""
	hasRefresh := strings.TrimSpace(refreshToken) != ""
	if !hasAccess && hasRefresh {
		return "refresh_required"
	}
	if !hasAccess {
		return ""
	}
	if expiresAt.IsZero() {
		if hasRefresh {
			return "refreshable_unknown_expiry"
		}
		return "unknown"
	}
	now := time.Now()
	if !expiresAt.After(now) {
		if hasRefresh {
			return "refresh_required"
		}
		return "expired"
	}
	if !expiresAt.After(now.Add(5 * time.Minute)) {
		if hasRefresh {
			return "refresh_soon"
		}
		return "expires_soon"
	}
	return "fresh"
}

func ImportCodexAuth(cfg config.Config, sourcePath string) (ProviderStatus, error) {
	return NewManager(cfg).ImportCodexAuth(sourcePath)
}

func (m Manager) ImportCodexAuth(sourcePath string) (ProviderStatus, error) {
	dest := m.cfg.CodexAuthFile
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
	if !hasCodexTokens(payload) {
		return ProviderStatus{}, fmt.Errorf("Codex auth file %s does not contain OAuth tokens", sourcePath)
	}
	if err := writeAuthPayload(dest, payload); err != nil {
		return ProviderStatus{}, err
	}
	status := NewManager(config.Config{CodexAuthFile: dest}).CodexStatus()
	status.Source = sourcePath
	status.Path = dest
	return status, nil
}

func SaveCodexAuthJSON(cfg config.Config, raw json.RawMessage) (ProviderStatus, error) {
	return NewManager(cfg).SaveCodexAuthJSON(raw)
}

func (m Manager) SaveCodexAuthJSON(raw json.RawMessage) (ProviderStatus, error) {
	dest := m.cfg.CodexAuthFile
	if strings.TrimSpace(dest) == "" {
		dest = config.DefaultCodexAuthFile()
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ProviderStatus{}, fmt.Errorf("invalid Codex auth JSON: %w", err)
	}
	if !hasCodexTokens(payload) {
		return ProviderStatus{}, fmt.Errorf("Codex auth JSON does not contain OAuth tokens")
	}
	if err := writeAuthPayload(dest, payload); err != nil {
		return ProviderStatus{}, err
	}
	return NewManager(config.Config{CodexAuthFile: dest}).CodexStatus(), nil
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
			if value := stringField(payload, candidate); value != "" {
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

func hasCodexTokens(payload map[string]any) bool {
	if strings.TrimSpace(stringField(payload, "personal_access_token")) != "" {
		return true
	}
	tokens, _ := payload["tokens"].(map[string]any)
	if tokens == nil {
		return false
	}
	return stringField(tokens, "access_token") != "" || stringField(tokens, "refresh_token") != ""
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

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return strings.TrimSpace(value)
}

func accountIDFromJWT(token string) string {
	claims := jwtClaims(token)
	if value := stringField(claims, "chatgpt_account_id"); value != "" {
		return value
	}
	if auth, _ := claims["https://api.openai.com/auth"].(map[string]any); auth != nil {
		return stringField(auth, "chatgpt_account_id")
	}
	return ""
}

func expirationFromJWT(token string) time.Time {
	claims := jwtClaims(token)
	switch exp := claims["exp"].(type) {
	case float64:
		return time.Unix(int64(exp), 0)
	case string:
		if parsed, err := strconv.ParseInt(exp, 10, 64); err == nil && parsed > 0 {
			return time.Unix(parsed, 0)
		}
	}
	return time.Time{}
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 || parts[1] == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return nil
	}
	var claims map[string]any
	_ = json.Unmarshal(raw, &claims)
	return claims
}
