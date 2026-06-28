package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestDefaultsToDarkTheme(t *testing.T) {
	m := newTestModel(t)
	if !m.textarea.Focused() {
		t.Fatalf("textarea should start focused")
	}
	if m.theme != "dark" {
		t.Fatalf("theme = %q, want dark", m.theme)
	}
	if got := m.currentModel(); got != "deepseek-v4-flash" {
		t.Fatalf("model = %q, want deepseek-v4-flash", got)
	}
	if got := m.currentThinking().effort; got != "high" {
		t.Fatalf("reasoning effort = %q, want high", got)
	}
	if got := m.toolView; got != "collapsed" {
		t.Fatalf("toolView = %q, want collapsed", got)
	}
}

func TestGatewayNoticeSetsInitialStatus(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	m := NewModel(config.Default(), Options{GatewayNotice: "gateway http://127.0.0.1:8765 is not reachable; local mode active"})
	if !strings.Contains(m.status, "local mode active") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestSlashCommands(t *testing.T) {
	m := newTestModel(t)

	for _, tc := range []struct {
		input     string
		wantModel string
		wantThink string
		wantTheme string
	}{
		{input: "/theme dark", wantTheme: "dark"},
		{input: "/theme light", wantTheme: "light"},
		{input: "/model pro", wantModel: "deepseek-v4-pro"},
		{input: "/model flash", wantModel: "deepseek-v4-flash"},
		{input: "/model gpt", wantModel: "gpt-5.5"},
		{input: "/model spark", wantModel: "gpt-5.3-codex-spark"},
		{input: "/reasoning max", wantThink: "max"},
		{input: "/reasoning xhigh", wantThink: "xhigh"},
		{input: "/reasoning medium", wantThink: "medium"},
		{input: "/reasoning low", wantThink: "low"},
		{input: "/reasoning off", wantThink: ""},
		{input: "/reasoning high", wantThink: "high"},
		{input: "/thinking off"},
		{input: "/thinking on"},
		{input: "/toolview collapsed"},
		{input: "/thinkview collapsed"},
	} {
		handled, _ := m.handleSlashCommand(tc.input)
		if !handled {
			t.Fatalf("handleSlashCommand(%q) returned false", tc.input)
		}
		if tc.wantModel != "" && m.currentModel() != tc.wantModel {
			t.Fatalf("%q model = %q, want %q", tc.input, m.currentModel(), tc.wantModel)
		}
		if tc.wantTheme != "" && m.theme != tc.wantTheme {
			t.Fatalf("%q theme = %q, want %q", tc.input, m.theme, tc.wantTheme)
		}
		if strings.HasPrefix(tc.input, "/reasoning") && (tc.wantThink != "" || strings.Contains(tc.input, " off")) {
			if got := m.currentThinking().effort; got != tc.wantThink {
				t.Fatalf("%q reasoning = %q, want %q", tc.input, got, tc.wantThink)
			}
		}
	}
}

func TestTUISelectsCodexProviderForGPTModels(t *testing.T) {
	m := newTestModel(t)
	handled, _ := m.handleSlashCommand("/model gpt")
	if !handled {
		t.Fatalf("/model gpt returned false")
	}
	if got := m.currentProvider(); got != "openai-codex" {
		t.Fatalf("currentProvider = %q", got)
	}
	if got := m.currentConfig().Provider; got != "openai-codex" {
		t.Fatalf("currentConfig.Provider = %q", got)
	}
	if got := m.costText(); got != "cost subscription" {
		t.Fatalf("costText = %q", got)
	}

	handled, _ = m.handleSlashCommand("/model flash")
	if !handled {
		t.Fatalf("/model flash returned false")
	}
	if got := m.currentProvider(); got != "deepseek" {
		t.Fatalf("currentProvider = %q", got)
	}
}

func TestRunGatewaySendsSelectedProviderModelAndReasoning(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sessions/session-1/run":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(protocol.Event{Type: protocol.EventAssistantDelta, Data: "ok"})
		case "/v1/sessions/session-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"messages": []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}, {Role: protocol.RoleAssistant, Content: "ok"}},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	m.maxRounds = 77
	if ok, _ := m.handleSlashCommand("/model gpt"); !ok {
		t.Fatal("/model gpt failed")
	}
	if ok, _ := m.handleSlashCommand("/reasoning xhigh"); !ok {
		t.Fatal("/reasoning xhigh failed")
	}
	m.runGateway("ping")
	var done runDoneMsg
	for i := 0; i < 3; i++ {
		msg := <-m.events
		if typed, ok := msg.(runDoneMsg); ok {
			done = typed
			break
		}
	}
	if done.err != nil {
		t.Fatal(done.err)
	}
	if captured["provider"] != "openai-codex" ||
		captured["model"] != "gpt-5.5" ||
		captured["profile"] != "billy" ||
		captured["thinking"] != "enabled" ||
		captured["reasoning_effort"] != "xhigh" ||
		int(captured["max_tool_rounds"].(float64)) != 77 ||
		captured["prompt"] != "ping" {
		t.Fatalf("captured = %#v", captured)
	}
}

func TestTUITracksGatewayEventSeq(t *testing.T) {
	m := newTestModel(t)
	m.applyEvent(protocol.Event{Seq: 5, Type: protocol.EventRunStarted})
	m.applyEvent(protocol.Event{Seq: 3, Type: protocol.EventAssistantDelta, Data: "older"})
	if got := m.lastGatewayEventSeq; got != 5 {
		t.Fatalf("lastGatewayEventSeq = %d, want 5", got)
	}
}

func TestReplayGatewayEventsCmdFetchesAfterSeq(t *testing.T) {
	var sawReplay bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sessions/session-1/events":
			if got := r.URL.Query().Get("after_seq"); got != "7" {
				t.Fatalf("after_seq = %q, want 7", got)
			}
			if got := r.URL.Query().Get("follow"); got != "false" {
				t.Fatalf("follow = %q, want false", got)
			}
			sawReplay = true
			_ = json.NewEncoder(w).Encode(protocol.Event{Seq: 8, Type: protocol.EventRunStarted})
			_ = json.NewEncoder(w).Encode(protocol.Event{Seq: 9, Type: protocol.EventAssistantDelta, Data: "replayed"})
		case "/v1/sessions/session-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"messages": []protocol.Message{{Role: protocol.RoleAssistant, Content: "replayed"}},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	m.lastGatewayEventSeq = 7
	msg := m.replayGatewayEventsCmd(false)()
	typed, ok := msg.(replayEventsMsg)
	if !ok {
		t.Fatalf("message = %T, want replayEventsMsg", msg)
	}
	if typed.err != nil {
		t.Fatal(typed.err)
	}
	next, _ := m.Update(typed)
	updated := next.(Model)
	if !sawReplay {
		t.Fatal("replay endpoint was not called")
	}
	if updated.lastGatewayEventSeq != 9 {
		t.Fatalf("lastGatewayEventSeq = %d, want 9", updated.lastGatewayEventSeq)
	}
	if len(updated.messages) != 1 || updated.messages[0].Content != "replayed" {
		t.Fatalf("messages = %#v", updated.messages)
	}
}

func TestProfileSlashCommandStartsNewProfileChat(t *testing.T) {
	m := newTestModel(t)
	oldChat := m.localChatID
	handled, cmd := m.handleSlashCommand("/profile Billy/Profile")
	if !handled {
		t.Fatal("/profile failed")
	}
	if cmd != nil {
		t.Fatalf("local profile switch should not create gateway command")
	}
	if got := m.currentProfile(); got != "billyprofile" {
		t.Fatalf("profile = %q", got)
	}
	if m.localChatID == oldChat {
		t.Fatalf("profile switch should start a new chat")
	}
	if len(m.messages) != 1 {
		t.Fatalf("custom missing profile should fall back to base system only, got %#v", m.messages)
	}

	handled, _ = m.handleSlashCommand("/profile billy")
	if !handled {
		t.Fatal("/profile billy failed")
	}
	if len(m.messages) != 2 || !strings.Contains(m.messages[1].Content, "# Billyharness profile: billy") {
		t.Fatalf("billy profile not injected: %#v", m.messages)
	}
}

func TestProfileSlashCommandAppliesProfileMetadata(t *testing.T) {
	m := newTestModel(t)
	dir := filepath.Join(config.BillyHomeDir(), "profiles", "pro")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.toml"), []byte(`
name = "pro"
model = "deepseek-v4-pro"
thinking = "enabled"
reasoning_effort = "max"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	handled, cmd := m.handleSlashCommand("/profile pro")
	if !handled || cmd != nil {
		t.Fatalf("/profile pro handled=%t cmd=%v", handled, cmd)
	}
	if got := m.currentModel(); got != "deepseek-v4-pro" {
		t.Fatalf("model = %q", got)
	}
	if got := m.currentThinking().effort; got != "max" {
		t.Fatalf("reasoning = %q", got)
	}
}

func TestRunGatewayTurnsStreamedRunFailedIntoRunDoneError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/session-1/run" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(protocol.Event{Type: protocol.EventRunFailed, Data: "boom"})
	}))
	t.Cleanup(server.Close)

	m := newTestModel(t)
	m.gatewayURL = server.URL
	m.sessionID = "session-1"
	m.runGateway("ping")
	var done runDoneMsg
	for i := 0; i < 3; i++ {
		msg := <-m.events
		if typed, ok := msg.(runDoneMsg); ok {
			done = typed
			break
		}
	}
	if done.err == nil || !strings.Contains(done.err.Error(), "boom") {
		t.Fatalf("done = %#v", done)
	}
}

