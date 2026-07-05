package telegrambot

import (
	"reflect"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
)

func TestCommandDocsProjectTelegramCommands(t *testing.T) {
	commands := telegramCommands()
	docs := CommandDocs()
	if len(docs) != len(commands) {
		t.Fatalf("CommandDocs length = %d, want %d", len(docs), len(commands))
	}
	for i, command := range commands {
		doc := docs[i]
		if doc.ActionID != command.actionID ||
			doc.Usage != command.usage ||
			doc.Summary != command.summary ||
			doc.Class != command.class.String() ||
			doc.BypassRunLock != command.bypassRunLock {
			t.Fatalf("CommandDocs[%d] = %#v, want projection of %#v", i, doc, command)
		}
		if !reflect.DeepEqual(doc.Aliases, command.aliases) {
			t.Fatalf("CommandDocs[%d] aliases = %#v, want %#v", i, doc.Aliases, command.aliases)
		}
		if doc.Class == "" || strings.HasPrefix(doc.Class, "unknown") {
			t.Fatalf("CommandDocs[%d] has invalid class %q", i, doc.Class)
		}
	}
}

func TestCommandDocsCoverClientUXTelegramAliases(t *testing.T) {
	byAction := map[string]CommandDoc{}
	for _, doc := range CommandDocs() {
		byAction[doc.ActionID] = doc
	}
	for _, def := range clientux.ActionDefinitions() {
		if len(def.TelegramAliases) == 0 {
			continue
		}
		doc, ok := byAction[def.ID]
		if !ok {
			t.Fatalf("telegram aliases for action %q are not consumed by CommandDocs", def.ID)
		}
		if !reflect.DeepEqual(doc.Aliases, def.TelegramAliases) {
			t.Fatalf("CommandDocs aliases for action %q = %#v, want %#v", def.ID, doc.Aliases, def.TelegramAliases)
		}
	}
}

func TestCommandDocsReturnDefensiveCopies(t *testing.T) {
	docs := CommandDocs()
	if len(docs) == 0 || len(docs[0].Aliases) == 0 {
		t.Fatal("test fixture expected at least one telegram alias")
	}
	want := docs[0].Aliases[0]
	docs[0].Aliases[0] = "mutated"
	if got := CommandDocs()[0].Aliases[0]; got != want {
		t.Fatalf("CommandDocs leaked aliases slice: got %q, want %q", got, want)
	}
}
