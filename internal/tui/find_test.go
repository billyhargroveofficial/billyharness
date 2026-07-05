package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func TestFindTranscriptMultiMatchNavigation(t *testing.T) {
	m := newTestModel(t)
	m.width = 96
	m.height = 32
	m.blocks = []transcript.Cell{
		{Kind: "user", CellType: transcript.CellTypeUser, Content: "Gateway auth token"},
		{Kind: "assistant", CellType: transcript.CellTypeAssistantFinal, Content: "gateway session replay"},
	}
	m.reflow(true)

	action, ok := actionForSlash("/find")
	if !ok {
		t.Fatal("/find action not registered")
	}
	handled, cmd := action.run(&m, "Gateway")
	if !handled || cmd != nil {
		t.Fatalf("find handled=%t cmd=%v", handled, cmd)
	}
	if m.findQuery != "Gateway" || len(m.findMatches) != 2 {
		t.Fatalf("find query=%q matches=%d", m.findQuery, len(m.findMatches))
	}
	if !strings.Contains(m.status, "match 1/2") {
		t.Fatalf("status after find = %q", m.status)
	}

	result := action.keyRun(&m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !result.skipTextareaUpdate || !result.skipViewportUpdate {
		t.Fatalf("ctrl+f result = %#v", result)
	}
	if !strings.Contains(m.status, "match 2/2") {
		t.Fatalf("status after next = %q", m.status)
	}

	action.keyRun(&m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt})
	if !strings.Contains(m.status, "match 1/2") {
		t.Fatalf("status after previous = %q", m.status)
	}
}

func TestFindTranscriptNoMatchStatus(t *testing.T) {
	m := newTestModel(t)
	m.width = 96
	m.height = 32
	m.blocks = []transcript.Cell{{Kind: "assistant", CellType: transcript.CellTypeAssistantFinal, Content: "answer only"}}
	m.reflow(true)

	action, ok := actionForSlash("/find")
	if !ok {
		t.Fatal("/find action not registered")
	}
	handled, _ := action.run(&m, "missing")
	if handled {
		t.Fatal("no-match find should not report handled")
	}
	if len(m.findMatches) != 0 || !strings.Contains(m.status, "no matches for 'missing'") {
		t.Fatalf("matches=%d status=%q", len(m.findMatches), m.status)
	}
}

func TestFindTranscriptReflowKeepsHighlights(t *testing.T) {
	m := newTestModel(t)
	m.width = 96
	m.height = 32
	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: "Gateway first"})
	m.reflow(true)

	action, ok := actionForSlash("/find")
	if !ok {
		t.Fatal("/find action not registered")
	}
	if handled, _ := action.run(&m, "gateway"); !handled {
		t.Fatalf("find failed status=%q", m.status)
	}
	if len(m.findMatches) != 1 {
		t.Fatalf("initial matches = %d", len(m.findMatches))
	}

	m.applyEvent(protocol.Event{Type: protocol.EventAssistantDelta, Data: " and gateway second"})
	m.reflow(false)
	if m.findQuery != "gateway" || len(m.findMatches) != 2 {
		t.Fatalf("after reflow query=%q matches=%d content=%q", m.findQuery, len(m.findMatches), m.viewportContent)
	}
}