func TestHiddenReasoningIsPreserved(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24

	handled, _ := m.handleSlashCommand("/thinking off")
	if !handled {
		t.Fatalf("/thinking off returned false")
	}
	m.appendToOpenBlock("reasoning", "THINKING", "hidden reasoning", protocol.EventAssistantReasoning)
	if len(m.blocks) != 1 {
		t.Fatalf("reasoning block was not preserved")
	}
	m.resize(false)
	if strings.Contains(m.viewport.View(), "hidden reasoning") {
		t.Fatalf("hidden reasoning should not render")
	}

	handled, _ = m.handleSlashCommand("/thinking on")
	if !handled {
		t.Fatalf("/thinking on returned false")
	}
	m.reflow(false)
	if !strings.Contains(m.viewport.View(), "hidden reasoning") {
		t.Fatalf("reasoning should render again after /thinking on")
	}
}

func TestToolAndThinkViewsAffectRendering(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("tool", "TOOL shell", "line1\nline2\nline3")
	m.addBlock("reasoning", "THINKING", "private chain")

	handled, _ := m.handleSlashCommand("/toolview hidden")
	if !handled {
		t.Fatalf("/toolview hidden returned false")
	}
	handled, _ = m.handleSlashCommand("/thinkview collapsed")
	if !handled {
		t.Fatalf("/thinkview collapsed returned false")
	}
	m.resize(false)
	view := m.viewport.View()
	if strings.Contains(view, "TOOL shell") {
		t.Fatalf("hidden tool block should not render")
	}
	if !strings.Contains(view, "[collapsed:") || strings.Contains(view, "private chain") {
		t.Fatalf("collapsed thinking should render a preview without full content, view=%q", view)
	}
}

