package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

var (
	errNoPendingUserInput      = errors.New("no pending user input request")
	errUserInputRequestUnknown = errors.New("pending user input request not found")
)

type pendingUserInput struct {
	request protocol.UserInputRequestEvent
	reply   chan userInputResolution
}

type userInputResolution struct {
	answer protocol.UserInputAnswerEvent
	reject protocol.UserInputRejectEvent
	err    error
}

func (s *Session) askUser(ctx context.Context, request protocol.UserInputRequestEvent, emit func(protocol.Event)) (protocol.UserInputAnswerEvent, error) {
	if s == nil {
		return protocol.UserInputAnswerEvent{}, fmt.Errorf("gateway session unavailable")
	}
	if emit == nil {
		emit = func(protocol.Event) {}
	}
	request = cloneUserInputRequest(request)
	request.SessionID = s.ID
	if strings.TrimSpace(request.RequestID) == "" {
		request.RequestID = strings.TrimSpace(request.CallID)
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return protocol.UserInputAnswerEvent{}, fmt.Errorf("user input request id required")
	}
	pending := &pendingUserInput{
		request: request,
		reply:   make(chan userInputResolution, 1),
	}
	s.mu.Lock()
	s.ensureRuntime()
	if s.pendingInput != nil {
		s.mu.Unlock()
		reject := rejectEventFromRequest(request, "another user input request is already pending", "gateway")
		emit(protocol.Event{Type: protocol.EventUserInputRejected, RunID: reject.RunID, TurnID: reject.TurnID, StepID: reject.StepID, CallID: reject.CallID, AttemptID: reject.AttemptID, Data: reject})
		return protocol.UserInputAnswerEvent{}, fmt.Errorf("another user input request is already pending")
	}
	s.pendingInput = pending
	s.mu.Unlock()

	emit(protocol.Event{Type: protocol.EventUserInputRequested, RunID: request.RunID, TurnID: request.TurnID, StepID: request.StepID, CallID: request.CallID, AttemptID: request.AttemptID, Data: request})
	select {
	case resolution := <-pending.reply:
		if resolution.err != nil {
			reject := resolution.reject
			if reject.RequestID == "" {
				reject = rejectEventFromRequest(request, resolution.err.Error(), "gateway")
			}
			emit(protocol.Event{Type: protocol.EventUserInputRejected, RunID: reject.RunID, TurnID: reject.TurnID, StepID: reject.StepID, CallID: reject.CallID, AttemptID: reject.AttemptID, Data: reject})
			return protocol.UserInputAnswerEvent{}, resolution.err
		}
		answer := resolution.answer
		answer.Status = "answered"
		emit(protocol.Event{Type: protocol.EventUserInputAnswered, RunID: answer.RunID, TurnID: answer.TurnID, StepID: answer.StepID, CallID: answer.CallID, AttemptID: answer.AttemptID, Data: answer})
		return answer, nil
	case <-ctx.Done():
		s.clearPendingUserInput(request.RequestID)
		reject := rejectEventFromRequest(request, ctx.Err().Error(), "gateway")
		emit(protocol.Event{Type: protocol.EventUserInputRejected, RunID: reject.RunID, TurnID: reject.TurnID, StepID: reject.StepID, CallID: reject.CallID, AttemptID: reject.AttemptID, Data: reject})
		return protocol.UserInputAnswerEvent{}, ctx.Err()
	}
}

func (s *Session) answerUserInput(requestID string, req gatewayapi.UserInputAnswerRequest) (protocol.UserInputAnswerEvent, error) {
	pending, err := s.takePendingUserInput(requestID)
	if err != nil {
		return protocol.UserInputAnswerEvent{}, err
	}
	answer, err := answerEventFromRequest(pending.request, req)
	if err != nil {
		s.restorePendingUserInput(pending)
		return protocol.UserInputAnswerEvent{}, err
	}
	pending.reply <- userInputResolution{answer: answer}
	return answer, nil
}

func (s *Session) rejectUserInput(requestID string, req gatewayapi.UserInputRejectRequest) (protocol.UserInputRejectEvent, error) {
	pending, err := s.takePendingUserInput(requestID)
	if err != nil {
		return protocol.UserInputRejectEvent{}, err
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "gateway"
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "rejected by user"
	}
	reject := rejectEventFromRequest(pending.request, reason, source)
	pending.reply <- userInputResolution{reject: reject, err: errors.New(reason)}
	return reject, nil
}

func (s *Session) clearPendingUserInput(requestID string) {
	if s == nil {
		return
	}
	requestID = strings.TrimSpace(requestID)
	s.mu.Lock()
	if s.pendingInput != nil && (requestID == "" || s.pendingInput.request.RequestID == requestID) {
		s.pendingInput = nil
	}
	s.mu.Unlock()
}

