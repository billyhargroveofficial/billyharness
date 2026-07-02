package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestStreamEventsDoesNotDuplicateEmittedRunFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	streamEvents(rec, func(emit func(protocol.Event)) error {
		emit(protocol.Event{Type: protocol.EventRunStarted})
		emit(protocol.Event{Type: protocol.EventRunFailed, Data: "provider boom"})
		return errors.New("provider boom")
	})

	events := readProtocolEvents(t, rec.Body)
	failed := 0
	for _, event := range events {
		if event.Type == protocol.EventRunFailed {
			failed++
			if got := fmt.Sprint(event.Data); got != "provider boom" {
				t.Fatalf("failure data = %q", got)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("run.failed count = %d, events=%v", failed, events)
	}
}

func TestStreamEventsSynthesizesFailureForSetupError(t *testing.T) {
	rec := httptest.NewRecorder()
	streamEvents(rec, func(emit func(protocol.Event)) error {
		return errors.New("setup boom")
	})

	events := readProtocolEvents(t, rec.Body)
	if len(events) != 1 {
		t.Fatalf("event count = %d, events=%v", len(events), events)
	}
	if events[0].Type != protocol.EventRunFailed {
		t.Fatalf("event type = %s", events[0].Type)
	}
	if got := fmt.Sprint(events[0].Data); got != "setup boom" {
		t.Fatalf("failure data = %q", got)
	}
}

func TestStreamEventsDoesNotAppendFailureAfterRunCompleted(t *testing.T) {
	rec := httptest.NewRecorder()
	streamEvents(rec, func(emit func(protocol.Event)) error {
		emit(protocol.Event{Type: protocol.EventRunStarted})
		emit(protocol.Event{Type: protocol.EventRunCompleted})
		return errors.New("late cleanup boom")
	})

	events := readProtocolEvents(t, rec.Body)
	for _, event := range events {
		if event.Type == protocol.EventRunFailed {
			t.Fatalf("unexpected run.failed after completed run: events=%v", events)
		}
	}
}

func TestStreamEventsDoesNotBlockRunWhenClientWriterStalls(t *testing.T) {
	writer := newBlockingResponseWriter()
	runDone := make(chan struct{})
	streamDone := make(chan struct{})
	go func() {
		streamEvents(writer, func(emit func(protocol.Event)) error {
			emit(protocol.Event{Seq: 1, Type: protocol.EventRunStarted})
			select {
			case <-writer.writeStarted:
			case <-time.After(time.Second):
				t.Error("writer did not start")
			}
			for i := 0; i < liveRunStreamBuffer+20; i++ {
				emit(protocol.Event{Seq: int64(i + 2), Type: protocol.EventAssistantDelta, Data: fmt.Sprintf("delta-%03d", i)})
			}
			emit(protocol.Event{Seq: int64(liveRunStreamBuffer + 22), Type: protocol.EventRunCompleted})
			close(runDone)
			return nil
		})
		close(streamDone)
	}()

	select {
	case <-writer.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("writer did not block on first write")
	}
	select {
	case <-runDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("run was blocked by stalled response writer")
	}
	select {
	case <-streamDone:
		t.Fatal("stream finished before writer was unblocked")
	default:
	}
	close(writer.unblock)
	select {
	case <-streamDone:
	case <-time.After(time.Second):
		t.Fatal("stream did not finish after writer unblocked")
	}

	events := readProtocolEvents(t, bytes.NewReader(writer.bytes()))
	var sawGap bool
	for _, event := range events {
		if event.Type == protocol.EventGatewayStreamGap {
			sawGap = true
			break
		}
	}
	if !sawGap {
		t.Fatalf("stream events missing gap hint: %#v", events)
	}
}

type blockingResponseWriter struct {
	header       http.Header
	writeStarted chan struct{}
	unblock      chan struct{}
	once         sync.Once
	mu           sync.Mutex
	body         bytes.Buffer
	status       int
}

func newBlockingResponseWriter() *blockingResponseWriter {
	return &blockingResponseWriter{
		header:       http.Header{},
		writeStarted: make(chan struct{}),
		unblock:      make(chan struct{}),
	}
}

func (w *blockingResponseWriter) Header() http.Header {
	return w.header
}

func (w *blockingResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *blockingResponseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.writeStarted)
		<-w.unblock
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}

func (w *blockingResponseWriter) Flush() {}

func (w *blockingResponseWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.body.Bytes()...)
}
