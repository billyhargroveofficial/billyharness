package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
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

func TestActionRegistryBacksSlashCommandsAndHelp(t *testing.T) {
	commands := slashCommands()
	byName := make(map[string]slashCommand, len(commands))
	for _, command := range commands {
		if command.id == "" {
			t.Fatalf("slash command %q missing action id", command.name)
		}
		if command.title == "" {
			t.Fatalf("slash command %q missing title", command.name)
		}
		if command.category == "" {
			t.Fatalf("slash command %q missing category", command.name)
		}
		byName[command.name] = command
	}

	help := helpText()
	for _, action := range actionRegistry() {
		if action.slash == "" {
			continue
		}
		if action.id == "" || action.title == "" || action.category == "" {
			t.Fatalf("action for %q missing registry metadata: %#v", action.slash, action)
		}
		def, ok := clientux.ActionDefinitionByID(action.id)
		if !ok {
			t.Fatalf("action %q missing shared client UX definition", action.id)
		}
		if action.title != def.Title || action.category != def.Category || action.slash != def.Slash || action.slashArgs != def.SlashArgs || action.summary != def.Summary {
			t.Fatalf("action %q not hydrated from shared definition: %#v vs %#v", action.id, action, def)
		}
		if !reflect.DeepEqual(action.slashAliases, def.SlashAliases) || !reflect.DeepEqual(action.telegramAliases, def.TelegramAliases) {
			t.Fatalf("action %q aliases not hydrated from shared definition: %#v vs %#v", action.id, action, def)
		}
		if action.run == nil {
			t.Fatalf("action for %q missing runner", action.slash)
		}
		for _, alias := range action.telegramAliases {
			if !strings.HasPrefix(alias, "/") {
				t.Fatalf("telegram alias for %q must be slash-prefixed, got %q", action.slash, alias)
			}
		}
		command, ok := byName[action.slash]
		if !ok {
			t.Fatalf("action %q missing from slashCommands", action.slash)
		}
		if command.summary != action.summary || command.args != action.slashArgs {
			t.Fatalf("slash command %q not derived from registry: %#v vs %#v", action.slash, command, action)
		}
		if !strings.Contains(help, action.slash) {
			t.Fatalf("helpText missing %q:\n%s", action.slash, help)
		}
	}
}

func TestActionRegistryBacksKeybindingsAndHelp(t *testing.T) {
	help := helpText()
	for _, action := range actionRegistry() {
		keys := actionKeybindings(action)
		if len(keys) == 0 {
			continue
		}
		if action.id == "" || action.title == "" || action.category == "" {
			t.Fatalf("key action missing registry metadata: %#v", action)
		}
		if action.keyRun == nil {
			t.Fatalf("key action %q missing runner", action.id)
		}
		for _, key := range keys {
			if !strings.Contains(help, key) {
				t.Fatalf("helpText missing keybinding %q:\n%s", key, help)
			}
		}
	}

	action, ok := actionForKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if !ok || action.id != "palette.open" {
		t.Fatalf("ctrl+k action = %#v ok=%t", action, ok)
	}
	action, ok = actionForKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !ok || action.id != "message.send" {
		t.Fatalf("enter action = %#v ok=%t", action, ok)
	}
}

func TestActionRegistryDispatchesSlashAliases(t *testing.T) {
	m := newTestModel(t)
	handled, _ := m.handleSlashCommand("/show-reasoning off")
	if !handled {
		t.Fatal("/show-reasoning alias was not handled")
	}
	if m.showThinking {
		t.Fatal("/show-reasoning off should hide thinking")
	}
	if !m.exactSlashCommand("/quit") {
		t.Fatal("/quit alias should be recognized as an exact slash command")
	}
}

