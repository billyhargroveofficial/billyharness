package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestDiagnosticsRunConfiguredCommandProducesBoundedIssuesAndOutputRef(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	cfg.DiagnosticsCommands = []config.DiagnosticCommand{{
		Name:             "lint",
		Command:          "sh",
		Args:             []string{"-c", "printf 'pkg/main.go:3:2: error: bad\\n'; yes extra | head -c 2048; exit 1"},
		Timeout:          5 * time.Second,
		MaxOutputBytes:   128,
		MaxIssues:        10,
		MaxIssuesPerFile: 5,
		Enabled:          true,
	}}
	registry := NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "diagnostics_run",
		Arguments: rawArgs(map[string]any{"name": "lint"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputRef == "" || !strings.HasPrefix(result.OutputRef, filepath.Join(home, "tool-output")) {
		t.Fatalf("output ref = %q metadata=%#v", result.OutputRef, result.Metadata)
	}
	if _, err := os.Stat(result.OutputRef); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "<diagnostics") || strings.Contains(result.Content, "extra extra extra") {
		t.Fatalf("content leaked raw output: %q", result.Content)
	}
	if result.Metadata["diagnostics_name"] != "lint" ||
		anyInt64(result.Metadata["diagnostics_exit_code"]) != 1 ||
		anyInt64(result.Metadata["diagnostics_issue_count"]) != 1 ||
		result.Metadata["diagnostics_output_truncated"] != true {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestDiagnosticsRunRejectsUnknownCommandInsteadOfArbitraryArgv(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.WorkspaceRoots = []string{root}
	cfg.AutoApproveDangerous = true
	cfg.DiagnosticsCommands = []config.DiagnosticCommand{{
		Name:    "known",
		Command: "sh",
		Args:    []string{"-c", "true"},
		Enabled: true,
	}}
	registry := NewRegistry(cfg)
	result, err := registry.Call(context.Background(), protocol.ToolCall{
		Name:      "diagnostics_run",
		Arguments: rawArgs(map[string]any{"name": "rm", "argv": []string{"rm", "-rf", "."}}),
	})
	if err == nil || result.ErrorCode != "validation_error" {
		t.Fatalf("expected validation error, result=%#v err=%v", result, err)
	}
	result, err = registry.Call(context.Background(), protocol.ToolCall{
		Name:      "diagnostics_run",
		Arguments: rawArgs(map[string]any{"name": "missing"}),
	})
	if err == nil || result.ErrorCode != "diagnostics_unknown_command" {
		t.Fatalf("expected unknown command, result=%#v err=%v", result, err)
	}
}
