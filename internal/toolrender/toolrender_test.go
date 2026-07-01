package toolrender

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestCallLineCompactsLongWebFetchURL(t *testing.T) {
	call := protocol.ToolCall{
		Name:      "web_fetch",
		Arguments: []byte(`{"url":"https://example.com/some/really/long/path/that/should/not/eat/the/whole/message?with=a&lot=of&query=params"}`),
	}
	telegram := CallLine(call, StyleTelegram)
	if !strings.Contains(telegram, "🌐 web_fetch") || !strings.Contains(telegram, "example.com") {
		t.Fatalf("telegram line = %q", telegram)
	}
	if strings.Contains(telegram, "with=a&lot=of&query=params") || len([]rune(telegram)) > 120 {
		t.Fatalf("telegram line did not compact URL: %q", telegram)
	}

	tui := CallLine(call, StyleTUI)
	if !strings.Contains(tui, "Fetched example.com") || strings.Contains(tui, "with=a&lot=of&query=params") {
		t.Fatalf("tui line did not compact URL: %q", tui)
	}
}

func TestCallLineFormatsShellArgvForTUI(t *testing.T) {
	call := protocol.ToolCall{
		Name:      "shell_exec",
		Arguments: []byte(`{"argv":["rg","-n","selection","internal/tui"],"cwd":"/root/billyharness"}`),
	}
	if got := CallLine(call, StyleTUI); got != "Ran rg -n selection internal/tui" {
		t.Fatalf("line = %q", got)
	}
}

func TestCallLineFormatsWebExtract(t *testing.T) {
	call := protocol.ToolCall{
		Name:      "web_extract",
		Arguments: []byte(`{"url":"https://example.com/long/path?secret=query","query":"pricing limits"}`),
	}
	if got := CallLine(call, StyleTelegram); !strings.Contains(got, "🧩 web_extract") || !strings.Contains(got, "pricing limits") || strings.Contains(got, "secret=query") {
		t.Fatalf("telegram line = %q", got)
	}
	if got := CallLine(call, StyleTUI); !strings.Contains(got, "Extracted example.com") || !strings.Contains(got, "pricing limits") || strings.Contains(got, "secret=query") {
		t.Fatalf("tui line = %q", got)
	}
}

