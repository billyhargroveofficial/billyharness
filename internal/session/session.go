package session

import (
	"context"
	"errors"
	"sync"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

var ErrBusy = errors.New("session run already active")

type Runner interface {
	RunMessages(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error)
}

type RunnerFunc func(context.Context, []protocol.Message, func(protocol.Event)) ([]protocol.Message, error)

func (f RunnerFunc) RunMessages(ctx context.Context, messages []protocol.Message, emit func(protocol.Event)) ([]protocol.Message, error) {
	return f(ctx, messages, emit)
}

type Session struct {
	mu       sync.Mutex
	messages []protocol.Message
	cancel   context.CancelFunc
	running  bool
}

func New(messages []protocol.Message) *Session {
	return &Session{messages: cloneMessages(messages)}
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

func (s *Session) Run(ctx context.Context, runner Runner, prompt string, emit func(protocol.Event)) error {
	if runner == nil {
		return errors.New("session runner is nil")
	}
	if emit == nil {
		emit = func(protocol.Event) {}
	}
	runCtx, cancel := context.WithCancel(ctx)
	base, ok := s.startRun(cancel)
	if !ok {
		cancel()
		return ErrBusy
	}
	runMessages := append(base, protocol.Message{Role: protocol.RoleUser, Content: prompt})
	next, err := runner.RunMessages(runCtx, runMessages, emit)
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

func (s *Session) startRun(cancel context.CancelFunc) ([]protocol.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil, false
	}
	s.running = true
	s.cancel = cancel
	return cloneMessages(s.messages), true
}

func (s *Session) finishRun(next, fallback []protocol.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(next) > 0 {
		s.messages = cloneMessages(next)
	} else if len(fallback) > 0 {
		s.messages = cloneMessages(fallback)
	}
	s.running = false
	s.cancel = nil
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