func (s *Session) takePendingUserInput(requestID string) (*pendingUserInput, error) {
	if s == nil {
		return nil, errNoPendingUserInput
	}
	requestID = strings.TrimSpace(requestID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingInput == nil {
		return nil, errNoPendingUserInput
	}
	if requestID == "" || s.pendingInput.request.RequestID != requestID {
		return nil, errUserInputRequestUnknown
	}
	pending := s.pendingInput
	s.pendingInput = nil
	return pending, nil
}

func (s *Session) restorePendingUserInput(pending *pendingUserInput) {
	if s == nil || pending == nil {
		return
	}
	s.mu.Lock()
	if s.pendingInput == nil {
		s.pendingInput = pending
	}
	s.mu.Unlock()
}

func answerEventFromRequest(request protocol.UserInputRequestEvent, req gatewayapi.UserInputAnswerRequest) (protocol.UserInputAnswerEvent, error) {
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "gateway"
	}
	answers, err := normalizeUserInputAnswers(request.Questions, req)
	if err != nil {
		return protocol.UserInputAnswerEvent{}, err
	}
	return protocol.UserInputAnswerEvent{
		RequestID: request.RequestID,
		SessionID: request.SessionID,
		RunID:     request.RunID,
		TurnID:    request.TurnID,
		StepID:    request.StepID,
		CallID:    request.CallID,
		AttemptID: request.AttemptID,
		Source:    source,
		Answers:   answers,
		Status:    "answered",
	}, nil
}

func rejectEventFromRequest(request protocol.UserInputRequestEvent, reason, source string) protocol.UserInputRejectEvent {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "gateway"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "rejected"
	}
	return protocol.UserInputRejectEvent{
		RequestID: request.RequestID,
		SessionID: request.SessionID,
		RunID:     request.RunID,
		TurnID:    request.TurnID,
		StepID:    request.StepID,
		CallID:    request.CallID,
		AttemptID: request.AttemptID,
		Source:    source,
		Reason:    reason,
		Status:    "rejected",
	}
}

func normalizeUserInputAnswers(questions []protocol.UserInputQuestion, req gatewayapi.UserInputAnswerRequest) ([]protocol.UserInputAnswer, error) {
	answers := protocol.CloneUserInputAnswers(req.Answers)
	if len(answers) == 0 && strings.TrimSpace(req.Text) != "" {
		questionID := ""
		if len(questions) > 0 {
			questionID = questions[0].ID
		}
		answers = []protocol.UserInputAnswer{{QuestionID: questionID, Text: strings.TrimSpace(req.Text)}}
	}
	if len(answers) == 0 {
		return nil, fmt.Errorf("answer required")
	}
	if len(questions) > 0 && len(answers) > len(questions) {
		return nil, fmt.Errorf("too many answers for request")
	}
	out := make([]protocol.UserInputAnswer, 0, len(answers))
	for i, answer := range answers {
		if strings.TrimSpace(answer.QuestionID) == "" && i < len(questions) {
			answer.QuestionID = questions[i].ID
		}
		question, ok := findUserInputQuestion(questions, answer.QuestionID)
		answer.QuestionID = strings.TrimSpace(answer.QuestionID)
		answer.OptionID = strings.TrimSpace(answer.OptionID)
		answer.OptionLabel = strings.TrimSpace(answer.OptionLabel)
		answer.Text = strings.TrimSpace(answer.Text)
		if ok {
			answer = normalizeUserInputOptionAnswer(question, answer)
		}
		if answer.OptionID == "" && answer.OptionLabel == "" && answer.Text == "" {
			return nil, fmt.Errorf("answer %d is empty", i+1)
		}
		if len([]rune(answer.Text)) > 2000 {
			return nil, fmt.Errorf("answer %d exceeds 2000 characters", i+1)
		}
		out = append(out, answer)
	}
	return out, nil
}

func findUserInputQuestion(questions []protocol.UserInputQuestion, id string) (protocol.UserInputQuestion, bool) {
	id = strings.TrimSpace(id)
	for _, question := range questions {
		if question.ID == id {
			return question, true
		}
	}
	return protocol.UserInputQuestion{}, false
}

func normalizeUserInputOptionAnswer(question protocol.UserInputQuestion, answer protocol.UserInputAnswer) protocol.UserInputAnswer {
	if answer.OptionID != "" {
		for _, option := range question.Options {
			if strings.EqualFold(option.ID, answer.OptionID) {
				answer.OptionID = option.ID
				answer.OptionLabel = option.Label
				return answer
			}
		}
	}
	text := strings.TrimSpace(answer.Text)
	if text == "" || answer.OptionID != "" {
		return answer
	}
	for _, option := range question.Options {
		if strings.EqualFold(text, option.ID) || strings.EqualFold(text, option.Label) {
			answer.OptionID = option.ID
			answer.OptionLabel = option.Label
			answer.Text = ""
			return answer
		}
	}
	return answer
}

func cloneUserInputRequest(request protocol.UserInputRequestEvent) protocol.UserInputRequestEvent {
	request.Questions = protocol.CloneUserInputQuestions(request.Questions)
	return request
}
