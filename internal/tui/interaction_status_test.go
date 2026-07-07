package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	uxprojector "github.com/billyhargroveofficial/billyharness/internal/clientux/projector"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/displayfmt"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestAltEnterInsertsNewline(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("first")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "first\n" {
		t.Fatalf("textarea value = %q, want first newline", got)
	}
}

func TestPrintableKeysReachTextarea(t *testing.T) {
	m := newTestModel(t)

	next, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/" {
		t.Fatalf("textarea value = %q, want /", got)
	}
}

func TestMouseScrollDisablesFollowOutput(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", strings.Repeat("line\n", 80))
	m.resize(true)
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should start at bottom")
	}

	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	updated := next.(Model)
	if updated.followOutput {
		t.Fatalf("mouse wheel up should disable followOutput")
	}
	if updated.viewport.AtBottom() {
		t.Fatalf("mouse wheel up should scroll away from bottom")
	}

	next, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModAlt})
	updated = next.(Model)
	if !updated.followOutput {
		t.Fatalf("end key should restore followOutput")
	}
	if !updated.viewport.AtBottom() {
		t.Fatalf("end key should move to bottom")
	}
}

func TestLiveUpdateKeepsViewportAnchoredAtBottom(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 18
	m.addBlock("assistant", "ASSISTANT", strings.Repeat("old line\n", 80))
	m.resize(true)
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should start at bottom")
	}
	m.followOutput = true
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Repeat("new line\n", 8)})
	m.reflow(m.followOutput)
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should stay anchored at bottom during live update")
	}
}

func TestSlashPopupCompletesCommand(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/the")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/theme " {
		t.Fatalf("textarea value = %q, want /theme space", got)
	}
}

func TestCommandPaletteOpensSlashRegistry(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/" {
		t.Fatalf("Ctrl+K textarea = %q, want /", got)
	}
	if updated.status != "command palette" {
		t.Fatalf("status = %q, want command palette", updated.status)
	}
	popup := stripANSITest(updated.slashPopupView())
	for _, want := range []string{"/help", "/status", "/diff"} {
		if !strings.Contains(popup, want) {
			t.Fatalf("command palette missing %q:\n%s", want, popup)
		}
	}

	next, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated = next.(Model)
	if len(updated.blocks) == 0 || updated.blocks[len(updated.blocks)-1].Title != "HELP" {
		t.Fatalf("Enter on default command should run /help, blocks=%#v", updated.blocks)
	}
}

func TestSlashPopupKeepsLeftBorderVisibleAndSelectedMarker(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.textarea.SetValue("/model ")

	popup := stripANSITest(m.slashPopupView())
	lines := strings.Split(popup, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], " ┌") {
		t.Fatalf("popup should keep a safe column before the left border, got %q", popup)
	}
	if !strings.Contains(popup, "│› ") {
		t.Fatalf("popup selected row should have a visible marker, got %q", popup)
	}
}

func TestSlashPopupCompletesArgument(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/theme")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated := next.(Model)
	if got := updated.theme; got != "light" {
		t.Fatalf("theme = %q, want light", got)
	}
	if got := updated.textarea.Value(); got != "" {
		t.Fatalf("textarea value = %q, want cleared after command runs", got)
	}
}

func TestSlashPopupTabCompletesArgumentWithoutRunning(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/theme")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/theme light" {
		t.Fatalf("textarea value = %q, want /theme light", got)
	}
	if got := updated.theme; got != "dark" {
		t.Fatalf("theme should not change on tab, got %q", got)
	}
}

func TestSlashPopupCompletesResumeArgument(t *testing.T) {
	m := newTestModel(t)
	original := m.localChatID
	m.textarea.SetValue("/resume")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	updated := next.(Model)
	next, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	updated = next.(Model)

	want := "/resume " + shortID(original)
	if got := updated.textarea.Value(); got != want {
		t.Fatalf("textarea value = %q, want %q", got, want)
	}
}

func TestSlashPopupKeepsSelectedCommandVisiblePastFirstPage(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.textarea.SetValue("/")
	for i, command := range slashCommands() {
		if command.name == "/thinkview" {
			m.slashIndex = i
			break
		}
	}

	view := stripANSITest(m.slashPopupView())
	if !strings.Contains(view, "/thinkview") {
		t.Fatalf("selected command should be visible, popup=%q", view)
	}
	if strings.Contains(view, "/help") {
		t.Fatalf("popup should scroll past first page, popup=%q", view)
	}
	if !strings.Contains(view, "previous matches") {
		t.Fatalf("popup should show previous count, popup=%q", view)
	}
}

func TestSlashArgPopupKeepsSelectedArgumentVisiblePastFirstPage(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.textarea.SetValue("/reasoning ")
	m.slashIndex = 6

	view := stripANSITest(m.slashPopupView())
	if !strings.Contains(view, "toggle") {
		t.Fatalf("selected argument should be visible, popup=%q", view)
	}
	if strings.Contains(view, "xhigh") {
		t.Fatalf("popup should scroll past first argument page, popup=%q", view)
	}
	if !strings.Contains(view, "previous matches") {
		t.Fatalf("popup should show previous count, popup=%q", view)
	}
}

