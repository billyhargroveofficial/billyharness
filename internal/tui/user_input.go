package tui

import (
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func formatUserInputRequest(req protocol.UserInputRequestEvent) string {
	var lines []string
	for i, question := range req.Questions {
		if question.Header != "" && (i == 0 || question.Header != req.Questions[i-1].Header) {
			lines = append(lines, question.Header)
		}
		lines = append(lines, question.Question)
		for _, option := range question.Options {
			line := "- " + option.Label
			if option.Description != "" {
				line += ": " + option.Description
			}
			lines = append(lines, line)
		}
		if question.AllowFreeform {
			lines = append(lines, "- Freeform answer accepted.")
		}
		if i != len(req.Questions)-1 {
			lines = append(lines, "")
		}
	}
	if len(lines) == 0 {
		return "The agent is asking for input."
	}
	return strings.Join(lines, "\n")
}
