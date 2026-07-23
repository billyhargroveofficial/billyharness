package provider

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// FinishKind is the provider-neutral reason a model turn stopped. A model
// turn stopping is not, by itself, proof that the surrounding agent objective
// completed successfully.
type FinishKind string

const (
	FinishNatural       FinishKind = "natural"
	FinishToolCalls     FinishKind = "tool_calls"
	FinishOutputLimit   FinishKind = "output_limit"
	FinishContextLimit  FinishKind = "context_limit"
	FinishPause         FinishKind = "pause"
	FinishRefusal       FinishKind = "refusal"
	FinishContentFilter FinishKind = "content_filter"
	FinishResourceLimit FinishKind = "resource_limit"
	FinishUnknown       FinishKind = "unknown"
)

// Finish describes why a provider ended a streamed model turn. RawReason is a
// bounded, control-character-free provider token retained for diagnostics; it
// must never contain response content or credentials.
type Finish struct {
	Kind      FinishKind `json:"kind,omitempty"`
	RawReason string     `json:"raw_reason,omitempty"`
}

// Validate rejects the legacy zero value and unrecognized normalized kinds.
// Unknown is a valid, explicit fail-closed classification.
func (f Finish) Validate() error {
	switch f.Kind {
	case FinishNatural,
		FinishToolCalls,
		FinishOutputLimit,
		FinishContextLimit,
		FinishPause,
		FinishRefusal,
		FinishContentFilter,
		FinishResourceLimit,
		FinishUnknown:
		return nil
	case "":
		return errors.New("missing model finish kind")
	default:
		return fmt.Errorf("invalid model finish kind %q", f.Kind)
	}
}

// FinishOrLegacyNatural is the only compatibility path for old fake providers
// whose EventDone used the zero Finish value. Real provider adapters and new
// fakes must always emit an explicit finish kind.
func FinishOrLegacyNatural(f Finish) Finish {
	if f == (Finish{}) {
		return Finish{Kind: FinishNatural, RawReason: "legacy_zero"}
	}
	return NormalizeFinish(f)
}

// NormalizeFinish applies the provider boundary's structural safety rules to
// diagnostic metadata without guessing at secrets or changing the finish kind.
// Provider adapters should call this before emitting EventDone; consumers may
// call it again defensively before persistence or telemetry.
func NormalizeFinish(f Finish) Finish {
	f.RawReason = sanitizeFinishReason(f.RawReason)
	return f
}

// FinishError is a typed, provider-neutral unsuccessful or ambiguous model
// termination. Callers can classify it without parsing an error string.
type FinishError struct {
	Finish Finish
}

func (e *FinishError) Error() string {
	if e == nil {
		return ""
	}
	kind := e.Finish.Kind
	if kind == "" {
		kind = FinishUnknown
	}
	reason := sanitizeFinishReason(e.Finish.RawReason)
	if reason == "" {
		return fmt.Sprintf("model response ended with %s", kind)
	}
	return fmt.Sprintf("model response ended with %s (provider reason %q)", kind, reason)
}

// FinishErrorFor returns nil only for finish kinds whose consistency must be
// decided with the assembled response: natural completion and tool calls. All
// other (including zero or invalid) values fail closed as a FinishError.
func FinishErrorFor(f Finish) error {
	f = NormalizeFinish(f)
	switch f.Kind {
	case FinishNatural, FinishToolCalls:
		return nil
	case FinishOutputLimit,
		FinishContextLimit,
		FinishPause,
		FinishRefusal,
		FinishContentFilter,
		FinishResourceLimit,
		FinishUnknown:
		return &FinishError{Finish: f}
	case "":
		return &FinishError{Finish: Finish{Kind: FinishUnknown, RawReason: firstFinishReason(f.RawReason, "missing")}}
	default:
		return &FinishError{Finish: Finish{Kind: FinishUnknown, RawReason: firstFinishReason(f.RawReason, string(f.Kind))}}
	}
}

// FinishFromError extracts normalized termination metadata from a wrapped
// FinishError.
func FinishFromError(err error) (Finish, bool) {
	var finishErr *FinishError
	if !errors.As(err, &finishErr) || finishErr == nil {
		return Finish{}, false
	}
	return NormalizeFinish(finishErr.Finish), true
}

func normalizeChatFinishReason(raw string) Finish {
	reason := sanitizeFinishReason(raw)
	canonical := strings.ToLower(reason)
	canonical = strings.NewReplacer("-", "_", " ", "_").Replace(canonical)

	kind := FinishUnknown
	switch canonical {
	case "stop", "end_turn", "stop_sequence", "complete", "completed":
		kind = FinishNatural
	case "tool_calls", "function_call", "tool_use":
		kind = FinishToolCalls
	case "length", "max_tokens", "max_output_tokens", "token_limit", "model_length":
		kind = FinishOutputLimit
	case "context_length", "context_limit", "context_length_exceeded", "context_window_exceeded", "model_context_window_exceeded", "input_too_long":
		kind = FinishContextLimit
	case "pause", "pause_turn":
		kind = FinishPause
	case "refusal", "refused":
		kind = FinishRefusal
	case "content_filter", "safety", "blocked", "blocklist", "prohibited_content", "recitation":
		kind = FinishContentFilter
	case "resource_limit", "resource_exhausted", "rate_limit_exceeded", "insufficient_system_resource", "overloaded":
		kind = FinishResourceLimit
	}
	return Finish{Kind: kind, RawReason: reason}
}

const maxFinishReasonRunes = 128

func sanitizeFinishReason(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	runes := make([]rune, 0, min(len(raw), maxFinishReasonRunes))
	for _, r := range raw {
		if len(runes) == maxFinishReasonRunes {
			break
		}
		if unicode.IsControl(r) {
			continue
		}
		runes = append(runes, r)
	}
	return strings.TrimSpace(string(runes))
}

func firstFinishReason(values ...string) string {
	for _, value := range values {
		if value = sanitizeFinishReason(value); value != "" {
			return value
		}
	}
	return ""
}