func TestSemanticCopyTextUsesRawTranscriptFields(t *testing.T) {
	m := newTestModel(t)
	m.addBlock("user", "USER", "hello")
	m.addBlock("assistant", "ASSISTANT", "answer **raw**")
	m.addBlock("tool", "Done Read README.md", "rendered output")
	m.blocks[2].RawCopy = "raw tool output"
	m.selected = 2
	m.textarea.SetValue("/model flash")

	for _, tc := range []struct {
		target string
		want   string
	}{
		{"selected", "raw tool output"},
		{"last", "answer **raw**"},
		{"tool", "raw tool output"},
		{"command", "/model flash"},
	} {
		got, _, ok := m.semanticCopyText(tc.target)
		if !ok || got != tc.want {
			t.Fatalf("semanticCopyText(%q) = %q ok=%v, want %q", tc.target, got, ok, tc.want)
		}
	}

	transcript, _, ok := m.semanticCopyText("transcript")
	if !ok {
		t.Fatal("transcript copy returned false")
	}
	for _, want := range []string{"hello", "answer **raw**", "raw tool output"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript copy missing %q: %q", want, transcript)
		}
	}
	for _, bad := range []string{"USER", "ASSISTANT", "Done Read README.md"} {
		if strings.Contains(transcript, bad) {
			t.Fatalf("transcript copy should not include UI chrome %q: %q", bad, transcript)
		}
	}
}

func TestSemanticCopyCodeBlockUsesRawMarkdown(t *testing.T) {
	m := newTestModel(t)
	m.addBlock("assistant", "ASSISTANT", "intro\n```go\nfmt.Println(\"hi\")\n```\n")
	m.addBlock("assistant", "ASSISTANT", "later\n~~~sh\nprintf hi\n~~~")
	m.selected = 0

	got, label, ok := m.semanticCopyText("code")
	if !ok || label != "code block" || got != "fmt.Println(\"hi\")" {
		t.Fatalf("selected code copy = %q label=%q ok=%v", got, label, ok)
	}

	m.selected = -1
	got, _, ok = m.semanticCopyText("code")
	if !ok || got != "printf hi" {
		t.Fatalf("last code copy = %q ok=%v, want latest fenced block", got, ok)
	}
}