func TestToolAndThinkingBlocksRenderWithoutSelectionMarkersOrIndent(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	tool := stripANSITest(m.renderBlock(0, block{kind: "tool", title: "TOOL shell", content: "result"}))
	thinking := stripANSITest(m.renderBlock(1, block{kind: "reasoning", title: "THINKING", content: "thought"}))
	for _, rendered := range []string{tool, thinking} {
		if strings.Contains(rendered, ">") {
			t.Fatalf("block should not render selection marker: %q", rendered)
		}
		if strings.Contains(rendered, "┌") || strings.Contains(rendered, "└─") {
			t.Fatalf("activity block should not render heavy box borders: %q", rendered)
		}
	}
}

func TestToolBlocksRenderCodexActivityStyle(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.toolView = "expanded"
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			Name:      "shell_exec",
			Arguments: json.RawMessage(`{"argv":["rg","-n","selection","internal/tui"],"cwd":"/root/billyharness","timeout_sec":20}`),
		},
	})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: "internal/tui/tui.go:2422: selection\n"})

	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	for _, want := range []string{"• Ran rg -n selection internal/tui", "└ cwd: /root/billyharness", "│ internal/tui/tui.go:2422: selection"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("tool activity block missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "TOOL") || strings.Contains(rendered, `"argv"`) {
		t.Fatalf("tool activity block should not show raw tool/json chrome: %q", rendered)
	}
}

func TestToolBlocksAreOneLineByDefault(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			Name:      "web_search",
			Arguments: json.RawMessage(`{"query":"agent loop benchmark","limit":5}`),
		},
	})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: strings.Repeat("result line\n", 20)})

	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if got := strings.Count(strings.TrimSpace(rendered), "\n"); got != 0 {
		t.Fatalf("collapsed tool block should be one line, got %d newlines: %q", got, rendered)
	}
	if !strings.Contains(rendered, "• Searched web agent loop benchmark") {
		t.Fatalf("collapsed tool block should show query in title: %q", rendered)
	}
	if strings.Contains(rendered, "result line") || strings.Contains(rendered, `"query"`) {
		t.Fatalf("collapsed tool block should not show output or raw JSON: %q", rendered)
	}

	m.toggleSelectedBlock()
	expanded := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(expanded, "result line") {
		t.Fatalf("Ctrl+E toggle should expand selected collapsed tool block: %q", expanded)
	}
}

