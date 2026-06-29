package codexauth

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func Claims(token string) map[string]any {
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
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil
	}
	return claims
}

func StringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return strings.TrimSpace(value)
}

func AccountIDFromJWT(token string) string {
	return AccountIDFromClaims(Claims(token))
}

func AccountIDFromClaims(claims map[string]any) string {
	if len(claims) == 0 {
		return ""
	}
	for _, key := range []string{
		"chatgpt_account_id",
		"https://api.openai.com/auth.chatgpt_account_id",
	} {
		if value := StringField(claims, key); value != "" {
			return value
		}
	}
	if auth, _ := claims["https://api.openai.com/auth"].(map[string]any); auth != nil {
		if value := StringField(auth, "chatgpt_account_id"); value != "" {
			return value
		}
	}
	if orgs, _ := claims["organizations"].([]any); len(orgs) > 0 {
		if org, _ := orgs[0].(map[string]any); org != nil {
			if value := StringField(org, "id"); value != "" {
				return value
			}
		}
	}
	return ""
}

func FedRAMPFromClaims(claims map[string]any) bool {
	if len(claims) == 0 {
		return false
	}
	if value, ok := claims["chatgpt_account_is_fedramp"].(bool); ok {
		return value
	}
	if auth, _ := claims["https://api.openai.com/auth"].(map[string]any); auth != nil {
		if value, ok := auth["chatgpt_account_is_fedramp"].(bool); ok {
			return value
		}
	}
	return false
}

func ExpirationFromJWT(token string) time.Time {
	return ExpirationFromClaims(Claims(token))
}

func ExpirationFromClaims(claims map[string]any) time.Time {
	if len(claims) == 0 {
		return time.Time{}
	}
	switch exp := claims["exp"].(type) {
	case float64:
		return time.Unix(int64(exp), 0)
	case json.Number:
		n, _ := exp.Int64()
		if n > 0 {
			return time.Unix(n, 0)
		}
	case string:
		n, _ := strconv.ParseInt(exp, 10, 64)
		if n > 0 {
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}

func AuthMode(payload map[string]any) string {
	if mode := StringField(payload, "auth_mode"); mode != "" {
		return mode
	}
	if token := StringField(payload, "personal_access_token"); token != "" {
		return "personalAccessToken"
	}
	if _, ok := payload["bedrock_api_key"]; ok {
		return "bedrockApiKey"
	}
	if key := StringField(payload, "OPENAI_API_KEY"); key != "" {
		return "apikey"
	}
	return "chatgpt"
}

func HasTokens(payload map[string]any) bool {
	if strings.TrimSpace(StringField(payload, "personal_access_token")) != "" {
		return true
	}
	tokens, _ := payload["tokens"].(map[string]any)
	if tokens == nil {
		return false
	}
	return StringField(tokens, "access_token") != "" || StringField(tokens, "refresh_token") != ""
}

func RefreshStatus(accessToken, refreshToken string, expiresAt time.Time, personalAccessToken bool) string {
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
