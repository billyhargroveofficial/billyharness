package toolrender

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type Style int

const (
	StyleTUI Style = iota
	StyleTelegram
)

func CallKeyAndLine(data any, style Style) (string, string) {
	call, ok := DecodeCall(data)
	if !ok {
		return "", Preview(mustJSON(data), 600)
	}
	key := call.ID
	if key == "" {
		key = call.Name
	}
	return key, CallLine(call, style)
}

func CallLine(call protocol.ToolCall, style Style) string {
	args := CallArgs(call)
	if line, ok := registeredCallLine(call, args, style); ok {
		return line
	}
	return fallbackCallLine(call, args, style)
}

func CallName(data any) string {
	call, ok := DecodeCall(data)
	if ok && call.Name != "" {
		return call.Name
	}
	return "call"
}

func CallArgs(data any) map[string]any {
	bytes, _ := json.Marshal(data)
	call, ok := DecodeCall(data)
	if !ok || len(call.Arguments) == 0 || string(call.Arguments) == "null" {
		var generic map[string]any
		if err := json.Unmarshal(bytes, &generic); err != nil {
			return nil
		}
		switch args := generic["arguments"].(type) {
		case map[string]any:
			return args
		case string:
			var parsed map[string]any
			if err := json.Unmarshal([]byte(args), &parsed); err == nil {
				return parsed
			}
		}
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(call.Arguments, &args); err == nil {
		return args
	}
	var raw string
	if err := json.Unmarshal(call.Arguments, &raw); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err == nil {
			return args
		}
	}
	return nil
}

func DecodeCall(data any) (protocol.ToolCall, bool) {
	bytes, _ := json.Marshal(data)
	var call protocol.ToolCall
	if err := json.Unmarshal(bytes, &call); err != nil || call.Name == "" {
		return protocol.ToolCall{}, false
	}
	return call, true
}

func ResultKeyAndLine(data any, base string, style Style) (string, string) {
	summary, ok := ResultSummaryFor(data, base, style)
	if !ok {
		return "", ""
	}
	return summary.Key, summary.Line
}

type ResultSummary struct {
	Key             string
	Line            string
	IsError         bool
	Truncated       bool
	OutputRef       string
	DurationMS      int64
	EstimatedTokens int64
	OriginalBytes   int64
	CacheLabel      string
}

func ResultSummaryFor(data any, base string, style Style) (ResultSummary, bool) {
	result, ok := DecodeResult(data)
	if !ok {
		return ResultSummary{}, false
	}
	key := result.CallID
	if key == "" {
		key = result.Name
	}
	if base == "" {
		base = result.Name
	}
	summary := ResultSummary{
		Key:       key,
		IsError:   result.IsError,
		Truncated: result.Truncated,
		OutputRef: result.OutputRef,
	}
	var parts []string
	if result.IsError {
		prefix := "Failed"
		if style == StyleTelegram {
			prefix = "⛔"
		}
		parts = append(parts, strings.TrimSpace(prefix+" "+base))
		if text := CompactText(strings.TrimSpace(result.Content), 96); text != "" && text != "-" {
			parts = append(parts, text)
		}
		summary.Line = strings.Join(parts, " · ")
		return summary, true
	}
	prefix := "Done"
	if style == StyleTelegram {
		prefix = "✅"
	}
	parts = append(parts, strings.TrimSpace(prefix+" "+base))
	if result.Truncated {
		parts = append(parts, "truncated")
	}
	if result.OutputRef != "" {
		parts = append(parts, "ref "+CompactText(filepathBase(result.OutputRef), 56))
	}
	if durationMS := metadataInt(result.Metadata, "duration_ms"); durationMS > 0 {
		summary.DurationMS = durationMS
		parts = append(parts, CompactDurationMS(durationMS))
	}
	if cacheLabel := webCacheLabel(result.Metadata); cacheLabel != "" {
		summary.CacheLabel = cacheLabel
		parts = append(parts, cacheLabel)
	}
	if tokens := metadataInt(result.Metadata, "estimated_text_tokens"); tokens > 0 {
		summary.EstimatedTokens = tokens
		parts = append(parts, "~"+CompactInt(tokens)+" tok")
	}
	if original := metadataInt(result.Metadata, "original_output_bytes"); original > 0 {
		summary.OriginalBytes = original
		parts = append(parts, CompactInt(original)+"B")
	}
	summary.Line = strings.Join(parts, " · ")
	return summary, true
}

func DecodeResult(data any) (protocol.ToolResult, bool) {
	bytes, _ := json.Marshal(data)
	var result protocol.ToolResult
	if err := json.Unmarshal(bytes, &result); err != nil {
		return protocol.ToolResult{}, false
	}
	return result, result.Name != "" || result.CallID != "" || result.Content != ""
}

