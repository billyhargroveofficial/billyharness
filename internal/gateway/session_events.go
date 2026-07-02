package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
)

type SessionStatus = gatewayapi.SessionStatus

type eventHub struct {
	mu          sync.Mutex
	subscribers map[chan protocol.Event]struct{}
	dropped     int64
}

const eventHubSubscriberBuffer = 256

func newGatewaySession(id string, created time.Time, messages []protocol.Message) *Session {
	return newGatewaySessionWithOwner(id, created, messages, gatewayapi.SessionOwner{})
}

func newGatewaySessionWithOwner(id string, created time.Time, messages []protocol.Message, owner gatewayapi.SessionOwner) *Session {
	if created.IsZero() {
		created = time.Now().UTC()
	}
	session := &Session{
		ID:      id,
		Created: created,
		Owner:   normalizeSessionOwner(owner),
		Thread:  sessionpkg.New(messages),
		events:  newEventHub(),
	}
	session.status = SessionStatus{
		ID:               id,
		Created:          created,
		Owner:            session.Owner,
		MessageCount:     len(messages),
		AttachmentCount:  protocol.MessageAttachmentCount(messages),
		ImageSubmissions: protocol.MessageImageSubmissionCount(messages),
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
	ch := make(chan protocol.Event, eventHubSubscriberBuffer)
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
	var dropped int64
	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
			dropped++
		}
	}
	if dropped > 0 {
		h.mu.Lock()
		h.dropped += dropped
		h.mu.Unlock()
	}
}

func (h *eventHub) Dropped() int64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dropped
}

func (s *Session) ensureRuntime() {
	if s.events == nil {
		s.events = newEventHub()
	}
	if s.terminalRunIDs == nil {
		s.terminalRunIDs = map[string]struct{}{}
	}
	if s.status.ID == "" {
		s.status = SessionStatus{
			ID:               s.ID,
			Created:          s.Created,
			Owner:            s.Owner,
			Running:          s.Thread != nil && s.Thread.Running(),
			MessageCount:     len(s.messages()),
			AttachmentCount:  protocol.MessageAttachmentCount(s.messages()),
			ImageSubmissions: protocol.MessageImageSubmissionCount(s.messages()),
		}
	}
	if s.status.Owner == (gatewayapi.SessionOwner{}) {
		s.status.Owner = s.Owner
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
	hub := s.events
	s.mu.Unlock()
	status.Running = s.Thread != nil && s.Thread.Running()
	messages := s.messages()
	status.MessageCount = len(messages)
	status.AttachmentCount = protocol.MessageAttachmentCount(messages)
	status.ImageSubmissions = protocol.MessageImageSubmissionCount(messages)
	status.DroppedEvents = hub.Dropped()
	status.Owner = s.Owner
	return status
}

func (s *Session) restoreStatus(status SessionStatus) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.ensureRuntime()
	if status.ID == "" {
		status.ID = s.ID
	}
	if status.Created.IsZero() {
		status.Created = s.Created
	}
	if status.Owner == (gatewayapi.SessionOwner{}) {
		status.Owner = s.Owner
	}
	status.Running = s.Thread != nil && s.Thread.Running()
	messages := s.messages()
	status.MessageCount = len(messages)
	status.AttachmentCount = protocol.MessageAttachmentCount(messages)
	status.ImageSubmissions = protocol.MessageImageSubmissionCount(messages)
	s.status = status
	s.mu.Unlock()
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
	if runID := s.activeRunIDSnapshot(); runID != "" {
		s.observeRunEvent(protocol.Event{Type: protocol.EventRunFailed, RunID: runID, Data: reason})
	}
	s.Thread.Cancel()
	return true
}

func (s *Session) interruptActiveRunAndWait(ctx context.Context, reason string) (bool, error) {
	if s == nil || s.Thread == nil || !s.Thread.Running() {
		return false, nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "gateway session interrupted"
	}
	if runID := s.activeRunIDSnapshot(); runID != "" {
		s.observeRunEvent(protocol.Event{Type: protocol.EventRunFailed, RunID: runID, Data: reason})
	}
	return s.Thread.CancelAndWait(ctx)
}

func (s *Session) activeRunIDSnapshot() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureRuntime()
	return s.activeRunID
}

func (s *Session) beginRunStatus(req RunRequest) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.ensureRuntime()
	s.status.ID = s.ID
	s.status.Created = s.Created
	s.status.Owner = s.Owner
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
	s.status.AccessMode = config.NormalizeAccessMode(req.AccessMode)
	messages := s.messages()
	s.status.MessageCount = len(messages)
	s.status.AttachmentCount = protocol.MessageAttachmentCount(messages)
	s.status.ImageSubmissions = protocol.MessageImageSubmissionCount(messages)
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

func (s *Session) observeRunEvent(event protocol.Event) (protocol.Event, bool) {
	if s == nil {
		return event, true
	}
	if event.Type == protocol.EventRunStarted {
		s.mu.Lock()
		s.ensureRuntime()
		if strings.TrimSpace(event.RunID) != "" {
			s.activeRunID = event.RunID
		}
		s.mu.Unlock()
		return s.publish(event), true
	}
	now := time.Now().UTC()
	var statusEvent *protocol.Event
	terminalDuplicate := false
	s.mu.Lock()
	s.ensureRuntime()
	if isTerminalRunEvent(event.Type) {
		if strings.TrimSpace(event.RunID) == "" && strings.TrimSpace(s.activeRunID) != "" {
			event.RunID = s.activeRunID
		}
		s.pendingInput = nil
		runID := strings.TrimSpace(event.RunID)
		if runID != "" {
			if _, seen := s.terminalRunIDs[runID]; seen {
				terminalDuplicate = true
			} else {
				s.terminalRunIDs[runID] = struct{}{}
				if s.activeRunID == runID {
					s.activeRunID = ""
				}
			}
		}
	}
	if terminalDuplicate {
		s.mu.Unlock()
		return event, false
	}
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
		messages := s.messages()
		s.status.MessageCount = len(messages)
		s.status.AttachmentCount = protocol.MessageAttachmentCount(messages)
		s.status.ImageSubmissions = protocol.MessageImageSubmissionCount(messages)
		s.status.LastError = ""
		status := s.status
		statusEvent = &protocol.Event{Type: protocol.EventSessionStatus, Data: status}
	case protocol.EventRunFailed:
		s.status.Running = false
		s.status.FinishedAt = now
		messages := s.messages()
		s.status.MessageCount = len(messages)
		s.status.AttachmentCount = protocol.MessageAttachmentCount(messages)
		s.status.ImageSubmissions = protocol.MessageImageSubmissionCount(messages)
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
	return event, true
}

func (s *Session) publish(event protocol.Event) protocol.Event {
	if s == nil {
		return event
	}
	s.mu.Lock()
	s.ensureRuntime()
	hub := s.events
	s.mu.Unlock()
	event = s.recordEvent(event)
	hub.Publish(event)
	return event
}

func (s *Session) recordEvent(event protocol.Event) protocol.Event {
	if s == nil || s.eventRecorder == nil {
		return event
	}
	return s.eventRecorder(event)
}
