// Portions of this file are adapted from OpenAI Codex.
// Original project: https://github.com/openai/codex
// Copyright 2025 OpenAI
// Licensed under the Apache License, Version 2.0.

package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

type codexAuth struct {
	AccessToken  string
	RefreshToken string
	AccountID    string
	ExpiresAt    time.Time
	LastRefresh  time.Time
	FedRAMP      bool
	PAT          bool
	AuthFile     string
	filePayload  map[string]any
}

func loadCodexAuth(ctx context.Context, cfg config.Config, client *http.Client) (*codexAuth, error) {
	resolved := credentials.NewManager(cfg).ResolveCodexAuth()
	if token := strings.TrimSpace(resolved.AccessToken.Value); token != "" {
		auth := &codexAuth{
			AccessToken: token,
			AccountID:   strings.TrimSpace(resolved.AccountID.Value),
		}
		if strings.HasPrefix(auth.AccessToken, "at-") {
			if err := auth.hydratePAT(ctx, cfg, client); err != nil {
				return nil, err
			}
			return auth, nil
		}
		claims := jwtClaims(auth.AccessToken)
		if auth.AccountID == "" {
			auth.AccountID = accountIDFromClaims(claims)
		}
		auth.ExpiresAt = expirationFromClaims(claims)
		auth.FedRAMP = fedRAMPFromClaims(claims)
		return auth, nil
	}

	path := resolved.AuthFile
	if path == "" {
		return nil, fmt.Errorf("missing %s or Codex auth file; run `codex login` or set FAST_AGENT_CODEX_AUTH_FILE", credentials.CodexAccessTokenEnv)
	}
	auth, err := readCodexAuthFile(ctx, cfg, client, path)
	if err != nil {
		return nil, err
	}
	if auth.AccessToken == "" && auth.RefreshToken == "" {
		return nil, fmt.Errorf("Codex auth file %s does not contain usable tokens", path)
	}
	if auth.AccessToken != "" && !auth.needsRefresh(time.Now()) {
		return auth, nil
	}
	if auth.RefreshToken == "" {
		return nil, fmt.Errorf("Codex access token is expired and %s has no refresh token", path)
	}
	if err := auth.refresh(ctx, cfg, client); err != nil {
		return nil, err
	}
	return auth, nil
}

func (a *codexAuth) needsRefresh(now time.Time) bool {
	if a.PAT {
		return false
	}
	if a.AccessToken == "" {
		return true
	}
	if a.ExpiresAt.IsZero() {
		return !a.LastRefresh.IsZero() && now.After(a.LastRefresh.Add(8*24*time.Hour))
	}
	return now.After(a.ExpiresAt.Add(-5 * time.Minute))
}