func TestCallLineSnapshotsCommonTools(t *testing.T) {
	tests := []struct {
		name     string
		call     protocol.ToolCall
		style    Style
		expected string
	}{
		{
			name:     "tui shell",
			call:     protocol.ToolCall{Name: "shell_exec", Arguments: []byte(`{"argv":["rg","-n","selection","internal/tui"]}`)},
			style:    StyleTUI,
			expected: "Ran rg -n selection internal/tui",
		},
		{
			name:     "telegram shell",
			call:     protocol.ToolCall{Name: "shell_exec", Arguments: []byte(`{"argv":["rg","-n","selection","internal/tui"]}`)},
			style:    StyleTelegram,
			expected: "⚙️ shell rg -n selection internal/tui",
		},
		{
			name:     "tui shell background",
			call:     protocol.ToolCall{Name: "shell_exec", Arguments: []byte(`{"argv":["npm","run","dev"],"background":true}`)},
			style:    StyleTUI,
			expected: "Started npm run dev",
		},
		{
			name:     "telegram shell background",
			call:     protocol.ToolCall{Name: "shell_exec", Arguments: []byte(`{"argv":["npm","run","dev"],"background":true}`)},
			style:    StyleTelegram,
			expected: "⚙️ shell start npm run dev",
		},
		{
			name:     "tui shell output",
			call:     protocol.ToolCall{Name: "shell_output", Arguments: []byte(`{"process_id":"shell-1","cursor":12}`)},
			style:    StyleTUI,
			expected: "Read shell shell-1",
		},
		{
			name:     "telegram shell kill",
			call:     protocol.ToolCall{Name: "shell_kill", Arguments: []byte(`{"process_id":"shell-1"}`)},
			style:    StyleTelegram,
			expected: "⚙️ shell kill shell-1",
		},
		{
			name:     "tui shell processes",
			call:     protocol.ToolCall{Name: "shell_processes", Arguments: []byte(`{"include_exited":true}`)},
			style:    StyleTUI,
			expected: "Listed shell processes",
		},
		{
			name:     "telegram shell processes",
			call:     protocol.ToolCall{Name: "shell_processes", Arguments: []byte(`{}`)},
			style:    StyleTelegram,
			expected: "⚙️ shell processes",
		},
		{
			name:     "tui diagnostics",
			call:     protocol.ToolCall{Name: "diagnostics_run", Arguments: []byte(`{"name":"go-test"}`)},
			style:    StyleTUI,
			expected: "Ran diagnostics go-test",
		},
		{
			name:     "telegram diagnostics default",
			call:     protocol.ToolCall{Name: "diagnostics_run", Arguments: []byte(`{}`)},
			style:    StyleTelegram,
			expected: "🩺 diagnostics default",
		},
		{
			name:     "tui read",
			call:     protocol.ToolCall{Name: "fs_read_file", Arguments: []byte(`{"path":"/root/billyharness/README.md"}`)},
			style:    StyleTUI,
			expected: "Read /root/billyharness/README.md",
		},
		{
			name:     "telegram read",
			call:     protocol.ToolCall{Name: "fs_read_file", Arguments: []byte(`{"path":"/root/billyharness/README.md"}`)},
			style:    StyleTelegram,
			expected: "📖 read /root/billyharness/README.md",
		},
		{
			name:     "tui read window",
			call:     protocol.ToolCall{Name: "fs_read_file", Arguments: []byte(`{"path":"/root/billyharness/README.md","offset":10,"limit":5}`)},
			style:    StyleTUI,
			expected: "Read /root/billyharness/README.md lines 10-14",
		},
		{
			name:     "telegram read window",
			call:     protocol.ToolCall{Name: "fs_read_file", Arguments: []byte(`{"path":"/root/billyharness/README.md","offset":10,"limit":5}`)},
			style:    StyleTelegram,
			expected: "📖 read /root/billyharness/README.md lines 10-14",
		},
		{
			name:     "tui web search",
			call:     protocol.ToolCall{Name: "web_search", Arguments: []byte(`{"query":"agent loop benchmark"}`)},
			style:    StyleTUI,
			expected: "Searched web agent loop benchmark",
		},
		{
			name:     "telegram web search",
			call:     protocol.ToolCall{Name: "web_search", Arguments: []byte(`{"query":"agent loop benchmark"}`)},
			style:    StyleTelegram,
			expected: "🔎 web_search agent loop benchmark",
		},
		{
			name:     "tui memory list",
			call:     protocol.ToolCall{Name: "memory_list", Arguments: []byte(`{"source":"profile","query":"style"}`)},
			style:    StyleTUI,
			expected: "Listed memory source=profile query=style",
		},
		{
			name:     "telegram memory add",
			call:     protocol.ToolCall{Name: "memory_add", Arguments: []byte(`{"topic":"style","path":"topics/style.md"}`)},
			style:    StyleTelegram,
			expected: "🧠 memory add topic=style path=topics/style.md",
		},
		{
			name:     "tui mcp call",
			call:     protocol.ToolCall{Name: "mcp_call", Arguments: []byte(`{"name":"mcp__server__tool"}`)},
			style:    StyleTUI,
			expected: "Called MCP mcp__server__tool",
		},
		{
			name:     "telegram mcp call",
			call:     protocol.ToolCall{Name: "mcp_call", Arguments: []byte(`{"name":"mcp__server__tool"}`)},
			style:    StyleTelegram,
			expected: "🔌 mcp call mcp__server__tool",
		},
		{
			name:     "tui todo write",
			call:     protocol.ToolCall{Name: "todo_write", Arguments: []byte(`{"todos":[{"id":"x","content":"secret-ish raw payload","status":"pending"}]}`)},
			style:    StyleTUI,
			expected: "Updated plan",
		},
		{
			name:     "telegram todo write",
			call:     protocol.ToolCall{Name: "todo_write", Arguments: []byte(`{"todos":[{"id":"x","content":"secret-ish raw payload","status":"pending"}]}`)},
			style:    StyleTelegram,
			expected: "📝 plan update",
		},
		{
			name:     "tui fs grep",
			call:     protocol.ToolCall{Name: "fs_grep", Arguments: []byte(`{"pattern":"TODO|FIXME","path":"internal","include":"*.go"}`)},
			style:    StyleTUI,
			expected: "Grepped TODO|FIXME in internal",
		},
		{
			name:     "telegram fs glob",
			call:     protocol.ToolCall{Name: "fs_glob", Arguments: []byte(`{"pattern":"**/*.go","path":"internal/tools"}`)},
			style:    StyleTelegram,
			expected: "🧭 glob **/*.go in internal/tools",
		},
		{
			name:     "tui fs find files",
			call:     protocol.ToolCall{Name: "fs_find_files", Arguments: []byte(`{"query":"tui file","path":"internal"}`)},
			style:    StyleTUI,
			expected: "Found files tui file in internal",
		},
		{
			name:     "telegram fs find files",
			call:     protocol.ToolCall{Name: "fs_find_files", Arguments: []byte(`{"query":"tui file","path":"internal"}`)},
			style:    StyleTelegram,
			expected: "🗂 find files tui file in internal",
		},
		{
			name:     "tui fs edit",
			call:     protocol.ToolCall{Name: "fs_edit_file", Arguments: []byte(`{"path":"internal/tools/tools.go","edits":[{"old_string":"secret raw payload","new_string":"replacement"}]}`)},
			style:    StyleTUI,
			expected: "Edited internal/tools/tools.go",
		},
		{
			name:     "telegram fs edit",
			call:     protocol.ToolCall{Name: "fs_edit_file", Arguments: []byte(`{"path":"internal/tools/tools.go","edits":[{"old_string":"secret raw payload","new_string":"replacement"}]}`)},
			style:    StyleTelegram,
			expected: "✏️ edit internal/tools/tools.go",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CallLine(tt.call, tt.style); got != tt.expected {
				t.Fatalf("CallLine() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestGoldenBundleToolRenderParityLines(t *testing.T) {
	bundle := testkit.ReadCanonicalAgentLoopBundle(t)
	if !bundle.OfflineReplay || bundle.Trace != testkit.CanonicalAgentLoopTrace {
		t.Fatalf("bundle = %#v", bundle)
	}
	seen := map[string]bool{}
	for _, event := range goldenTraceEvents(t) {
		switch event.Type {
		case protocol.EventToolCallRequested:
			_, tui := CallKeyAndLine(event.Data, StyleTUI)
			_, telegram := CallKeyAndLine(event.Data, StyleTelegram)
			switch event.CallID {
			case "call-web":
				if strings.Contains(tui, "example.com/research") && strings.Contains(telegram, "web_fetch") {
					seen["web-call"] = true
				}
			case "call-mcp":
				if strings.Contains(tui, "Called MCP mcp__fake__search") && strings.Contains(telegram, "mcp call mcp__fake__search") {
					seen["mcp-call"] = true
				}
			case "call-shell":
				if strings.Contains(tui, "Started npm run dev") && strings.Contains(telegram, "shell start npm run dev") {
					seen["shell-call"] = true
				}
			}
		case protocol.EventToolOutputRefCreated:
			_, tui := OutputRefLine(event.Data, StyleTUI)
			_, telegram := OutputRefLine(event.Data, StyleTelegram)
			if event.CallID == "call-web" &&
				strings.Contains(tui, "ref web_fetch.txt") &&
				strings.Contains(telegram, "ref web_fetch.txt") {
				seen["web-ref"] = true
			}
		case protocol.EventToolCallAborted:
			_, tui := ResultKeyAndLine(mustToolResult(t, event.Data), "", StyleTUI)
			_, telegram := ResultKeyAndLine(mustToolResult(t, event.Data), "", StyleTelegram)
			if event.CallID == "call-shell" &&
				strings.Contains(tui, "shell_exec") &&
				strings.Contains(telegram, "shell_exec") &&
				strings.Contains(tui+"\n"+telegram, "interrupted by newer user input") {
				seen["shell-abort"] = true
			}
		}
	}
	for _, want := range []string{"web-call", "mcp-call", "shell-call", "web-ref", "shell-abort"} {
		if !seen[want] {
			t.Fatalf("golden toolrender parity missing %s; seen=%#v", want, seen)
		}
	}
}

func TestTelegramCallLineCoversCommonToolTargets(t *testing.T) {
	tests := []struct {
		name string
		call protocol.ToolCall
		want []string
	}{
		{
			name: "fs path",
			call: protocol.ToolCall{Name: "fs_read_file", Arguments: []byte(`{"path":"/root/billyharness/README.md"}`)},
			want: []string{"📖 read", "/root/billyharness/README.md"},
		},
		{
			name: "web query",
			call: protocol.ToolCall{Name: "web_search", Arguments: []byte(`{"query":"telegram rich messages"}`)},
			want: []string{"🔎 web_search", "telegram rich messages"},
		},
		{
			name: "web url",
			call: protocol.ToolCall{Name: "web_fetch", Arguments: []byte(`{"url":"https://example.com/a/very/long/page?secret=hidden"}`)},
			want: []string{"🌐 web_fetch", "example.com"},
		},
		{
			name: "mcp server and tool",
			call: protocol.ToolCall{Name: "mcp_list_tools", Arguments: []byte(`{"server":"telegram-parilka","query":"history"}`)},
			want: []string{"🔌 mcp tools", "server=telegram-parilka", "query=history"},
		},
		{
			name: "mcp call",
			call: protocol.ToolCall{Name: "mcp_call", Arguments: []byte(`{"name":"mcp__telegram_parilka__read_history"}`)},
			want: []string{"🔌 mcp call", "mcp__telegram_parilka__read_history"},
		},
		{
			name: "shell argv",
			call: protocol.ToolCall{Name: "shell_exec", Arguments: []byte(`{"argv":["rg","-n","toolview","internal/telegrambot"]}`)},
			want: []string{"⚙️ shell", "rg -n toolview internal/telegrambot"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CallLine(tt.call, StyleTelegram)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("line missing %q: %q", want, got)
				}
			}
			if strings.Contains(got, "secret=hidden") {
				t.Fatalf("line leaked URL query: %q", got)
			}
		})
	}
}

func TestToolRenderFSGrepGlob(t *testing.T) {
	grep := CallLine(protocol.ToolCall{
		Name:      "fs_grep",
		Arguments: []byte(`{"pattern":"secret raw pattern","path":"internal","include":"*.go","context":2}`),
	}, StyleTelegram)
	for _, want := range []string{"🔍 grep", "secret raw pattern", "internal"} {
		if !strings.Contains(grep, want) {
			t.Fatalf("grep line missing %q: %q", want, grep)
		}
	}
	for _, notWant := range []string{`"pattern"`, `"context"`} {
		if strings.Contains(grep, notWant) {
			t.Fatalf("grep line leaked raw args: %q", grep)
		}
	}

	glob := CallLine(protocol.ToolCall{
		Name:      "fs_glob",
		Arguments: []byte(`{"pattern":"**/*.go","path":"internal/tools","sort":"modified"}`),
	}, StyleTUI)
	for _, want := range []string{"Globbed", "**/*.go", "internal/tools"} {
		if !strings.Contains(glob, want) {
			t.Fatalf("glob line missing %q: %q", want, glob)
		}
	}
	for _, notWant := range []string{`"pattern"`, `"sort"`} {
		if strings.Contains(glob, notWant) {
			t.Fatalf("glob line leaked raw args: %q", glob)
		}
	}
}

func TestToolRenderFSFindFiles(t *testing.T) {
	line := CallLine(protocol.ToolCall{
		Name:      "fs_find_files",
		Arguments: []byte(`{"query":"secret raw query","path":"internal","limit":10}`),
	}, StyleTelegram)
	for _, want := range []string{"find files", "secret raw query", "internal"} {
		if !strings.Contains(line, want) {
			t.Fatalf("find-files line missing %q: %q", want, line)
		}
	}
	for _, notWant := range []string{`"query"`, `"limit"`} {
		if strings.Contains(line, notWant) {
			t.Fatalf("find-files line leaked raw args: %q", line)
		}
	}
}

func TestTodoPlanStateSummary(t *testing.T) {
	state := protocol.TodoState{Todos: []protocol.TodoItem{
		{ID: "done", Content: "Inspect plan surfaces", Status: "completed", Priority: "high"},
		{ID: "build", Content: "Build todo_write", Status: "in_progress", Priority: "high"},
		{ID: "test", Content: "Run tests", Status: "pending", Priority: "medium"},
	}}
	result := protocol.ToolResult{
		CallID: "call_todo",
		Name:   "todo_write",
		Metadata: map[string]any{
			"todo_state": state,
		},
	}
	summary, ok := ResultSummaryFor(result, "", StyleTelegram)
	if !ok || summary.Key != "call_todo" {
		t.Fatalf("summary = %#v ok=%v", summary, ok)
	}
	for _, want := range []string{"📝", "3 todos", "1 in progress", "1 pending", "1 completed", "now: Build todo_write"} {
		if !strings.Contains(summary.Line, want) {
			t.Fatalf("todo summary missing %q: %q", want, summary.Line)
		}
	}
	if strings.Contains(CallLine(protocol.ToolCall{Name: "todo_write", Arguments: []byte(`{"todos":[{"content":"raw"}]}`)}, StyleTelegram), "raw") {
		t.Fatal("todo_write call line leaked raw arguments")
	}
}

func TestResultKeyAndLineCompactsMetadata(t *testing.T) {
	result := protocol.ToolResult{
		CallID:    "call_fetch",
		Name:      "web_fetch",
		Content:   strings.Repeat("full payload ", 50),
		Truncated: true,
		OutputRef: "/root/billyharness/tool-output/20260627/123456-web_fetch-call_fetch-abcd.txt",
		Metadata:  map[string]any{"estimated_text_tokens": int64(1800), "web_cache_hit": true, "duration_ms": int64(123)},
	}
	key, line := ResultKeyAndLine(result, "🌐 web_fetch example.com/path?…", StyleTelegram)
	if key != "call_fetch" {
		t.Fatalf("key = %q", key)
	}
	for _, want := range []string{"✅", "🌐 web_fetch", "truncated", "123456-web_fetch-call_fetch-abcd.txt", "123ms", "cache hit", "~1.8k tok"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line missing %q: %q", want, line)
		}
	}
	if strings.Contains(line, "full payload") {
		t.Fatalf("success summary leaked raw payload: %q", line)
	}
	summary, ok := ResultSummaryFor(result, "🌐 web_fetch example.com/path?…", StyleTelegram)
	if !ok || summary.Key != key || summary.Line != line || !summary.Truncated ||
		summary.OutputRef == "" || summary.DurationMS != 123 || summary.EstimatedTokens != 1800 ||
		summary.CacheLabel != "cache hit" {
		t.Fatalf("summary = %#v ok=%v line=%q", summary, ok, line)
	}
}

func TestToolCompactSummaryDrivesResultLine(t *testing.T) {
	result := protocol.ToolResult{
		CallID:  "call_custom",
		Name:    "custom_tool",
		Content: strings.Repeat("raw payload ", 40),
		Compact: &protocol.ToolCompact{
			CallID:          "call_custom",
			Name:            "custom_tool",
			Lifecycle:       "result",
			Status:          protocol.StepStatusCompleted,
			Title:           "custom_tool",
			Summary:         "completed custom_tool target=src/main.go",
			OutputRef:       "/root/billyharness/tool-output/custom.txt",
			DurationMS:      25,
			EstimatedTokens: 1400,
			OriginalBytes:   9000,
			Truncated:       true,
		},
	}
	summary, ok := ResultSummaryFor(result, "", StyleTelegram)
	if !ok || summary.Key != "call_custom" || summary.OutputRef == "" || summary.EstimatedTokens != 1400 || summary.OriginalBytes != 9000 {
		t.Fatalf("summary = %#v ok=%v", summary, ok)
	}
	for _, want := range []string{"✅", "completed custom_tool", "custom.txt", "25ms", "~1.4k tok", "9kB", "truncated"} {
		if !strings.Contains(summary.Line, want) {
			t.Fatalf("compact result line missing %q: %q", want, summary.Line)
		}
	}
	if strings.Contains(summary.Line, "raw payload") {
		t.Fatalf("compact result leaked raw content: %q", summary.Line)
	}
}

func TestToolCompactV2FieldsDriveBoundedSubjectLine(t *testing.T) {
	longURL := "https://example.com/" + strings.Repeat("very-long-segment/", 12) + "final?secret=do-not-render"
	longQuery := strings.Repeat("context cache diagnostics ", 12)
	compact := protocol.ToolCompact{
		DisplayVersion:  2,
		CallID:          "call_web",
		Name:            "web_fetch",
		Lifecycle:       "result",
		Status:          protocol.StepStatusCompleted,
		Group:           "web",
		Summary:         "fetched web context",
		URL:             longURL,
		Query:           longQuery,
		Preview:         `{"raw":"json","secret":"nope"}`,
		CollapseDefault: true,
	}
	summary := ResultSummaryFromCompact(compact, "", StyleTelegram)
	if !summary.CollapseDefault || summary.Group != "web" || summary.URL != longURL || summary.Query != longQuery {
		t.Fatalf("summary metadata = %#v", summary)
	}
	line := summary.Line
	if len([]rune(line)) > 280 {
		t.Fatalf("compact line too long (%d): %q", len([]rune(line)), line)
	}
	for _, want := range []string{"✅", "fetched web context", "url example.com/", "query context cache"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line missing %q: %q", want, line)
		}
	}
	for _, notWant := range []string{"secret=do-not-render", `"raw"`, `"secret"`} {
		if strings.Contains(line, notWant) {
			t.Fatalf("line leaked raw detail %q: %q", notWant, line)
		}
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("line should middle-truncate long subject: %q", line)
	}
}

