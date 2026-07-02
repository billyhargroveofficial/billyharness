package projector

import (
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestEventPresentationPolicyClassifiesLifecycleEvents(t *testing.T) {
	tests := []struct {
		name       string
		eventType  protocol.EventType
		assertions func(t *testing.T, policy EventPresentation)
	}{
		{
			name:      "tool requested is visible and counted",
			eventType: protocol.EventToolCallRequested,
			assertions: func(t *testing.T, policy EventPresentation) {
				if !policy.Transcript || !policy.CompactProgress || !policy.StatusLine || !policy.FinalFooter || !policy.ContextReport {
					t.Fatalf("tool requested policy = %#v", policy)
				}
				if policy.LowLevelToolLifecycle {
					t.Fatalf("tool requested should not be low-level lifecycle: %#v", policy)
				}
			},
		},
		{
			name:      "tool progress is low-level only",
			eventType: protocol.EventToolCallProgress,
			assertions: func(t *testing.T, policy EventPresentation) {
				if policy.Transcript || policy.CompactProgress || policy.FinalFooter || policy.ContextReport {
					t.Fatalf("tool progress should not affect transcript/progress/footer/context: %#v", policy)
				}
				if !policy.StatusLine || !policy.LowLevelToolLifecycle {
					t.Fatalf("tool progress should stay status-only low-level lifecycle: %#v", policy)
				}
			},
		},
		{
			name:      "stream still running is compact progress",
			eventType: protocol.EventStreamStillRunning,
			assertions: func(t *testing.T, policy EventPresentation) {
				if policy.Transcript || !policy.CompactProgress || !policy.StatusLine || policy.FinalFooter || policy.ContextReport {
					t.Fatalf("still-running policy = %#v", policy)
				}
			},
		},
		{
			name:      "provider usage is footer and context only",
			eventType: protocol.EventProviderUsageUpdate,
			assertions: func(t *testing.T, policy EventPresentation) {
				if policy.Transcript || policy.CompactProgress || !policy.FinalFooter || !policy.ContextReport {
					t.Fatalf("provider usage policy = %#v", policy)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.assertions(t, EventPresentationPolicy(tt.eventType))
		})
	}
}
