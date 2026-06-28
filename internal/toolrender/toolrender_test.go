package toolrender

import (
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
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

func TestResultKeyAndLineCompactsMetadata(t *testing.T) {
	key, line := ResultKeyAndLine(protocol.ToolResult{
		CallID:    "call_fetch",
		Name:      "web_fetch",
		Truncated: true,
		OutputRef: "/root/billyharness/tool-output/20260627/123456-web_fetch-call_fetch-abcd.txt",
		Metadata:  map[string]any{"estimated_text_tokens": int64(1800), "web_cache_hit": true, "duration_ms": int64(123)},
	}, "🌐 web_fetch example.com/path?…", StyleTelegram)
	if key != "call_fetch" {
		t.Fatalf("key = %q", key)
	}
	for _, want := range []string{"✅", "🌐 web_fetch", "truncated", "123456-web_fetch-call_fetch-abcd.txt", "123ms", "cache hit", "~1.8k tok"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line missing %q: %q", want, line)
		}
	}
}