func TestSlashPopupEscDismissesUntilTextChanges(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/the")
	if m.slashPopupView() == "" {
		t.Fatalf("slash popup should render")
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	updated := next.(Model)
	if got := updated.slashPopupView(); got != "" {
		t.Fatalf("slash popup should be dismissed, got %q", got)
	}
}

func TestInlineStatusShowsOnlyCompactCoreSegments(t *testing.T) {
	m := newTestModel(t)
	m.width = 180
	m.version = "0.1.0"
	m.dangerous = true
	m.inputTok = 1000
	m.outputTok = 500
	m.cacheHitTok = 700
	m.cacheMissTok = 300
	m.lastInputTok = 1000
	m.lastOutputTok = 500
	m.lastCacheHitTok = 700
	m.lastCacheMissTok = 300
	m.toolSummaryInTok = 20000
	m.toolSummaryOutTok = 900
	m.toolSummaryAPITok = 0

	status := stripANSITest(m.inlineStatusView())
	if strings.HasPrefix(status, " ") {
		t.Fatalf("inline status should align without leading padding: %q", status)
	}
	for _, want := range []string{
		"📁 billyharness",
		"⎇ ",
		"🤖 v4-flash high",
		"0.1% 1.5k",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("status %q does not contain %q", status, want)
		}
	}
	if strings.Contains(status, "\n") {
		t.Fatalf("status should be one line, got %q", status)
	}
	for _, bad := range []string{
		"Full Access",
		"model cost",
		"cache hit",
		"cache miss",
		"websum",
		"sumapi",
		"agent turns",
		"tools ",
		"v0.1.0",
		"theme dark",
		"profile billy",
		"Main [",
		"used",
		"cached ",
	} {
		if strings.Contains(status, bad) {
			t.Fatalf("status should not contain old noisy segment %q: %q", bad, status)
		}
	}
}

func TestInlineStatusContextWindowFollowsModelInfo(t *testing.T) {
	models := []string{
		"gpt-5.5",
		"gpt-5.4-mini",
		"gpt-5.3-codex-spark",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
	}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			m := newTestModel(t)
			m.width = 220
			m.lastInputTok = 128
			if !m.setModel(model) {
				t.Fatalf("setModel(%q) failed: %s", model, m.status)
			}
			window := modelinfo.Lookup(model).ContextWindowTokens
			wantPercent := displayfmt.FixedPercentValue(float64(128)/float64(window)*100, 1)
			status := stripANSITest(m.inlineStatusView())
			if !strings.Contains(status, wantPercent+" 128") {
				t.Fatalf("inline status = %q, want context %s 128", status, wantPercent)
			}
			if strings.Contains(status, "/") || strings.Contains(status, "Compact") || strings.Contains(status, "override") {
				t.Fatalf("inline status should omit denominator/compact/override noise: %q", status)
			}
		})
	}
}

func TestInlineStatusKeepsGPTPrefixAndCodexQuota(t *testing.T) {
	m := newTestModel(t)
	m.width = 260
	m.lastInputTok = 6000
	m.lastOutputTok = 100
	if !m.setModel("gpt") {
		t.Fatalf("setModel(gpt) failed: %s", m.status)
	}
	if !m.setReasoning("xhigh") {
		t.Fatalf("setReasoning(xhigh) failed: %s", m.status)
	}
	now := time.Now()
	m.codexRateLimits = codexRateLimitSnapshot{
		Primary: &codexRateLimitWindow{
			UsedPercent:        95,
			WindowDurationMins: 300,
			ResetsAt:           now.Add(65 * time.Minute),
		},
		Secondary: &codexRateLimitWindow{
			UsedPercent:        18,
			WindowDurationMins: 10080,
			ResetsAt:           now.Add(73*time.Hour + 15*time.Minute),
		},
	}

	status := stripANSITest(m.inlineStatusView())
	for _, want := range []string{
		"🤖 gpt 5.5 xhigh 2.4% 6.1k",
		"● 5h 95.0%",
		"● 18.0%",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("status %q does not contain %q", status, want)
		}
	}
	for _, bad := range []string{"🤖 5.5", "xhigh · 2.4%"} {
		if strings.Contains(status, bad) {
			t.Fatalf("status should not contain %q: %q", bad, status)
		}
	}
}

func TestCodexRateLimitStatusFormatting(t *testing.T) {
	m := newTestModel(t)
	if !m.setModel("gpt") {
		t.Fatalf("setModel(gpt) failed: %s", m.status)
	}
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	m.codexRateLimits = codexRateLimitSnapshot{
		Primary: &codexRateLimitWindow{
			UsedPercent:        95,
			WindowDurationMins: 300,
			ResetsAt:           now.Add(65 * time.Minute),
		},
		Secondary: &codexRateLimitWindow{
			UsedPercent:        18,
			WindowDurationMins: 10080,
			ResetsAt:           now.Add(73*time.Hour + 15*time.Minute),
		},
	}

	want := "● 5h 95.0% 1hr 5m  ● 18.0% 3d 1hr 15m"
	if got := m.codexRateLimitsStatusText(now); got != want {
		t.Fatalf("codex rate limit status = %q, want %q", got, want)
	}
}

func TestCodexRateLimitStatusHiddenForNonCodexProvider(t *testing.T) {
	m := newTestModel(t)
	if !m.setModel("deepseek-v4-flash") {
		t.Fatalf("setModel(deepseek-v4-flash) failed: %s", m.status)
	}
	m.codexRateLimits = codexRateLimitSnapshot{
		Primary: &codexRateLimitWindow{
			UsedPercent:        95,
			WindowDurationMins: 300,
			ResetsAt:           time.Now().Add(time.Hour),
		},
	}
	if got := m.codexRateLimitsStatusText(time.Now()); got != "" {
		t.Fatalf("non-Codex provider should hide Codex quota, got %q", got)
	}
}

