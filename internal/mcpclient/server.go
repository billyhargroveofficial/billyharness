package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type managedServer struct {
	settings         ManagerSettings
	server           config.MCPServer
	reconnectable    bool
	mu               sync.Mutex
	client           *stdioClient
	status           ServerStatus
	specs            []protocol.ToolSpec
	instructions     string
	onStatus         func(ServerStatus)
	onCatalogChanged func()
	closed           bool
	starting         bool
}

func newManagedServer(settings ManagerSettings, server config.MCPServer, onStatus func(ServerStatus), onCatalogChanged func()) *managedServer {
	state := mcpStateDisconnected
	if !server.Enabled {
		state = mcpStateDisabled
	}
	unsupportedReason := strings.TrimSpace(server.UnsupportedReason)
	if strings.TrimSpace(server.URL) != "" {
		unsupportedReason = unsupportedRemoteReason(server)
	}
	return &managedServer{
		settings:         cloneManagerSettings(settings),
		server:           server,
		reconnectable:    server.Enabled && server.URL == "" && strings.TrimSpace(server.Command) != "",
		onStatus:         onStatus,
		onCatalogChanged: onCatalogChanged,
		status: ServerStatus{
			Name:              server.Name,
			Transport:         mcpTransport(server),
			Command:           strings.TrimSpace(server.Command),
			URL:               strings.TrimSpace(server.URL),
			UnsupportedReason: unsupportedReason,
			Enabled:           server.Enabled,
			Required:          server.Required,
			State:             state,
		},
	}
}

func mcpTransport(server config.MCPServer) string {
	if server.URL != "" {
		return "streamable-http"
	}
	return "stdio"
}

func unsupportedRemoteReason(server config.MCPServer) string {
	reason := strings.TrimSpace(server.UnsupportedReason)
	if reason != "" {
		return reason
	}
	return "streamable HTTP MCP is not implemented in billyharness yet; use stdio MCP or remove the url server"
}

func (s *managedServer) start(ctx context.Context, reconnect bool) ([]protocol.ToolSpec, string, error) {
	s.mu.Lock()
	if !s.reconnectable {
		err := fmt.Errorf("MCP %s cannot reconnect with transport %s", s.server.Name, mcpTransport(s.server))
		s.recordFailureLocked(mcpStateFailed, err, false)
		catalogChanged := s.clearCatalogLocked()
		status := cloneStatus(s.status)
		s.mu.Unlock()
		s.publishStatus(status)
		if catalogChanged {
			s.publishCatalogChanged()
		}
		return nil, "", err
	}
	if s.closed {
		err := fmt.Errorf("MCP %s manager is closed", s.server.Name)
		s.recordFailureLocked(mcpStateDisconnected, err, false)
		catalogChanged := s.clearCatalogLocked()
		status := cloneStatus(s.status)
		s.mu.Unlock()
		s.publishStatus(status)
		if catalogChanged {
			s.publishCatalogChanged()
		}
		return nil, "", err
	}
	return s.startLocked(ctx, reconnect)
}

func (s *managedServer) ensureConnected(ctx context.Context) (*stdioClient, error) {
	s.mu.Lock()
	status, statusChanged, catalogChanged := s.absorbClientLocked()
	if statusChanged || catalogChanged {
		s.mu.Unlock()
		if statusChanged {
			s.publishStatus(status)
		}
		if catalogChanged {
			s.publishCatalogChanged()
		}
		s.mu.Lock()
	}
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s manager is closed", s.server.Name)
	}
	if !s.status.Enabled {
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s is disabled", s.server.Name)
	}
	if !s.reconnectable {
		if s.status.Error != "" {
			s.mu.Unlock()
			return nil, fmt.Errorf("MCP %s unavailable: %s", s.server.Name, s.status.Error)
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s unavailable", s.server.Name)
	}
	if s.client != nil && s.client.connected.Load() {
		client := s.client
		s.mu.Unlock()
		return client, nil
	}
	if s.starting {
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s reconnect already in progress", s.server.Name)
	}
	now := time.Now().UTC()
	if s.status.NextRetryAt != nil && now.Before(*s.status.NextRetryAt) {
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP %s reconnect backoff active until %s after last error: %s", s.server.Name, s.status.NextRetryAt.Format(time.RFC3339Nano), s.status.LastError)
	}
	_, _, err := s.startLocked(ctx, true)
	if err != nil {
		return nil, err
	}
	return s.client, nil
}