type callRenderFunc func(protocol.ToolCall, map[string]any) (string, bool)

type callRenderer struct {
	tui      callRenderFunc
	telegram callRenderFunc
}

var callRenderers = map[string]callRenderer{
	"shell_exec": {
		tui: func(_ protocol.ToolCall, args map[string]any) (string, bool) {
			if argv := stringSliceArg(args["argv"]); len(argv) > 0 {
				return "Ran " + Preview(formatArgv(argv), 120), true
			}
			return "", false
		},
		telegram: func(_ protocol.ToolCall, args map[string]any) (string, bool) {
			if argv := stringSliceArg(args["argv"]); len(argv) > 0 {
				return "⚙️ shell " + CompactText(formatArgv(argv), 120), true
			}
			return "⚙️ shell " + CompactArg(args["argv"], 120), true
		},
	},
	"fs_read_file": {
		tui:      staticCallLine(fsReadTUILine),
		telegram: staticCallLine(fsReadTelegramLine),
	},
	"fs_list": {
		tui:      staticCallLine(func(args map[string]any) string { return "Listed " + firstArgDefault(args, ".", "path") }),
		telegram: staticCallLine(func(args map[string]any) string { return "📁 list " + CompactArg(args["path"], 96) }),
	},
	"fs_search": {
		tui: func(_ protocol.ToolCall, args map[string]any) (string, bool) {
			query := firstArg(args, "query", "pattern")
			path := firstArgDefault(args, ".", "path")
			if query != "" {
				return "Searched " + Preview(query, 72) + " in " + path, true
			}
			return "", false
		},
		telegram: staticCallLine(func(args map[string]any) string {
			return "🔍 search " + CompactArg(args["query"], 56) + " in " + CompactArg(args["path"], 56)
		}),
	},
	"fs_write_file": {
		tui:      staticCallLine(func(args map[string]any) string { return "Wrote " + firstArg(args, "path", "file") }),
		telegram: staticCallLine(func(args map[string]any) string { return "✍️ write " + CompactArg(args["path"], 96) }),
	},
	"fs_make_dir": {
		tui: staticCallLine(func(args map[string]any) string { return "Created dir " + firstArg(args, "path") }),
	},
	"web_fetch": {
		tui:      staticCallLine(func(args map[string]any) string { return "Fetched " + CompactURL(args["url"], 88) }),
		telegram: staticCallLine(func(args map[string]any) string { return "🌐 web_fetch " + CompactURL(args["url"], 88) }),
	},
	"web_extract": {
		tui: staticCallLine(func(args map[string]any) string {
			return "Extracted " + JoinParts(CompactURL(args["url"], 64), OptionalPart("query", args["query"], 48))
		}),
		telegram: staticCallLine(func(args map[string]any) string {
			return "🧩 web_extract " + JoinParts(CompactURL(args["url"], 60), OptionalPart("query", args["query"], 48))
		}),
	},
	"web_search": {
		tui:      staticCallLine(func(args map[string]any) string { return "Searched web " + CompactArg(args["query"], 96) }),
		telegram: staticCallLine(func(args map[string]any) string { return "🔎 web_search " + CompactArg(args["query"], 96) }),
	},
	"web_crawl": {
		tui:      staticCallLine(func(args map[string]any) string { return "Crawled " + CompactURL(args["url"], 88) }),
		telegram: staticCallLine(func(args map[string]any) string { return "🕸 web_crawl " + CompactURL(args["url"], 88) }),
	},
	"time_now": {
		tui: staticCallLine(func(map[string]any) string { return "Checked time" }),
	},
	"mcp_list_tools": {
		tui: staticCallLine(func(args map[string]any) string {
			return "Listed MCP tools " + JoinParts("server="+CompactArg(args["server"], 32), OptionalPart("query", args["query"], 48))
		}),
		telegram: staticCallLine(func(args map[string]any) string {
			return "🔌 mcp tools " + JoinParts("server="+CompactArg(args["server"], 32), OptionalPart("query", args["query"], 48))
		}),
	},
	"mcp_call": {
		tui:      staticCallLine(func(args map[string]any) string { return "Called MCP " + CompactArg(args["name"], 80) }),
		telegram: staticCallLine(func(args map[string]any) string { return "🔌 mcp call " + CompactArg(args["name"], 80) }),
	},
}

func staticCallLine(render func(map[string]any) string) callRenderFunc {
	return func(_ protocol.ToolCall, args map[string]any) (string, bool) {
		return render(args), true
	}
}

