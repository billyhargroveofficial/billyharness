package mcpstatus

import (
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
)

type Response struct {
	ConfigFiles []string                 `json:"config_files"`
	Allowed     []string                 `json:"allowed"`
	Enabled     bool                     `json:"enabled"`
	Servers     []mcpclient.ServerStatus `json:"servers"`
}

func Format(status Response) string {
	configFiles := strings.Join(status.ConfigFiles, ", ")
	if configFiles == "" {
		configFiles = "(none)"
	}
	allowed := strings.Join(status.Allowed, ", ")
	if allowed == "" {
		allowed = "(all)"
	}
	lines := []string{
		"config: " + configFiles,
		"allowed: " + allowed,
		"native: web_search, web_fetch, web_extract, web_crawl",
	}
	if !status.Enabled {
		lines = append(lines, "mcp: disabled")
		return strings.Join(lines, "\n")
	}
	if len(status.Servers) == 0 {
		lines = append(lines, "servers: none configured")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "")
	for _, server := range status.Servers {
		line := fmt.Sprintf("%-18s %-13s %-15s tools:%d", server.Name, serverState(server), server.Transport, server.ToolCount)
		if server.Command != "" {
			line += " command:" + oneLine(server.Command, 80)
		}
		if server.URL != "" {
			line += " url:" + oneLine(server.URL, 100)
		}
		if server.Required {
			line += " required"
		}
		if server.PID > 0 {
			line += fmt.Sprintf(" pid:%d", server.PID)
		}
		if server.RestartCount > 0 {
			line += fmt.Sprintf(" restarts:%d", server.RestartCount)
		}
		if server.RetryCount > 0 {
			line += fmt.Sprintf(" retries:%d", server.RetryCount)
		}
		if server.RetryBackoffMS > 0 {
			line += fmt.Sprintf(" backoff:%dms", server.RetryBackoffMS)
		}
		if server.NextRetryAt != nil && !server.NextRetryAt.IsZero() {
			line += " next_retry:" + server.NextRetryAt.Local().Format("15:04:05")
		}
		if server.StartedAt != nil && !server.StartedAt.IsZero() {
			line += " started:" + server.StartedAt.Local().Format("15:04:05")
		}
		if server.LastConnectedAt != nil && !server.LastConnectedAt.IsZero() {
			line += " connected_at:" + server.LastConnectedAt.Local().Format("15:04:05")
		}
		if server.LastEventAt != nil && !server.LastEventAt.IsZero() {
			line += " event_at:" + server.LastEventAt.Local().Format("15:04:05")
		}
		if server.Error != "" {
			line += "\n  error: " + oneLine(server.Error, 180)
		}
		if server.LastError != "" && server.LastError != server.Error {
			line += "\n  last: " + oneLine(server.LastError, 180)
		}
		if server.LastErrorAt != nil && !server.LastErrorAt.IsZero() {
			line += "\n  last_error_at: " + server.LastErrorAt.Local().Format("2006-01-02 15:04:05")
		}
		if server.StderrTail != "" {
			line += "\n  stderr: " + oneLine(server.StderrTail, 180)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func serverState(server mcpclient.ServerStatus) string {
	if strings.TrimSpace(server.State) != "" {
		return strings.TrimSpace(server.State)
	}
	if !server.Enabled {
		return "disabled"
	}
	if server.Connected {
		return "connected"
	}
	if server.Error != "" {
		return "failed"
	}
	return "disconnected"
}

func oneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
