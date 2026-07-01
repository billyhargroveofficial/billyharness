package provider_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/testkit/fakeprovider"
)

func TestFakeProviderReplayScriptCoversSlowPartialRetryAndCancel(t *testing.T) {
	script := fakeprovider.New(fakeprovider.Step{
		Delay: time.Millisecond,
		Events: []provider.Event{
			{Kind: provider.EventContent, Text: "slow "},
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ToolID: "call_partial", ToolName: "fs_read_file", ArgsDelta: `{"path"`},
			{Kind: provider.EventToolCallDelta, ToolIndex: 0, ArgsDelta: `:"README.md"}`},
			{Kind: provider.EventRequestMetadata, Request: provider.RequestMetadata{
				RequestID:         "local-request",
				ProviderID:        "fake",
				ModelID:           "fake-model",
				ProviderRequestID: "fake-request-2",
				Attempts:          2,
				Retries:           1,
				StatusCode:        200,
			}},
			{Kind: provider.EventDone},
		},
	})
	events, errs := script.Stream(context.Background(), provider.Request{RequestID: "local-request", Model: "fake-model"})
	var content string
	var metadata provider.RequestMetadata
	var acc provider.ToolAccumulator
	var sawDone bool
	for event := range events {
		switch event.Kind {
		case provider.EventContent:
			content += event.Text
		case provider.EventToolCallDelta:
			acc.Push(event)
		case provider.EventRequestMetadata:
			metadata = event.Request
		case provider.EventDone:
			sawDone = true
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	calls, err := acc.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if content != "slow " || !sawDone || len(calls) != 1 || calls[0].Name != "fs_read_file" || string(calls[0].Arguments) != `{"path":"README.md"}` {
		t.Fatalf("content=%q sawDone=%v calls=%#v", content, sawDone, calls)
	}
	if metadata.ProviderRequestID != "fake-request-2" || metadata.Attempts != 2 || metadata.Retries != 1 {
		t.Fatalf("metadata = %#v", metadata)
	}
	if script.Calls() != 1 || len(script.Requests()) != 1 || script.Requests()[0].RequestID != "local-request" {
		t.Fatalf("script calls=%d requests=%#v", script.Calls(), script.Requests())
	}

	cancelScript := fakeprovider.New(fakeprovider.Step{WaitForCancel: true})
	ctx, cancel := context.WithCancel(context.Background())
	events, errs = cancelScript.Stream(ctx, provider.Request{Model: "fake-model"})
	cancel()
	for range events {
		t.Fatal("cancel script should not emit events")
	}
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err = %v", err)
	}
	if got := cancelScript.Requests()[0].Model; !strings.Contains(got, "fake-model") {
		t.Fatalf("cancel request not recorded: %#v", cancelScript.Requests())
	}
}
