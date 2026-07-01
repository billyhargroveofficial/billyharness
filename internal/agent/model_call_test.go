package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestRunMessagesCoalescesAssistantAndReasoningDeltas(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"

	const contentChunks = 2000
	const reasoningChunks = 600
	events := make([]provider.Event, 0, contentChunks+reasoningChunks+2)
	var wantContent strings.Builder
	for i := 0; i < contentChunks; i++ {
		text := fmt.Sprintf("chunk-%04d ", i)
		wantContent.WriteString(text)
		events = append(events, provider.Event{Kind: provider.EventContent, Text: text})
	}
	var wantReasoning strings.Builder
	for i := 0; i < reasoningChunks; i++ {
		text := fmt.Sprintf("think-%04d ", i)
		wantReasoning.WriteString(text)
		events = append(events, provider.Event{Kind: provider.EventReasoning, Text: text})
	}
	events = append(events,
		provider.Event{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 10, OutputTokens: 20}},
		provider.Event{Kind: provider.EventDone},
	)

	prov := &scriptedProvider{steps: [][]provider.Event{events}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var emitted []protocol.Event
	messages, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "stream"},
	}, func(event protocol.Event) {
		emitted = append(emitted, event)
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotContent, gotReasoning strings.Builder
	var contentDeltaEvents, reasoningDeltaEvents int
	for _, event := range emitted {
		switch event.Type {
		case protocol.EventAssistantDelta:
			contentDeltaEvents++
			gotContent.WriteString(fmt.Sprint(event.Data))
		case protocol.EventAssistantReasoning:
			reasoningDeltaEvents++
			gotReasoning.WriteString(fmt.Sprint(event.Data))
		}
	}
	if gotContent.String() != wantContent.String() {
		t.Fatalf("content mismatch len got=%d want=%d", gotContent.Len(), wantContent.Len())
	}
	if gotReasoning.String() != wantReasoning.String() {
		t.Fatalf("reasoning mismatch len got=%d want=%d", gotReasoning.Len(), wantReasoning.Len())
	}
	if contentDeltaEvents >= contentChunks/10 {
		t.Fatalf("content delta events = %d, want far fewer than chunks %d", contentDeltaEvents, contentChunks)
	}
	if reasoningDeltaEvents >= reasoningChunks/10 {
		t.Fatalf("reasoning delta events = %d, want far fewer than chunks %d", reasoningDeltaEvents, reasoningChunks)
	}
	if got := lastAssistantContent(messages); got != wantContent.String() {
		t.Fatalf("assistant transcript len got=%d want=%d", len(got), wantContent.Len())
	}
}

func TestRunMessagesFlushesCoalescedDeltasBeforeUsageAndToolBoundaries(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.MaxToolRounds = 1
	prov := &scriptedProvider{steps: [][]provider.Event{{
		{Kind: provider.EventContent, Text: "hello "},
		{Kind: provider.EventContent, Text: "world"},
		{Kind: provider.EventUsage, Usage: provider.Usage{InputTokens: 4, OutputTokens: 2}},
		{Kind: provider.EventReasoning, Text: "think "},
		{Kind: provider.EventReasoning, Text: "more"},
		{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_1", ToolName: "time_now", ArgsDelta: `{}`},
		{Kind: provider.EventDone},
	}}}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	var emitted []protocol.Event
	_, err := a.RunMessages(context.Background(), []protocol.Message{
		{Role: protocol.RoleSystem, Content: "system"},
		{Role: protocol.RoleUser, Content: "stream"},
	}, func(event protocol.Event) {
		emitted = append(emitted, event)
	})
	if err == nil || !strings.Contains(err.Error(), "exceeded max tool rounds") {
		t.Fatalf("err = %v", err)
	}

	usageIndex := firstEventIndex(emitted, protocol.EventProviderUsageUpdate)
	toolIndex := firstEventIndex(emitted, protocol.EventToolCallRequested)
	if usageIndex < 0 || toolIndex < 0 {
		t.Fatalf("missing usage/tool events: %#v", emitted)
	}
	if got := collectStringEvents(emitted[:usageIndex], protocol.EventAssistantDelta); got != "hello world" {
		t.Fatalf("content before usage = %q", got)
	}
	if got := collectStringEvents(emitted[:toolIndex], protocol.EventAssistantReasoning); got != "think more" {
		t.Fatalf("reasoning before tool request = %q", got)
	}
}

func TestRunMessagesEmitsFirstCoalescedDeltaBeforeStreamCompletes(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov := &blockingFirstDeltaProvider{release: make(chan struct{})}
	a := New(cfg, prov, tools.NewRegistry(cfg))
	delta := make(chan string, 4)
	done := make(chan error, 1)
	go func() {
		_, err := a.RunMessages(context.Background(), []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "stream"},
		}, func(event protocol.Event) {
			if event.Type == protocol.EventAssistantDelta {
				select {
				case delta <- fmt.Sprint(event.Data):
				default:
				}
			}
		})
		done <- err
	}()

	select {
	case got := <-delta:
		if got != "first" {
			close(prov.release)
			t.Fatalf("first delta = %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		close(prov.release)
		<-done
		t.Fatal("first delta was not emitted before stream completion")
	}
	select {
	case got := <-delta:
		if got != " second" {
			close(prov.release)
			t.Fatalf("timed delta = %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		close(prov.release)
		<-done
		t.Fatal("pending delta was not flushed by time threshold before stream completion")
	}
	close(prov.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after provider release")
	}
}

type blockingFirstDeltaProvider struct {
	release chan struct{}
}

func (p *blockingFirstDeltaProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, <-chan error) {
	events := make(chan provider.Event, 2)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		select {
		case events <- provider.Event{Kind: provider.EventContent, Text: "first"}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		select {
		case events <- provider.Event{Kind: provider.EventContent, Text: " second"}:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		select {
		case <-p.release:
		case <-ctx.Done():
			errs <- ctx.Err()
			return
		}
		events <- provider.Event{Kind: provider.EventDone}
	}()
	return events, errs
}

func lastAssistantContent(messages []protocol.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == protocol.RoleAssistant {
			return messages[i].Content
		}
	}
	return ""
}

func firstEventIndex(events []protocol.Event, eventType protocol.EventType) int {
	for i, event := range events {
		if event.Type == eventType {
			return i
		}
	}
	return -1
}

func collectStringEvents(events []protocol.Event, eventType protocol.EventType) string {
	var b strings.Builder
	for _, event := range events {
		if event.Type == eventType {
			b.WriteString(fmt.Sprint(event.Data))
		}
	}
	return b.String()
}
