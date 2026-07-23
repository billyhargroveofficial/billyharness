package agent

import (
	"context"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type readyDoneAndTerminalErrorProvider struct{}

func (readyDoneAndTerminalErrorProvider) Stream(context.Context, provider.Request) (<-chan provider.Event, <-chan error) {
	finish := provider.Finish{Kind: provider.FinishOutputLimit, RawReason: "length"}
	events := make(chan provider.Event, 1)
	events <- provider.Event{Kind: provider.EventDone, Finish: finish}
	close(events)
	errs := make(chan error, 1)
	errs <- &provider.FinishError{Finish: finish}
	close(errs)
	return events, errs
}

func TestAgentRecordsReadyDoneBeforeTerminalStreamError(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"

	for i := 0; i < 64; i++ {
		a := New(cfg, readyDoneAndTerminalErrorProvider{}, tools.NewRegistry(cfg))
		var emitted []protocol.Event
		_, err := a.RunMessages(context.Background(), finishTestMessages(), func(event protocol.Event) {
			emitted = append(emitted, event)
		})
		if err == nil {
			t.Fatalf("iteration %d: RunMessages() error = nil", i)
		}
		finished, ok := firstModelCallEvent(emitted, protocol.EventModelCallFinished)
		if !ok || finished.FinishKind != string(provider.FinishOutputLimit) || finished.FinishRawReason != "length" {
			t.Fatalf("iteration %d: model finish telemetry = %#v, present=%t", i, finished, ok)
		}
	}
}
