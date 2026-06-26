package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

func TestDefaultsToLightTheme(t *testing.T) {
	m := NewModel(config.Default(), Options{})
	if !m.textarea.Focused() {
		t.Fatalf("textarea should start focused")
	}
	if m.theme != "light" {
		t.Fatalf("theme = %q, want light", m.theme)
	}
	if got := m.currentModel(); got != "deepseek-v4-flash" {
		t.Fatalf("model = %q, want deepseek-v4-flash", got)
	}
	if got := m.currentThinking().effort; got != "high" {
		t.Fatalf("reasoning effort = %q, want high", got)
	}
}

func TestSlashCommands(t *testing.T) {
	m := NewModel(config.Default(), Options{})

	for _, tc := range []struct {
		input     string
		wantModel string
		wantThink string
		wantTheme string
	}{
		{input: "/theme dark", wantTheme: "dark"},
		{input: "/theme light", wantTheme: "light"},
		{input: "/model pro", wantModel: "deepseek-v4-pro"},
		{input: "/model flash", wantModel: "deepseek-v4-flash"},
		{input: "/reasoning max", wantThink: "max"},
		{input: "/reasoning off", wantThink: ""},
		{input: "/reasoning high", wantThink: "high"},
		{input: "/thinking off"},
		{input: "/thinking on"},
	} {
		if !m.handleSlashCommand(tc.input) {
			t.Fatalf("handleSlashCommand(%q) returned false", tc.input)
		}
		if tc.wantModel != "" && m.currentModel() != tc.wantModel {
			t.Fatalf("%q model = %q, want %q", tc.input, m.currentModel(), tc.wantModel)
		}
		if tc.wantTheme != "" && m.theme != tc.wantTheme {
			t.Fatalf("%q theme = %q, want %q", tc.input, m.theme, tc.wantTheme)
		}
		if strings.HasPrefix(tc.input, "/reasoning") && (tc.wantThink != "" || strings.Contains(tc.input, " off")) {
			if got := m.currentThinking().effort; got != tc.wantThink {
				t.Fatalf("%q reasoning = %q, want %q", tc.input, got, tc.wantThink)
			}
		}
	}
}

func TestHiddenReasoningIsPreserved(t *testing.T) {
	m := NewModel(config.Default(), Options{})
	m.width = 80
	m.height = 24

	if !m.handleSlashCommand("/thinking off") {
		t.Fatalf("/thinking off returned false")
	}
	m.appendToOpenBlock("reasoning", "THINKING", "hidden reasoning")
	if len(m.blocks) != 1 {
		t.Fatalf("reasoning block was not preserved")
	}
	m.resize(false)
	if strings.Contains(m.viewport.View(), "hidden reasoning") {
		t.Fatalf("hidden reasoning should not render")
	}

	if !m.handleSlashCommand("/thinking on") {
		t.Fatalf("/thinking on returned false")
	}
	m.reflow(false)
	if !strings.Contains(m.viewport.View(), "hidden reasoning") {
		t.Fatalf("reasoning should render again after /thinking on")
	}
}

func TestAltEnterInsertsNewline(t *testing.T) {
	m := NewModel(config.Default(), Options{})
	m.textarea.SetValue("first")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "first\n" {
		t.Fatalf("textarea value = %q, want first newline", got)
	}
}

func TestPrintableKeysReachTextarea(t *testing.T) {
	m := NewModel(config.Default(), Options{})

	next, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	updated := next.(Model)
	if got := updated.textarea.Value(); got != "/" {
		t.Fatalf("textarea value = %q, want /", got)
	}
}

func TestMouseScrollDisablesFollowOutput(t *testing.T) {
	m := NewModel(config.Default(), Options{})
	m.width = 80
	m.height = 24
	m.addBlock("assistant", "ASSISTANT", strings.Repeat("line\n", 80))
	m.resize(true)
	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should start at bottom")
	}

	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	updated := next.(Model)
	if updated.followOutput {
		t.Fatalf("mouse wheel up should disable followOutput")
	}
	if updated.viewport.AtBottom() {
		t.Fatalf("mouse wheel up should scroll away from bottom")
	}

	next, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModAlt})
	updated = next.(Model)
	if !updated.followOutput {
		t.Fatalf("end key should restore followOutput")
	}
	if !updated.viewport.AtBottom() {
		t.Fatalf("end key should move to bottom")
	}
}
