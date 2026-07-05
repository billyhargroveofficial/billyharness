package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestResolveChatSessionFindsMessageBodyText(t *testing.T) {
	dir := t.TempDir()
	sessions := []chatSession{
		{
			ID:        "20260705-120001-aaaaaa",
			Title:     "ordinary setup",
			UpdatedAt: time.Now().Add(-2 * time.Hour),
			Messages:  []protocol.Message{{Role: protocol.RoleAssistant, Content: "plain note"}},
		},
		{
			ID:        "20260705-120002-bbbbbb",
			Title:     "debugging note",
			UpdatedAt: time.Now(),
			Messages: []protocol.Message{{
				Role:    protocol.RoleTool,
				Content: "The gateway auth token is read from the environment.",
			}},
		},
		{
			ID:        "20260705-120003-cccccc",
			Title:     "other topic",
			UpdatedAt: time.Now().Add(-time.Hour),
			Messages:  []protocol.Message{{Role: protocol.RoleUser, Parts: []protocol.MessagePart{protocol.TextPart("vision attachment")}}},
		},
	}
	for _, session := range sessions {
		if err := saveChatSession(dir, session); err != nil {
			t.Fatal(err)
		}
	}

	session, matches, err := resolveChatSession(dir, "gateway auth")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 || session.ID != "20260705-120002-bbbbbb" {
		t.Fatalf("resolved session=%#v matches=%#v", session, matches)
	}
}

func TestSearchChatSessionsReturnsMultipleSnippets(t *testing.T) {
	dir := t.TempDir()
	fixtures := []chatSession{
		{
			ID:        "20260705-120001-aaaaaa",
			Title:     "gateway auth token notes",
			UpdatedAt: time.Now(),
			Messages:  []protocol.Message{{Role: protocol.RoleAssistant, Content: "title match"}},
		},
		{
			ID:        "20260705-120002-bbbbbb",
			Title:     "body-only",
			UpdatedAt: time.Now().Add(-time.Minute),
			Messages:  []protocol.Message{{Role: protocol.RoleTool, Parts: []protocol.MessagePart{protocol.TextPart("tool result mentions gateway auth token rotation")}}},
		},
		{
			ID:        "20260705-120003-cccccc",
			Title:     "unrelated",
			UpdatedAt: time.Now().Add(-2 * time.Minute),
			Messages:  []protocol.Message{{Role: protocol.RoleUser, Content: "nothing matching"}},
		},
	}
	for _, session := range fixtures {
		if err := saveChatSession(dir, session); err != nil {
			t.Fatal(err)
		}
	}

	matches, err := searchChatSessions(dir, "gateway auth")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %#v", matches)
	}
	for _, match := range matches {
		if strings.TrimSpace(match.snippet) == "" || !strings.Contains(strings.ToLower(match.snippet), "gateway auth") {
			t.Fatalf("bad snippet for %s: %q", match.session.ID, match.snippet)
		}
	}
	if formatted := formatSessionMatches(matches); !strings.Contains(formatted, "matched:") || !strings.Contains(formatted, shortID(matches[0].session.ID)) {
		t.Fatalf("formatted matches = %q", formatted)
	}
}