func registeredCallLine(call protocol.ToolCall, args map[string]any, style Style) (string, bool) {
	entry, ok := callRenderers[call.Name]
	if !ok {
		return "", false
	}
	var render callRenderFunc
	switch style {
	case StyleTelegram:
		render = entry.telegram
	default:
		render = entry.tui
	}
	if render == nil {
		return "", false
	}
	return render(call, args)
}

func fallbackCallLine(call protocol.ToolCall, args map[string]any, style Style) string {
	if style == StyleTelegram {
		return "🛠 " + call.Name + " " + Preview(string(call.Arguments), 160)
	}
	for _, key := range []string{"path", "command", "cmd", "query", "url", "pattern", "glob", "file"} {
		if text, ok := args[key].(string); ok && text != "" {
			return fmt.Sprintf("Called %s %s", call.Name, Preview(text, 80))
		}
	}
	return "Called " + call.Name
}

func CompactArg(value any, limit int) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return "-"
	}
	text = strings.Join(strings.Fields(text), " ")
	return CompactText(text, limit)
}

func CompactURL(value any, limit int) string {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" || raw == "<nil>" {
		return "-"
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		path := parsed.EscapedPath()
		if path == "" {
			path = "/"
		}
		if parsed.RawQuery != "" {
			path += "?…"
		}
		raw = parsed.Host + path
	}
	return CompactText(raw, limit)
}

func CompactText(text string, limit int) string {
	if limit <= 0 {
		limit = 80
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit < 12 {
		return string(runes[:limit]) + "…"
	}
	head := (limit - 1) * 2 / 3
	tail := limit - 1 - head
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

func Preview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "empty"
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	head := limit - 80
	if head < 1 {
		head = limit
	}
	return string(runes[:head]) + "\n...[truncated " + fmt.Sprint(len(runes)) + " chars]"
}

func OptionalPart(name string, value any, limit int) string {
	text := CompactArg(value, limit)
	if text == "-" {
		return ""
	}
	return name + "=" + text
}

func JoinParts(parts ...string) string {
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " ")
}

func CompactDurationMS(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return trimFloat(float64(ms)/1000) + "s"
	default:
		minutes := ms / 60_000
		seconds := (ms % 60_000) / 1000
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
}

func CompactInt(value int64) string {
	switch {
	case value >= 1_000_000:
		return trimFloat(float64(value)/1_000_000) + "M"
	case value >= 1_000:
		return trimFloat(float64(value)/1_000) + "k"
	default:
		return fmt.Sprint(value)
	}
}

func trimFloat(value float64) string {
	text := fmt.Sprintf("%.1f", value)
	return strings.TrimSuffix(text, ".0")
}

func firstArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := args[key].(string); ok && text != "" {
			return Preview(text, 120)
		}
	}
	return ""
}

func firstArgDefault(args map[string]any, fallback string, keys ...string) string {
	if value := firstArg(args, keys...); value != "" {
		return value
	}
	return fallback
}

func intArg(args map[string]any, key string) int {
	switch value := args[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

func fsReadTUILine(args map[string]any) string {
	return "Read " + fsReadTargetLine(firstArg(args, "path", "file"), args)
}

func fsReadTelegramLine(args map[string]any) string {
	return "📖 read " + fsReadTargetLine(CompactArg(args["path"], 96), args)
}

func fsReadTargetLine(path string, args map[string]any) string {
	offset := intArg(args, "offset")
	limit := intArg(args, "limit")
	if offset <= 0 && limit <= 0 {
		return path
	}
	if offset <= 0 {
		offset = 1
	}
	if limit > 0 {
		return fmt.Sprintf("%s lines %d-%d", path, offset, offset+limit-1)
	}
	return fmt.Sprintf("%s from line %d", path, offset)
}

func stringSliceArg(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, text)
		}
		return out
	default:
		return nil
	}
}

func formatArgv(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellQuoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./:=+,%@", r))
	}) < 0 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func filepathBase(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func metadataInt(metadata map[string]any, key string) int64 {
	if len(metadata) == 0 {
		return 0
	}
	switch value := metadata[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	default:
		return 0
	}
}

func webCacheLabel(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	if metadataBool(metadata, "web_cache_hit") {
		return "cache hit"
	}
	if metadataBool(metadata, "web_cache_miss") {
		return "cache miss"
	}
	return ""
}

func metadataBool(metadata map[string]any, key string) bool {
	if len(metadata) == 0 {
		return false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	case int:
		return value != 0
	case int64:
		return value != 0
	case float64:
		return value != 0
	default:
		return false
	}
}

func mustJSON(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(bytes)
}
