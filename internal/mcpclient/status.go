package mcpclient

import (
	"net/url"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

func cloneStatus(status ServerStatus) ServerStatus {
	out := status
	out.TransportState = normalizeTransportState(out)
	out.CatalogState = normalizeCatalogState(out)
	out.Diagnostics = cloneStatusDiagnostics(out.Diagnostics)
	out.URL = redactURLCredentials(out.URL)
	out.LastError = secrets.Redact(out.LastError)
	out.StderrTail = secrets.Redact(out.StderrTail)
	out.Error = secrets.Redact(out.Error)
	if status.StartedAt != nil {
		value := *status.StartedAt
		out.StartedAt = &value
	}
	if status.LastConnectedAt != nil {
		value := *status.LastConnectedAt
		out.LastConnectedAt = &value
	}
	if status.LastEventAt != nil {
		value := *status.LastEventAt
		out.LastEventAt = &value
	}
	if status.LastErrorAt != nil {
		value := *status.LastErrorAt
		out.LastErrorAt = &value
	}
	if status.NextRetryAt != nil {
		value := *status.NextRetryAt
		out.NextRetryAt = &value
	}
	return out
}

func cloneStatusDiagnostics(in []StatusDiagnostic) []StatusDiagnostic {
	if len(in) == 0 {
		return nil
	}
	out := make([]StatusDiagnostic, 0, len(in))
	for _, diag := range in {
		diag.Code = strings.TrimSpace(diag.Code)
		diag.Severity = strings.TrimSpace(diag.Severity)
		diag.Message = secrets.Redact(strings.TrimSpace(diag.Message))
		if diag.Code == "" && diag.Message == "" {
			continue
		}
		out = append(out, diag)
	}
	return out
}

func statusDiagnostic(code, severity, message string) StatusDiagnostic {
	return StatusDiagnostic{
		Code:     strings.TrimSpace(code),
		Severity: strings.TrimSpace(severity),
		Message:  secrets.Redact(strings.TrimSpace(message)),
	}
}

func normalizeTransportState(status ServerStatus) string {
	if trimmed := strings.TrimSpace(status.TransportState); trimmed != "" {
		return trimmed
	}
	if !status.Enabled {
		return mcpTransportStateDisabled
	}
	switch strings.TrimSpace(status.State) {
	case mcpStateConnected, mcpStateReconnected:
		return mcpTransportStateConnected
	case mcpStateRestarting:
		return mcpTransportStateRestarting
	case mcpStateFailed:
		return mcpTransportStateFailed
	case mcpStateCrashed:
		return mcpTransportStateCrashed
	case mcpStateUnsupported:
		return mcpTransportStateUnsupported
	case mcpStateDisabled:
		return mcpTransportStateDisabled
	}
	if status.Connected {
		return mcpTransportStateConnected
	}
	if status.Error != "" || status.LastError != "" {
		return mcpTransportStateFailed
	}
	return mcpTransportStateDisconnected
}

func normalizeCatalogState(status ServerStatus) string {
	if trimmed := strings.TrimSpace(status.CatalogState); trimmed != "" {
		return trimmed
	}
	if !status.Enabled {
		return mcpCatalogStateDisabled
	}
	if normalizeTransportState(status) == mcpTransportStateUnsupported {
		return mcpCatalogStateUnsupported
	}
	if !status.Connected {
		return mcpCatalogStateDisconnected
	}
	if status.ToolCount == 0 {
		return mcpCatalogStateConnectedNoTools
	}
	return mcpCatalogStateReady
}

func redactURLCredentials(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword("redacted", "redacted")
	} else {
		u.User = url.User("redacted")
	}
	return u.String()
}

func cloneCatalogChange(change CatalogChange) CatalogChange {
	out := change
	out.Collisions = append([]string(nil), change.Collisions...)
	return out
}

func clonePrompts(prompts []Prompt) []Prompt {
	if len(prompts) == 0 {
		return nil
	}
	out := make([]Prompt, 0, len(prompts))
	for _, prompt := range prompts {
		prompt.Arguments = append([]PromptArgument(nil), prompt.Arguments...)
		out = append(out, prompt)
	}
	return out
}

func mcpStatusChanged(before, after ServerStatus) bool {
	return before.Connected != after.Connected ||
		before.State != after.State ||
		before.TransportState != after.TransportState ||
		before.CatalogState != after.CatalogState ||
		before.ToolCount != after.ToolCount ||
		before.PID != after.PID ||
		before.LastError != after.LastError ||
		before.Error != after.Error ||
		before.RetryCount != after.RetryCount ||
		before.RestartCount != after.RestartCount ||
		before.RetryBackoffMS != after.RetryBackoffMS ||
		statusDiagnosticsKey(before.Diagnostics) != statusDiagnosticsKey(after.Diagnostics)
}

func statusDiagnosticsKey(in []StatusDiagnostic) string {
	if len(in) == 0 {
		return ""
	}
	var parts []string
	for _, diag := range in {
		parts = append(parts, diag.Code+"|"+diag.Severity+"|"+diag.Message)
	}
	return strings.Join(parts, "\n")
}

func mcpReconnectBackoff(retryCount int) time.Duration {
	if retryCount <= 1 {
		return time.Second
	}
	backoff := time.Second
	for i := 1; i < retryCount; i++ {
		backoff *= 2
		if backoff >= maxMCPReconnectBackoff {
			return maxMCPReconnectBackoff
		}
	}
	return backoff
}

func redactServerError(server config.MCPServer, err error) string {
	if err == nil {
		return ""
	}
	return secrets.Redact(err.Error(), serverSecrets(server)...)
}

func timePtr(t time.Time) *time.Time {
	return &t
}
