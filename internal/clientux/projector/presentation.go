package projector

import "github.com/billyhargroveofficial/billyharness/internal/protocol"

type EventPresentation = protocol.EventPresentation

func EventPresentationPolicy(eventType protocol.EventType) EventPresentation {
	return protocol.EventPresentationPolicy(eventType)
}
