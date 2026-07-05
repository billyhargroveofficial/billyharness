package docsgen

import (
	"bytes"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type eventsReferenceData struct {
	EnvelopeFields []eventEnvelopeFieldDoc
	EventTypes     []protocol.EventTypeSpec
	LifecycleRules []eventlog.LifecycleRuleDoc
	Sources        []protocol.EventSourceSpec
}

type eventEnvelopeFieldDoc struct {
	Field       string
	JSON        string
	Description string
}

func GenerateEvents() ([]byte, error) {
	data := eventsReferenceInput()
	var b bytes.Buffer
	b.Write(header("internal/protocol, internal/eventlog"))
	b.WriteString("# Protocol Events Reference\n\n")
	b.WriteString("This reference documents the event envelope, event-type vocabulary, and lifecycle rules that have a runtime-owned declarative table.\n\n")
	b.WriteString("## Envelope Fields\n\n")
	b.WriteString(markdownTable([]string{"Field", "JSON", "Description"}, eventEnvelopeFieldRows(data.EnvelopeFields)))
	b.WriteString("\n## Event Types\n\n")
	b.WriteString(markdownTable([]string{"Type", "Required IDs", "Payload", "Description"}, eventTypeRows(data.EventTypes)))
	b.WriteString("\n## Lifecycle Rules\n\n")
	b.WriteString("This section is intentionally partial: it renders only lifecycle entities whose structural checks are consumed from `internal/eventlog.LifecycleRules()`. Turn, step, tool-attempt, output-ref, user-input, and hook checks remain procedural in `LifecycleValidator.Observe()` because they depend on optional ids, payload phases, or cross-map ownership.\n\n")
	b.WriteString(markdownTable([]string{"Event", "Entity", "Kind", "Parent", "Terminal"}, lifecycleRuleRows(data.LifecycleRules)))
	b.WriteString("\n")
	b.WriteString(lifecycleRuleDiagrams(data.LifecycleRules))
	b.WriteString("\n## Event Sources\n\n")
	b.WriteString(markdownTable([]string{"Source", "Description"}, eventSourceRows(data.Sources)))
	footer, err := sourceHashFooter(data)
	if err != nil {
		return nil, err
	}
	b.Write(footer)
	return b.Bytes(), nil
}

func eventsReferenceInput() eventsReferenceData {
	return eventsReferenceData{
		EnvelopeFields: eventEnvelopeFieldDocs(),
		EventTypes:     protocol.EventTypeDocs(),
		LifecycleRules: eventlog.LifecycleRules(),
		Sources:        protocol.EventSourceDocs(),
	}
}

func eventEnvelopeFieldDocs() []eventEnvelopeFieldDoc {
	return []eventEnvelopeFieldDoc{
		{Field: "SchemaVersion", JSON: "schema_version", Description: "Event schema version; zero is accepted as legacy"},
		{Field: "Seq", JSON: "seq", Description: "Monotonic sequence within an event stream"},
		{Field: "Source", JSON: "source", Description: "Emitter class from EventSourceDocs"},
		{Field: "TS", JSON: "ts", Description: "RFC3339Nano event timestamp"},
		{Field: "SubmissionID", JSON: "submission_id", Description: "Submission/request correlation id"},
		{Field: "RunID", JSON: "run_id", Description: "Run lifecycle correlation id"},
		{Field: "TurnID", JSON: "turn_id", Description: "Conversation turn correlation id"},
		{Field: "StepID", JSON: "step_id", Description: "Runtime step correlation id"},
		{Field: "CallID", JSON: "call_id", Description: "Tool/user-input call correlation id"},
		{Field: "AttemptID", JSON: "attempt_id", Description: "Tool attempt correlation id"},
		{Field: "ParentStepID", JSON: "parent_step_id", Description: "Parent step for nested tool-call steps"},
		{Field: "ProfileHash", JSON: "profile_hash", Description: "Profile/instruction hash captured for diagnostics"},
		{Field: "DurationMS", JSON: "duration_ms", Description: "Duration in milliseconds when known"},
		{Field: "Type", JSON: "type", Description: "Event type from EventTypeDocs"},
		{Field: "Data", JSON: "data", Description: "Event-specific payload"},
	}
}

func eventEnvelopeFieldRows(fields []eventEnvelopeFieldDoc) [][]string {
	rows := make([][]string, 0, len(fields))
	for _, field := range fields {
		rows = append(rows, []string{field.Field, field.JSON, field.Description})
	}
	return rows
}

func eventTypeRows(types []protocol.EventTypeSpec) [][]string {
	rows := make([][]string, 0, len(types))
	for _, typ := range types {
		rows = append(rows, []string{
			string(typ.Type),
			strings.Join(typ.RequiredIDs, ", "),
			typ.Payload,
			typ.Doc,
		})
	}
	return rows
}

func lifecycleRuleRows(rules []eventlog.LifecycleRuleDoc) [][]string {
	rows := make([][]string, 0, len(rules))
	for _, rule := range rules {
		rows = append(rows, []string{
			string(rule.Event),
			rule.Entity,
			rule.Kind,
			rule.Parent,
			boolString(rule.Terminal),
		})
	}
	return rows
}

func lifecycleRuleDiagrams(rules []eventlog.LifecycleRuleDoc) string {
	byEntity := map[string][]eventlog.LifecycleRuleDoc{}
	for _, rule := range rules {
		byEntity[rule.Entity] = append(byEntity[rule.Entity], rule)
	}
	entities := make([]string, 0, len(byEntity))
	for entity := range byEntity {
		entities = append(entities, entity)
	}
	sort.Strings(entities)
	var b strings.Builder
	for _, entity := range entities {
		entityRules := byEntity[entity]
		b.WriteString("### ")
		b.WriteString(entity)
		b.WriteString("\n\n```mermaid\nstateDiagram-v2\n")
		activeState := entity + "_active"
		terminalState := entity + "_terminal"
		for _, rule := range entityRules {
			switch rule.Kind {
			case "starts":
				b.WriteString("    [*] --> ")
				b.WriteString(activeState)
				b.WriteString(" : ")
				b.WriteString(string(rule.Event))
				b.WriteString("\n")
			case "progresses":
				b.WriteString("    ")
				b.WriteString(activeState)
				b.WriteString(" --> ")
				b.WriteString(activeState)
				b.WriteString(" : ")
				b.WriteString(string(rule.Event))
				b.WriteString("\n")
			case "terminates":
				b.WriteString("    ")
				b.WriteString(activeState)
				b.WriteString(" --> ")
				b.WriteString(terminalState)
				b.WriteString(" : ")
				b.WriteString(string(rule.Event))
				b.WriteString("\n")
			}
		}
		b.WriteString("```\n\n")
	}
	return b.String()
}

func eventSourceRows(sources []protocol.EventSourceSpec) [][]string {
	rows := make([][]string, 0, len(sources))
	for _, source := range sources {
		rows = append(rows, []string{string(source.Source), source.Doc})
	}
	return rows
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
