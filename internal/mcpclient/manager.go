package mcpclient

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

type Manager struct {
	tools            []ExternalTool
	instructions     []string
	collisions       []string
	catalogVersion   int64
	mu               sync.RWMutex
	servers          []*managedServer
	listenerSeq      int64
	listenersMu      sync.RWMutex
	statusListeners  map[int64]func(ServerStatus)
	catalogListeners map[int64]func(CatalogChange)
}

func ManagerSettingsFromConfig(cfg config.Config) ManagerSettings {
	return ManagerSettingsFromProjections(cfg.ToolPolicySettings(), cfg.MCPSettings())
}

func ManagerSettingsFromProjections(toolPolicy config.ToolPolicySettings, mcpSettings config.MCPSettings) ManagerSettings {
	return ManagerSettings{
		WorkspaceRoots:     append([]string(nil), toolPolicy.WorkspaceRoots...),
		MaxToolOutputBytes: toolPolicy.MaxToolOutputBytes,
		MCP:                cloneMCPSettings(mcpSettings),
	}
}

func NewManager(ctx context.Context, cfg config.Config) (*Manager, error) {
	return NewManagerFromSettings(ctx, ManagerSettingsFromConfig(cfg))
}

func NewManagerFromSettings(ctx context.Context, settings ManagerSettings) (*Manager, error) {
	manager := &Manager{}
	var errs []string
	settings = cloneManagerSettings(settings)
	for _, server := range settings.MCP.Servers {
		runtime := newManagedServer(settings, server, manager.emitStatus, func() {
			manager.rebuildCatalog()
		})
		manager.addServer(runtime)
		if !server.Enabled {
			continue
		}
		if server.URL != "" {
			reason := unsupportedRemoteReason(server)
			err := fmt.Errorf("MCP server %s unsupported: %s", server.Name, reason)
			runtime.recordStaticErrorState(mcpStateUnsupported, err)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		if strings.TrimSpace(server.Command) == "" {
			err := fmt.Errorf("MCP server %s has no command", server.Name)
			runtime.recordStaticError(err)
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
		_, _, err := runtime.start(ctx, false)
		if err != nil {
			if server.Required {
				errs = append(errs, err.Error())
			}
			continue
		}
	}
	change := manager.rebuildCatalog()
	errs = append(errs, change.Collisions...)
	if len(errs) > 0 {
		manager.Close()
		return nil, fmt.Errorf("MCP initialization failed: %s", strings.Join(errs, "; "))
	}
	return manager, nil
}

func cloneManagerSettings(settings ManagerSettings) ManagerSettings {
	settings.WorkspaceRoots = append([]string(nil), settings.WorkspaceRoots...)
	settings.MCP = cloneMCPSettings(settings.MCP)
	return settings
}

func cloneMCPSettings(settings config.MCPSettings) config.MCPSettings {
	return config.Config{
		MCPEnabled:        settings.Enabled,
		MCPConfigFiles:    settings.ConfigFiles,
		MCPAllowedServers: settings.AllowedServers,
		MCPServers:        settings.Servers,
	}.MCPSettings()
}

func (m *Manager) AddStatusListener(listener func(ServerStatus)) func() {
	if m == nil || listener == nil {
		return func() {}
	}
	id := atomic.AddInt64(&m.listenerSeq, 1)
	m.listenersMu.Lock()
	if m.statusListeners == nil {
		m.statusListeners = map[int64]func(ServerStatus){}
	}
	m.statusListeners[id] = listener
	m.listenersMu.Unlock()
	return func() {
		m.listenersMu.Lock()
		delete(m.statusListeners, id)
		m.listenersMu.Unlock()
	}
}

func (m *Manager) AddCatalogListener(listener func(CatalogChange)) func() {
	if m == nil || listener == nil {
		return func() {}
	}
	id := atomic.AddInt64(&m.listenerSeq, 1)
	m.listenersMu.Lock()
	if m.catalogListeners == nil {
		m.catalogListeners = map[int64]func(CatalogChange){}
	}
	m.catalogListeners[id] = listener
	m.listenersMu.Unlock()
	return func() {
		m.listenersMu.Lock()
		delete(m.catalogListeners, id)
		m.listenersMu.Unlock()
	}
}

func (m *Manager) emitStatus(status ServerStatus) {
	if m == nil {
		return
	}
	m.listenersMu.RLock()
	listeners := make([]func(ServerStatus), 0, len(m.statusListeners))
	for _, listener := range m.statusListeners {
		listeners = append(listeners, listener)
	}
	m.listenersMu.RUnlock()
	for _, listener := range listeners {
		listener(cloneStatus(status))
	}
}

func (m *Manager) emitCatalog(change CatalogChange) {
	if m == nil {
		return
	}
	m.listenersMu.RLock()
	listeners := make([]func(CatalogChange), 0, len(m.catalogListeners))
	for _, listener := range m.catalogListeners {
		listeners = append(listeners, listener)
	}
	m.listenersMu.RUnlock()
	for _, listener := range listeners {
		listener(cloneCatalogChange(change))
	}
}

func (m *Manager) Tools() []ExternalTool {
	if m == nil {
		return nil
	}
	return m.CatalogSnapshot().Tools
}

func (m *Manager) CatalogSnapshot() CatalogSnapshot {
	if m == nil {
		return CatalogSnapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return CatalogSnapshot{
		Version:      m.catalogVersion,
		Tools:        append([]ExternalTool(nil), m.tools...),
		Instructions: append([]string(nil), m.instructions...),
		Collisions:   append([]string(nil), m.collisions...),
	}
}

func (m *Manager) Statuses() []ServerStatus {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	servers := append([]*managedServer(nil), m.servers...)
	m.mu.RUnlock()
	statuses := make([]ServerStatus, 0, len(servers))
	for _, server := range servers {
		statuses = append(statuses, server.snapshot())
	}
	return statuses
}

func (m *Manager) Refresh(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.RLock()
	servers := append([]*managedServer(nil), m.servers...)
	m.mu.RUnlock()
	for _, server := range servers {
		if server != nil {
			_, _ = server.ensureConnected(ctx)
		}
	}
}

func (m *Manager) Instructions() []string {
	if m == nil {
		return nil
	}
	return m.CatalogSnapshot().Instructions
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	servers := append([]*managedServer(nil), m.servers...)
	m.mu.Unlock()
	for _, server := range servers {
		if server != nil {
			server.close()
		}
	}
}

func (m *Manager) addServer(server *managedServer) {
	m.servers = append(m.servers, server)
}

func (m *Manager) rebuildCatalog() CatalogChange {
	if m == nil {
		return CatalogChange{}
	}
	m.mu.Lock()
	servers := append([]*managedServer(nil), m.servers...)
	catalogs := make([]serverCatalog, 0, len(servers))
	for _, server := range servers {
		if server != nil {
			catalogs = append(catalogs, server.catalogSnapshot())
		}
	}
	tools, instructions, collisions := buildCatalog(catalogs)
	m.catalogVersion++
	change := CatalogChange{
		Version:    m.catalogVersion,
		ToolCount:  len(tools),
		Collisions: append([]string(nil), collisions...),
	}
	m.tools = tools
	m.instructions = instructions
	m.collisions = append([]string(nil), collisions...)
	m.mu.Unlock()
	m.emitCatalog(change)
	return change
}