func TestToolCompactNoRawJSONFallbackForUnknownTool(t *testing.T) {
	call := protocol.ToolCall{
		Name:      "custom_tool",
		Arguments: []byte(`{"token":"secret","payload":{"raw":"json"},"items":[1,2,3]}`),
	}
	line := CallLine(call, StyleTelegram)
	if !strings.Contains(line, "custom_tool") {
		t.Fatalf("line = %q", line)
	}
	for _, notWant := range []string{"token", "secret", "payload", "{", "raw"} {
		if strings.Contains(line, notWant) {
			t.Fatalf("unknown tool line leaked raw args %q: %q", notWant, line)
		}
	}
}

func TestProgressAndOutputRefLinesUseToolCompact(t *testing.T) {
	progress := protocol.ToolProgressEvent{
		CallID: "call_1",
		Name:   "custom_tool",
		Phase:  "executing",
		Status: protocol.StepStatusStarted,
		Compact: &protocol.ToolCompact{
			CallID:    "call_1",
			Name:      "custom_tool",
			Lifecycle: "executing",
			Status:    protocol.StepStatusStarted,
			Summary:   "started custom_tool target=README.md",
		},
	}
	key, line := ProgressLine(progress, StyleTUI)
	if key != "call_1" || !strings.Contains(line, "started custom_tool") {
		t.Fatalf("progress key=%q line=%q", key, line)
	}
	ref := protocol.ToolOutputRefEvent{
		CallID:    "call_1",
		Name:      "custom_tool",
		AttemptID: "attempt_1",
		OutputRef: "/root/billyharness/tool-output/custom.txt",
		Compact: &protocol.ToolCompact{
			CallID:    "call_1",
			Name:      "custom_tool",
			Lifecycle: "output_ref",
			Status:    "output_ref",
			Summary:   "custom_tool output ref",
			OutputRef: "/root/billyharness/tool-output/custom.txt",
		},
	}
	key, line = OutputRefLine(ref, StyleTUI)
	if key != "call_1" || !strings.Contains(line, "custom_tool output ref") || !strings.Contains(line, "custom.txt") {
		t.Fatalf("output ref key=%q line=%q", key, line)
	}
}

