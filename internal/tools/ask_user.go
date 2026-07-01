package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	AskUserToolName = "ask_user"

	maxAskUserQuestions     = 3
	minAskUserOptions       = 2
	maxAskUserOptions       = 4
	maxAskUserHeaderRunes   = 48
	maxAskUserQuestionRunes = 500
	maxAskUserLabelRunes    = 80
	maxAskUserDescRunes     = 240
	maxAskUserIDRunes       = 80
)

type AskUserInput struct {
	Header    string                 `json:"header,omitempty"`
	Questions []AskUserQuestionInput `json:"questions"`
}

type AskUserQuestionInput struct {
	ID            string               `json:"id,omitempty"`
	Header        string               `json:"header,omitempty"`
	Question      string               `json:"question"`
	Options       []AskUserOptionInput `json:"options"`
	AllowFreeform bool                 `json:"allow_freeform,omitempty"`
}

type AskUserOptionInput struct {
	ID          string `json:"id,omitempty"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

func (r *Registry) addAskUser() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        AskUserToolName,
			Description: "Ask the user one to three bounded clarification questions and wait for their answer before continuing.",
			Parameters:  raw(`{"type":"object","properties":{"header":{"type":"string","description":"Optional short header for the request."},"questions":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"object","properties":{"id":{"type":"string"},"header":{"type":"string"},"question":{"type":"string"},"options":{"type":"array","minItems":2,"maxItems":4,"items":{"type":"object","properties":{"id":{"type":"string"},"label":{"type":"string"},"description":{"type":"string"}},"required":["label","description"],"additionalProperties":false}},"allow_freeform":{"type":"boolean","description":"Whether a freeform answer is acceptable in addition to the listed options."}},"required":["question","options"],"additionalProperties":false}}},"required":["questions"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Parallel: ParallelMetadata{Policy: ParallelPolicyExclusiveWorkspace, RequiresExclusiveWorkspace: true, Cancellable: true, MaxConcurrency: 1},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			if _, err := ParseAskUserQuestions(args); err != nil {
				return Result{}, err
			}
			err := fmt.Errorf("ask_user is only available during a gateway session run")
			return errorResult("ask_user_unavailable", err.Error()), err
		},
	})
}

func ParseAskUserQuestions(args json.RawMessage) ([]protocol.UserInputQuestion, error) {
	var in AskUserInput
	if err := json.Unmarshal(normalizeArgs(args), &in); err != nil {
		return nil, err
	}
	if len(in.Questions) == 0 {
		return nil, fmt.Errorf("ask_user requires at least one question")
	}
	if len(in.Questions) > maxAskUserQuestions {
		return nil, fmt.Errorf("ask_user supports at most %d questions", maxAskUserQuestions)
	}
	topHeader, err := cleanAskUserText(in.Header, maxAskUserHeaderRunes, "header", false)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.UserInputQuestion, 0, len(in.Questions))
	for i, question := range in.Questions {
		clean, err := normalizeAskUserQuestion(i, topHeader, question)
		if err != nil {
			return nil, err
		}
		out = append(out, clean)
	}
	return out, nil
}

func normalizeAskUserQuestion(index int, topHeader string, in AskUserQuestionInput) (protocol.UserInputQuestion, error) {
	id, err := cleanAskUserText(in.ID, maxAskUserIDRunes, "question id", false)
	if err != nil {
		return protocol.UserInputQuestion{}, err
	}
	if id == "" {
		id = fmt.Sprintf("q%d", index+1)
	}
	header, err := cleanAskUserText(in.Header, maxAskUserHeaderRunes, "question header", false)
	if err != nil {
		return protocol.UserInputQuestion{}, err
	}
	if header == "" {
		header = topHeader
	}
	text, err := cleanAskUserText(in.Question, maxAskUserQuestionRunes, "question", true)
	if err != nil {
		return protocol.UserInputQuestion{}, err
	}
	if len(in.Options) < minAskUserOptions {
		return protocol.UserInputQuestion{}, fmt.Errorf("question %q requires at least %d options", id, minAskUserOptions)
	}
	if len(in.Options) > maxAskUserOptions {
		return protocol.UserInputQuestion{}, fmt.Errorf("question %q supports at most %d options", id, maxAskUserOptions)
	}
	options := make([]protocol.UserInputOption, 0, len(in.Options))
	seen := map[string]struct{}{}
	for i, option := range in.Options {
		clean, err := normalizeAskUserOption(id, i, option)
		if err != nil {
			return protocol.UserInputQuestion{}, err
		}
		key := strings.ToLower(clean.ID)
		if _, exists := seen[key]; exists {
			return protocol.UserInputQuestion{}, fmt.Errorf("question %q has duplicate option id %q", id, clean.ID)
		}
		seen[key] = struct{}{}
		options = append(options, clean)
	}
	return protocol.UserInputQuestion{
		ID:            id,
		Header:        header,
		Question:      text,
		Options:       options,
		AllowFreeform: in.AllowFreeform,
	}, nil
}

func normalizeAskUserOption(questionID string, index int, in AskUserOptionInput) (protocol.UserInputOption, error) {
	id, err := cleanAskUserText(in.ID, maxAskUserIDRunes, "option id", false)
	if err != nil {
		return protocol.UserInputOption{}, err
	}
	if id == "" {
		id = fmt.Sprintf("%s_o%d", questionID, index+1)
	}
	label, err := cleanAskUserText(in.Label, maxAskUserLabelRunes, "option label", true)
	if err != nil {
		return protocol.UserInputOption{}, err
	}
	description, err := cleanAskUserText(in.Description, maxAskUserDescRunes, "option description", true)
	if err != nil {
		return protocol.UserInputOption{}, err
	}
	return protocol.UserInputOption{ID: id, Label: label, Description: description}, nil
}

func cleanAskUserText(value string, maxRunes int, field string, required bool) (string, error) {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		if required {
			return "", fmt.Errorf("%s is required", field)
		}
		return "", nil
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must be valid UTF-8", field)
	}
	if len([]rune(value)) > maxRunes {
		return "", fmt.Errorf("%s exceeds %d characters", field, maxRunes)
	}
	return value, nil
}
