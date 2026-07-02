package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestRunPreservesHistoryAcrossPrompts(t *testing.T) {
	s := New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	runner := RunnerFunc(func(_ context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
		last := messages[len(messages)-1]
		return append(messages, protocol.Message{Role: protocol.RoleAssistant, Content: "ok: " + last.Content}), nil
	})

	if err := s.Run(context.Background(), runner, "one", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), runner, "two", nil); err != nil {
		t.Fatal(err)
	}

	messages := s.Messages()
	if len(messages) != 5 {
		t.Fatalf("message count = %d, want 5: %+v", len(messages), messages)
	}
	if messages[1].Content != "one" || messages[2].Content != "ok: one" ||
		messages[3].Content != "two" || messages[4].Content != "ok: two" {
		t.Fatalf("unexpected history: %+v", messages)
	}
}

func TestRunInputAppendsAttachmentParts(t *testing.T) {
	s := New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	ref := protocol.AttachmentRef{
		ID:         "att_test",
		Kind:       protocol.AttachmentKindImage,
		StorageRef: "att_test.png",
		FileName:   "screen.png",
		SHA256:     "abc123",
	}
	var captured []protocol.Message
	runner := RunnerFunc(func(_ context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
		captured = messages
		return append(messages, protocol.Message{Role: protocol.RoleAssistant, Content: "ok"}), nil
	})

	if err := s.RunInput(context.Background(), runner, "look", []protocol.AttachmentRef{ref}, nil); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 2 {
		t.Fatalf("captured messages = %#v", captured)
	}
	user := captured[1]
	if user.Content != "look" || user.AttachmentCount() != 1 || len(user.Parts) != 2 ||
		user.Parts[0].Text != "look" || user.Parts[1].Attachment == nil || user.Parts[1].Attachment.ID != "att_test" {
		t.Fatalf("user message = %#v", user)
	}
	ref.ID = "mutated"
	if got := s.Messages()[1].Parts[1].Attachment.ID; got != "att_test" {
		t.Fatalf("stored attachment aliased caller ref: %q", got)
	}
}

func TestRunInputTextOnlyPreservesLegacyMessageShape(t *testing.T) {
	s := New(nil)
	if err := s.RunInput(context.Background(), RunnerFunc(func(_ context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
		if len(messages) != 1 || messages[0].Content != "hello" || len(messages[0].Parts) != 0 {
			t.Fatalf("text-only message = %#v", messages)
		}
		return messages, nil
	}), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRunReturnsBusyForConcurrentRun(t *testing.T) {
	s := New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	if s.InputPolicy() != InputPolicyRejectWhileActive {
		t.Fatalf("input policy = %q", s.InputPolicy())
	}
	started := make(chan struct{})
	release := make(chan struct{})
	runner := RunnerFunc(func(ctx context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
		close(started)
		select {
		case <-release:
			return append(messages, protocol.Message{Role: protocol.RoleAssistant, Content: "done"}), nil
		case <-ctx.Done():
			return messages, ctx.Err()
		}
	})

	done := make(chan error, 1)
	go func() {
		done <- s.Run(context.Background(), runner, "first", nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}

	err := s.Run(context.Background(), runner, "second", nil)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second run error = %v, want ErrBusy", err)
	}
	_, decision := s.startRun(func() {})
	if decision.Accepted || decision.Queued || !decision.Running || decision.Policy != InputPolicyRejectWhileActive || decision.Reason != "active_run" {
		t.Fatalf("busy decision = %#v", decision)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first run error = %v", err)
	}
}

func TestNewWithOptionsSetsInputPolicy(t *testing.T) {
	s := NewWithOptions(nil, Options{InputPolicy: InputPolicyRejectWhileActive})
	if s.InputPolicy() != InputPolicyRejectWhileActive {
		t.Fatalf("input policy = %q", s.InputPolicy())
	}
	base, decision := s.startRun(func() {})
	if !decision.Accepted || decision.Queued || decision.Running || decision.Reason != "idle" || base != nil {
		t.Fatalf("idle decision=%#v base=%#v", decision, base)
	}
	s.finishRun(nil, nil)
}

func TestRunQueuesFollowUpWhenPolicyAllows(t *testing.T) {
	s := NewWithOptions([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}}, Options{InputPolicy: InputPolicyQueueWhileActive})
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls int32
	runner := RunnerFunc(func(ctx context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
		call := atomic.AddInt32(&calls, 1)
		last := messages[len(messages)-1]
		switch call {
		case 1:
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return messages, ctx.Err()
			}
		case 2:
			close(secondStarted)
		default:
			t.Fatalf("unexpected call %d", call)
		}
		return append(messages, protocol.Message{Role: protocol.RoleAssistant, Content: "ok: " + last.Content}), nil
	})

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- s.Run(context.Background(), runner, "one", nil)
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first run did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- s.Run(context.Background(), runner, "two", nil)
	}()
	select {
	case <-secondStarted:
		t.Fatal("queued run started before active run finished")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first run error = %v", err)
	}
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second run did not start after first finished")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second run error = %v", err)
	}
	messages := s.Messages()
	if len(messages) != 5 ||
		messages[1].Content != "one" ||
		messages[2].Content != "ok: one" ||
		messages[3].Content != "two" ||
		messages[4].Content != "ok: two" {
		t.Fatalf("queued history = %#v", messages)
	}
}

