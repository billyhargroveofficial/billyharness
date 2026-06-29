package mcpclient

import (
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

func cloneStatus(status ServerStatus) ServerStatus {
	out := status
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

func cloneCatalogChange(change CatalogChange) CatalogChange {
	out := change
	out.Collisions = append([]string(nil), change.Collisions...)
	return out
}

func mcpStatusChanged(before, after ServerStatus) bool {
	return before.Connected != after.Connected ||
		before.State != after.State ||
		before.ToolCount != after.ToolCount ||
		before.PID != after.PID ||
		before.LastError != after.LastError ||
		before.Error != after.Error ||
		before.RetryCount != after.RetryCount ||
		before.RestartCount != after.RestartCount ||
		before.RetryBackoffMS != after.RetryBackoffMS
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
