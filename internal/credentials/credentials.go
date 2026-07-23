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
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

const deepSeekKeyEnv = "DEEPSEEK_API_KEY"

const (
	CodexAccessTokenEnv = "CODEX_ACCESS_TOKEN"
	CodexAccountIDEnv   = "CODEX_CHATGPT_ACCOUNT_ID"
)

type ProviderStatus struct {
	Configured bool   `json:"configured"`
	Provider   string `json:"provider,omitempty"`
	AuthType   string `json:"auth_type,omitempty"`
	Status     string `json:"status,omitempty"`
	Credential string `json:"credential,omitempty"`
	Source     string `json:"source,omitempty"`
	Provenance string `json:"provenance,omitempty"`
	Path       string `json:"path,omitempty"`
	AccountID  string `json:"account_id,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Refresh    string `json:"refresh_status,omitempty"`
}

type Status struct {
	DeepSeek       ProviderStatus `json:"deepseek"`
	Qwen           ProviderStatus `json:"qwen"`
	Codex          ProviderStatus `json:"codex"`
	ActiveProvider string         `json:"active_provider,omitempty"`
	ActiveModel    string         `json:"active_model,omitempty"`
	CostMode       string         `json:"cost_mode,omitempty"`
}

type SecretValue struct {
	Value      string
	Source     string
	Provenance string
	Path       string
	EnvVar     string
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

func CurrentStatusForRuntime(auth config.AuthSettings, provider, model string) Status {
	manager := NewManagerFromAuthSettings(auth)
	baselineAuth := auth
	baselineAuth.APIKeyEnv = deepSeekKeyEnv
	status := NewManagerFromAuthSettings(baselineAuth).Status()
	provider = modelinfo.ProviderForModel(modelinfo.NormalizeAlias(model), provider)
	switch provider {
	case modelinfo.ProviderDeepSeek:
		status.DeepSeek = manager.ProviderAPIKeyStatus(provider)
	case modelinfo.ProviderQwen:
		status.Qwen = manager.ProviderAPIKeyStatus(provider)
	}
	return RuntimeStatus(status, provider, model)
}

func RuntimeStatus(status Status, provider, model string) Status {
	model = modelinfo.NormalizeAlias(model)
	provider = modelinfo.ProviderForModel(model, provider)
	status.ActiveProvider = provider
	status.ActiveModel = model
	status.CostMode = costModeForRuntime(provider, model)
	return status
}

func (m Manager) Status() Status {
	deepSeekManager := m
	if strings.TrimSpace(m.auth.APIKeyEnv) == modelinfo.Provider(modelinfo.ProviderQwen).APIKeyEnv {
		deepSeekManager = m.managerForProvider(modelinfo.ProviderDeepSeek)
	}
	return Status{
		DeepSeek: deepSeekManager.DeepSeekStatus(),
		Qwen:     m.managerForProvider(modelinfo.ProviderQwen).ProviderAPIKeyStatus(modelinfo.ProviderQwen),
		Codex:    m.CodexStatus(),
	}
}

func DeepSeekStatus() ProviderStatus {
	return NewManagerFromAuthSettings(config.AuthSettings{APIKeyEnv: deepSeekKeyEnv}).DeepSeekStatus()
}

func (m Manager) DeepSeekStatus() ProviderStatus {
	return m.ProviderAPIKeyStatus(modelinfo.ProviderDeepSeek)
}

func (m Manager) ProviderAPIKeyStatus(provider string) ProviderStatus {
	provider = modelinfo.NormalizeProvider(provider)
	secret, err := m.ResolveProviderAPIKey(provider)
	if err != nil {
		return classifyProviderStatus(provider, "api-key", ProviderStatus{Path: deepSeekDotenvPath()})
	}
	return classifyProviderStatus(provider, "api-key", ProviderStatus{Configured: true, Source: secret.Source, Provenance: secret.Provenance, Path: secret.Path})
}

func (m Manager) ResolveDeepSeekAPIKey() (SecretValue, error) {
	return m.ResolveProviderAPIKey(modelinfo.ProviderDeepSeek)
}

func (m Manager) ResolveProviderAPIKey(provider string) (SecretValue, error) {
	provider = modelinfo.NormalizeProvider(provider)
	providerInfo := modelinfo.Provider(provider)
	envKey := strings.TrimSpace(m.auth.APIKeyEnv)
	if envKey == "" {
		envKey = providerInfo.APIKeyEnv
	}
	if envKey == "" {
		return SecretValue{Path: deepSeekDotenvPath()}, fmt.Errorf("provider %s does not define an API key environment variable", provider)
	}
	fileKey := strings.ReplaceAll(provider, "-", "_") + "_api_key"
	if secret := m.lookupSecret(envKey, fileKey); strings.TrimSpace(secret.Value) != "" {
		return secret, nil
	}
	if providerInfo.Custom {
		if secret := m.lookupCredentialFileSecret(envKey, "deepseek_api_key"); strings.TrimSpace(secret.Value) != "" {
			return secret, nil
		}
	}
	return SecretValue{Path: deepSeekDotenvPath(), EnvVar: envKey}, fmt.Errorf("missing API key env var %s", envKey)
}

func (m Manager) managerForProvider(provider string) Manager {
	auth := m.auth
	if envKey := modelinfo.Provider(provider).APIKeyEnv; envKey != "" {
		auth.APIKeyEnv = envKey
	}
	return NewManagerFromAuthSettings(auth)
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
	path, err := config.EffectiveWritableDotenvPath()
	if err != nil {
		return ProviderStatus{}, err
	}
	if err := upsertDotenvValue(path, envKey, apiKey); err != nil {
		return ProviderStatus{}, fmt.Errorf("write DeepSeek API key to active dotenv path %s: %w", path, err)
	}
	return classifyProviderStatus("deepseek", "api-key", ProviderStatus{Configured: true, Source: path, Provenance: "dotenv", Path: path}), nil
}

func CodexStatusFromAuthSettings(auth config.AuthSettings) ProviderStatus {
	return NewManagerFromAuthSettings(auth).CodexStatus()
}

func (m Manager) CodexStatus() ProviderStatus {
	resolved := m.ResolveCodexAuth()
	path := resolved.AuthFile
	status := classifyProviderStatus("codex", "codex-oauth", ProviderStatus{Path: path})
	if token := strings.TrimSpace(resolved.AccessToken.Value); token != "" {
		return codexEnvStatus(token, strings.TrimSpace(resolved.AccountID.Value), resolved.AccessToken, path)
	}
	payload, err := readAuthPayload(path)
	if err != nil {
		return status
	}
	status.Mode = codexauth.StringField(payload, "auth_mode")
	if token := codexauth.StringField(payload, "personal_access_token"); token != "" {
		status.Configured = true
		status.Status = "configured"
		status.Credential = "redacted"
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
	status.Status = "configured"
	status.Credential = "redacted"
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
	if value, source, ok := config.LookupEnvOrDotenvSource(envKey); ok && strings.TrimSpace(value) != "" {
		return secretFromConfigEnvSource(strings.TrimSpace(value), source)
	}
	if secret := m.lookupCredentialFileSecret(envKey, fileKey); strings.TrimSpace(secret.Value) != "" {
		return secret
	}
	return SecretValue{EnvVar: envKey, Path: m.CredentialFilePath()}
}

func (m Manager) lookupCredentialFileSecret(envKey, fileKey string) SecretValue {
	path := m.CredentialFilePath()
	value, ok := credentialFileValue(path, fileKey, envKey)
	if !ok || strings.TrimSpace(value) == "" {
		return SecretValue{Path: path, EnvVar: envKey}
	}
	return SecretValue{Value: strings.TrimSpace(value), Source: path, Provenance: "credential_file", Path: path, EnvVar: envKey}
}

func secretFromConfigEnvSource(value string, source config.EnvValueSource) SecretValue {
	switch source.Kind {
	case config.EnvValueSourceEnvironment:
		return SecretValue{Value: value, Source: "env:" + source.Key, Provenance: "env", EnvVar: source.Key}
	case config.EnvValueSourceDotenv:
		return SecretValue{Value: value, Source: source.Path, Provenance: "dotenv", Path: source.Path, EnvVar: source.Key}
	default:
		return SecretValue{Value: value, Source: source.Kind, Provenance: source.Kind, Path: source.Path, EnvVar: source.Key}
	}
}

func codexEnvStatus(token, accountID string, secret SecretValue, path string) ProviderStatus {
	status := classifyProviderStatus("codex", "codex-oauth", ProviderStatus{Configured: true, Source: secret.Source, Provenance: secret.Provenance, Path: path, AccountID: accountID})
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

func classifyProviderStatus(provider, authType string, status ProviderStatus) ProviderStatus {
	status.Provider = strings.TrimSpace(provider)
	status.AuthType = strings.TrimSpace(authType)
	if status.Configured {
		status.Status = "configured"
		status.Credential = "redacted"
	} else {
		status.Status = "missing"
		status.Credential = "missing"
	}
	return status
}

func costModeForRuntime(provider, model string) string {
	if mode := modelinfo.Lookup(model).CostMode; mode != "" {
		return mode
	}
	switch modelinfo.NormalizeProvider(provider) {
	case modelinfo.ProviderOpenAICodex, modelinfo.ProviderQwen:
		return "subscription"
	case modelinfo.ProviderMock:
		return "none"
	case modelinfo.ProviderDeepSeek:
		return "metered"
	default:
		return "metered"
	}
}

func FormatStatusText(status Status) string {
	var parts []string
	if strings.TrimSpace(status.ActiveProvider) != "" || strings.TrimSpace(status.ActiveModel) != "" || strings.TrimSpace(status.CostMode) != "" {
		runtime := []string{}
		if status.ActiveProvider != "" {
			runtime = append(runtime, "provider="+status.ActiveProvider)
		}
		if status.ActiveModel != "" {
			runtime = append(runtime, "model="+status.ActiveModel)
		}
		if status.CostMode != "" {
			runtime = append(runtime, "cost_mode="+status.CostMode)
		}
		parts = append(parts, "runtime: "+strings.Join(runtime, " "))
	}
	parts = append(parts,
		FormatProviderStatusText("deepseek", status.DeepSeek),
		FormatProviderStatusText("qwen", status.Qwen),
		FormatProviderStatusText("codex", status.Codex),
	)
	return strings.Join(parts, "\n\n")
}

func FormatProviderStatusText(name string, status ProviderStatus) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(status.Provider)
	}
	if name == "" {
		name = "provider"
	}
	state := strings.TrimSpace(status.Status)
	if state == "" {
		if status.Configured {
			state = "configured"
		} else {
			state = "missing"
		}
	}
	credential := strings.TrimSpace(status.Credential)
	if credential == "" {
		if status.Configured {
			credential = "redacted"
		} else {
			credential = "missing"
		}
	}
	parts := []string{name + ": " + state}
	if status.AuthType != "" {
		parts = append(parts, "auth="+status.AuthType)
	}
	parts = append(parts, "credential="+credential)
	if status.Mode != "" {
		parts = append(parts, "mode="+status.Mode)
	}
	if status.Refresh != "" {
		parts = append(parts, "refresh="+status.Refresh)
	}
	if status.Provenance != "" {
		parts = append(parts, "provenance="+status.Provenance)
	}
	if status.AccountID != "" {
		parts = append(parts, "account="+status.AccountID)
	}
	if status.ExpiresAt != "" {
		parts = append(parts, "expires="+status.ExpiresAt)
	}
	if status.Path != "" {
		parts = append(parts, "path="+status.Path)
	}
	if status.Source != "" && status.Source != status.Path {
		parts = append(parts, "source="+status.Source)
	}
	return strings.Join(parts, "\n  ")
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
	return config.DefaultDotenvPath()
}

func deepSeekDotenvPath() string {
	return config.EffectiveDotenvPath()
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
