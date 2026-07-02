package projector

import "github.com/billyhargroveofficial/billyharness/internal/protocol"

type EventPresentation struct {
	Transcript            bool
	CompactProgress       bool
	StatusLine            bool
	FinalFooter           bool
	ContextReport         bool
	LowLevelToolLifecycle bool
	FlushImmediate        bool
}

func (p EventPresentation) FlushesStreamQueue() bool {
	return p.FlushImmediate
}

func EventPresentationPolicy(eventType protocol.EventType) EventPresentation {
	switch eventType {
	case protocol.EventRunStarted:
		return EventPresentation{Transcript: true, StatusLine: true}
	case protocol.EventRunCompleted:
		return EventPresentation{Transcript: true, StatusLine: true, FinalFooter: true, ContextReport: true, FlushImmediate: true}
	case protocol.EventRunFailed:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FinalFooter: true, ContextReport: true, FlushImmediate: true}
	case protocol.EventModelCallStarted:
		return EventPresentation{StatusLine: true}
	case protocol.EventAssistantReasoning, protocol.EventAssistantDelta:
		return EventPresentation{Transcript: true, StatusLine: true, FinalFooter: true}
	case protocol.EventToolAudit:
		return EventPresentation{Transcript: true, StatusLine: true}
	case protocol.EventToolCallRequested:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FinalFooter: true, ContextReport: true, FlushImmediate: true}
	case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FinalFooter: true, ContextReport: true, FlushImmediate: true}
	case protocol.EventToolCallStarted, protocol.EventToolOutputRefCreated:
		return EventPresentation{StatusLine: true, LowLevelToolLifecycle: true, FlushImmediate: true}
	case protocol.EventToolCallProgress,
		protocol.EventToolPermissionRequested, protocol.EventToolPermissionDecided:
		return EventPresentation{StatusLine: true, LowLevelToolLifecycle: true}
	case protocol.EventStepStarted, protocol.EventStepCompleted:
		return EventPresentation{Transcript: true, StatusLine: true, FlushImmediate: true}
	case protocol.EventContextCompacted:
		return EventPresentation{Transcript: true, StatusLine: true, ContextReport: true}
	case protocol.EventContextThreshold:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, ContextReport: true}
	case protocol.EventStreamStillRunning:
		return EventPresentation{CompactProgress: true, StatusLine: true, FlushImmediate: true}
	case protocol.EventTurnChangeRecorded, protocol.EventTurnChangeReverted:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FlushImmediate: true}
	case protocol.EventProviderUsageUpdate, protocol.EventProviderHelperUsage:
		return EventPresentation{StatusLine: true, FinalFooter: true, ContextReport: true}
	case protocol.EventUserInputRequested, protocol.EventUserInputAnswered, protocol.EventUserInputRejected:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FlushImmediate: true}
	default:
		return EventPresentation{}
	}
}
