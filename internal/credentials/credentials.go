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

type ProviderStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	Path       string `json:"path,omitempty"`
	AccountID  string `json:"account_id,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	Mode       string `json:"mode,omitempty"`
}

type Status struct {
	DeepSeek ProviderStatus `json:"deepseek"`
	Codex    ProviderStatus `json:"codex"`
}

func CurrentStatus(cfg config.Config) Status {
	return Status{
		DeepSeek: DeepSeekStatus(),
		Codex:    CodexStatus(cfg),
	}
}

func DeepSeekStatus() ProviderStatus {
	if value := strings.TrimSpace(os.Getenv(deepSeekKeyEnv)); value != "" {
		return ProviderStatus{Configured: true, Source: "env:" + deepSeekKeyEnv}
	}
	path := BillyDotenvPath()
	if value, ok := dotenvValue(path, deepSeekKeyEnv); ok && strings.TrimSpace(value) != "" {
		return ProviderStatus{Configured: true, Source: path, Path: path}
	}
	if value, ok := config.LookupEnvOrDotenv(deepSeekKeyEnv); ok && strings.TrimSpace(value) != "" {
		return ProviderStatus{Configured: true, Source: ".env"}
	}
	return ProviderStatus{Path: path}
}

func SaveDeepSeekAPIKey(apiKey string) (ProviderStatus, error) {
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
	path := cfg.CodexAuthFile
	if strings.TrimSpace(path) == "" {
		path = config.DefaultCodexAuthFile()
	}
	status := ProviderStatus{Path: path}
	payload, err := readAuthPayload(path)
	if err != nil {
		return status
	}
	status.Configured = true
	status.Source = path
	status.Mode = stringField(payload, "auth_mode")
	if token := stringField(payload, "personal_access_token"); token != "" {
		status.Mode = "personalAccessToken"
		status.AccountID = stringField(payload, "chatgpt_account_id")
		return status
	}
	tokens, _ := payload["tokens"].(map[string]any)
	if tokens == nil {
		return status
	}
	if status.AccountID = stringField(tokens, "account_id"); status.AccountID == "" {
		status.AccountID = accountIDFromJWT(stringField(tokens, "id_token"))
	}
	if status.AccountID == "" {
		status.AccountID = accountIDFromJWT(stringField(tokens, "access_token"))
	}
	if exp := expirationFromJWT(stringField(tokens, "access_token")); !exp.IsZero() {
		status.ExpiresAt = exp.UTC().Format(time.RFC3339)
	}
	return status
}

func ImportCodexAuth(cfg config.Config, sourcePath string) (ProviderStatus, error) {
	dest := cfg.CodexAuthFile
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
	status := CodexStatus(config.Config{CodexAuthFile: dest})
	status.Source = sourcePath
	status.Path = dest
	return status, nil
}

func SaveCodexAuthJSON(cfg config.Config, raw json.RawMessage) (ProviderStatus, error) {
	dest := cfg.CodexAuthFile
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
	return CodexStatus(config.Config{CodexAuthFile: dest}), nil
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