func (s *managedServer) startLocked(ctx context.Context, reconnect bool) ([]protocol.ToolSpec, string, error) {
	if s.starting {
		s.mu.Unlock()
		return nil, "", fmt.Errorf("MCP %s reconnect already in progress", s.server.Name)
	}
	oldClient := s.client
	s.client = nil
	catalogChanged := s.clearCatalogLocked()
	s.starting = true
	now := time.Now().UTC()
	s.status.Connected = false
	s.status.State = mcpStateRestarting
	s.status.LastEventAt = timePtr(now)
	s.status.Error = ""
	s.status.StderrTail = ""
	s.status.PID = 0
	s.status.NextRetryAt = nil
	s.status.RetryBackoffMS = 0
	if reconnect {
		s.status.RetryCount++
	}
	restartingStatus := cloneStatus(s.status)
	s.mu.Unlock()
	s.publishStatus(restartingStatus)
	if catalogChanged {
		s.publishCatalogChanged()
	}

	if oldClient != nil {
		oldClient.close()
	}
	client, specs, instructions, err := startStdio(ctx, s.settings, s.server, s.handleNotification)
	s.mu.Lock()
	s.starting = false
	if s.closed {
		closedErr := fmt.Errorf("MCP %s manager is closed", s.server.Name)
		s.recordFailureLocked(mcpStateDisconnected, closedErr, false)
		catalogChanged := s.clearCatalogLocked()
		status := cloneStatus(s.status)
		s.mu.Unlock()
		s.publishStatus(status)
		if catalogChanged {
			s.publishCatalogChanged()
		}
		if client != nil {
			client.close()
		}
		return nil, "", closedErr
	}
	if err != nil {
		s.recordFailureLocked(mcpStateFailed, err, reconnect)
		catalogChanged := s.clearCatalogLocked()
		status := cloneStatus(s.status)
		s.mu.Unlock()
		s.publishStatus(status)
		if catalogChanged {
			s.publishCatalogChanged()
		}
		return nil, "", err
	}
	s.client = client
	now = time.Now().UTC()
	state := mcpStateConnected
	if reconnect {
		state = mcpStateReconnected
		s.status.RestartCount++
	}
	s.specs = append([]protocol.ToolSpec(nil), specs...)
	s.instructions = instructions
	s.status.Connected = true
	s.status.State = state
	s.status.ToolCount = len(specs)
	s.status.PID = client.pid()
	if !client.startedAt.IsZero() {
		startedAt := client.startedAt
		s.status.StartedAt = &startedAt
	}
	s.status.LastConnectedAt = timePtr(now)
	s.status.LastEventAt = timePtr(now)
	s.status.Error = ""
	s.status.StderrTail = ""
	s.status.NextRetryAt = nil
	s.status.RetryBackoffMS = 0
	status := cloneStatus(s.status)
	s.mu.Unlock()
	s.publishStatus(status)
	s.publishCatalogChanged()
	return specs, instructions, nil
}

func (s *managedServer) callTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	client, err := s.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	text, err := client.callTool(ctx, name, args)
	if err != nil && !client.connected.Load() {
		s.mu.Lock()
		status, changed, catalogChanged := s.absorbClientLocked()
		s.mu.Unlock()
		if changed {
			s.publishStatus(status)
		}
		if catalogChanged {
			s.publishCatalogChanged()
		}
	}
	return text, err
}

func (s *managedServer) handleNotification(method string, _ json.RawMessage) {
	switch method {
	case "notifications/tools/list_changed", "tools/list_changed":
		go s.refreshCatalogFromNotification()
	}
}

func (s *managedServer) refreshCatalogFromNotification() {
	timeout := s.server.StartupTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = s.refreshCatalog(ctx)
}

func (s *managedServer) refreshCatalog(ctx context.Context) error {
	s.mu.Lock()
	client := s.client
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("MCP %s manager is closed", s.server.Name)
	}
	if client == nil || !client.connected.Load() {
		s.mu.Unlock()
		return fmt.Errorf("MCP %s unavailable", s.server.Name)
	}
	s.mu.Unlock()

	specs, err := client.listTools(ctx)
	if err != nil {
		if !client.connected.Load() {
			s.mu.Lock()
			status, changed, catalogChanged := s.absorbClientLocked()
			s.mu.Unlock()
			if changed {
				s.publishStatus(status)
			}
			if catalogChanged {
				s.publishCatalogChanged()
			}
		}
		return err
	}

	s.mu.Lock()
	if s.closed || s.client != client {
		s.mu.Unlock()
		return nil
	}
	s.specs = append([]protocol.ToolSpec(nil), specs...)
	s.status.ToolCount = len(specs)
	s.status.LastEventAt = timePtr(time.Now().UTC())
	status := cloneStatus(s.status)
	s.mu.Unlock()
	s.publishStatus(status)
	s.publishCatalogChanged()
	return nil
}