func TestToolResultsUpdateMatchingBlockByCallIDOutOfOrder(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.toolView = "expanded"
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call-a",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"a.txt"}`),
		},
	})
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			ID:        "call-b",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"b.txt"}`),
		},
	})

	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{CallID: "call-b", Name: "fs_read_file", Content: "beta"}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{CallID: "call-a", Name: "fs_read_file", Content: "alpha"}})

	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(m.blocks))
	}
	if !strings.Contains(m.blocks[0].content, "alpha") || strings.Contains(m.blocks[0].content, "beta") {
		t.Fatalf("call-a block content = %q", m.blocks[0].content)
	}
	if !strings.Contains(m.blocks[1].content, "beta") || strings.Contains(m.blocks[1].content, "alpha") {
		t.Fatalf("call-b block content = %q", m.blocks[1].content)
	}
	if m.blocks[0].callID != "call-a" || m.blocks[1].callID != "call-b" {
		t.Fatalf("call ids = %q %q", m.blocks[0].callID, m.blocks[1].callID)
	}
}

func TestTranscriptBlocksCarryTypedCellMetadata(t *testing.T) {
	m := newTestModel(t)
	m.addBlock("user", "USER", "hello")
	if m.blocks[0].cellType != cellTypeUser || m.blocks[0].id == "" || m.blocks[0].renderCacheKey == "" || m.blocks[0].rawCopy != "hello" {
		t.Fatalf("user cell = %#v", m.blocks[0])
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "draft"})
	assistantIndex := len(m.blocks) - 1
	streamKey := m.blocks[assistantIndex].renderCacheKey
	if m.blocks[assistantIndex].cellType != cellTypeAssistantStream || !m.blocks[assistantIndex].live {
		t.Fatalf("assistant stream cell = %#v", m.blocks[assistantIndex])
	}
	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})
	if m.blocks[assistantIndex].cellType != cellTypeAssistantFinal || m.blocks[assistantIndex].live || m.blocks[assistantIndex].renderCacheKey == streamKey {
		t.Fatalf("assistant final cell = %#v", m.blocks[assistantIndex])
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantReasoning, Data: "hidden"})
	if got := m.blocks[len(m.blocks)-1].cellType; got != cellTypeThinking {
		t.Fatalf("thinking cellType = %q", got)
	}

	m.applyEvent(protocol.Event{
		Type:   protocol.EventToolCallRequested,
		TurnID: "turn-001",
		StepID: "turn-001:tool-call-001",
		Data: protocol.ToolCall{
			ID:        "call-1",
			Name:      "fs_read_file",
			Arguments: json.RawMessage(`{"path":"README.md"}`),
		},
	})
	toolCell := m.blocks[len(m.blocks)-1]
	if toolCell.cellType != cellTypeToolCall || toolCell.turnID != "turn-001" || toolCell.stepID != "turn-001:tool-call-001" || toolCell.callID != "call-1" {
		t.Fatalf("tool cell = %#v", toolCell)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventContextCompacted, Data: map[string]any{"reason": "threshold"}})
	if got := m.blocks[len(m.blocks)-1].cellType; got != cellTypeCompaction {
		t.Fatalf("compaction cellType = %q", got)
	}
	m.addInfoBlock("MCP", "connected")
	if got := m.blocks[len(m.blocks)-1].cellType; got != cellTypeMCPStatus {
		t.Fatalf("mcp cellType = %q", got)
	}
	m.addBlock("error", "ERROR", "boom")
	if got := m.blocks[len(m.blocks)-1].cellType; got != cellTypeError {
		t.Fatalf("error cellType = %q", got)
	}

	decoded := decodeBlocks(encodeBlocks(m.blocks))
	m.blocks = decoded
	m.ensureBlockMetadata()
	for i, block := range m.blocks {
		if block.id == "" || block.cellType == "" || block.renderCacheKey == "" || block.rawCopy == "" {
			t.Fatalf("decoded block[%d] missing metadata: %#v", i, block)
		}
	}
}

func TestToolAuditUpdatesMatchingBlockByCallID(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.toolView = "expanded"
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{ID: "call-a", Name: "fs_write_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
	})
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{ID: "call-b", Name: "fs_read_file", Arguments: json.RawMessage(`{"path":"b.txt"}`)},
	})
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolAudit,
		Data: map[string]any{
			"call_id":       "call-a",
			"name":          "fs_write_file",
			"risk":          "write",
			"auto_approved": true,
		},
	})

	if !strings.Contains(m.blocks[0].content, "audit: write fs_write_file auto-approved") {
		t.Fatalf("call-a block content = %q", m.blocks[0].content)
	}
	if strings.Contains(m.blocks[1].content, "audit:") {
		t.Fatalf("call-b should not receive call-a audit: %q", m.blocks[1].content)
	}
}

func TestToolBlocksCompactLongWebFetchURL(t *testing.T) {
	m := newTestModel(t)
	m.width = 160
	m.applyEvent(protocol.Event{
		Type: protocol.EventToolCallRequested,
		Data: protocol.ToolCall{
			Name:      "web_fetch",
			Arguments: json.RawMessage(`{"url":"https://example.com/some/really/long/path/that/should/not/eat/the/whole/tui/line?with=a&lot=of&query=params"}`),
		},
	})

	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(rendered, "Fetched example.com") {
		t.Fatalf("tool block should show compact host/path: %q", rendered)
	}
	if strings.Contains(rendered, "with=a&lot=of&query=params") {
		t.Fatalf("tool block leaked full query string: %q", rendered)
	}
	if len([]rune(rendered)) > 140 {
		t.Fatalf("tool block title too long: %d %q", len([]rune(rendered)), rendered)
	}
}

func TestUserAndAssistantBlocksRenderWithoutRoleLabels(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	user := m.renderBlock(0, block{kind: "user", title: "USER", content: "hello"})
	assistant := m.renderBlock(1, block{kind: "assistant", title: "ASSISTANT", content: "world"})
	if strings.Contains(strings.ToLower(user), "user") {
		t.Fatalf("user block should not render role label: %q", user)
	}
	if strings.Contains(strings.ToLower(assistant), "assistant") {
		t.Fatalf("assistant block should not render role label: %q", assistant)
	}
	if !strings.Contains(user, "hello") || !strings.Contains(assistant, "world") {
		t.Fatalf("blocks should render content, got user=%q assistant=%q", user, assistant)
	}
}

func TestAssistantBlockRendersTerminalSafeMarkdown(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	rendered := stripANSITest(m.renderBlock(0, block{kind: "assistant", title: "ASSISTANT", content: strings.Join([]string{
		"# Summary",
		"",
		"- **fast** path with `code`",
		"- _lean_ path",
		"1. [docs](https://example.com)",
		"> quoted",
		"---",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
		"```go",
		"fmt.Println(1)",
		"```",
	}, "\n")}))
	for _, want := range []string{"Summary", "•", "fast", "lean", "code", "docs", "https://example.com", "│ quoted", "────", "┌", "Name", "Billy", "10", "fmt.Println(1)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown missing %q: %q", want, rendered)
		}
	}
	for _, leak := range []string{"```", "**", "_lean_", "`10`"} {
		if strings.Contains(rendered, leak) {
			t.Fatalf("markdown syntax %q should not leak into rendered output: %q", leak, rendered)
		}
	}
}

