package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestContextThresholdEventRendersContextBlock(t *testing.T) {
	m := newTestModel(t)
	m.applyEvent(protocol.Event{Type: protocol.EventContextThreshold, Data: protocol.ContextThresholdEvent{
		Percent:             70,
		ContextEpoch:        2,
		ThresholdKey:        "epoch:2/70",
		EstimatedTokens:     705000,
		ContextWindowTokens: 1000000,
		ThresholdTokens:     700000,
		RemainingTokens:     295000,
		MessageCount:        44,
		Round:               3,
		Stage:               "after_tool_results",
		Estimator:           "chars_div_4",
	}})
	if len(m.blocks) == 0 {
		t.Fatal("expected context threshold block")
	}
	block := m.blocks[len(m.blocks)-1]
	if block.Title != "CONTEXT" || block.EventType != protocol.EventContextThreshold {
		t.Fatalf("block = %#v", block)
	}
	for _, want := range []string{"threshold: 70%", "epoch: 2", "active: 705k / 1.0m", "remaining window: 295k", "stage: after_tool_results", "key: epoch:2/70"} {
		if !strings.Contains(block.Content, want) {
			t.Fatalf("context threshold block missing %q:\n%s", want, block.Content)
		}
	}
}

func TestRunStatusShowsSpinnerWorkingAndElapsedWhileBusy(t *testing.T) {
	m := newTestModel(t)
	m.width = 160
	m.busy = true
	m.status = "running tool shell"
	m.runStartedAt = time.Now().Add(-3 * time.Second)
	m.spinnerFrame = 3

	status := m.inlineStatusView()
	for _, notWant := range []string{"● 3s", "working", "running tool shell"} {
		if strings.Contains(status, notWant) {
			t.Fatalf("inline status %q should omit run strip text %q", status, notWant)
		}
	}
	for _, frame := range spinnerFrames {
		if strings.Contains(status, frame) {
			t.Fatalf("inline status %q should not show spinner frame %q", status, frame)
		}
	}

	runStatus := m.runStatusView()
	if strings.HasPrefix(stripANSITest(runStatus), " ") {
		t.Fatalf("run status %q should align without leading padding", runStatus)
	}
	if !strings.Contains(runStatus, "working") {
		t.Fatalf("run status %q should show working", runStatus)
	}
	if !strings.Contains(runStatus, "3s") {
		t.Fatalf("run status %q should show elapsed seconds", runStatus)
	}
	for _, notWant := range []string{"running tool shell", "agent"} {
		if strings.Contains(runStatus, notWant) {
			t.Fatalf("run status %q should omit noisy state %q", runStatus, notWant)
		}
	}
	foundSpinner := false
	for _, frame := range spinnerFrames {
		if strings.Contains(runStatus, frame) {
			foundSpinner = true
			break
		}
	}
	if !foundSpinner {
		t.Fatalf("run status %q should show spinner", runStatus)
	}
}

func TestResizeDoesNotReserveHiddenSlashPopup(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 30
	m.resize(false)
	noPopupHeight := m.viewport.Height()

	m.textarea.SetValue("/the")
	m.resize(false)
	withPopupHeight := m.viewport.Height()
	if noPopupHeight <= withPopupHeight {
		t.Fatalf("hidden popup should not reserve rows: noPopup=%d withPopup=%d", noPopupHeight, withPopupHeight)
	}
	if noPopupHeight-withPopupHeight > 8 {
		t.Fatalf("popup should reserve only its rendered height, delta=%d", noPopupHeight-withPopupHeight)
	}
}

func TestChatCommands(t *testing.T) {
	m := newTestModel(t)
	original := m.localChatID
	m.addBlock("user", "USER", "hello")

	handled, cmd := m.handleSlashCommand("/new")
	if !handled || cmd != nil {
		t.Fatalf("/new handled=%v cmd=%v, want handled without command", handled, cmd)
	}
	if m.localChatID == original {
		t.Fatalf("/new should create a new local chat id")
	}
	if len(m.blocks) != 0 {
		t.Fatalf("/new should clear rendered blocks")
	}

	handled, _ = m.handleSlashCommand("/resume")
	if !handled {
		t.Fatalf("/resume should be handled")
	}
	if len(m.blocks) == 0 || !strings.Contains(m.blocks[len(m.blocks)-1].Content, shortID(original)) {
		t.Fatalf("/resume should list saved chats")
	}
}
