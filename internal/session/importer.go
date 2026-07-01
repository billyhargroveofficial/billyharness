package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	ImportFormatAuto     = "auto"
	ImportFormatJSONL    = "jsonl"
	ImportFormatMarkdown = "markdown"
)

type ImportOptions struct {
	Source string
	Format string
}

type ImportResult struct {
	Source      string             `json:"source,omitempty"`
	Format      string             `json:"format"`
	Messages    []protocol.Message `json:"messages"`
	Events      []protocol.Event   `json:"events,omitempty"`
	Diagnostics ImportDiagnostics  `json:"diagnostics"`
}

type ImportDiagnostics struct {
	ImportedMessages  int             `json:"imported_messages"`
	MessageCount      int             `json:"message_count"`
	UserMessages      int             `json:"user_messages,omitempty"`
	AssistantMessages int             `json:"assistant_messages,omitempty"`
	SystemMessages    int             `json:"system_messages,omitempty"`
	ApproxTokens      int             `json:"approx_tokens,omitempty"`
	Warnings          []ImportWarning `json:"warnings,omitempty"`
}

type ImportWarning struct {
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

func ImportTranscript(raw []byte, opts ImportOptions) (ImportResult, error) {
	format := normalizeImportFormat(opts.Format, raw)
	source := strings.TrimSpace(opts.Source)
	var messages []protocol.Message
	var warnings []ImportWarning
	var err error
	switch format {
	case ImportFormatJSONL:
		messages, warnings, err = importJSONLike(raw)
	case ImportFormatMarkdown:
		messages, warnings, err = importMarkdown(raw)
	default:
		return ImportResult{}, fmt.Errorf("unsupported import format %q", opts.Format)
	}
	if err != nil {
		return ImportResult{}, err
	}
	if len(messages) == 0 {
		return ImportResult{}, fmt.Errorf("no user or assistant messages found in %s import", format)
	}
	diag := buildImportDiagnostics(messages, warnings)
	marker := importMarker(source, format, diag)
	outMessages := append([]protocol.Message{marker}, messages...)
	event := protocol.Event{
		Type: protocol.EventSessionImported,
		Data: protocol.SessionImportedEvent{
			Source:           source,
			Format:           format,
			ImportedMessages: diag.ImportedMessages,
			MessageCount:     diag.MessageCount,
			ApproxTokens:     diag.ApproxTokens,
			Warnings:         warningStrings(warnings),
		},
	}
	return ImportResult{
		Source:      source,
		Format:      format,
		Messages:    outMessages,
		Events:      []protocol.Event{event},
		Diagnostics: diag,
	}, nil
}

func normalizeImportFormat(format string, raw []byte) string {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "", ImportFormatAuto:
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			return ImportFormatJSONL
		}
		return ImportFormatMarkdown
	case "json":
		return ImportFormatJSONL
	case "md":
		return ImportFormatMarkdown
	default:
		return format
	}
}

func importJSONLike(raw []byte) ([]protocol.Message, []ImportWarning, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil, fmt.Errorf("empty import")
	}
	if trimmed[0] == '[' {
		var records []map[string]any
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, nil, fmt.Errorf("invalid JSON transcript: %w", err)
		}
		return importJSONRecords(records)
	}
	if trimmed[0] == '{' && bytes.Contains(trimmed, []byte(`"messages"`)) {
		var wrapper struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err == nil && len(wrapper.Messages) > 0 {
			return importJSONRecords(wrapper.Messages)
		}
	}
	var records []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var warnings []ImportWarning
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, nil, fmt.Errorf("invalid JSONL at line %d: %w", lineNo, err)
		}
		record["_line"] = float64(lineNo)
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	messages, recordWarnings, err := importJSONRecords(records)
	warnings = append(warnings, recordWarnings...)
	return messages, warnings, err
}

func importJSONRecords(records []map[string]any) ([]protocol.Message, []ImportWarning, error) {
	var messages []protocol.Message
	var warnings []ImportWarning
	for i, record := range records {
		line := intFromAny(record["_line"])
		if line == 0 {
			line = i + 1
		}
		flat := flattenMessageRecord(record)
		role, ok := normalizeImportRole(stringFromAny(flat["role"]))
		if !ok {
			role, ok = normalizeImportRole(stringFromAny(flat["speaker"]))
		}
		if !ok {
			warnings = append(warnings, ImportWarning{Line: line, Reason: "unsupported_role", Detail: "record has no user/assistant role"})
			continue
		}
		for _, warning := range unsupportedToolWarnings(flat, line) {
			warnings = append(warnings, warning)
		}
		if role == protocol.RoleTool {
			warnings = append(warnings, ImportWarning{Line: line, Reason: "unsupported_tool_result", Detail: "tool role records are not guessed as chat messages"})
			continue
		}
		content := extractImportContent(flat)
		if strings.TrimSpace(content) == "" {
			warnings = append(warnings, ImportWarning{Line: line, Reason: "empty_message", Detail: string(role)})
			continue
		}
		messages = append(messages, protocol.Message{Role: role, Content: content})
	}
	return messages, warnings, nil
}

