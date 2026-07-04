package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

func errorResult(code, content string) Result {
	return Result{Content: content, IsError: true, ErrorCode: code}
}

func normalizeArgs(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || strings.TrimSpace(string(args)) == "" || strings.TrimSpace(string(args)) == "null" {
		return json.RawMessage(`{}`)
	}
	return args
}

func fileDisplayMetadata(path, summary string) map[string]any {
	target := fileDisplayTarget(path)
	return map[string]any{
		"path":                     path,
		"display_group":            "filesystem",
		"display_target":           target,
		"display_path":             path,
		"display_summary":          summary,
		"display_preview":          summary,
		"display_collapse_default": true,
	}
}

func fileDisplayTarget(path string) string {
	target := filepath.Base(strings.TrimSpace(path))
	if target == "" || target == "." || target == string(filepath.Separator) {
		return strings.TrimSpace(path)
	}
	return target
}
