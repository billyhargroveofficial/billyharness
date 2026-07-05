package eventlog

import (
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type ruleKind string

const (
	ruleStarts     ruleKind = "starts"
	ruleProgresses ruleKind = "progresses"
	ruleTerminates ruleKind = "terminates"
)

type lifecycleRule struct {
	Event    protocol.EventType
	Entity   string
	Kind     ruleKind
	Parent   string
	Terminal bool
}

type LifecycleRuleDoc struct {
	Event    protocol.EventType
	Entity   string
	Kind     string
	Parent   string
	Terminal bool
}

var lifecycleRules = [...]lifecycleRule{
	{Event: protocol.EventRunStarted, Entity: "run", Kind: ruleStarts},
	{Event: protocol.EventRunCompleted, Entity: "run", Kind: ruleTerminates, Terminal: true},
	{Event: protocol.EventRunFailed, Entity: "run", Kind: ruleTerminates, Terminal: true},
}

var lifecycleRulesByEvent = func() map[protocol.EventType]lifecycleRule {
	out := make(map[protocol.EventType]lifecycleRule, len(lifecycleRules))
	for _, rule := range lifecycleRules {
		out[rule.Event] = rule
	}
	return out
}()

func LifecycleRules() []LifecycleRuleDoc {
	out := make([]LifecycleRuleDoc, len(lifecycleRules))
	for i, rule := range lifecycleRules {
		out[i] = LifecycleRuleDoc{
			Event:    rule.Event,
			Entity:   rule.Entity,
			Kind:     string(rule.Kind),
			Parent:   rule.Parent,
			Terminal: rule.Terminal,
		}
	}
	return out
}

func lifecycleRuleFor(eventType protocol.EventType) (lifecycleRule, bool) {
	rule, ok := lifecycleRulesByEvent[eventType]
	return rule, ok
}

func (v *LifecycleValidator) observeLifecycleRule(event protocol.Event, rule lifecycleRule) error {
	switch rule.Entity {
	case "run":
		return v.observeRunLifecycleRule(event, rule)
	default:
		return fmt.Errorf("unsupported lifecycle rule entity %q for %s", rule.Entity, event.Type)
	}
}

func (v *LifecycleValidator) observeRunLifecycleRule(event protocol.Event, rule lifecycleRule) error {
	runID := strings.TrimSpace(event.RunID)
	if runID == "" {
		return fmt.Errorf("%s missing run_id", event.Type)
	}
	switch rule.Kind {
	case ruleStarts:
		v.runs[runID] = struct{}{}
	case ruleTerminates:
		if _, ok := v.runs[runID]; !ok {
			return fmt.Errorf("%s without started run %q", event.Type, runID)
		}
		if previous, ok := v.terminalRun[runID]; ok {
			return fmt.Errorf("duplicate terminal run event for %q: got %s after %s", runID, event.Type, previous)
		}
		v.terminalRun[runID] = event.Type
	case ruleProgresses:
		if _, ok := v.runs[runID]; !ok {
			return fmt.Errorf("%s without started run %q", event.Type, runID)
		}
	default:
		return fmt.Errorf("unsupported lifecycle rule kind %q for %s", rule.Kind, event.Type)
	}
	return nil
}