func TestInlineStatusLabelsExplicitContextWindowOverride(t *testing.T) {
	m := newTestModel(t)
	m.width = 220
	m.lastInputTok = 128
	m.runtime.ContextWindowTokens = 1_000_000
	m.runtime.ContextWindowSource = "override"
	m.runtime.ContextCompactTokens = 600_000
	status := stripANSITest(m.inlineStatusView())
	if !strings.Contains(status, "0.0% 128") {
		t.Fatalf("inline status should show compact context usage: %q", status)
	}
	for _, bad := range []string{"override", "Compact", "/1.0m", "used"} {
		if strings.Contains(status, bad) {
			t.Fatalf("inline status should omit %q noise: %q", bad, status)
		}
	}
}

func TestInlineStatusIsWidthAware(t *testing.T) {
	for _, width := range []int{80, 120, 160} {
		m := newTestModel(t)
		m.width = width
		m.version = "0.1.0"
		m.dangerous = true
		m.status = "completed"
		m.modelCalls = 12
		m.toolCalls = 34
		m.lastInputTok = 420000
		m.lastOutputTok = 1200
		m.lastCacheHitTok = 390000
		m.lastCacheMissTok = 30000
		m.toolSummaryInTok = 37000
		m.toolSummaryOutTok = 2500
		status := m.inlineStatusView()
		lines := strings.Split(status, "\n")
		if len(lines) != 1 {
			t.Fatalf("width %d: status should render as one line, got %q", width, status)
		}
		for _, line := range lines {
			if got := xansi.StringWidth(stripANSITest(line)); got > width {
				t.Fatalf("width %d: status line width=%d exceeds viewport: %q", width, got, line)
			}
		}
		for _, want := range []string{"📁", "🤖", "%"} {
			if !strings.Contains(status, want) {
				t.Fatalf("width %d: status missing priority segment %q: %q", width, want, status)
			}
		}
	}
}

func TestStatusCommandShowsDetailedStatusBlock(t *testing.T) {
	m := newTestModel(t)
	handled, cmd := m.handleSlashCommand("/status")
	if !handled || cmd != nil {
		t.Fatalf("/status handled=%v cmd=%v, want handled without async command", handled, cmd)
	}
	if len(m.blocks) != 1 || m.blocks[0].Title != "STATUS" {
		t.Fatalf("/status should add one STATUS block, got %#v", m.blocks)
	}
	for _, want := range []string{"provider:", "model:", "profile:", "context:", "calls:"} {
		if !strings.Contains(m.blocks[0].Content, want) {
			t.Fatalf("/status block missing %q:\n%s", want, m.blocks[0].Content)
		}
	}
}

func TestStatusDebugCommandShowsRedactedRuntimeSnapshot(t *testing.T) {
	m := newTestModel(t)
	m.gatewayURL = "http://127.0.0.1:8765"
	m.localChatID = "local-123"
	m.sessionID = "sess-123"
	m.lastGatewayEventSeq = 42
	m.pendingStreamEvents = []protocol.Event{{Type: protocol.EventAssistantDelta, Data: "secret pending payload"}}
	m.streamBatchScheduled = true
	m.reflowCount = 7
	m.width = 100
	m.height = 40
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(20)
	m.viewport.SetContent("viewport body")
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call-secret",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"secret.txt"}`),
		},
	})
	m.blocks[0].Content = "secret transcript body"
	m.selected = 0
	m.transcriptStale = true

	handled, cmd := m.handleSlashCommand("/status debug")
	if !handled || cmd != nil {
		t.Fatalf("/status debug handled=%v cmd=%v, want handled without async command", handled, cmd)
	}
	if len(m.blocks) != 2 || m.blocks[1].Title != "STATUS DEBUG" {
		t.Fatalf("/status debug should add one STATUS DEBUG block, got %#v", m.blocks)
	}
	content := m.blocks[1].Content
	for _, want := range []string{
		"schema: 1",
		"session: local=local-123 gateway=sess-123 last_seq=42 mode=gateway",
		"last_seq=42",
		"stream: pending=1",
		"scheduled=true",
		"projector: available=true",
		"selection: active=false",
		"transcript: blocks=1 selected=0",
		"call_id=call-secret",
		"viewport: app=100x40 viewport=80x20",
		"reflows=7",
		"stale: transcript_projector=true",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("/status debug block missing %q:\n%s", want, content)
		}
	}
	for _, notWant := range []string{"secret transcript body", "secret pending payload", "secret.txt"} {
		if strings.Contains(content, notWant) {
			t.Fatalf("/status debug leaked %q:\n%s", notWant, content)
		}
	}

	handled, cmd = m.handleSlashCommand("/debug")
	if !handled || cmd != nil {
		t.Fatalf("/debug handled=%v cmd=%v, want handled without async command", handled, cmd)
	}
	if last := m.blocks[len(m.blocks)-1]; last.Title != "DEBUG" || !strings.Contains(last.Content, "schema: 1") {
		t.Fatalf("/debug should add DEBUG snapshot block, got %#v", last)
	}
	if strings.Contains(m.blocks[len(m.blocks)-1].Content, "secret transcript body") {
		t.Fatalf("/debug leaked transcript body:\n%s", m.blocks[len(m.blocks)-1].Content)
	}
}

