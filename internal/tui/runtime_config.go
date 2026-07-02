package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	tuiruntime "github.com/billyhargroveofficial/billyharness/internal/tui/runtimeclient"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func (m Model) selectedConfig() config.Config {
	cfg := m.cfg
	cfg.Model = m.currentModel()
	cfg.Provider = modelinfo.ProviderForModel(cfg.Model, cfg.Provider)
	cfg.Profile = config.NormalizeProfileName(cfg.Profile)
	cfg.Thinking = m.currentThinking().kind
	cfg.ReasoningEffort = m.currentThinking().effort
	cfg.StoreReasoningContent = true
	cfg.AutoApproveDangerous = cfg.AutoApproveDangerous || m.dangerous
	cfg.AccessMode = config.NormalizeAccessMode(m.accessMode)
	cfg.MaxToolRounds = m.maxRounds
	cfg.ApplyModelProviderDefaults()
	return cfg
}

func (m *Model) refreshConfigProjections() {
	cfg := m.selectedConfig()
	m.authSettings = cfg.AuthSettings()
	m.providerBinding = cfg.ProviderBinding()
	m.profile = cfg.ProfileSelection()
	m.runtime = cfg.RuntimeLimits()
	m.toolPolicy = cfg.ToolPolicySettings()
	m.diagnosticsSettings = cfg.DiagnosticsSettings()
	m.mcpSettings = cfg.MCPSettings()
	m.hookSettings = cfg.HookSettings()
	m.instructions = cfg.InstructionSettings()
}

func (m Model) runtimeClientSettings() tuiruntime.Settings {
	return tuiruntime.Settings{
		ProviderBinding: m.providerBinding,
		Profile:         m.profile,
		Runtime:         m.runtime,
		ToolPolicy:      m.toolPolicy,
		Diagnostics:     m.diagnosticsSettings,
		MCP:             m.mcpSettings,
		Hooks:           m.hookSettings,
		Instructions:    m.instructions,
	}
}

func (m Model) runtimeDiffSettings() config.RuntimeDiffSettings {
	return config.RuntimeDiffSettings{
		Provider:    m.providerBinding,
		Profile:     m.profile,
		Runtime:     m.runtime,
		ToolPolicy:  m.toolPolicy,
		Diagnostics: m.diagnosticsSettings,
		MCP:         m.mcpSettings,
		Hooks:       m.hookSettings,
		GatewayAddr: m.cfg.GatewayAddr,
	}
}

func (m Model) currentModel() string {
	return m.models[m.modelIndex]
}

func (m Model) currentProvider() string {
	if m.providerBinding.Provider.Provider != "" {
		return m.providerBinding.Provider.Provider
	}
	return modelinfo.ProviderForModel(m.currentModel(), m.cfg.Provider)
}

func (m Model) currentProfile() string {
	if m.profile.Profile != "" {
		return m.profile.Profile
	}
	return config.NormalizeProfileName(m.cfg.Profile)
}

func (m Model) currentThinking() thinkingMode {
	return m.thinking[m.thinkingIdx]
}

func (m Model) currentAccessMode() string {
	return config.NormalizeAccessMode(m.accessMode)
}

func (m *Model) setTheme(value string) bool {
	switch value {
	case "", "toggle", "next":
		if m.theme == "dark" {
			m.theme = "light"
		} else {
			m.theme = "dark"
		}
	case "light", "white":
		m.theme = "light"
	case "dark", "black":
		m.theme = "dark"
	default:
		m.status = "unknown theme " + value
		return false
	}
	m.status = "theme " + m.theme
	_ = m.saveSettings()
	return true
}

func (m *Model) cycleModel() {
	m.modelIndex = (m.modelIndex + 1) % len(m.models)
	m.refreshConfigProjections()
	m.status = m.modelStatusText()
	_ = m.saveSettings()
}

func (m Model) modelStatusText() string {
	model := m.currentModel()
	return "model " + model + " (" + modelinfo.InputCapabilityLabel(model) + ")"
}

func (m *Model) setModel(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.cycleModel()
		return true
	default:
		value = modelinfo.NormalizeAlias(value)
		if !strings.Contains(value, "/") && !strings.Contains(value, " ") {
			// Keep the TUI open to custom compatible model ids without another release.
			m.models = appendIfMissing(m.models, value)
		} else {
			m.status = "unknown model " + value
			return false
		}
	}
	m.models = appendIfMissing(m.models, value)
	for i, model := range m.models {
		if model == value {
			m.modelIndex = i
			break
		}
	}
	m.refreshConfigProjections()
	m.status = m.modelStatusText()
	_ = m.saveSettings()
	return true
}

func (m *Model) setProfile(value string) tea.Cmd {
	value = strings.TrimSpace(value)
	if value == "" {
		m.addInfoBlock("PROFILE", "current profile: "+m.currentProfile()+"\nfile: "+config.DefaultProfileFile(m.currentProfile()))
		m.status = "profile " + m.currentProfile()
		return nil
	}
	profile := config.NormalizeProfileName(value)
	if _, err := config.EnsureDefaultProfileFile(profile); err != nil {
		m.status = "profile error: " + err.Error()
		m.addBlock("error", "ERROR", err.Error())
		return nil
	}
	_ = m.saveCurrentSession()
	m.cfg.Profile = profile
	if err := m.cfg.ApplyProfileMetadata(); err != nil {
		m.status = "profile metadata error: " + err.Error()
		m.addBlock("error", "ERROR", err.Error())
		return nil
	}
	m.applyConfigSelection(m.cfg)
	m.resetFreshChatState("new chat")
	m.status = "profile " + m.currentProfile() + "; new chat"
	_ = m.saveSettings()
	_ = m.saveCurrentSession()
	if m.gatewayURL != "" {
		return m.createSessionCmd()
	}
	return nil
}

