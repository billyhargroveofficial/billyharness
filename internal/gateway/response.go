package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

const liveRunStreamBuffer = 256

func streamEvents(w http.ResponseWriter, run func(func(protocol.Event)) error) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	events := make(chan protocol.Event, liveRunStreamBuffer)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for event := range events {
			if !writeNDJSONEvent(w, flusher, event) {
				return
			}
		}
	}()

	var terminalEmitted atomic.Bool
	var droppedEvents atomic.Int64
	var lastQueuedSeq atomic.Int64
	queue := func(event protocol.Event) bool {
		select {
		case <-writerDone:
			return false
		default:
		}
		select {
		case events <- event:
			if event.Seq > 0 {
				lastQueuedSeq.Store(event.Seq)
			}
			return true
		case <-writerDone:
			return false
		default:
			return false
		}
	}
	emitGap := func(block bool) {
		dropped := droppedEvents.Swap(0)
		if dropped <= 0 {
			return
		}
		event := gatewayStreamGapEvent(dropped, lastQueuedSeq.Load())
		if block {
			select {
			case events <- event:
			case <-writerDone:
			}
			return
		}
		if !queue(event) {
			droppedEvents.Add(dropped)
		}
	}
	emit := func(event protocol.Event) {
		if isTerminalRunEvent(event.Type) {
			terminalEmitted.Store(true)
		}
		if droppedEvents.Load() > 0 {
			emitGap(false)
		}
		if !queue(event) {
			droppedEvents.Add(1)
		}
	}
	if err := run(emit); err != nil && !terminalEmitted.Load() {
		emit(protocol.Event{Type: protocol.EventRunFailed, Data: err.Error()})
	}
	if droppedEvents.Load() > 0 {
		emitGap(true)
	}
	close(events)
	<-writerDone
}

func gatewayStreamGapEvent(dropped, replayAfterSeq int64) protocol.Event {
	return protocol.Event{
		Type:   protocol.EventGatewayStreamGap,
		Source: protocol.EventSourceGateway,
		TS:     time.Now().UTC().Format(time.RFC3339Nano),
		Data: protocol.GatewayStreamGapEvent{
			DroppedEvents:  dropped,
			ReplayAfterSeq: replayAfterSeq,
			Message:        "live stream dropped events; replay /v1/sessions/{id}/events after the last durable seq",
		},
	}
}

func isTerminalRunEvent(eventType protocol.EventType) bool {
	return eventType == protocol.EventRunCompleted || eventType == protocol.EventRunFailed
}

func writeNDJSONEvent(w http.ResponseWriter, flusher http.Flusher, event protocol.Event) bool {
	body, err := marshalRedactedJSON(event)
	if err != nil {
		return false
	}
	body = append(body, '\n')
	if _, err := w.Write(body); err != nil {
		return false
	}
	if flusher != nil {
		flusher.Flush()
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := marshalRedactedJSON(value)
	if err != nil {
		http.Error(w, `{"error":"failed to encode JSON"}`, http.StatusInternalServerError)
		return
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func marshalRedactedJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	redactJSONStrings(decoded)
	return json.Marshal(decoded)
}

func redactJSONStrings(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if text, ok := item.(string); ok {
				v[key] = secrets.Redact(text)
				continue
			}
			redactJSONStrings(item)
		}
	case []any:
		for i, item := range v {
			if text, ok := item.(string); ok {
				v[i] = secrets.Redact(text)
				continue
			}
			redactJSONStrings(item)
		}
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}
