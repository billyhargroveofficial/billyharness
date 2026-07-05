package docsgen

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestEventsReferenceCoversEventTypeDocs(t *testing.T) {
	output, err := GenerateEvents()
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range protocol.EventTypeDocs() {
		if !bytes.Contains(output, []byte(string(spec.Type))) {
			t.Fatalf("events reference missing type %s", spec.Type)
		}
	}
}

func TestEventsReferenceIncludesLifecycleRules(t *testing.T) {
	output, err := GenerateEvents()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output, []byte("stateDiagram-v2")) {
		t.Fatal("events reference missing lifecycle state diagram")
	}
	if !bytes.Contains(output, []byte("intentionally partial")) {
		t.Fatal("events reference missing partial lifecycle boundary note")
	}
	for _, rule := range eventlog.LifecycleRules() {
		if !bytes.Contains(output, []byte(string(rule.Event))) {
			t.Fatalf("events reference missing lifecycle rule %s", rule.Event)
		}
	}
}

func TestEventEnvelopeFieldDocsMatchStruct(t *testing.T) {
	fields := eventEnvelopeFieldDocs()
	if got, want := len(fields), reflect.TypeOf(protocol.Event{}).NumField(); got != want {
		t.Fatalf("event envelope field docs = %d, want %d", got, want)
	}
	for _, field := range fields {
		if field.Field == "" || field.JSON == "" || field.Description == "" {
			t.Fatalf("incomplete event envelope field doc: %#v", field)
		}
	}
}
