package session

import (
	"context"
	"errors"
	"sync"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

var ErrBusy = errors.New("session run already active")

type InputPolicy string

const (
	InputPolicyRejectWhileActive InputPolicy = "reject_while_active"
	InputPolicyQueueWhileActive  InputPolicy = "queue_while_active"
)

type Options struct {
	InputPolicy InputPolicy
}

type InputDecision struct {
	Policy   InputPolicy
	Accepted bool
	Queued   bool
	Running  bool
	Reason   string
}

type Runner interface {
	RunMessages(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error)
}

type RunnerFunc func(context.Context, []protocol.Message, func(protocol.Event)) ([]protocol.Message, error)

func (f RunnerFunc) RunMessages(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
	return f(ctx, messages, emit)
}

type promptHistoryDiscarder interface {
	DiscardPromptHistory() bool
}

type Session struct {
	mu          sync.Mutex
	messages    []protocol.Message
	cancel      context.CancelFunc
	running     bool
	inputPolicy InputPolicy
	idle        chan struct{}
}

func New(messages []protocol.Message) *Session {
	return NewWithOptions(messages, Options{})
}

func NewWithOptions(messages []protocol.Message, opts Options) *Session {
	policy := opts.InputPolicy
	if policy == "" {
		policy = InputPolicyRejectWhileActive
	}
	return &Session{messages: cloneMessages(messages), inputPolicy: policy}
}

func (s *Session) Messages() []protocol.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneMessages(s.messages)
}

func (s *Session) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Session) InputPolicy() InputPolicy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputPolicy
}

func (s *Session) Run(ctx context.Context, runner Runner, prompt string, emit func(protocol.Event)) error {
	if runner == nil {
		return errors.New("session runner is nil")
	}
	if emit == nil {
		emit = func(protocol.Event) {}
	}
	runCtx, cancel := context.WithCancel(ctx)
	base, decision, err := s.waitStartRun(ctx, cancel)
	if err != nil || !decision.Accepted {
		cancel()
		if err != nil {
			return err
		}
		return ErrBusy
	}
	runMessages := append(base, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	next, err := runner.RunMessages(runCtx, runMessages, emit)
	// Cancellation rollback policy: interrupted runs restore the pre-run
	// transcript and discard the prompt plus any late runner messages. The event
	// stream remains durable; callers must emit or synthesize a terminal event
	// for replay validity.
	if runInterrupted(runCtx, err) {
		s.finishRun(base, base)
		return err
	}
	if discardPromptHistory(err) {
		s.finishRun(next, base)
		return err
	}
	s.finishRun(next, runMessages)
	return err
}

func (s *Session) Cancel() bool {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Session) CancelAndWait(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	running := s.running
	cancel := s.cancel
	idle := s.idle
	s.mu.Unlock()
	if !running || cancel == nil {
		return false, nil
	}
	cancel()
	if idle == nil {
		return true, nil
	}
	select {
	case <-idle:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

func runInterrupted(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func discardPromptHistory(err error) bool {
	var discard promptHistoryDiscarder
	return errors.As(err, &discard) && discard.DiscardPromptHistory()
}

func (s *Session) startRun(cancel context.CancelFunc) ([]protocol.Message, InputDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	decision := InputDecision{Policy: s.inputPolicy, Running: s.running}
	if s.running {
		decision.Reason = "active_run"
		return nil, decision
	}
	s.running = true
	s.cancel = cancel
	s.idle = make(chan struct{})
	decision.Accepted = true
	decision.Reason = "idle"
	return cloneMessages(s.messages), decision
}

func (s *Session) waitStartRun(ctx context.Context, cancel context.CancelFunc) ([]protocol.Message, InputDecision, error) {
	queued := false
	for {
		base, decision := s.startRun(cancel)
		if decision.Accepted {
			decision.Queued = queued
			if queued {
				decision.Reason = "queued_after_active_run"
			}
			return base, decision, nil
		}
		if decision.Policy != InputPolicyQueueWhileActive {
			return nil, decision, ErrBusy
		}
		queued = true
		wait := s.activeWaitChannel()
		if wait == nil {
			continue
		}
		select {
		case <-wait:
		case <-ctx.Done():
			decision.Queued = true
			decision.Reason = "queue_context_canceled"
			return nil, decision, ctx.Err()
		}
	}
}

func (s *Session) activeWaitChannel() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return nil
	}
	return s.idle
}

func (s *Session) finishRun(next, fallback []protocol.Message) {
	s.mu.Lock()
	if len(next) > 0 {
		s.messages = cloneMessages(next)
	} else if len(fallback) > 0 {
		s.messages = cloneMessages(fallback)
	}
	s.running = false
	s.cancel = nil
	idle := s.idle
	s.idle = nil
	s.mu.Unlock()
	if idle != nil {
		close(idle)
	}
}

func cloneMessages(messages []protocol.Message) []protocol.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]protocol.Message, len(messages))
	for i, msg := range messages {
		out[i] = msg
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = make([]protocol.ToolCall, len(msg.ToolCalls))
			for j, call := range msg.ToolCalls {
				out[i].ToolCalls[j] = call
				if len(call.Arguments) > 0 {
					out[i].ToolCalls[j].Arguments = append([]byte(nil), call.Arguments...)
				}
			}
		}
	}
	return out
}
