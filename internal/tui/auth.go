package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/credentials"
)

type authResultMsg struct {
	text string
	err  error
}

type authStatusResponse = credentials.Status

func (m *Model) handleAuthCommand(arg string) (bool, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "", "deepseek", "api", "key", "deepseek api key":
		m.authInputProvider = "deepseek"
		m.textarea.Placeholder = "Paste DeepSeek API key. Enter saves, Esc cancels."
		m.textarea.SetValue("")
		m.status = "paste DeepSeek API key"
		return true, nil
	case "codex", "oauth", "chatgpt", "codex oauth":
		m.status = "importing Codex OAuth"
		return true, m.authCodexImportCmd()
	case "status", "list":
		m.status = "loading auth status"
		return true, m.authStatusCmd()
	default:
		m.status = "unknown auth action " + arg
		return false, nil
	}
}

func (m *Model) cancelAuthInput() {
	m.authInputProvider = ""
	m.textarea.Placeholder = defaultTextareaPlaceholder
}

func (m Model) authSaveCmd(providerName, secret string) tea.Cmd {
	return func() tea.Msg {
		var (
			text string
			err  error
		)
		switch providerName {
		case "deepseek":
			text, err = m.saveDeepSeekCredential(secret)
		default:
			err = fmt.Errorf("unknown auth provider %s", providerName)
		}
		return authResultMsg{text: text, err: err}
	}
}

func (m Model) authCodexImportCmd() tea.Cmd {
	return func() tea.Msg {
		text, err := m.importCodexCredential()
		return authResultMsg{text: text, err: err}
	}
}

func (m Model) authStatusCmd() tea.Cmd {
	return func() tea.Msg {
		status, err := m.loadAuthStatus()
		if err != nil {
			return authResultMsg{err: err}
		}
		return authResultMsg{text: formatAuthStatus(status)}
	}
}

func (m Model) saveDeepSeekCredential(apiKey string) (string, error) {
	if m.gatewayURL == "" {
		status, err := credentials.NewManagerFromAuthSettings(m.authSettings).SaveDeepSeekAPIKey(apiKey)
		if err != nil {
			return "", err
		}
		return "DeepSeek API key saved\n" + formatProviderStatus("deepseek", status), nil
	}
	body, _ := json.Marshal(map[string]string{"api_key": apiKey})
	var out struct {
		DeepSeek credentials.ProviderStatus `json:"deepseek"`
	}
	if err := m.gatewayJSON(http.MethodPost, "/v1/auth/deepseek", body, &out); err != nil {
		return "", err
	}
	return "DeepSeek API key saved\n" + formatProviderStatus("deepseek", out.DeepSeek), nil
}

func (m Model) importCodexCredential() (string, error) {
	if m.gatewayURL == "" {
		status, err := credentials.NewManagerFromAuthSettings(m.authSettings).ImportCodexAuth("")
		if err != nil {
			return "", err
		}
		return "Codex OAuth imported\n" + formatProviderStatus("codex", status), nil
	}
	var out struct {
		Codex credentials.ProviderStatus `json:"codex"`
	}
	if err := m.gatewayJSON(http.MethodPost, "/v1/auth/codex/import", []byte(`{}`), &out); err != nil {
		return "", err
	}
	return "Codex OAuth imported\n" + formatProviderStatus("codex", out.Codex), nil
}

func (m Model) loadAuthStatus() (authStatusResponse, error) {
	if m.gatewayURL == "" {
		return credentials.CurrentStatusFromAuthSettings(m.authSettings), nil
	}
	var out authStatusResponse
	if err := m.gatewayJSON(http.MethodGet, "/v1/auth/status", nil, &out); err != nil {
		return authStatusResponse{}, err
	}
	return out, nil
}

func formatAuthStatus(status credentials.Status) string {
	return strings.Join([]string{
		formatProviderStatus("deepseek", status.DeepSeek),
		formatProviderStatus("codex", status.Codex),
	}, "\n")
}

func formatProviderStatus(name string, status credentials.ProviderStatus) string {
	state := "missing"
	if status.Configured {
		state = "configured"
	}
	parts := []string{name + ": " + state}
	if status.Mode != "" {
		parts = append(parts, "mode "+status.Mode)
	}
	if status.Refresh != "" {
		parts = append(parts, "refresh "+status.Refresh)
	}
	if status.AccountID != "" {
		parts = append(parts, "account "+status.AccountID)
	}
	if status.ExpiresAt != "" {
		parts = append(parts, "expires "+status.ExpiresAt)
	}
	if status.Path != "" {
		parts = append(parts, "path "+status.Path)
	}
	if status.Source != "" && status.Source != status.Path {
		parts = append(parts, "source "+status.Source)
	}
	return strings.Join(parts, "\n  ")
}