func TestCopySlashCommandReturnsClipboardCommand(t *testing.T) {
	m := newTestModel(t)
	m.addBlock("assistant", "ASSISTANT", "answer")
	m.selected = 0
	handled, cmd := m.handleSlashCommand("/copy selected")
	if !handled || cmd == nil {
		t.Fatalf("/copy selected handled=%v cmd=%v", handled, cmd)
	}
	if !strings.Contains(m.status, "selected cell") {
		t.Fatalf("status = %q", m.status)
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
	if got := m.providerBinding.Provider.Provider; got != "openai-codex" {
		t.Fatalf("providerBinding.Provider = %q", got)
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
	server := testkit.NewRouteServer(t,
		testkit.Route{
			Method: http.MethodPost,
			Path:   "/v1/sessions/session-1/run",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if !testkit.DecodeJSON(t, r, &captured) {
					return
				}
				testkit.WriteJSONLines(t, w, protocol.Event{Type: protocol.EventAssistantDelta, Data: "ok"})
			},
		},
		testkit.Route{
			Method: http.MethodGet,
			Path:   "/v1/sessions/session-1",
			Handler: func(w http.ResponseWriter, _ *http.Request) {
				testkit.WriteJSON(t, w, map[string]any{
					"messages": []protocol.Message{{Role: protocol.RoleUser, Content: "ping"}, {Role: protocol.RoleAssistant, Content: "ok"}},
				})
			},
		},
	)

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

func TestCreateGatewaySessionSendsTUIOwnerMetadata(t *testing.T) {
	var captured gatewayapi.CreateSessionRequest
	server := testkit.NewRouteServer(t, testkit.Route{
		Method: http.MethodPost,
		Path:   "/v1/sessions",
		Handler: func(w http.ResponseWriter, r *http.Request) {
			if !testkit.DecodeJSON(t, r, &captured) {
				return
			}
			testkit.WriteJSON(t, w, gatewayapi.SessionResponse{ID: "session-1"})
		},
	})

	m := newTestModel(t)
	m.gatewayURL = server.URL
	if ok, _ := m.handleSlashCommand("/model pro"); !ok {
		t.Fatal("/model pro failed")
	}
	msg := m.createSessionCmd()()
	ready, ok := msg.(sessionReadyMsg)
	if !ok || ready.id != "session-1" {
		t.Fatalf("createSessionCmd msg = %#v", msg)
	}
	if captured.Profile != "billy" || captured.Owner.ClientType != "tui" ||
		captured.Owner.TUIChatID != m.localChatID ||
		captured.Owner.Profile != "billy" ||
		captured.Owner.Model != "deepseek-v4-pro" {
		t.Fatalf("captured owner request = %#v", captured)
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

func TestTUIDropsReplayEventsAtOrBeforeCursor(t *testing.T) {
	m := newTestModel(t)
	m.lastGatewayEventSeq = 7

	m.applyEvent(protocol.Event{Seq: 7, Type: protocol.EventModelCallStarted})
	m.applyEvent(protocol.Event{Seq: 6, Type: protocol.EventModelCallStarted})
	if m.modelCalls != 0 {
		t.Fatalf("modelCalls after stale events = %d, want 0", m.modelCalls)
	}

	m.applyEvent(protocol.Event{Seq: 8, Type: protocol.EventModelCallStarted})
	if m.modelCalls != 1 {
		t.Fatalf("modelCalls after fresh event = %d, want 1", m.modelCalls)
	}
	if got := m.lastGatewayEventSeq; got != 8 {
		t.Fatalf("lastGatewayEventSeq = %d, want 8", got)
	}
}

func TestReplayGatewayEventsCmdFetchesAfterSeq(t *testing.T) {
	var sawReplay bool
	server := testkit.NewRouteServer(t,
		testkit.Route{
			Method: http.MethodGet,
			Path:   "/v1/sessions/session-1/events",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("after_seq"); got != "7" {
					t.Fatalf("after_seq = %q, want 7", got)
				}
				if got := r.URL.Query().Get("follow"); got != "false" {
					t.Fatalf("follow = %q, want false", got)
				}
				sawReplay = true
				testkit.WriteJSONLines(t, w,
					protocol.Event{Seq: 8, Type: protocol.EventRunStarted},
					protocol.Event{Seq: 9, Type: protocol.EventAssistantDelta, Data: "replayed"},
				)
			},
		},
		testkit.Route{
			Method: http.MethodGet,
			Path:   "/v1/sessions/session-1",
			Handler: func(w http.ResponseWriter, _ *http.Request) {
				testkit.WriteJSON(t, w, map[string]any{
					"messages": []protocol.Message{{Role: protocol.RoleAssistant, Content: "replayed"}},
				})
			},
		},
	)

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
	seedStaleChatRuntimeState(&m)
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
	assertFreshChatRuntimeState(t, m)
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

func TestNewChatClearsGatewayAndAccountingState(t *testing.T) {
	m := newTestModel(t)
	oldChat := m.localChatID
	initialMessages := append([]protocol.Message(nil), m.messages...)
	seedStaleChatRuntimeState(&m)

	if cmd := m.newChat(); cmd != nil {
		t.Fatalf("newChat returned unexpected command: %#v", cmd)
	}
	if m.localChatID == oldChat {
		t.Fatalf("new chat reused old chat id %q", oldChat)
	}
	assertFreshChatRuntimeState(t, m)
	if !reflect.DeepEqual(m.messages, initialMessages) {
		t.Fatalf("new chat messages = %#v, want initial %#v", m.messages, initialMessages)
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

func newTestModel(t testModelHelper) Model {
	t.Helper()
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	return NewModel(config.Default(), Options{})
}

func seedStaleChatRuntimeState(m *Model) {
	m.sessionID = "stale-gateway-session"
	m.lastGatewayEventSeq = 42
	m.modelCalls = 3
	m.toolCalls = 4
	m.inputTok = 100
	m.outputTok = 25
	m.cacheHitTok = 7
	m.cacheMissTok = 8
	m.reasoningTok = 9
	m.lastInputTok = 10
	m.lastOutputTok = 11
	m.lastCacheHitTok = 12
	m.lastCacheMissTok = 13
	m.toolSummaryInTok = 14
	m.toolSummaryOutTok = 15
	m.toolSummaryAPITok = 16
	m.followOutput = false
	m.messages = append(m.messages, protocol.Message{Role: protocol.RoleUser, Content: "stale prompt"})
	m.addBlock("assistant", "ASSISTANT", "stale answer")
}

func assertFreshChatRuntimeState(t testing.TB, m Model) {
	t.Helper()
	if m.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", m.sessionID)
	}
	if m.lastGatewayEventSeq != 0 {
		t.Fatalf("lastGatewayEventSeq = %d, want 0", m.lastGatewayEventSeq)
	}
	if !m.followOutput {
		t.Fatalf("followOutput = false, want true")
	}
	if len(m.blocks) != 0 {
		t.Fatalf("blocks length = %d, want 0", len(m.blocks))
	}
	if m.modelCalls != 0 || m.toolCalls != 0 {
		t.Fatalf("calls = model %d tool %d, want 0/0", m.modelCalls, m.toolCalls)
	}
	if m.inputTok != 0 || m.outputTok != 0 || m.cacheHitTok != 0 || m.cacheMissTok != 0 || m.reasoningTok != 0 {
		t.Fatalf("lifetime tokens = input %d output %d hit %d miss %d reasoning %d, want all 0",
			m.inputTok, m.outputTok, m.cacheHitTok, m.cacheMissTok, m.reasoningTok)
	}
	if m.lastInputTok != 0 || m.lastOutputTok != 0 || m.lastCacheHitTok != 0 || m.lastCacheMissTok != 0 {
		t.Fatalf("last tokens = input %d output %d hit %d miss %d, want all 0",
			m.lastInputTok, m.lastOutputTok, m.lastCacheHitTok, m.lastCacheMissTok)
	}
	if m.toolSummaryInTok != 0 || m.toolSummaryOutTok != 0 || m.toolSummaryAPITok != 0 {
		t.Fatalf("tool summary tokens = in %d out %d api %d, want all 0",
			m.toolSummaryInTok, m.toolSummaryOutTok, m.toolSummaryAPITok)
	}
	if m.uxProjector == nil {
		t.Fatalf("uxProjector is nil")
	}
}

func BenchmarkTUIReflowLongTranscriptCached(b *testing.B) {
	m := newBenchmarkLongTranscript(b, 1200)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.reflow(false)
	}
}

func BenchmarkTUIPrintableKeyLongTranscript(b *testing.B) {
	m := newBenchmarkLongTranscript(b, 1200)
	msg := tea.KeyPressMsg{Code: 'a', Text: "a"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%64 == 0 {
			m.textarea.SetValue("")
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
}

func newBenchmarkLongTranscript(b *testing.B, blocks int) Model {
	b.Helper()
	m := newTestModel(b)
	m.width = 120
	m.height = 40
	m.resize(true)
	for i := 0; i < blocks; i++ {
		switch i % 4 {
		case 0:
			m.addBlock("user", "USER", fmt.Sprintf("benchmark prompt %04d", i))
		case 1:
			m.addBlock("assistant", "ASSISTANT", fmt.Sprintf("benchmark answer %04d with **markdown** and `code`.\n\n| key | value |\n| --- | --- |\n| item | %04d |", i, i))
		case 2:
			m.addBlock("tool", fmt.Sprintf("Done Read file-%04d.go", i), "compact tool payload that should be cached")
		default:
			m.addBlock("reasoning", "THINKING", "short hidden or visible reasoning block")
		}
	}
	m.reflow(true)
	return m
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
