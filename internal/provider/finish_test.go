package provider

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeChatFinishReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want FinishKind
	}{
		{name: "OpenAI natural", raw: "stop", want: FinishNatural},
		{name: "Anthropic natural", raw: "end_turn", want: FinishNatural},
		{name: "stop sequence", raw: "stop_sequence", want: FinishNatural},
		{name: "tools", raw: "tool_calls", want: FinishToolCalls},
		{name: "legacy function", raw: "function_call", want: FinishToolCalls},
		{name: "Anthropic tools", raw: "tool_use", want: FinishToolCalls},
		{name: "OpenAI output cap", raw: "length", want: FinishOutputLimit},
		{name: "provider output cap", raw: "MAX TOKENS", want: FinishOutputLimit},
		{name: "context window", raw: "model_context_window_exceeded", want: FinishContextLimit},
		{name: "context length", raw: "context_length_exceeded", want: FinishContextLimit},
		{name: "Anthropic pause", raw: "pause_turn", want: FinishPause},
		{name: "refusal", raw: "refusal", want: FinishRefusal},
		{name: "OpenAI filter", raw: "content_filter", want: FinishContentFilter},
		{name: "Gemini safety", raw: "SAFETY", want: FinishContentFilter},
		{name: "resource", raw: "resource-exhausted", want: FinishResourceLimit},
		{name: "DeepSeek resource", raw: "insufficient_system_resource", want: FinishResourceLimit},
		{name: "Responses resource", raw: "rate_limit_exceeded", want: FinishResourceLimit},
		{name: "unrecognized", raw: "future_provider_reason", want: FinishUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeChatFinishReason(test.raw)
			if got.Kind != test.want || got.RawReason != strings.TrimSpace(test.raw) {
				t.Fatalf("normalizeChatFinishReason(%q) = %#v, want kind %q", test.raw, got, test.want)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("normalized finish is invalid: %v", err)
			}
		})
	}
}

func TestFinishValidationAndLegacyCompatibility(t *testing.T) {
	t.Parallel()
	if err := (Finish{}).Validate(); err == nil {
		t.Fatal("zero finish unexpectedly valid")
	}
	if err := (Finish{Kind: FinishKind("future")}).Validate(); err == nil {
		t.Fatal("unrecognized normalized kind unexpectedly valid")
	}
	legacy := FinishOrLegacyNatural(Finish{})
	if legacy.Kind != FinishNatural || legacy.RawReason != "legacy_zero" {
		t.Fatalf("legacy finish = %#v", legacy)
	}
	explicit := Finish{Kind: FinishUnknown, RawReason: "provider_new_reason"}
	if got := FinishOrLegacyNatural(explicit); got != explicit {
		t.Fatalf("explicit finish changed from %#v to %#v", explicit, got)
	}
	missingKind := Finish{RawReason: "provider_reason_without_kind"}
	if got := FinishOrLegacyNatural(missingKind); got != missingKind {
		t.Fatalf("non-zero invalid finish was treated as legacy: %#v", got)
	}
}

func TestFinishErrorClassification(t *testing.T) {
	t.Parallel()
	for _, kind := range []FinishKind{FinishNatural, FinishToolCalls} {
		if err := FinishErrorFor(Finish{Kind: kind}); err != nil {
			t.Fatalf("FinishErrorFor(%q) = %v", kind, err)
		}
	}
	for _, kind := range []FinishKind{
		FinishOutputLimit,
		FinishContextLimit,
		FinishPause,
		FinishRefusal,
		FinishContentFilter,
		FinishResourceLimit,
		FinishUnknown,
	} {
		finish := Finish{Kind: kind, RawReason: string(kind)}
		err := fmt.Errorf("wrapped: %w", FinishErrorFor(finish))
		var finishErr *FinishError
		if !errors.As(err, &finishErr) {
			t.Fatalf("FinishErrorFor(%q) = %T %v", kind, err, err)
		}
		got, ok := FinishFromError(err)
		if !ok || got != finish {
			t.Fatalf("FinishFromError(%q) = %#v, %v", kind, got, ok)
		}
	}

	got, ok := FinishFromError(FinishErrorFor(Finish{}))
	if !ok || got.Kind != FinishUnknown || got.RawReason != "missing" {
		t.Fatalf("zero finish error = %#v, %v", got, ok)
	}
}

func TestFinishErrorSanitizesDiagnosticReason(t *testing.T) {
	t.Parallel()
	raw := "  provider\nreason\t" + strings.Repeat("x", maxFinishReasonRunes+32)
	err := (&FinishError{Finish: Finish{Kind: FinishUnknown, RawReason: raw}}).Error()
	if strings.ContainsAny(err, "\n\t") || len([]rune(err)) > maxFinishReasonRunes+100 {
		t.Fatalf("unsafe finish diagnostic = %q", err)
	}
}

func TestNormalizeFinishStructurallyBoundsRawReason(t *testing.T) {
	t.Parallel()
	raw := " \x00provider\n" + strings.Repeat("x", maxFinishReasonRunes+32) + "\t "
	finish := NormalizeFinish(Finish{Kind: FinishUnknown, RawReason: raw})
	if finish.Kind != FinishUnknown {
		t.Fatalf("finish kind changed: %#v", finish)
	}
	if strings.ContainsAny(finish.RawReason, "\x00\n\t") || len([]rune(finish.RawReason)) != maxFinishReasonRunes {
		t.Fatalf("raw finish reason was not structurally normalized: %q", finish.RawReason)
	}
}
