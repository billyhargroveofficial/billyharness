package protocol

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

func EventPresentationPolicy(eventType EventType) EventPresentation {
	switch eventType {
	case EventRunStarted:
		return EventPresentation{Transcript: true, StatusLine: true}
	case EventRunCompleted:
		return EventPresentation{Transcript: true, StatusLine: true, FinalFooter: true, ContextReport: true, FlushImmediate: true}
	case EventRunFailed:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FinalFooter: true, ContextReport: true, FlushImmediate: true}
	case EventModelCallStarted:
		return EventPresentation{StatusLine: true}
	case EventAssistantReasoning, EventAssistantDelta:
		return EventPresentation{Transcript: true, StatusLine: true, FinalFooter: true}
	case EventToolAudit:
		return EventPresentation{Transcript: true, StatusLine: true}
	case EventToolCallRequested:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FinalFooter: true, ContextReport: true, FlushImmediate: true}
	case EventToolCallFinished, EventToolCallFailed, EventToolCallAborted:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FinalFooter: true, ContextReport: true, FlushImmediate: true}
	case EventToolCallStarted, EventToolOutputRefCreated:
		return EventPresentation{StatusLine: true, LowLevelToolLifecycle: true, FlushImmediate: true}
	case EventToolCallProgress,
		EventToolPermissionRequested, EventToolPermissionDecided:
		return EventPresentation{StatusLine: true, LowLevelToolLifecycle: true}
	case EventStepStarted, EventStepCompleted:
		return EventPresentation{Transcript: true, StatusLine: true, FlushImmediate: true}
	case EventContextCompacted:
		return EventPresentation{Transcript: true, StatusLine: true, ContextReport: true}
	case EventContextThreshold:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, ContextReport: true}
	case EventStreamStillRunning:
		return EventPresentation{CompactProgress: true, StatusLine: true, FlushImmediate: true}
	case EventGatewayStreamGap:
		return EventPresentation{CompactProgress: true, StatusLine: true, FlushImmediate: true}
	case EventTurnChangeRecorded, EventTurnChangeReverted:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FlushImmediate: true}
	case EventProviderUsageUpdate, EventProviderHelperUsage:
		return EventPresentation{StatusLine: true, FinalFooter: true, ContextReport: true}
	case EventUserInputRequested, EventUserInputAnswered, EventUserInputRejected:
		return EventPresentation{Transcript: true, CompactProgress: true, StatusLine: true, FlushImmediate: true}
	default:
		return EventPresentation{}
	}
}
