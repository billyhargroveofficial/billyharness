package telegrambot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type blockingHarness struct {
	startOnce  sync.Once
	cancelOnce sync.Once
	runStarted chan struct{}
	cancelled  chan struct{}
}

func newBlockingHarness() *blockingHarness {
	return &blockingHarness{
		runStarted: make(chan struct{}),
		cancelled:  make(chan struct{}),
	}
}

func (h *blockingHarness) CreateSession(context.Context, string) (string, error) {
	return "session-1", nil
}

func (h *blockingHarness) RunSession(ctx context.Context, _ string, _ gateway.RunRequest, _ func(protocol.Event)) error {
	h.startOnce.Do(func() { close(h.runStarted) })
	<-ctx.Done()
	h.cancelOnce.Do(func() { close(h.cancelled) })
	return ctx.Err()
}

func (h *blockingHarness) MCPStatus(context.Context) (string, error) {
	return "{}", nil
}

func TestCancelCommandBypassesActiveRunLock(t *testing.T) {
	harness := newBlockingHarness()
	bot, err := New(Options{
		BotToken:        "token",
		StatePath:       t.TempDir() + "/state.json",
		Model:           "deepseek-v4-flash",
		Profile:         "billy",
		ReasoningEffort: "high",
		EditInterval:    time.Millisecond,
		AllowedChatIDs:  map[int64]bool{123: true},
		SendEnabled:     false,
		DryRunDefault:   true,
	}, nil, harness)
	if err != nil {
		t.Fatal(err)
	}

	runDone := make(chan struct{})
	go func() {
		bot.handleMessage(context.Background(), Message{
			Chat: Chat{ID: 123},
			Text: "run a long task",
		})
		close(runDone)
	}()

	select {
	case <-harness.runStarted:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}

	cancelDone := make(chan struct{})
	go func() {
		bot.handleMessage(context.Background(), Message{
			Chat: Chat{ID: 123},
			Text: "/cancel",
		})
		close(cancelDone)
	}()

	select {
	case <-cancelDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("/cancel was blocked by the active run lock")
	}
	select {
	case <-harness.cancelled:
	case <-time.After(time.Second):
		t.Fatal("active run was not cancelled")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("run handler did not finish after cancel")
	}
}

func TestLiveTelegramRequiresAllowlistUnlessExplicitlyAllowed(t *testing.T) {
	bot, err := New(Options{
		BotToken:      "token",
		StatePath:     t.TempDir() + "/state.json",
		SendEnabled:   true,
		DryRunDefault: false,
	}, nil, newBlockingHarness())
	if err != nil {
		t.Fatal(err)
	}
	if bot.allowed(123) {
		t.Fatal("live bot without allowlist should be fail-closed")
	}
	if !bot.opts.RequireAllowlist {
		t.Fatal("live bot should force require-allowlist unless allow-all-chats is explicit")
	}

	allowAll, err := New(Options{
		BotToken:      "token",
		StatePath:     t.TempDir() + "/state.json",
		SendEnabled:   true,
		DryRunDefault: false,
		AllowAllChats: true,
	}, nil, newBlockingHarness())
	if err != nil {
		t.Fatal(err)
	}
	if !allowAll.allowed(123) {
		t.Fatal("allow-all-chats should explicitly permit unknown chats")
	}
}