func flattenMessageRecord(record map[string]any) map[string]any {
	for _, key := range []string{"message", "item", "event"} {
		if nested, ok := record[key].(map[string]any); ok {
			out := map[string]any{}
			for k, v := range record {
				out[k] = v
			}
			for k, v := range nested {
				out[k] = v
			}
			return out
		}
	}
	return record
}

func normalizeImportRole(role string) (protocol.Role, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system":
		return protocol.RoleSystem, true
	case "user", "human":
		return protocol.RoleUser, true
	case "assistant", "ai", "model":
		return protocol.RoleAssistant, true
	case "tool":
		return protocol.RoleTool, true
	default:
		return "", false
	}
}

func extractImportContent(record map[string]any) string {
	for _, key := range []string{"content", "text", "message_text"} {
		if text := contentFromAny(record[key]); text != "" {
			return text
		}
	}
	if parts := contentFromAny(record["parts"]); parts != "" {
		return parts
	}
	return ""
}

func contentFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var parts []string
		for _, item := range typed {
			if text := contentFromAny(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "content"} {
			if text := contentFromAny(typed[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func unsupportedToolWarnings(record map[string]any, line int) []ImportWarning {
	var warnings []ImportWarning
	for _, key := range []string{"tool_calls", "tool_call", "function_call", "tool_use", "tool_result", "tool_call_id"} {
		if value, ok := record[key]; ok && value != nil && fmt.Sprint(value) != "" {
			warnings = append(warnings, ImportWarning{Line: line, Reason: "unsupported_tool_call", Detail: key})
		}
	}
	if strings.Contains(strings.ToLower(stringFromAny(record["type"])), "tool") {
		warnings = append(warnings, ImportWarning{Line: line, Reason: "unsupported_tool_record", Detail: stringFromAny(record["type"])})
	}
	return warnings
}

var markdownSpeakerRE = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:\*\*)?\s*(user|human|assistant|ai|system)\s*(?:\*\*)?\s*:?\s*(.*)$`)

func importMarkdown(raw []byte) ([]protocol.Message, []ImportWarning, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var messages []protocol.Message
	var warnings []ImportWarning
	var role protocol.Role
	var body []string
	lineNo := 0
	flush := func() {
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if role != "" && text != "" {
			messages = append(messages, protocol.Message{Role: role, Content: text})
		}
		body = nil
	}
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if looksLikeToolMarkup(line) {
			warnings = append(warnings, ImportWarning{Line: lineNo, Reason: "unsupported_tool_markup", Detail: strings.TrimSpace(line)})
		}
		if match := markdownSpeakerRE.FindStringSubmatch(line); match != nil {
			nextRole, ok := normalizeImportRole(match[1])
			if ok {
				flush()
				role = nextRole
				if strings.TrimSpace(match[2]) != "" {
					body = append(body, strings.TrimSpace(match[2]))
				}
				continue
			}
		}
		if role != "" {
			body = append(body, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	flush()
	return messages, warnings, nil
}

func looksLikeToolMarkup(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "tool_call") ||
		strings.Contains(lower, "<tool") ||
		strings.Contains(lower, "function_call")
}

func buildImportDiagnostics(messages []protocol.Message, warnings []ImportWarning) ImportDiagnostics {
	diag := ImportDiagnostics{
		ImportedMessages: len(messages),
		MessageCount:     len(messages) + 1,
		Warnings:         append([]ImportWarning(nil), warnings...),
	}
	for _, msg := range messages {
		tokens := approxTokens(msg.Content)
		diag.ApproxTokens += tokens
		switch msg.Role {
		case protocol.RoleUser:
			diag.UserMessages++
		case protocol.RoleAssistant:
			diag.AssistantMessages++
		case protocol.RoleSystem:
			diag.SystemMessages++
		}
	}
	return diag
}

func importMarker(source, format string, diag ImportDiagnostics) protocol.Message {
	name := source
	if name == "" {
		name = "external transcript"
	} else {
		name = filepath.Base(name)
	}
	lines := []string{
		"# Imported external session",
		"source: " + name,
		"format: " + format,
		fmt.Sprintf("imported_messages: %d", diag.ImportedMessages),
		fmt.Sprintf("approx_tokens: %d", diag.ApproxTokens),
		"",
		"This transcript was converted into Billyharness messages. Unsupported tool-call records were not guessed; inspect import diagnostics before resuming work.",
	}
	return protocol.Message{Role: protocol.RoleSystem, Content: strings.Join(lines, "\n")}
}

func warningStrings(warnings []ImportWarning) []string {
	out := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if warning.Line > 0 {
			out = append(out, fmt.Sprintf("line %d: %s %s", warning.Line, warning.Reason, strings.TrimSpace(warning.Detail)))
		} else {
			out = append(out, strings.TrimSpace(warning.Reason+" "+warning.Detail))
		}
	}
	return out
}

func approxTokens(text string) int {
	runes := len([]rune(strings.TrimSpace(text)))
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}