func (a *codexAuth) refresh(ctx context.Context, cfg config.Config, client *http.Client) error {
	body, err := json.Marshal(map[string]string{
		"client_id":     cfg.CodexClientID,
		"grant_type":    "refresh_token",
		"refresh_token": a.RefreshToken,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.CodexRefreshURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Codex token refresh failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Codex token refresh HTTP %d: %s", resp.StatusCode, secrets.Redact(strings.TrimSpace(string(respBody)), a.RefreshToken, a.AccessToken))
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return fmt.Errorf("invalid Codex token refresh JSON: %w", err)
	}
	if out.AccessToken == "" {
		return fmt.Errorf("Codex token refresh returned empty access token")
	}
	a.AccessToken = out.AccessToken
	if out.RefreshToken != "" {
		a.RefreshToken = out.RefreshToken
	}
	claims := jwtClaims(a.AccessToken)
	if out.IDToken != "" {
		idClaims := jwtClaims(out.IDToken)
		if idAccount := accountIDFromClaims(idClaims); idAccount != "" {
			a.AccountID = idAccount
		}
		if idFedRAMP := fedRAMPFromClaims(idClaims); idFedRAMP {
			a.FedRAMP = true
		}
	}
	if a.AccountID == "" {
		a.AccountID = accountIDFromClaims(claims)
	}
	if !a.FedRAMP {
		a.FedRAMP = fedRAMPFromClaims(claims)
	}
	a.ExpiresAt = expirationFromClaims(claims)
	if a.ExpiresAt.IsZero() && out.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	a.LastRefresh = time.Now().UTC()
	if a.AuthFile != "" {
		a.updateFilePayload(out.IDToken)
		if err := writeCodexAuthFile(a.AuthFile, a.filePayload); err != nil {
			return err
		}
	}
	return nil
}

func (a *codexAuth) updateFilePayload(idToken string) {
	if a.filePayload == nil {
		a.filePayload = map[string]any{}
	}
	tokens, _ := a.filePayload["tokens"].(map[string]any)
	if tokens == nil {
		tokens = map[string]any{}
	}
	tokens["access_token"] = a.AccessToken
	tokens["refresh_token"] = a.RefreshToken
	if a.AccountID != "" {
		tokens["account_id"] = a.AccountID
	}
	if idToken != "" {
		tokens["id_token"] = idToken
	}
	a.filePayload["tokens"] = tokens
	a.filePayload["last_refresh"] = a.LastRefresh.Format(time.RFC3339)
}

func readCodexAuthFile(ctx context.Context, cfg config.Config, client *http.Client, path string) (*codexAuth, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Codex auth file %s: %w", path, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse Codex auth file %s: %w", path, err)
	}
	auth := &codexAuth{AuthFile: path, filePayload: payload}
	authMode := resolvedCodexAuthMode(payload)
	switch authMode {
	case "apikey", "bedrockApiKey":
		return nil, fmt.Errorf("Codex auth file %s uses %s; use FAST_AGENT_PROVIDER=deepseek/OpenAI API key path for API-key auth", path, authMode)
	case "agentIdentity":
		return nil, fmt.Errorf("Codex auth file %s uses agentIdentity; billyharness does not implement AgentAssertion signing yet", path)
	}
	if lastRefresh := stringField(payload, "last_refresh"); lastRefresh != "" {
		if parsed, err := time.Parse(time.RFC3339, lastRefresh); err == nil {
			auth.LastRefresh = parsed
		}
	}
	if token, _ := payload["personal_access_token"].(string); strings.TrimSpace(token) != "" {
		auth.AccessToken = strings.TrimSpace(token)
		auth.PAT = true
	}
	if tokens, _ := payload["tokens"].(map[string]any); tokens != nil {
		auth.AccessToken = stringField(tokens, "access_token")
		auth.RefreshToken = stringField(tokens, "refresh_token")
		auth.AccountID = stringField(tokens, "account_id")
		idClaims := jwtClaims(stringField(tokens, "id_token"))
		if auth.AccountID == "" {
			auth.AccountID = accountIDFromClaims(idClaims)
		}
		auth.FedRAMP = fedRAMPFromClaims(idClaims)
	}
	if auth.PAT {
		if err := auth.hydratePAT(ctx, cfg, client); err != nil {
			return nil, err
		}
		return auth, nil
	}
	claims := jwtClaims(auth.AccessToken)
	if auth.AccountID == "" {
		auth.AccountID = accountIDFromClaims(claims)
	}
	if !auth.FedRAMP {
		auth.FedRAMP = fedRAMPFromClaims(claims)
	}
	auth.ExpiresAt = expirationFromClaims(claims)
	return auth, nil
}

func resolvedCodexAuthMode(payload map[string]any) string {
	if mode := stringField(payload, "auth_mode"); mode != "" {
		return mode
	}
	if token := stringField(payload, "personal_access_token"); token != "" {
		return "personalAccessToken"
	}
	if _, ok := payload["bedrock_api_key"]; ok {
		return "bedrockApiKey"
	}
	if key := stringField(payload, "OPENAI_API_KEY"); key != "" {
		return "apikey"
	}
	return "chatgpt"
}

func (a *codexAuth) hydratePAT(ctx context.Context, cfg config.Config, client *http.Client) error {
	a.PAT = true
	endpoint := strings.TrimRight(cfg.CodexAuthAPIBaseURL, "/") + "/v1/user-auth-credential/whoami"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("personal access token metadata request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("personal access token metadata HTTP %d: %s", resp.StatusCode, secrets.Redact(strings.TrimSpace(string(body)), a.AccessToken))
	}
	var meta struct {
		AccountID string `json:"chatgpt_account_id"`
		FedRAMP   bool   `json:"chatgpt_account_is_fedramp"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return fmt.Errorf("invalid personal access token metadata JSON: %w", err)
	}
	if meta.AccountID == "" {
		return fmt.Errorf("personal access token metadata did not include chatgpt_account_id")
	}
	a.AccountID = meta.AccountID
	a.FedRAMP = meta.FedRAMP
	return nil
}

func writeCodexAuthFile(path string, payload map[string]any) error {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".auth.json.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, bytes.NewReader(body)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func codexAuthPath(cfg config.Config) string {
	return credentials.NewManager(cfg).CodexAuthFilePath()
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
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil
	}
	return claims
}

func accountIDFromClaims(claims map[string]any) string {
	if len(claims) == 0 {
		return ""
	}
	for _, key := range []string{
		"chatgpt_account_id",
		"https://api.openai.com/auth.chatgpt_account_id",
	} {
		if value := stringField(claims, key); value != "" {
			return value
		}
	}
	if auth, _ := claims["https://api.openai.com/auth"].(map[string]any); auth != nil {
		if value := stringField(auth, "chatgpt_account_id"); value != "" {
			return value
		}
	}
	if orgs, _ := claims["organizations"].([]any); len(orgs) > 0 {
		if org, _ := orgs[0].(map[string]any); org != nil {
			if value := stringField(org, "id"); value != "" {
				return value
			}
		}
	}
	return ""
}

func fedRAMPFromClaims(claims map[string]any) bool {
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

func expirationFromClaims(claims map[string]any) time.Time {
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

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	value, _ := m[key].(string)
	return strings.TrimSpace(value)
}