func TestStatusTextSeparatesSelectedAndRuntimeModel(t *testing.T) {
	m := newTestModel(t)
	m.applyEvent(protocol.Event{Type: protocol.EventSessionStatus, Data: gatewayapi.SessionStatus{
		Model:    "deepseek-v4-flash",
		Provider: "deepseek",
	}})
	if !m.setModel("gpt") {
		t.Fatalf("setModel failed: %s", m.status)
	}
	text := m.statusText()
	for _, want := range []string{
		"selected model: gpt-5.5",
		"active runtime model: deepseek-v4-flash",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("status text = %q, want %q", text, want)
		}
	}
}

func TestStatusTextShowsUnknownRuntimeModelWhenUnavailable(t *testing.T) {
	m := newTestModel(t)
	text := m.statusText()
	if !strings.Contains(text, "active runtime model: unknown") {
		t.Fatalf("status text should show unknown runtime model:\n%s", text)
	}
}

func TestAccessModeSlashCommandUpdatesRunRequest(t *testing.T) {
	m := newTestModel(t)
	m.width = 180
	handled, cmd := m.handleSlashCommand("/mode plan")
	if !handled || cmd != nil {
		t.Fatalf("/mode handled=%v cmd=%v, want handled without async command", handled, cmd)
	}
	if m.currentAccessMode() != config.AccessModePlan || m.toolPolicy.AccessMode != config.AccessModePlan {
		t.Fatalf("access mode=%q tool policy=%q", m.currentAccessMode(), m.toolPolicy.AccessMode)
	}
	if got := m.gatewayRunRequest("inspect").AccessMode; got != config.AccessModePlan {
		t.Fatalf("gateway run access mode = %q", got)
	}
	if status := m.inlineStatusView(); strings.Contains(status, "Plan") {
		t.Fatalf("inline status should omit access-mode noise: %q", status)
	}
}

func TestCompactEventTextShowsStructuredCompactionFields(t *testing.T) {
	text := compactEventText(map[string]any{
		"compaction_id":               "abc123",
		"context_epoch":               3,
		"reason":                      "prompt_tokens_at_or_above_threshold",
		"trigger_source":              "provider_usage",
		"trigger_prompt_tokens":       610000,
		"threshold_tokens":            600000,
		"input_span_hash":             "inputspanhash123456",
		"replacement_hash":            "replacementhash123456",
		"pre_history_hash":            "prehistoryhash123456",
		"post_history_hash":           "posthistoryhash123456",
		"before_estimated_tokens":     610000,
		"after_estimated_tokens":      98000,
		"cut_start_index":             4,
		"cut_end_index":               46,
		"replacement_index":           4,
		"keep_messages":               32,
		"max_summary_chars":           120000,
		"summary_strategy":            "model",
		"summary_provider":            "mock",
		"summary_model":               "mock-summary",
		"model_summary_input_tokens":  1234,
		"model_summary_output_tokens": 56,
		"compacted_messages":          42,
		"compacted_chars":             240000,
		"compacted_estimated_tokens":  60000,
		"protected_prefix": map[string]any{
			"messages":         3,
			"chars":            9000,
			"estimated_tokens": 2250,
			"reasons": map[string]any{
				"system_prompt":       1,
				"profile_soul":        1,
				"agents_instructions": 1,
			},
		},
		"active_messages": 35,
		"summary_chars":   12000,
		"top_context_contributors": []map[string]any{{
			"index":            6,
			"role":             "tool",
			"source":           "web_summaries",
			"name":             "web_fetch",
			"estimated_tokens": 42000,
			"preview":          "large web summary",
		}},
	})
	for _, want := range []string{
		"id: abc123",
		"epoch: 3",
		"reason: prompt_tokens_at_or_above_threshold (provider_usage)",
		"trigger: 610000 / threshold 600000 tokens",
		"context: before ~610k / after ~98k",
		"cut: [4:46) -> replacement index 4",
		"audit: input=inputspanhas replacement=replacementh pre=prehistoryha post=posthistoryh",
		"policy: keep 32 messages / summary cap 120000 chars",
		"summary: model mock/mock-summary",
		"summary usage: in 1.2k / out 56",
		"compacted messages: 42",
		"compacted budget: 240000 chars / ~60000 tokens",
		"protected prefix: 3 messages, 9000 chars, ~2250 tokens",
		"agents_instructions=1",
		"profile_soul=1",
		"system_prompt=1",
		"active messages: 35",
		"summary chars: 12000",
		"top contributors:",
		"#6 tool web_summaries/web_fetch ~42k - large web summary",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact text %q missing %q", text, want)
		}
	}
}