func TestMarkdownTableDoesNotLeakInlineDelimitersWhenTruncated(t *testing.T) {
	m := newTestModel(t)
	m.width = 48
	rendered := stripANSITest(m.renderBlock(0, block{kind: "assistant", title: "ASSISTANT", content: strings.Join([]string{
		"| Параметр | Значение |",
		"| --- | --- |",
		"| 🌡 **Температура с очень длинным описанием** | +21 °C |",
		"| 💦 **Влажность воздуха тоже длинная** | 45% |",
	}, "\n")}))
	for _, want := range []string{"┌", "Температ", "Влажност", "+21", "45%"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered table missing %q: %q", want, rendered)
		}
	}
	for _, leak := range []string{"**", "__", "`"} {
		if strings.Contains(rendered, leak) {
			t.Fatalf("table markdown delimiter %q leaked after truncation: %q", leak, rendered)
		}
	}
}

func TestLiveAssistantMarkdownKeepsUnstableTailRawUntilCompleted(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Join([]string{
		"## Weather",
		"",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
	}, "\n")})
	if len(m.blocks) != 1 || !m.blocks[0].live {
		t.Fatalf("assistant block should be live: %#v", m.blocks)
	}

	live := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "| **Billy** | `10` |") {
		t.Fatalf("live markdown tail should stay raw, got: %q", live)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})
	if m.blocks[0].live {
		t.Fatalf("assistant block should be finalized")
	}
	final := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(final, "┌") || !strings.Contains(final, "Billy") || !strings.Contains(final, "10") {
		t.Fatalf("final markdown table did not render: %q", final)
	}
	for _, leak := range []string{"**", "`10`"} {
		if strings.Contains(final, leak) {
			t.Fatalf("final markdown syntax %q leaked: %q", leak, final)
		}
	}
}

func TestAssistantDeltasUpdateSingleLiveBlock(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "Intro\n"})
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "This is **bo"})
	live := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "Intro") || !strings.Contains(live, "This is **bo") {
		t.Fatalf("incomplete bold line should stay raw while live: %q", live)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "ld**\n"})
	if len(m.blocks) != 1 {
		t.Fatalf("assistant deltas should update one block, got %#v", m.blocks)
	}
	if !m.blocks[0].live {
		t.Fatalf("assistant block should remain live")
	}
	if got := m.blocks[0].content; got != "Intro\nThis is **bold**\n" {
		t.Fatalf("assistant content = %q", got)
	}
	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if strings.Contains(rendered, "**") || !strings.Contains(rendered, "bold") {
		t.Fatalf("completed bold line should render as markdown while live: %q", rendered)
	}
}

func TestLiveAssistantMarkdownWaitsForCodeFenceBoundary(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Join([]string{
		"Before",
		"",
		"```go",
		"fmt.Println(1)",
		"```",
	}, "\n")})

	live := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "Before") {
		t.Fatalf("stable prefix should render before open fence tail: %q", live)
	}
	if !strings.Contains(live, "```go") || !strings.Contains(live, "fmt.Println(1)") {
		t.Fatalf("fence without newline boundary should stay raw while live: %q", live)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventRunCompleted})
	final := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if strings.Contains(final, "```") || !strings.Contains(final, "fmt.Println(1)") {
		t.Fatalf("final code fence should render without markdown fences: %q", final)
	}
}

func TestLiveAssistantMarkdownWaitsForTableBoundary(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: strings.Join([]string{
		"Scores",
		"",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
	}, "\n") + "\n"})

	live := stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "Scores") {
		t.Fatalf("stable prefix should render before table tail: %q", live)
	}
	if !strings.Contains(live, "| **Billy** | `10` |") || strings.Contains(live, "┌") {
		t.Fatalf("table without boundary should stay raw while live: %q", live)
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "\nDone\n"})
	live = stripANSITest(m.renderBlock(0, m.blocks[0]))
	if !strings.Contains(live, "┌") || !strings.Contains(live, "Billy") || !strings.Contains(live, "10") || !strings.Contains(live, "Done") {
		t.Fatalf("table should render after explicit boundary: %q", live)
	}
	for _, leak := range []string{"**", "`10`"} {
		if strings.Contains(live, leak) {
			t.Fatalf("rendered table syntax %q leaked: %q", leak, live)
		}
	}
}

func TestToolAuditRendersCompactBlock(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.applyEvent(protocol.Event{Type: protocol.EventToolAudit, Data: map[string]any{
		"name":          "shell_exec",
		"risk":          string(protocol.RiskExecute),
		"auto_approved": true,
	}})

	if len(m.blocks) != 1 || m.blocks[0].kind != "audit" {
		t.Fatalf("expected audit block, got %#v", m.blocks)
	}
	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	for _, want := range []string{"Tool audit", "execute shell_exec auto-approved"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("audit render missing %q: %q", want, rendered)
		}
	}
}

func TestToolAuditDoesNotSplitToolResult(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.toolView = "expanded"
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallRequested, Data: protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: json.RawMessage(`{"argv":["pwd"],"cwd":"/root/billyharness"}`),
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolAudit, Data: map[string]any{
		"name":          "shell_exec",
		"risk":          string(protocol.RiskExecute),
		"auto_approved": true,
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventToolCallFinished, Data: protocol.ToolResult{
		Name:    "shell_exec",
		Content: "/root/billyharness\n",
	}})

	if len(m.blocks) != 1 || m.blocks[0].kind != "tool" {
		t.Fatalf("tool audit/result should stay in one block: %#v", m.blocks)
	}
	rendered := stripANSITest(m.renderBlock(0, m.blocks[0]))
	for _, want := range []string{"audit: execute shell_exec auto-approved", "/root/billyharness"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("tool block missing %q: %q", want, rendered)
		}
	}
}

