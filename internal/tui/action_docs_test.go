package tui

import (
	"reflect"
	"testing"
)

func TestActionDocsProjectActionRegistry(t *testing.T) {
	actions := actionRegistry()
	docs := ActionDocs()
	if len(docs) != len(actions) {
		t.Fatalf("ActionDocs length = %d, want %d", len(docs), len(actions))
	}
	for i, action := range actions {
		doc := docs[i]
		if doc.ID != action.id ||
			doc.Title != action.title ||
			doc.Category != action.category ||
			doc.Keybinding != action.keybinding ||
			doc.Slash != action.slash ||
			doc.SlashArgs != action.slashArgs ||
			doc.Summary != action.summary {
			t.Fatalf("ActionDocs[%d] = %#v, want projection of %#v", i, doc, action)
		}
		if !reflect.DeepEqual(doc.KeyAliases, action.keyAliases) ||
			!reflect.DeepEqual(doc.SlashAliases, action.slashAliases) ||
			!reflect.DeepEqual(doc.TelegramAliases, action.telegramAliases) {
			t.Fatalf("ActionDocs[%d] aliases = %#v, want projection of %#v", i, doc, action)
		}
	}
}

func TestActionDocsReturnDefensiveCopies(t *testing.T) {
	docs := ActionDocs()
	for _, doc := range docs {
		if len(doc.KeyAliases) == 0 {
			continue
		}
		want := doc.KeyAliases[0]
		doc.KeyAliases[0] = "mutated"
		for _, fresh := range ActionDocs() {
			if fresh.ID == doc.ID && fresh.KeyAliases[0] != want {
				t.Fatalf("ActionDocs leaked key alias slice for %s", doc.ID)
			}
		}
		return
	}
	t.Fatal("test fixture expected at least one key alias")
}