func (s *managedServer) snapshot() ServerStatus {
	if s == nil {
		return ServerStatus{}
	}
	s.mu.Lock()
	status, changed, catalogChanged := s.absorbClientLocked()
	s.mu.Unlock()
	if changed {
		s.publishStatus(status)
	}
	if catalogChanged {
		s.publishCatalogChanged()
	}
	return status
}

func (s *managedServer) catalogSnapshot() serverCatalog {
	if s == nil {
		return serverCatalog{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return serverCatalog{
		runtime:      s,
		server:       s.server,
		specs:        append([]protocol.ToolSpec(nil), s.specs...),
		instructions: s.instructions,
	}
}

func (s *managedServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	client := s.client
	s.client = nil
	catalogChanged := s.clearCatalogLocked()
	now := time.Now().UTC()
	s.status.Connected = false
	s.status.State = mcpStateDisconnected
	s.status.LastEventAt = timePtr(now)
	s.status.Error = ""
	s.status.NextRetryAt = nil
	s.status.RetryBackoffMS = 0
	status := cloneStatus(s.status)
	s.mu.Unlock()
	s.publishStatus(status)
	if catalogChanged {
		s.publishCatalogChanged()
	}
	if client != nil {
		client.close()
	}
}

func (s *managedServer) recordStaticError(err error) {
	s.recordStaticErrorState(mcpStateFailed, err)
}

func (s *managedServer) recordStaticErrorState(state string, err error) {
	s.mu.Lock()
	s.recordFailureLocked(state, err, false)
	status := cloneStatus(s.status)
	s.mu.Unlock()
	s.publishStatus(status)
}

func (s *managedServer) recordFailureLocked(state string, err error, retryable bool) {
	now := time.Now().UTC()
	message := redactServerError(s.server, err)
	s.status.Connected = false
	s.status.State = state
	s.status.Error = message
	s.status.LastError = message
	s.status.LastErrorAt = timePtr(now)
	s.status.LastEventAt = timePtr(now)
	s.status.StderrTail = ""
	if s.client != nil {
		s.status.StderrTail = s.client.stderrTail()
	}
	if retryable {
		backoff := mcpReconnectBackoff(s.status.RetryCount)
		next := now.Add(backoff)
		s.status.RetryBackoffMS = backoff.Milliseconds()
		s.status.NextRetryAt = &next
	}
}

func (s *managedServer) clearCatalogLocked() bool {
	changed := len(s.specs) > 0 || strings.TrimSpace(s.instructions) != ""
	s.specs = nil
	s.instructions = ""
	s.status.ToolCount = 0
	return changed
}

func (s *managedServer) absorbClientLocked() (ServerStatus, bool, bool) {
	if s.client == nil {
		return cloneStatus(s.status), false, false
	}
	before := cloneStatus(s.status)
	client := s.client
	if client.connected.Load() {
		s.status.Connected = true
		s.status.PID = client.pid()
		if !client.startedAt.IsZero() {
			startedAt := client.startedAt
			s.status.StartedAt = &startedAt
		}
		after := cloneStatus(s.status)
		return after, mcpStatusChanged(before, after), false
	}
	state, lastErr, lastErrAt := client.lifecycleState()
	if state == "" {
		state = mcpStateCrashed
	}
	s.status.Connected = false
	s.status.State = state
	if lastErr != "" {
		s.status.LastError = lastErr
		s.status.Error = lastErr
	}
	if !lastErrAt.IsZero() {
		s.status.LastErrorAt = &lastErrAt
		s.status.LastEventAt = &lastErrAt
	}
	s.status.PID = client.pid()
	s.status.StderrTail = client.stderrTail()
	catalogChanged := s.clearCatalogLocked()
	after := cloneStatus(s.status)
	return after, mcpStatusChanged(before, after), catalogChanged
}

func (s *managedServer) publishStatus(status ServerStatus) {
	if s == nil || s.onStatus == nil {
		return
	}
	s.onStatus(cloneStatus(status))
}

func (s *managedServer) publishCatalogChanged() {
	if s == nil || s.onCatalogChanged == nil {
		return
	}
	s.onCatalogChanged()
}