func TestResumeChatDoesNotTreatLifetimeTokensAsContextUsage(t *testing.T) {
	m := newTestModel(t)
	session := chatSession{
		ID:              "20260628-120000-feedfacecafe",
		Title:           "saved chat",
		CreatedAt:       time.Now().UTC().Add(-time.Hour),
		UpdatedAt:       time.Now().UTC(),
		Model:           m.currentModel(),
		ReasoningKind:   m.currentThinking().kind,
		ReasoningEffort: m.currentThinking().effort,
		InputTokens:     900000,
		OutputTokens:    100000,
		CacheHitTokens:  200000,
		CacheMissTokens: 700000,
		ReasoningTokens: 50000,
	}
	if err := saveChatSession(m.sessionsDir, session); err != nil {
		t.Fatal(err)
	}

	if cmd := m.resumeChat(session.ID); cmd != nil {
		t.Fatalf("resumeChat returned unexpected command: %#v", cmd)
	}
	if got := m.inputTok; got != session.InputTokens {
		t.Fatalf("inputTok = %d, want saved lifetime %d", got, session.InputTokens)
	}
	if got := m.outputTok; got != session.OutputTokens {
		t.Fatalf("outputTok = %d, want saved lifetime %d", got, session.OutputTokens)
	}
	if got := m.contextTokens(); got != 0 {
		t.Fatalf("contextTokens = %d, want 0 after resume without live usage snapshot", got)
	}
}

func TestInlineStatusOmitsCacheCountersWhenLargerThanContext(t *testing.T) {
	m := newTestModel(t)
	m.width = 220
	m.lastInputTok = 100
	m.lastOutputTok = 20
	m.lastCacheHitTok = 900
	m.lastCacheMissTok = 50

	status := stripANSITest(m.inlineStatusView())
	for _, want := range []string{"0.0% 120", "🤖"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status missing %q: %q", want, status)
		}
	}
	for _, bad := range []string{"cache hit", "cache miss", "cached context", "miss 50"} {
		if strings.Contains(status, bad) {
			t.Fatalf("inline status should omit cache counter %q: %q", bad, status)
		}
	}
}

func TestInlineStatusOmitsHelperAPICallsAndCost(t *testing.T) {
	m := newTestModel(t)
	m.width = 240
	m.helperAPICalls = 2
	m.helperCostUSD = 0.0045

	status := stripANSITest(m.inlineStatusView())
	for _, bad := range []string{"helper API calls", "helper API cost"} {
		if strings.Contains(status, bad) {
			t.Fatalf("inline status should omit helper API noise %q: %q", bad, status)
		}
	}
}

func TestInlineStatusOmitsSubscriptionAndHelperCost(t *testing.T) {
	m := newTestModel(t)
	m.width = 240
	handled, _ := m.handleSlashCommand("/model gpt")
	if !handled {
		t.Fatal("/model gpt returned false")
	}
	m.helperCostUSD = 0.0045

	status := stripANSITest(m.inlineStatusView())
	for _, bad := range []string{"subscription", "helper API cost", "model cost"} {
		if strings.Contains(status, bad) {
			t.Fatalf("inline status should omit cost noise %q: %q", bad, status)
		}
	}
	if m.costText() != "subscription" {
		t.Fatalf("helper API cost should not become main model cost: costText=%q status=%q", m.costText(), status)
	}
}

func TestTUIToolCountUsesRequestedCalls(t *testing.T) {
	events := toolCountSemanticsEvents()
	m := newTestModel(t)
	m.width = 220
	p := uxprojector.New()
	var snapshot uxprojector.Snapshot
	for _, event := range events {
		snapshot = p.Apply(event)
		m.applyEvent(event)
	}
	if snapshot.ToolCalls != 3 {
		t.Fatalf("projector tool calls = %d, want 3", snapshot.ToolCalls)
	}
	if m.toolCalls != snapshot.ToolCalls {
		t.Fatalf("tui tool calls = %d, projector = %d", m.toolCalls, snapshot.ToolCalls)
	}
	status := stripANSITest(m.inlineStatusView())
	for _, bad := range []string{"tools 3", "tools 4"} {
		if strings.Contains(status, bad) {
			t.Fatalf("inline status should omit tool count %q: %q", bad, status)
		}
	}
}

func toolCountSemanticsEvents() []protocol.Event {
	return []protocol.Event{
		{Seq: 1, Type: protocol.EventRunStarted},
		{Seq: 2, Type: protocol.EventModelCallStarted},
		{Seq: 3, Type: protocol.EventToolCallRequested, CallID: "call-requested-only", Data: protocol.ToolCall{ID: "call-requested-only", Name: "time_now"}},
		{Seq: 4, Type: protocol.EventToolCallRequested, CallID: "call-failed", Data: protocol.ToolCall{ID: "call-failed", Name: "shell_exec"}},
		{Seq: 5, Type: protocol.EventToolCallStarted, CallID: "call-failed"},
		{Seq: 6, Type: protocol.EventToolCallFailed, CallID: "call-failed", Data: protocol.ToolResult{CallID: "call-failed", Name: "shell_exec", Content: "permission denied", IsError: true}},
		{Seq: 7, Type: protocol.EventToolCallRequested, CallID: "call-aborted", Data: protocol.ToolCall{ID: "call-aborted", Name: "web_fetch"}},
		{Seq: 8, Type: protocol.EventToolCallStarted, CallID: "call-aborted"},
		{Seq: 9, Type: protocol.EventToolCallAborted, CallID: "call-aborted", Data: protocol.ToolResult{CallID: "call-aborted", Name: "web_fetch", Content: "interrupted", IsError: true}},
		{Seq: 10, Type: protocol.EventToolCallStarted, CallID: "call-started-only"},
		{Seq: 11, Type: protocol.EventRunCompleted},
	}
}

