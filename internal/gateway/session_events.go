package gateway

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
)

type SessionStatus struct {
	ID              string    `json:"id"`
	Created         time.Time `json:"created"`
	Running         bool      `json:"running"`
	RunSeq          int64     `json:"run_seq"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	LastEvent       string    `json:"last_event,omitempty"`
	LastEventAt     time.Time `json:"last_event_at,omitempty"`
	Model           string    `json:"model,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	Profile         string    `json:"profile,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	MessageCount    int       `json:"message_count"`
	ModelCalls      int       `json:"model_calls"`
	ToolCalls       int       `json:"tool_calls"`
	LastError       string    `json:"last_error,omitempty"`
}

type eventHub struct {
	mu          sync.Mutex
	subscribers map[chan protocol.Event]struct{}
}

func newGatewaySession(id string, created time.Time, messages []protocol.Message) *Session {
	if created.IsZero() {
		created = time.Now().UTC()
	}
	session := &Session{
		ID:      id,
		Created: created,
		Thread:  sessionpkg.New(messages),
		events:  newEventHub(),
	}
	session.status = SessionStatus{
		ID:           id,
		Created:      created,
		MessageCount: len(messages),
	}
	return session
}

func newEventHub() *eventHub {
	return &eventHub{subscribers: map[chan protocol.Event]struct{}{}}
}

func (h *eventHub) Subscribe() (<-chan protocol.Event, func()) {
	if h == nil {
		ch := make(chan protocol.Event)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan protocol.Event, 256)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subscribers, ch)
		h.mu.Unlock()
	}
}

func (h *eventHub) Publish(event protocol.Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	subscribers := make([]chan protocol.Event, 0, len(h.subscribers))
	for ch := range h.subscribers {
		subscribers = append(subscribers, ch)
	}
	h.mu.Unlock()
	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Session) ensureRuntime() {
	if s.events == nil {
		s.events = newEventHub()
	}
	if s.status.ID == "" {
		s.status = SessionStatus{
			ID:           s.ID,
			Created:      s.Created,
			Running:      s.Thread != nil && s.Thread.Running(),
			MessageCount: len(s.messages()),
		}
	}
}

func (s *Session) messages() []protocol.Message {
	if s == nil || s.Thread == nil {
		return nil
	}
	return s.Thread.Messages()
}

func (s *Session) Status() SessionStatus {
	if s == nil {
		return SessionStatus{}
	}
	s.mu.Lock()
	s.ensureRuntime()
	status := s.status
	s.mu.Unlock()
	status.Running = s.Thread != nil && s.Thread.Running()
	status.MessageCount = len(s.messages())
	return status
}

func (s *Session) Subscribe() (<-chan protocol.Event, func()) {
	s.mu.Lock()
	s.ensureRuntime()
	hub := s.events
	s.mu.Unlock()
	return hub.Subscribe()
}

func (s *Session) abortActiveRun(reason string) bool {
	if s == nil || s.Thread == nil || !s.Thread.Running() {
		return false
	}
	if strings.TrimSpace(reason) == "" {
		reason = "gateway session aborted"
	}
	s.observeRunEvent(protocol.Event{Type: protocol.EventRunFailed, Data: reason})
	s.Thread.Cancel()
	return true
}

func (s *Session) beginRunStatus(req RunRequest) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.ensureRuntime()
	s.status.ID = s.ID
	s.status.Created = s.Created
	s.status.Running = true
	s.status.RunSeq++
	s.status.StartedAt = now
	s.status.FinishedAt = time.Time{}
	s.status.LastEvent = string(protocol.EventRunStarted)
	s.status.LastEventAt = now
	s.status.Model = req.Model
	s.status.Provider = req.Provider
	s.status.Profile = req.Profile
	s.status.ReasoningEffort = req.ReasoningEffort
	s.status.MessageCount = len(s.messages())
	s.status.ModelCalls = 0
	s.status.ToolCalls = 0
	s.status.LastError = ""
	status := s.status
	hub := s.events
	s.mu.Unlock()
	status.Running = s.Thread != nil && s.Thread.Running()
	event := protocol.Event{Type: protocol.EventSessionStatus, Data: status}
	event = s.recordEvent(event)
	hub.Publish(event)
}

func (s *Session) observeRunEvent(event protocol.Event) {
	if s == nil {
		return
	}
	if event.Type == protocol.EventRunStarted {
		s.publish(event)
		return
	}
	now := time.Now().UTC()
	var statusEvent *protocol.Event
	s.mu.Lock()
	s.ensureRuntime()
	s.status.LastEvent = string(event.Type)
	s.status.LastEventAt = now
	switch event.Type {
	case protocol.EventModelCallStarted:
		s.status.ModelCalls++
	case protocol.EventToolCallStarted:
		s.status.ToolCalls++
	case protocol.EventRunCompleted:
		s.status.Running = false
		s.status.FinishedAt = now
		s.status.MessageCount = len(s.messages())
		s.status.LastError = ""
		status := s.status
		statusEvent = &protocol.Event{Type: protocol.EventSessionStatus, Data: status}
	case protocol.EventRunFailed:
		s.status.Running = false
		s.status.FinishedAt = now
		s.status.MessageCount = len(s.messages())
		s.status.LastError = fmt.Sprint(event.Data)
		status := s.status
		statusEvent = &protocol.Event{Type: protocol.EventSessionStatus, Data: status}
	}
	hub := s.events
	s.mu.Unlock()
	event = s.recordEvent(event)
	hub.Publish(event)
	if statusEvent != nil {
		storedStatus := s.recordEvent(*statusEvent)
		hub.Publish(storedStatus)
	}
}

func (s *Session) publish(event protocol.Event) {
	s.mu.Lock()
	s.ensureRuntime()
	hub := s.events
	s.mu.Unlock()
	event = s.recordEvent(event)
	hub.Publish(event)
}

func (s *Session) recordEvent(event protocol.Event) protocol.Event {
	if s == nil || s.eventRecorder == nil {
		return event
	}
	return s.eventRecorder(event)
}