func (m *Model) applyConfigSelection(cfg config.Config) {
	defer m.refreshConfigProjections()
	model := modelinfo.NormalizeAlias(cfg.Model)
	if model != "" {
		m.models = appendIfMissing(m.models, model)
		for i, candidate := range m.models {
			if candidate == model {
				m.modelIndex = i
				break
			}
		}
	}
	for i, mode := range m.thinking {
		if mode.kind == cfg.Thinking && mode.effort == cfg.ReasoningEffort {
			m.thinkingIdx = i
			return
		}
	}
	for i, mode := range m.thinking {
		if mode.effort == cfg.ReasoningEffort || (cfg.Thinking == "disabled" && mode.kind == "disabled") {
			m.thinkingIdx = i
			return
		}
	}
}

func (m *Model) setAccessMode(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "next", "toggle":
		switch m.currentAccessMode() {
		case config.AccessModeBuild:
			value = config.AccessModeGuarded
		case config.AccessModeGuarded:
			value = config.AccessModePlan
		default:
			value = config.AccessModeBuild
		}
	case config.AccessModeBuild, config.AccessModeGuarded, config.AccessModePlan, "safe", "readonly", "read-only", "read_only", "analysis":
	default:
		m.status = "unknown access mode " + value
		return false
	}
	m.accessMode = config.NormalizeAccessMode(value)
	m.refreshConfigProjections()
	m.status = "mode " + m.accessMode
	_ = m.saveSettings()
	return true
}

func (m *Model) cycleReasoning() {
	m.thinkingIdx = (m.thinkingIdx + 1) % len(m.thinking)
	m.refreshConfigProjections()
	m.status = m.currentThinking().label
	_ = m.saveSettings()
}

func (m *Model) setReasoning(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.cycleReasoning()
		return true
	case "high", "on", "enabled":
		value = "high"
	case "xhigh", "x-high", "extra", "extra-high":
		value = "xhigh"
	case "max", "maximum":
		value = "max"
	case "medium", "med":
		value = "medium"
	case "low", "minimal", "min":
		value = "low"
	case "off", "none", "disabled":
		value = "off"
	default:
		m.status = "unknown reasoning " + value
		return false
	}
	for i, mode := range m.thinking {
		if mode.effort == value || (value == "off" && mode.kind == "disabled") {
			m.thinkingIdx = i
			m.refreshConfigProjections()
			m.status = m.currentThinking().label
			_ = m.saveSettings()
			return true
		}
	}
	m.status = "unknown reasoning " + value
	return false
}

func (m *Model) toggleThinkingDisplay() {
	if m.thinkView == "hidden" {
		m.thinkView = "expanded"
	} else {
		m.thinkView = "hidden"
	}
	m.showThinking = m.thinkView != "hidden"
	m.status = "thinking blocks " + m.thinkView
	_ = m.saveSettings()
}

func (m *Model) setThinkingDisplay(value string) bool {
	switch value {
	case "", "toggle", "next":
		m.toggleThinkingDisplay()
	case "on", "show", "shown", "visible", "yes", "true":
		m.showThinking = true
		m.thinkView = "expanded"
		m.status = "thinking blocks expanded"
	case "off", "hide", "hidden", "no", "false":
		m.showThinking = false
		m.thinkView = "hidden"
		m.status = "thinking blocks hidden"
	default:
		m.status = "unknown thinking display " + value
		return false
	}
	_ = m.saveSettings()
	return true
}

func (m *Model) setToolView(value string) bool {
	switch value {
	case "", "toggle", "next":
		switch m.toolView {
		case "auto":
			value = "collapsed"
		case "collapsed":
			value = "current"
		case "current":
			value = "expanded"
		case "expanded":
			value = "errors"
		case "errors":
			value = "hidden"
		default:
			value = "auto"
		}
	case "show", "visible", "on":
		value = "auto"
	case "open":
		value = "expanded"
	case "close":
		value = "collapsed"
	case "turn", "current-turn":
		value = "current"
	case "failed", "error":
		value = "errors"
	}
	if !validViewMode(value, []string{"auto", "expanded", "collapsed", "current", "hidden", "errors"}) {
		m.status = "unknown toolview " + value
		return false
	}
	m.toolView = value
	m.status = "tool blocks " + value
	_ = m.saveSettings()
	return true
}

func (m *Model) setTranscriptMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "toggle", "next":
		if m.transcriptMode == transcript.ExportModeRaw {
			value = transcript.ExportModeRich
		} else {
			value = transcript.ExportModeRaw
		}
	case "raw", "plain", "debug":
		value = transcript.ExportModeRaw
	case "rich", "pretty":
		value = transcript.ExportModeRich
	default:
		m.status = "unknown transcript mode " + value
		return false
	}
	m.transcriptMode = value
	m.status = "transcript " + value
	m.clearRichRenderCache()
	_ = m.saveSettings()
	return true
}

func (m *Model) setThinkView(value string) bool {
	switch value {
	case "", "toggle", "next":
		switch m.thinkView {
		case "expanded":
			value = "collapsed"
		case "collapsed":
			value = "hidden"
		default:
			value = "expanded"
		}
	case "show", "visible", "on", "open":
		value = "expanded"
	case "close":
		value = "collapsed"
	case "hide", "off":
		value = "hidden"
	}
	if !validViewMode(value, []string{"expanded", "collapsed", "hidden"}) {
		m.status = "unknown thinkview " + value
		return false
	}
	m.thinkView = value
	m.showThinking = value != "hidden"
	m.status = "thinking blocks " + value
	_ = m.saveSettings()
	return true
}