func TestCancelStopsActiveRun(t *testing.T) {
	s := New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	started := make(chan struct{})
	runner := RunnerFunc(func(ctx context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
		close(started)
		<-ctx.Done()
		return messages, ctx.Err()
	})

	done := make(chan error, 1)
	go func() {
		done <- s.Run(context.Background(), runner, "cancel me", nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	if !s.Running() {
		t.Fatal("session did not report running")
	}
	if !s.Cancel() {
		t.Fatal("cancel returned false for active run")
	}
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	if s.Running() {
		t.Fatal("session still reports running after cancellation")
	}
	if s.Cancel() {
		t.Fatal("cancel returned true with no active run")
	}
	messages := s.Messages()
	if len(messages) != 1 || messages[0].Content != "system" {
		t.Fatalf("cancelled run should not persist its prompt, messages=%#v", messages)
	}
}

func TestCancelAndWaitWaitsForIdle(t *testing.T) {
	s := New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Run(context.Background(), RunnerFunc(func(ctx context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
			close(started)
			<-ctx.Done()
			return messages, ctx.Err()
		}), "cancel me", nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	cancelled, err := s.CancelAndWait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("cancelled = false, want true")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	if s.Running() {
		t.Fatal("session still reports running after CancelAndWait")
	}
}

func TestCancelAndWaitReturnsContextErrorWhenRunDoesNotStop(t *testing.T) {
	s := New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Run(context.Background(), RunnerFunc(func(ctx context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
			close(started)
			<-release
			return messages, ctx.Err()
		}), "stubborn", nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	cancelled, err := s.CancelAndWait(ctx)
	if !cancelled || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled=%t err=%v, want deadline", cancelled, err)
	}
	close(release)
	<-done
}

func TestCancelDropsPromptWhenRunnerReturnsNilAfterContextCancelled(t *testing.T) {
	s := New([]protocol.Message{{Role: protocol.RoleSystem, Content: "system"}})
	started := make(chan struct{})
	runner := RunnerFunc(func(ctx context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
		close(started)
		<-ctx.Done()
		return append(messages, protocol.Message{Role: protocol.RoleAssistant, Content: "late old answer"}), nil
	})

	done := make(chan error, 1)
	go func() {
		done <- s.Run(context.Background(), runner, "old prompt", nil)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	if !s.Cancel() {
		t.Fatal("cancel returned false for active run")
	}
	if err := <-done; err != nil {
		t.Fatalf("runner returned nil after cancellation, got err=%v", err)
	}
	messages := s.Messages()
	if len(messages) != 1 || messages[0].Content != "system" {
		t.Fatalf("late nil cancelled run should not persist prompt/answer, messages=%#v", messages)
	}
}

func TestRunDiscardsPromptHistoryWhenRunnerRequestsDiscard(t *testing.T) {
	s := New(nil)
	runner := RunnerFunc(func(_ context.Context, messages []protocol.Message, _ func(protocol.Event)) ([]protocol.Message, error) {
		if len(messages) != 1 || messages[0].Content != "blocked prompt" {
			t.Fatalf("run messages = %#v", messages)
		}
		return nil, discardPromptErr{}
	})
	err := s.Run(context.Background(), runner, "blocked prompt", nil)
	var discard discardPromptErr
	if !errors.As(err, &discard) {
		t.Fatalf("err = %v", err)
	}
	if messages := s.Messages(); len(messages) != 0 {
		t.Fatalf("discarded prompt should not persist with empty base history: %#v", messages)
	}
}

func TestMessagesReturnsDeepCopy(t *testing.T) {
	s := New([]protocol.Message{{
		Role: protocol.RoleAssistant,
		ToolCalls: []protocol.ToolCall{{
			ID:        "call-1",
			Name:      "tool",
			Arguments: []byte(`{"path":"/tmp/a"}`),
		}},
	}})
	messages := s.Messages()
	messages[0].ToolCalls[0].Arguments[9] = 'X'

	fresh := s.Messages()
	if string(fresh[0].ToolCalls[0].Arguments) != `{"path":"/tmp/a"}` {
		t.Fatalf("messages leaked mutable RawMessage: %s", fresh[0].ToolCalls[0].Arguments)
	}
}

type discardPromptErr struct{}

func (discardPromptErr) Error() string {
	return "discard prompt"
}

func (discardPromptErr) DiscardPromptHistory() bool {
	return true
}