func TestLightThemeTextSurfacesUseForegroundOnly(t *testing.T) {
	styles := newThemeStyles(tuiThemes["light"])
	surfaces := map[string]string{
		"status":        styles.status.Render("status"),
		"runStatus":     styles.runStatus.Render("working"),
		"input":         styles.input.Render("prompt"),
		"popup":         styles.popup.Render("popup"),
		"popupSelected": styles.popupSelected.Render("selected"),
		"assistant":     styles.assistant.Render("assistant"),
		"tool":          styles.tool.Render("tool"),
		"error":         styles.error.Render("error"),
	}
	for name, rendered := range surfaces {
		if strings.Contains(rendered, "48;2;") {
			t.Fatalf("%s should not paint an opaque background, rendered=%q", name, rendered)
		}
	}
	if !strings.Contains(surfaces["status"], "38;2;45;53;36") {
		t.Fatalf("light status should keep explicit foreground color, rendered=%q", surfaces["status"])
	}
}

func TestViewLeavesTerminalBackgroundUnspecified(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 24
	m.resize(true)

	view := m.View()
	if view.BackgroundColor != nil {
		t.Fatalf("view should leave terminal background to the user's terminal theme, got %#v", view.BackgroundColor)
	}
	if view.ForegroundColor == nil {
		t.Fatal("view should keep an explicit foreground color")
	}
}

func TestViewInputBorderHasVisibleLeftEdge(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 24
	m.resize(true)

	view := stripANSITest(m.View().Content)
	lines := strings.Split(view, "\n")
	inputTop := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "┌") && strings.Contains(line, "┐") {
			inputTop = i
			break
		}
	}
	if inputTop < 0 {
		t.Fatalf("input top border not found in view:\n%s", view)
	}
	if inputTop+2 >= len(lines) {
		t.Fatalf("input border truncated near line %d in view:\n%s", inputTop, view)
	}
	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{name: "top", line: lines[inputTop], want: "┌"},
		{name: "body", line: lines[inputTop+1], want: "│"},
		{name: "bottom", line: lines[inputTop+2], want: "└"},
	} {
		if !strings.HasPrefix(tc.line, tc.want) {
			t.Fatalf("input %s line should start with %q, got %q", tc.name, tc.want, tc.line)
		}
	}
	if strings.HasPrefix(lines[inputTop+3], " ") {
		t.Fatalf("status line should align with input border, got %q", lines[inputTop+3])
	}
}

func TestFormatMCPStatusShowsOwnConfigAndNativeWebTools(t *testing.T) {
	startedAt := time.Date(2026, 6, 28, 8, 0, 0, 0, time.Local)
	connectedAt := time.Date(2026, 6, 28, 8, 0, 2, 0, time.Local)
	eventAt := time.Date(2026, 6, 28, 8, 0, 3, 0, time.Local)
	lastErrorAt := time.Date(2026, 6, 28, 8, 1, 0, 0, time.Local)
	nextRetryAt := time.Date(2026, 6, 28, 8, 1, 5, 0, time.Local)
	text := formatMCPStatus(mcpStatusResponse{
		ConfigFiles: []string{"/root/billyharness/mcp.config.toml"},
		Allowed:     []string{"telegram", "telegram-parilka", "github", "context7"},
		Enabled:     true,
		Servers: []mcpclient.ServerStatus{{
			Name:            "github",
			Transport:       "stdio",
			Command:         "npx",
			Enabled:         true,
			Connected:       true,
			State:           "reconnected",
			ToolCount:       7,
			PID:             4242,
			StartedAt:       &startedAt,
			LastConnectedAt: &connectedAt,
			LastEventAt:     &eventAt,
			RestartCount:    1,
			RetryCount:      1,
		}, {
			Name:           "context7",
			Transport:      "stdio",
			Command:        "npx",
			Enabled:        true,
			Connected:      false,
			State:          "failed",
			Error:          "MCP context7 transport: EOF",
			LastError:      "MCP context7 transport: EOF",
			LastErrorAt:    &lastErrorAt,
			StderrTail:     "server closed",
			RetryBackoffMS: 5000,
			NextRetryAt:    &nextRetryAt,
		}, {
			Name:              "remote",
			Transport:         "streamable-http",
			URL:               "https://example.com/mcp",
			Enabled:           true,
			State:             "unsupported",
			UnsupportedReason: "streamable HTTP MCP is not implemented in billyharness yet; use stdio MCP or remove the url server",
			Error:             "MCP server remote unsupported: streamable HTTP MCP is not implemented in billyharness yet; use stdio MCP or remove the url server",
		}},
		Prompts: []mcpclient.Prompt{{
			Server:      "github",
			Name:        "review",
			Description: "Review a target",
			Arguments:   []mcpclient.PromptArgument{{Name: "target", Required: true}},
		}},
	})
	for _, want := range []string{
		"source: runtime config",
		"/root/billyharness/mcp.config.toml",
		"allowed: telegram, telegram-parilka, github, context7",
		"native: web_search, web_fetch, web_extract, web_crawl",
		"github",
		"reconnected",
		"command:npx",
		"tools:7",
		"pid:4242",
		"restarts:1",
		"retries:1",
		"connected_at:08:00:02",
		"event_at:08:00:03",
		"context7",
		"failed",
		"MCP context7 transport: EOF",
		"backoff:5000ms",
		"next_retry:08:01:05",
		"last_error_at: 2026-06-28 08:01:00",
		"stderr: server closed",
		"remote",
		"unsupported",
		"streamable-http",
		"url:https://example.com/mcp",
		"unsupported: streamable HTTP MCP is not implemented",
		"prompts:",
		"github/review <target> - Review a target (metadata only)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("mcp status missing %q: %q", want, text)
		}
	}
}

