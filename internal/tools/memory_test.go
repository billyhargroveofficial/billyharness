package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestMemoryToolsPreviewConfirmListSearchReadRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	cfg := config.Default()
	registry := NewRegistry(cfg)

	preview, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "memory_add",
		Arguments: rawArgs(map[string]any{
			"type":    "user",
			"topic":   "style",
			"summary": "Prefers exact evidence",
			"path":    "topics/style.md",
			"body":    "Use exact test evidence in summaries.\n",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Content, "not written") {
		t.Fatalf("preview = %#v", preview)
	}
	if _, err := os.Stat(filepath.Join(home, "memory", "topics", "style.md")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote topic file: %v", err)
	}

	written, err := registry.Call(context.Background(), protocol.ToolCall{
		Name: "memory_add",
		Arguments: rawArgs(map[string]any{
			"type":    "user",
			"topic":   "style",
			"summary": "Prefers exact evidence",
			"path":    "topics/style.md",
			"body":    "Use exact test evidence in summaries.\n",
			"confirm": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(written.Content, "written=true") || written.Metadata["display_group"] != "memory" {
		t.Fatalf("written = %#v", written)
	}
	list, err := registry.Call(context.Background(), protocol.ToolCall{Name: "memory_list", Arguments: rawArgs(map[string]any{})})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, "topic=style") {
		t.Fatalf("list = %s", list.Content)
	}
	search, err := registry.Call(context.Background(), protocol.ToolCall{Name: "memory_search", Arguments: rawArgs(map[string]any{"query": "test evidence"})})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search.Content, "body source=home topic=style") {
		t.Fatalf("search = %s", search.Content)
	}
	read, err := registry.Call(context.Background(), protocol.ToolCall{Name: "memory_read", Arguments: rawArgs(map[string]any{"topic": "style"})})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Content, "Use exact test evidence") {
		t.Fatalf("read = %s", read.Content)
	}
	removed, err := registry.Call(context.Background(), protocol.ToolCall{Name: "memory_remove", Arguments: rawArgs(map[string]any{"topic": "style", "confirm": true})})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(removed.Content, "removed=true") {
		t.Fatalf("remove = %s", removed.Content)
	}
}

func TestMemoryToolsRespectPlanModeVisibilityAndPolicy(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	cfg := config.Default()
	cfg.AccessMode = config.AccessModePlan
	registry := NewRegistry(cfg)
	specs := registry.Specs()
	for _, visible := range []string{"memory_list", "memory_search", "memory_read"} {
		if !hasSpec(specs, visible) {
			t.Fatalf("plan mode missing read memory tool %s", visible)
		}
	}
	for _, hidden := range []string{"memory_add", "memory_replace", "memory_remove"} {
		if hasSpec(specs, hidden) {
			t.Fatalf("plan mode exposed write memory tool %s", hidden)
		}
		_, err := registry.Call(context.Background(), protocol.ToolCall{Name: hidden, Arguments: rawArgs(map[string]any{})})
		if err == nil || !strings.Contains(err.Error(), "plan mode") {
			t.Fatalf("write memory tool %s err=%v", hidden, err)
		}
	}
}

func TestMemoryToolsDisabledWhenMemoryDisabled(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	cfg := config.Default()
	cfg.MemoryEnabled = false
	registry := NewRegistry(cfg)
	if hasSpec(registry.Specs(), "memory_list") {
		t.Fatalf("memory tools should be hidden when memory is disabled")
	}
}