func TestUnsupportedMarkdownImageIsOmitted(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	rendered := stripANSITest(m.renderBlock(0, block{kind: "assistant", title: "ASSISTANT", content: "![secret diagram](https://example.com/image.png)"}))
	if strings.Contains(rendered, "https://example.com/image.png") {
		t.Fatalf("image URL should not render as supported markdown: %q", rendered)
	}
	if !strings.Contains(rendered, "image omitted: secret diagram") {
		t.Fatalf("image placeholder missing: %q", rendered)
	}
}

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

func TestTranscriptSelectionText(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "alpha\nbeta\ngamma")
	m.resize(true)
	m.selectStart = selectionPoint{row: 0, col: 1}
	m.selectEnd = selectionPoint{row: 1, col: 4}
	got := stripANSITest(m.selectedTranscriptText())
	if !strings.Contains(got, "lpha") || !strings.Contains(got, "bet") {
		t.Fatalf("selected text = %q", got)
	}
	start, end := m.selectionByteRange()
	if start < 0 || end <= start {
		t.Fatalf("selection byte range = %d:%d, want visible highlight range", start, end)
	}
}

func TestTranscriptSelectionClampKeepsRightEdgeExclusive(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(3)
	m.viewport.SetHeight(1)
	m.viewportContent = "abc"
	m.viewport.SetContent("abc")
	m.selectStart = selectionPoint{row: 0, col: 0}
	m.selectEnd = m.selectionPointFromMouseClamped(999, 0)

	if m.selectEnd.col != 3 {
		t.Fatalf("end col = %d, want exclusive right edge 3", m.selectEnd.col)
	}
	if got := m.selectedTranscriptText(); got != "abc" {
		t.Fatalf("selected text = %q, want abc", got)
	}
}

func TestTranscriptSelectionIsVisiblyHighlighted(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "alpha\nbeta\ngamma")
	m.resize(true)
	base := m.viewport.View()
	m.selectStart = selectionPoint{row: 0, col: 1}
	m.selectEnd = selectionPoint{row: 0, col: 4}
	m.applySelectionHighlight()
	highlighted := m.viewport.View()
	if highlighted == base {
		t.Fatalf("selection should alter rendered viewport")
	}
	if stripANSITest(highlighted) != stripANSITest(base) {
		t.Fatalf("selection highlight should preserve visible text")
	}
	if !strings.Contains(highlighted, "48;2;255;209;102") {
		t.Fatalf("selection highlight should use visible yellow background, rendered=%q", highlighted)
	}
}

func TestTranscriptSelectionHighlightsCorrectStyledLine(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", "alpha\nbeta\ngamma")
	m.resize(true)

	base := m.viewport.GetContent()
	baseLines := strings.Split(base, "\n")
	betaRow := -1
	betaCol := 0
	for row, line := range baseLines {
		visible := stripANSITest(line)
		idx := strings.Index(visible, "beta")
		if idx >= 0 {
			betaRow = row
			betaCol = len([]rune(visible[:idx]))
			break
		}
	}
	if betaRow < 0 {
		t.Fatalf("rendered transcript should contain beta, content=%q", stripANSITest(base))
	}

	m.selectStart = selectionPoint{row: betaRow, col: betaCol}
	m.selectEnd = selectionPoint{row: betaRow, col: betaCol + 3}
	m.applySelectionHighlight()

	highlighted := m.viewport.GetContent()
	if stripANSITest(highlighted) != stripANSITest(base) {
		t.Fatalf("selection highlight should preserve visible text")
	}
	highlightedLines := strings.Split(highlighted, "\n")
	if !strings.Contains(highlightedLines[betaRow], "48;2;255;209;102") {
		t.Fatalf("beta line should be highlighted, line=%q", highlightedLines[betaRow])
	}
	for row, line := range highlightedLines {
		if row != betaRow && strings.Contains(stripANSITest(line), "alpha") && strings.Contains(line, "48;2;255;209;102") {
			t.Fatalf("selection highlight landed on wrong line %d: %q", row, line)
		}
	}
}

