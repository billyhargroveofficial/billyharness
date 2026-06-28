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
	switch style {
	case StyleTelegram:
		return telegramCallLine(call, args)
	default:
		return tuiCallLine(call, args)
	}
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
	result, ok := DecodeResult(data)
	if !ok {
		return "", ""
	}
	key := result.CallID
	if key == "" {
		key = result.Name
	}
	if base == "" {
		base = result.Name
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
		return key, strings.Join(parts, " · ")
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
	if tokens := metadataInt(result.Metadata, "estimated_text_tokens"); tokens > 0 {
		parts = append(parts, "~"+CompactInt(tokens)+" tok")
	}
	if original := metadataInt(result.Metadata, "original_output_bytes"); original > 0 {
		parts = append(parts, CompactInt(original)+"B")
	}
	return key, strings.Join(parts, " · ")
}

func DecodeResult(data any) (protocol.ToolResult, bool) {
	bytes, _ := json.Marshal(data)
	var result protocol.ToolResult
	if err := json.Unmarshal(bytes, &result); err != nil {
		return protocol.ToolResult{}, false
	}
	return result, result.Name != "" || result.CallID != "" || result.Content != ""
}

func tuiCallLine(call protocol.ToolCall, args map[string]any) string {
	switch call.Name {
	case "shell_exec":
		if argv := stringSliceArg(args["argv"]); len(argv) > 0 {
			return "Ran " + Preview(formatArgv(argv), 120)
		}
	case "fs_read_file":
		return "Read " + firstArg(args, "path", "file")
	case "fs_list":
		return "Listed " + firstArgDefault(args, ".", "path")
	case "fs_search":
		query := firstArg(args, "query", "pattern")
		path := firstArgDefault(args, ".", "path")
		if query != "" {
			return "Searched " + Preview(query, 72) + " in " + path
		}
	case "fs_write_file":
		return "Wrote " + firstArg(args, "path", "file")
	case "fs_make_dir":
		return "Created dir " + firstArg(args, "path")
	case "web_fetch":
		return "Fetched " + CompactURL(args["url"], 88)
	case "web_extract":
		return "Extracted " + JoinParts(CompactURL(args["url"], 64), OptionalPart("query", args["query"], 48))
	case "web_search":
		return "Searched web " + CompactArg(args["query"], 96)
	case "web_crawl":
		return "Crawled " + CompactURL(args["url"], 88)
	case "time_now":
		return "Checked time"
	case "mcp_list_tools":
		return "Listed MCP tools " + JoinParts("server="+CompactArg(args["server"], 32), OptionalPart("query", args["query"], 48))
	case "mcp_call":
		return "Called MCP " + CompactArg(args["name"], 80)
	}
	for _, key := range []string{"path", "command", "cmd", "query", "url", "pattern", "glob", "file"} {
		if text, ok := args[key].(string); ok && text != "" {
			return fmt.Sprintf("Called %s %s", call.Name, Preview(text, 80))
		}
	}
	return "Called " + call.Name
}

func telegramCallLine(call protocol.ToolCall, args map[string]any) string {
	switch call.Name {
	case "web_search":
		return "🔎 web_search " + CompactArg(args["query"], 96)
	case "web_fetch":
		return "🌐 web_fetch " + CompactURL(args["url"], 88)
	case "web_extract":
		return "🧩 web_extract " + JoinParts(CompactURL(args["url"], 60), OptionalPart("query", args["query"], 48))
	case "web_crawl":
		return "🕸 web_crawl " + CompactURL(args["url"], 88)
	case "fs_read_file":
		return "📖 read " + CompactArg(args["path"], 96)
	case "fs_list":
		return "📁 list " + CompactArg(args["path"], 96)
	case "fs_search":
		return "🔍 search " + CompactArg(args["query"], 56) + " in " + CompactArg(args["path"], 56)
	case "fs_write_file":
		return "✍️ write " + CompactArg(args["path"], 96)
	case "shell_exec":
		if argv := stringSliceArg(args["argv"]); len(argv) > 0 {
			return "⚙️ shell " + CompactText(formatArgv(argv), 120)
		}
		return "⚙️ shell " + CompactArg(args["argv"], 120)
	case "mcp_list_tools":
		return "🔌 mcp tools " + JoinParts("server="+CompactArg(args["server"], 32), OptionalPart("query", args["query"], 48))
	case "mcp_call":
		return "🔌 mcp call " + CompactArg(args["name"], 80)
	default:
		return "🛠 " + call.Name + " " + Preview(string(call.Arguments), 160)
	}
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

func mustJSON(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(bytes)
}
