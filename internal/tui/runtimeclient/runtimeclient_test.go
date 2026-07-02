package runtimeclient

import (
	"context"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestInitialMessagesDelegatesInstructionProjection(t *testing.T) {
	messages := InitialMessages(config.InstructionSettings{})
	if len(messages) == 0 || messages[0].Role != protocol.RoleSystem {
		t.Fatalf("messages = %#v, want system message", messages)
	}
	if !strings.Contains(messages[0].Content, "fast coding and research agent") {
		t.Fatalf("system message missing harness prompt: %#v", messages)
	}
}

func TestRunLocalReturnsProviderConstructionError(t *testing.T) {
	_, err := RunLocal(context.Background(), Settings{}, nil, "hello", nil, nil, nil)
	if err == nil {
		t.Fatal("RunLocal should return provider construction error for empty settings")
	}
}

func TestMCPStatusReturnsDisabledStatusForDefaultSettings(t *testing.T) {
	status, err := MCPStatus(context.Background(), Settings{})
	if err != nil {
		t.Fatalf("MCPStatus returned error: %v", err)
	}
	if status.Enabled {
		t.Fatalf("default MCP status should be disabled: %#v", status)
	}
}
