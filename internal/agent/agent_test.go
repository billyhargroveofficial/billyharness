package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunWithMockProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(cfg)
	a := New(cfg, prov, registry)

	var content string
	if err := a.Run(context.Background(), "hello", func(event protocol.Event) {
		if event.Type == protocol.EventAssistantDelta {
			content += fmt.Sprint(event.Data)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if content != "mock: hello" {
		t.Fatalf("content = %q", content)
	}
}
