package gateway

import (
	"encoding/json"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func publicConfigStatusResponse(resolved config.ResolvedConfig) ConfigStatusResponse {
	return ConfigStatusResponse{
		Config:      publicConfigMap(resolved.SanitizedValues()),
		Values:      publicConfigValues(resolved.SanitizedValues()),
		Diagnostics: publicConfigDiagnostics(resolved),
		Warnings:    append([]string(nil), resolved.Warnings...),
	}
}

func publicConfigMap(values []config.ResolvedValue) map[string]any {
	out := make(map[string]any, len(values))
	for _, value := range values {
		out[value.Key] = publicConfigValue(value.Key, value.Value)
	}
	return out
}

func publicConfigValues(values []config.ResolvedValue) []gatewayapi.ConfigStatusValue {
	out := make([]gatewayapi.ConfigStatusValue, 0, len(values))
	for _, value := range values {
		redacted := value.Redacted || publicConfigRedactedKey(value.Key)
		out = append(out, gatewayapi.ConfigStatusValue{
			Key:      value.Key,
			Value:    publicConfigValue(value.Key, value.Value),
			Source:   value.Source,
			Redacted: redacted,
			Warning:  value.Warning,
			Error:    value.Error,
		})
	}
	return out
}

func publicConfigDiagnostics(resolved config.ResolvedConfig) map[string]any {
	body, err := json.Marshal(resolved.DiagnosticSnapshot())
	if err != nil {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil
	}
	sanitizePublicConfigJSON(decoded)
	return decoded
}

func sanitizePublicConfigJSON(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			normalized := strings.ToLower(strings.TrimSpace(key))
			switch {
			case normalized == "source_key" || normalized == "source_path":
				delete(v, key)
			case publicConfigRedactedKey(normalized):
				v[key] = "[redacted]"
			default:
				sanitizePublicConfigJSON(item)
			}
		}
	case []any:
		for _, item := range v {
			sanitizePublicConfigJSON(item)
		}
	}
}

func publicConfigValue(key string, value any) any {
	if publicConfigRedactedKey(key) {
		return "[redacted]"
	}
	return value
}

func publicConfigRedactedKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "api_key_env",
		"credential_file",
		"codex_auth_file",
		"web_tavily_api_key_env",
		"web_exa_api_key_env",
		"web_hermes_env_files",
		"diagnostics_config_files",
		"mcp_config_files",
		"hooks_config_files":
		return true
	default:
		return false
	}
}