func TestTurnChangeSummaryAndDetailsExposePatchRef(t *testing.T) {
	change := protocol.TurnChangeEvent{
		ChangeID:       "change-1",
		TurnID:         "turn-1",
		ToolName:       "shell_exec",
		FileCount:      2,
		Added:          1,
		Modified:       1,
		Additions:      12,
		Deletions:      3,
		BinaryFiles:    1,
		LargeFiles:     1,
		Reversible:     true,
		PatchOutputRef: "/root/billyharness/tool-output/20260701/change-1.patch.json",
		Files: []protocol.TurnChangeFile{
			{RelPath: "internal/a.go", Change: "modified", Additions: 12, Deletions: 3, Reversible: true},
			{RelPath: "asset.bin", Change: "added", Binary: true, Large: true, Reversible: true},
		},
	}
	summary := TurnChangeSummary(change)
	for _, want := range []string{"2 files", "+12 -3", "1 added", "1 modified", "1 binary", "1 large", "shell changes", "patch ref change-1.patch.json"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %q", want, summary)
		}
	}
	details := TurnChangeDetails(change)
	for _, want := range []string{"change: change-1", "turn: turn-1", "patch_ref: /root/billyharness/tool-output/20260701/change-1.patch.json", "M internal/a.go +12 -3", "A asset.bin binary large"} {
		if !strings.Contains(details, want) {
			t.Fatalf("details missing %q:\n%s", want, details)
		}
	}
	if strings.Contains(details, "@@ ") {
		t.Fatalf("details should not inline a patch:\n%s", details)
	}
}

func goldenTraceEvents(t *testing.T) []protocol.Event {
	t.Helper()
	records := testkit.ReadTraceRecords(t, testkit.CanonicalAgentLoopTracePath(t))
	events := make([]protocol.Event, 0, len(records))
	for _, record := range records {
		var event protocol.Event
		if err := json.Unmarshal(record.Event, &event); err != nil {
			t.Fatalf("decode event seq %d: %v", record.Seq, err)
		}
		events = append(events, event)
	}
	return events
}

func mustToolResult(t *testing.T, value any) protocol.ToolResult {
	t.Helper()
	var result protocol.ToolResult
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