func TestAuthDeepSeekGatewayFlowDoesNotRenderSecret(t *testing.T) {
	var captured map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/deepseek" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deepseek": map[string]any{"configured": true, "path": "/root/billyharness/.env"},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	handled, cmd := m.handleSlashCommand("/auth deepseek")
	if !handled || cmd != nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	if m.authInputProvider != "deepseek" {
		t.Fatalf("authInputProvider = %q", m.authInputProvider)
	}
	m.textarea.SetValue("sk-secret-value")
	next, cmd := m.send()
	updated := next.(Model)
	if updated.textarea.Value() != "" {
		t.Fatalf("textarea should be cleared")
	}
	msg := cmd().(authResultMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if captured["api_key"] != "sk-secret-value" {
		t.Fatalf("captured = %#v", captured)
	}
	if strings.Contains(msg.text, "sk-secret-value") {
		t.Fatalf("auth result leaked secret: %q", msg.text)
	}
}

func TestAuthCodexGatewayImport(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/codex/import" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		called = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"codex": map[string]any{"configured": true, "path": "/root/billyharness/auth/codex.json", "account_id": "acct_123"},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	handled, cmd := m.handleSlashCommand("/auth codex")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(authResultMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if !called || !strings.Contains(msg.text, "acct_123") {
		t.Fatalf("called=%v text=%q", called, msg.text)
	}
	for _, want := range []string{"codex: configured", "credential=redacted"} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("auth import text missing %q: %q", want, msg.text)
		}
	}
}

func TestConfigCommandShowsSanitizedGatewaySummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/config" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"values": []config.ResolvedValue{
				{Key: "provider", Value: "deepseek", Source: config.SourceGateway, SourceKey: "provider"},
				{Key: "model", Value: "deepseek-v4-flash", Source: config.SourceGateway, SourceKey: "model"},
				{Key: "api_key", Value: "[redacted]", Redacted: true, Source: config.SourceEnvironment, SourceKey: "DEEPSEEK_API_KEY"},
			},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	handled, cmd := m.handleSlashCommand("/config")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(configStatusMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	for _, want := range []string{"billyharness config", "provider:", "deepseek", "model:", "deepseek-v4-flash"} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("config summary missing %q:\n%s", want, msg.text)
		}
	}
	if strings.Contains(msg.text, "sk-") || strings.Contains(msg.text, "DEEPSEEK_API_KEY=") {
		t.Fatalf("config summary leaked secret-ish content:\n%s", msg.text)
	}
}

func TestContextCommandShowsGatewayContextReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/session-1/context" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(gatewayapi.SessionContextResponse{
			ID:                      "session-1",
			MessageCount:            4,
			EstimatedTokens:         580000,
			ContextWindowTokens:     1000000,
			ContextCompactTokens:    600000,
			PercentUsed:             58,
			CompactThresholdPercent: 60,
			Estimator:               "chars_div_4",
			Runtime:                 gatewayapi.ContextRuntime{Model: "deepseek-v4-flash", ReasoningMode: "high", AccessMode: "build"},
			Usage:                   gatewayapi.ContextUsage{CacheHitTokens: 700, CacheMissTokens: 300, WebSummaryInputTokens: 20000, WebSummaryOutputTokens: 900, HelperModelAPITokens: 20900, HelperAPICalls: 2, HelperCostUSD: 0.0045},
			Prompt:                  gatewayapi.ContextPrompt{SectionCount: 2, ApproxTokens: 1200, TotalBytes: 4800, CacheStatus: "changed", CacheReason: "tool_schema_changed"},
			Sources: []gatewayapi.ContextSource{
				{Source: "web_summaries", MessageCount: 2, EstimatedTokens: 320000, Percent: 55.2},
				{Source: "user_messages", MessageCount: 1, EstimatedTokens: 1000, Percent: 0.2},
			},
			Thresholds: []gatewayapi.ContextThreshold{
				{Percent: 50, Tokens: 500000, Crossed: true},
				{Percent: 70, Tokens: 700000, RemainingTokens: 120000},
			},
			TopContributors: []gatewayapi.ContextContributor{
				{Index: 2, Role: "tool", Source: "web_summaries", Name: "web_fetch", EstimatedTokens: 320000, Preview: "summary"},
			},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	handled, cmd := m.handleSlashCommand("/context")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(contextStatusMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	for _, want := range []string{"active context: 580.0k / 1.00M", "compact threshold: 600.0k (60.0%, below)", "runtime: model=deepseek-v4-flash", "provider cache: hit=700", "helper usage: websum=20.0k", "sumapi=20.9k", "helper API calls=2", "helper API cost=$0.004500", "prompt cache: status=changed", "thresholds: ●50% ○70%", "web_summaries", "top contributors"} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("context report missing %q:\n%s", want, msg.text)
		}
	}
}

func TestProcessesCommandShowsGatewayDashboard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/processes" || r.URL.Query().Get("include_exited") != "true" {
			t.Fatalf("url = %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(gatewayapi.ManagedProcessResponse{
			Processes: protocol.ManagedProcessList{
				Running: 1,
				Processes: []protocol.ManagedProcessStatus{{
					ID:            "shell-1",
					Running:       true,
					ElapsedMS:     2500,
					DetectedPorts: []int{5173},
					OutputRef:     "/tmp/tool-output/shell.txt",
				}},
			},
			Text: "managed shell processes: 1 running, 0 exited\n- shell-1 running ports=5173 output_ref=/tmp/tool-output/shell.txt",
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	handled, cmd := m.handleSlashCommand("/processes")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(processStatusMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	for _, want := range []string{"managed shell processes", "shell-1", "ports=5173", "output_ref="} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("process dashboard missing %q:\n%s", want, msg.text)
		}
	}
}

