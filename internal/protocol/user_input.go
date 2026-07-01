package protocol

import "encoding/json"

func DecodeUserInputRequest(value any) (UserInputRequestEvent, bool) {
	var out UserInputRequestEvent
	if decodeUserInputPayload(value, &out) != nil || out.RequestID == "" {
		return UserInputRequestEvent{}, false
	}
	out.Questions = CloneUserInputQuestions(out.Questions)
	return out, true
}

func DecodeUserInputAnswer(value any) (UserInputAnswerEvent, bool) {
	var out UserInputAnswerEvent
	if decodeUserInputPayload(value, &out) != nil || out.RequestID == "" {
		return UserInputAnswerEvent{}, false
	}
	out.Answers = CloneUserInputAnswers(out.Answers)
	return out, true
}

func DecodeUserInputReject(value any) (UserInputRejectEvent, bool) {
	var out UserInputRejectEvent
	if decodeUserInputPayload(value, &out) != nil || out.RequestID == "" {
		return UserInputRejectEvent{}, false
	}
	return out, true
}

func CloneUserInputQuestions(in []UserInputQuestion) []UserInputQuestion {
	if len(in) == 0 {
		return nil
	}
	out := make([]UserInputQuestion, len(in))
	for i, question := range in {
		out[i] = question
		out[i].Options = append([]UserInputOption(nil), question.Options...)
	}
	return out
}

func CloneUserInputAnswers(in []UserInputAnswer) []UserInputAnswer {
	if len(in) == 0 {
		return nil
	}
	return append([]UserInputAnswer(nil), in...)
}

func decodeUserInputPayload(value any, out any) error {
	switch data := value.(type) {
	case nil:
		return json.Unmarshal(nil, out)
	case json.RawMessage:
		return json.Unmarshal(data, out)
	case []byte:
		return json.Unmarshal(data, out)
	default:
		body, err := json.Marshal(data)
		if err != nil {
			return err
		}
		return json.Unmarshal(body, out)
	}
}