func TestTranscriptSelectionHighlightMatchesSelectedGraphemes(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 10
	line := "\x1b[90mmeta\x1b[m привет 🏳️‍🌈 中 done"
	target := "привет 🏳️‍🌈 中"
	visible := xansi.Strip(line)
	startByte := strings.Index(visible, target)
	if startByte < 0 {
		t.Fatalf("test target missing from %q", visible)
	}
	startCol := xansi.StringWidth(visible[:startByte])
	endCol := startCol + xansi.StringWidth(target)
	m.viewportContent = line
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(m.height)
	m.viewport.SetContent(line)
	m.selectStart = selectionPoint{row: 0, col: startCol}
	m.selectEnd = selectionPoint{row: 0, col: endCol}

	if got := m.selectedTranscriptText(); got != target {
		t.Fatalf("selected text = %q, want %q", got, target)
	}
	highlighted := m.selectionHighlightedContent()
	if got := selectionBackgroundText(highlighted); got != target {
		t.Fatalf("highlighted selection = %q, want %q; rendered=%q", got, target, highlighted)
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

func TestInlineStatusShowsModelAccessCacheCostAndSession(t *testing.T) {
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

	status := m.inlineStatusView()
	for _, want := range []string{
		"deepseek-v4-flash",
		"🧠 high",
		"Full Access",
		"Context",
		"cost $",
		"cache hit",
		"websum",
		"20k→900",
		"sumapi 0",
		"agent turns",
		"v0.1.0",
		"theme dark",
		"Main [",
	} {
		if !strings.Contains(status, want) {
			t.Fatalf("status %q does not contain %q", status, want)
		}
	}
	if strings.Count(status, "\n") != 1 {
		t.Fatalf("status should be two lines, got %q", status)
	}
	for _, bad := range []string{"reasoning 0", "1.5k used", "cached "} {
		if strings.Contains(status, bad) {
			t.Fatalf("status should not contain raw provider counter %q: %q", bad, status)
		}
	}
}

func TestCompactEventTextShowsStructuredCompactionFields(t *testing.T) {
	text := compactEventText(map[string]any{
		"compaction_id":              "abc123",
		"reason":                     "prompt_tokens_at_or_above_threshold",
		"trigger_source":             "provider_usage",
		"trigger_prompt_tokens":      610000,
		"threshold_tokens":           600000,
		"keep_messages":              32,
		"max_summary_chars":          120000,
		"compacted_messages":         42,
		"compacted_chars":            240000,
		"compacted_estimated_tokens": 60000,
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
	})
	for _, want := range []string{
		"id: abc123",
		"reason: prompt_tokens_at_or_above_threshold (provider_usage)",
		"trigger: 610000 / threshold 600000 tokens",
		"policy: keep 32 messages / summary cap 120000 chars",
		"compacted messages: 42",
		"compacted budget: 240000 chars / ~60000 tokens",
		"protected prefix: 3 messages, 9000 chars, ~2250 tokens",
		"agents_instructions=1",
		"profile_soul=1",
		"system_prompt=1",
		"active messages: 35",
		"summary chars: 12000",
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

func TestProviderUsageUpdateDeduplicatesCumulativeSnapshots(t *testing.T) {
	m := newTestModel(t)
	m.width = 180
	m.applyEvent(protocol.Event{Type: protocol.EventRunStarted})
	m.applyEvent(protocol.Event{Type: protocol.EventModelCallStarted})

	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      100,
		"output_tokens":     20,
		"cache_hit_tokens":  40,
		"cache_miss_tokens": 60,
		"reasoning_tokens":  5,
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      100,
		"output_tokens":     20,
		"cache_hit_tokens":  40,
		"cache_miss_tokens": 60,
		"reasoning_tokens":  5,
	}})
	m.applyEvent(protocol.Event{Type: protocol.EventProviderUsageUpdate, Data: map[string]any{
		"input_tokens":      125,
		"output_tokens":     25,
		"cache_hit_tokens":  50,
		"cache_miss_tokens": 75,
		"reasoning_tokens":  7,
	}})

	if m.inputTok != 125 || m.outputTok != 25 || m.cacheHitTok != 50 || m.cacheMissTok != 75 || m.reasoningTok != 7 {
		t.Fatalf("usage totals = in:%d out:%d hit:%d miss:%d reasoning:%d",
			m.inputTok, m.outputTok, m.cacheHitTok, m.cacheMissTok, m.reasoningTok)
	}
	if got := m.contextTokens(); got != 150 {
		t.Fatalf("contextTokens = %d, want current snapshot input+output 150", got)
	}
	status := m.inlineStatusView()
	if !strings.Contains(status, "cache hit 50") || !strings.Contains(status, "miss 75") {
		t.Fatalf("status should show last cache snapshot, got %q", status)
	}
	for _, bad := range []string{"cache hit 90", "miss 135", "reasoning 7", "157 used"} {
		if strings.Contains(status, bad) {
			t.Fatalf("status should not show cumulative raw counter %q: %q", bad, status)
		}
	}
}

func TestLightThemeStatusLineUsesThemeBackground(t *testing.T) {
	styles := newThemeStyles(tuiThemes["light"])
	rendered := styles.status.Render("status")
	if !strings.Contains(rendered, "48;2;221;232;215") {
		t.Fatalf("light status should use theme status bg, rendered=%q", rendered)
	}
}

func TestMarkdownTableRowKeepsEscapedAndCodePipesInCells(t *testing.T) {
	cells, ok := parseMarkdownTableRow("| `a\\|b` | escaped \\| literal | plain |")
	if !ok {
		t.Fatal("expected markdown table row")
	}
	want := []string{"`a|b`", "escaped | literal", "plain"}
	if len(cells) != len(want) {
		t.Fatalf("cells = %#v, want %#v", cells, want)
	}
	for i := range want {
		if cells[i] != want[i] {
			t.Fatalf("cells = %#v, want %#v", cells, want)
		}
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
	})
	for _, want := range []string{
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
		_ = json.NewEncoder(w).Encode(gateway.SessionContextResponse{
			ID:                   "session-1",
			MessageCount:         4,
			EstimatedTokens:      580000,
			ContextWindowTokens:  1000000,
			ContextCompactTokens: 600000,
			PercentUsed:          58,
			Estimator:            "chars_div_4",
			Sources: []gateway.ContextSource{
				{Source: "web_summaries", MessageCount: 2, EstimatedTokens: 320000, Percent: 55.2},
				{Source: "user_messages", MessageCount: 1, EstimatedTokens: 1000, Percent: 0.2},
			},
			Thresholds: []gateway.ContextThreshold{
				{Percent: 50, Tokens: 500000, Crossed: true},
				{Percent: 70, Tokens: 700000, RemainingTokens: 120000},
			},
			TopContributors: []gateway.ContextContributor{
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
	for _, want := range []string{"active context: 580.0k / 1.00M", "thresholds: ●50% ○70%", "web_summaries", "top contributors"} {
		if !strings.Contains(msg.text, want) {
			t.Fatalf("context report missing %q:\n%s", want, msg.text)
		}
	}
}

func TestContextThresholdEventRendersContextBlock(t *testing.T) {
	m := newTestModel(t)
	m.applyEvent(protocol.Event{Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
		Percent:             70,
		EstimatedTokens:     705000,
		ContextWindowTokens: 1000000,
		ThresholdTokens:     700000,
		RemainingTokens:     295000,
		MessageCount:        44,
		Round:               3,
		Stage:               "after_tool_results",
		Estimator:           "chars_div_4",
	}})
	if len(m.blocks) == 0 {
		t.Fatal("expected context threshold block")
	}
	block := m.blocks[len(m.blocks)-1]
	if block.title != "CONTEXT" || block.eventType != protocol.EventContextThreshold {
		t.Fatalf("block = %#v", block)
	}
	for _, want := range []string{"threshold: 70%", "active: 705k / 1.0m", "remaining window: 295k", "stage: after_tool_results"} {
		if !strings.Contains(block.content, want) {
			t.Fatalf("context threshold block missing %q:\n%s", want, block.content)
		}
	}
}

func TestRunStatusShowsSpinnerAndInlineStatusShowsElapsedWhileBusy(t *testing.T) {
	m := newTestModel(t)
	m.width = 160
	m.busy = true
	m.status = "running tool shell"
	m.runStartedAt = time.Now().Add(-3 * time.Second)
	m.spinnerFrame = 3

	status := m.inlineStatusView()
	if !strings.Contains(status, "running 3s") {
		t.Fatalf("status %q should show elapsed seconds", status)
	}
	for _, frame := range spinnerFrames {
		if strings.Contains(status, frame) {
			t.Fatalf("inline status %q should not show spinner frame %q", status, frame)
		}
	}

	runStatus := m.runStatusView()
	if !strings.Contains(runStatus, "running tool shell") || !strings.Contains(runStatus, "3s") {
		t.Fatalf("run status %q should show live state and elapsed seconds", runStatus)
	}
	foundSpinner := false
	for _, frame := range spinnerFrames {
		if strings.Contains(runStatus, frame) {
			foundSpinner = true
			break
		}
	}
	if !foundSpinner {
		t.Fatalf("run status %q should show spinner", runStatus)
	}
}

func TestResizeDoesNotReserveHiddenSlashPopup(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 30
	m.resize(false)
	noPopupHeight := m.viewport.Height()

	m.textarea.SetValue("/the")
	m.resize(false)
	withPopupHeight := m.viewport.Height()
	if noPopupHeight <= withPopupHeight {
		t.Fatalf("hidden popup should not reserve rows: noPopup=%d withPopup=%d", noPopupHeight, withPopupHeight)
	}
	if noPopupHeight-withPopupHeight > 8 {
		t.Fatalf("popup should reserve only its rendered height, delta=%d", noPopupHeight-withPopupHeight)
	}
}

func TestChatCommands(t *testing.T) {
	m := newTestModel(t)
	original := m.localChatID
	m.addBlock("user", "USER", "hello")

	handled, cmd := m.handleSlashCommand("/new")
	if !handled || cmd != nil {
		t.Fatalf("/new handled=%v cmd=%v, want handled without command", handled, cmd)
	}
	if m.localChatID == original {
		t.Fatalf("/new should create a new local chat id")
	}
	if len(m.blocks) != 0 {
		t.Fatalf("/new should clear rendered blocks")
	}

	handled, _ = m.handleSlashCommand("/resume")
	if !handled {
		t.Fatalf("/resume should be handled")
	}
	if len(m.blocks) == 0 || !strings.Contains(m.blocks[len(m.blocks)-1].content, shortID(original)) {
		t.Fatalf("/resume should list saved chats")
	}
}

func newTestModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	return NewModel(config.Default(), Options{})
}

func stripANSITest(text string) string {
	return regexp.MustCompile(`\x1b\[[0-9;:]*[A-Za-z]`).ReplaceAllString(text, "")
}

func selectionBackgroundText(text string) string {
	var out strings.Builder
	selected := false
	for i := 0; i < len(text); {
		if text[i] == '\x1b' {
			end := i + 1
			if end < len(text) && text[end] == '[' {
				end++
			}
			for end < len(text) && (text[end] < '@' || text[end] > '~') {
				end++
			}
			if end < len(text) {
				seq := text[i : end+1]
				if strings.HasSuffix(seq, "m") {
					if seq == "\x1b[m" || strings.Contains(seq, "[0") {
						selected = false
					}
					if strings.Contains(seq, "48;2;255;209;102") {
						selected = true
					}
				}
				i = end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if selected {
			out.WriteRune(r)
		}
		i += size
	}
	return out.String()
}