func TestMemoryCommandManagesLocalMemory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	m := newTestModel(t)
	m.instructions = config.Default().InstructionSettings()

	handled, cmd := m.handleSlashCommand(`/memory add type=user topic=style summary="Prefers concise evidence" path=topics/style.md body="Concise evidence only" confirm=true`)
	if !handled || cmd != nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].Title != "MEMORY" {
		t.Fatalf("memory add did not append memory block")
	}
	if body := m.blocks[len(m.blocks)-1].Content; !strings.Contains(body, "written=true") {
		t.Fatalf("memory add block = %s", body)
	}
	handled, cmd = m.handleSlashCommand(`/memory list`)
	if !handled || cmd != nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	body := m.blocks[len(m.blocks)-1].Content
	if !strings.Contains(body, "topic=style") || !strings.Contains(body, "prefers concise evidence") {
		t.Fatalf("memory list block = %s", body)
	}
}

func TestDiffCommandRequestsGatewayPreview(t *testing.T) {
	var gotReq gatewayapi.SessionUndoRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/session-1/undo" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(gatewayapi.SessionUndoResponse{
			ChangeID: "change-1",
			Preview:  true,
			Patch:    "--- before\n+++ after\n@@ -1 +1 @@\n-old\n+new\n",
			Change: protocol.TurnChangeEvent{
				ChangeID:       "change-1",
				FileCount:      1,
				Modified:       1,
				Additions:      1,
				Deletions:      1,
				Reversible:     true,
				PatchOutputRef: "/root/billyharness/tool-output/change-1.json",
				Files: []protocol.TurnChangeFile{
					{RelPath: "README.md", Change: "modified", Additions: 1, Deletions: 1, Reversible: true},
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	handled, cmd := m.handleSlashCommand("/diff change-1")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd)
	}
	msg := cmd().(turnDiffPreviewMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if gotReq.ChangeID != "change-1" || !gotReq.Preview {
		t.Fatalf("undo request = %#v", gotReq)
	}
	for _, want := range []string{"summary: 1 file", "patch_ref: /root/billyharness/tool-output/change-1.json", "preview:", "@@ -1 +1 @@", "+new"} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("diff preview missing %q:\n%s", want, msg.text)
		}
	}
}

func TestUndoRedoCommandsRequestGatewayApply(t *testing.T) {
	var undoReq gatewayapi.SessionUndoRequest
	var redoCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q", r.Method)
		}
		switch r.URL.Path {
		case "/v1/sessions/session-1/undo":
			if err := json.NewDecoder(r.Body).Decode(&undoReq); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(gatewayapi.SessionUndoResponse{
				ChangeID:      "change-1",
				RestoredFiles: []string{"/workspace/README.md"},
				Change: protocol.TurnChangeEvent{
					ChangeID:   "change-1",
					Status:     "reverted",
					FileCount:  1,
					Modified:   1,
					Reversible: true,
					Files:      []protocol.TurnChangeFile{{RelPath: "README.md", Change: "modified", Reversible: true}},
				},
			})
		case "/v1/sessions/session-1/redo":
			redoCalled = true
			_ = json.NewEncoder(w).Encode(gatewayapi.SessionUndoResponse{
				ChangeID:      "change-1",
				RestoredFiles: []string{"/workspace/README.md"},
				Change: protocol.TurnChangeEvent{
					ChangeID:   "change-1",
					Status:     "redone",
					FileCount:  1,
					Modified:   1,
					Reversible: true,
					Files:      []protocol.TurnChangeFile{{RelPath: "README.md", Change: "modified", Reversible: true}},
				},
			})
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	handled, cmd := m.handleSlashCommand("/undo change-1")
	if !handled || cmd == nil {
		t.Fatalf("undo handled=%v cmd=%v", handled, cmd)
	}
	undoMsg := cmd().(turnUndoMsg)
	if undoMsg.err != nil {
		t.Fatal(undoMsg.err)
	}
	if undoReq.ChangeID != "change-1" || undoReq.Preview {
		t.Fatalf("undo request = %#v", undoReq)
	}
	if !strings.Contains(undoMsg.text, "status: reverted") || !strings.Contains(undoMsg.text, "undo files:") {
		t.Fatalf("undo text = %q", undoMsg.text)
	}

	handled, cmd = m.handleSlashCommand("/redo")
	if !handled || cmd == nil {
		t.Fatalf("redo handled=%v cmd=%v", handled, cmd)
	}
	redoMsg := cmd().(turnRedoMsg)
	if redoMsg.err != nil {
		t.Fatal(redoMsg.err)
	}
	if !redoCalled || !strings.Contains(redoMsg.text, "status: redone") || !strings.Contains(redoMsg.text, "redo files:") {
		t.Fatalf("redoCalled=%v text=%q", redoCalled, redoMsg.text)
	}
}

type testModelHelper interface {
	Helper()
	Cleanup(func())
	Setenv(string, string)
	TempDir() string
}
