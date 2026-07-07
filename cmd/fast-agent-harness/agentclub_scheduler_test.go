package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func TestAgentclubSchedulerRunOnceDeliversDueScheduleWithoutRun(t *testing.T) {
	path := writeAgentclubScheduleConfig(t)
	statePath := filepath.Join(t.TempDir(), "state", "agentclub-scheduler-state.json")
	var got agentclub.TriggerDeliveryRequest
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agentclub/triggers/fixture.schedule/deliveries":
			requestCount++
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			writeAgentclubTestJSON(t, w, agentclub.TriggerDeliveryResponse{
				SchemaVersion:   agentclub.SchemaVersion,
				Admitted:        true,
				InputID:         "input-1",
				State:           "admitted",
				TargetSessionID: "session-1",
				BindingID:       "fixture.schedule",
				TriggerKind:     agentclub.TriggerKindSchedule,
				Source:          "fixture",
				Capability:      "fixture.review",
				EventType:       "review_queue",
				RunDispatched:   false,
			})
		case strings.Contains(r.URL.Path, "/run"):
			t.Fatalf("scheduler must not dispatch runs: %s", r.URL.Path)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	now := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	summary, err := agentclubSchedulerRunOnce(context.Background(), agentclubSchedulerRunOptions{
		gatewayURL: server.URL,
		configPath: path,
		statePath:  statePath,
		now:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 || summary.DeliveredCount != 1 || summary.RunDispatched {
		t.Fatalf("requestCount=%d summary=%#v", requestCount, summary)
	}
	if got.ScheduledAtUTC != "2026-07-07T00:00:00Z" || string(got.Payload) == "" {
		t.Fatalf("delivery request = %#v payload=%s", got, string(got.Payload))
	}
	state, err := agentclub.LoadSchedulerState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.RunCount != 1 || state.DeliveryCount != 1 || state.Triggers["fixture.schedule"].LastScheduledAtUTC != "2026-07-07T00:00:00Z" {
		t.Fatalf("state = %#v", state)
	}

	second, err := agentclubSchedulerRunOnce(context.Background(), agentclubSchedulerRunOptions{
		gatewayURL: server.URL,
		configPath: path,
		statePath:  statePath,
		now:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 || second.DueCount != 0 || second.DeliveredCount != 0 {
		t.Fatalf("second requestCount=%d summary=%#v", requestCount, second)
	}
}

func TestAgentclubSchedulerStatusShowsDueState(t *testing.T) {
	path := writeAgentclubScheduleConfig(t)
	statePath := filepath.Join(t.TempDir(), "agentclub-scheduler-state.json")
	state := agentclub.NewSchedulerState()
	agentclub.RecordSchedulerSuccess(&state, "fixture.schedule", time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 7, 0, 0, 1, 0, time.UTC))
	if err := agentclub.SaveSchedulerState(statePath, state); err != nil {
		t.Fatal(err)
	}
	status, err := buildAgentclubSchedulerStatus(path, statePath, time.Date(2026, 7, 7, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if status.EnabledCount != 1 || status.DueCount != 1 || len(status.Schedules) != 1 {
		t.Fatalf("status = %#v", status)
	}
	text := formatAgentclubSchedulerStatus(status)
	for _, want := range []string{"agent-club scheduler:", "enabled=1", "due=1", "fixture.schedule"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status output missing %q:\n%s", want, text)
		}
	}
}

func TestAgentclubSchedulerRunOnceRedactsGatewayErrors(t *testing.T) {
	path := writeAgentclubScheduleConfig(t)
	statePath := filepath.Join(t.TempDir(), "agentclub-scheduler-state.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`candidate@example.com delivery-secret should not leak`))
	}))
	t.Cleanup(server.Close)

	summary, err := agentclubSchedulerRunOnce(context.Background(), agentclubSchedulerRunOptions{
		gatewayURL: server.URL,
		configPath: path,
		statePath:  statePath,
		now:        time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
	})
	if err == nil || summary.ErrorCount != 1 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	state, loadErr := agentclub.LoadSchedulerState(statePath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	lastErr := state.Triggers["fixture.schedule"].LastError
	for _, forbidden := range []string{"candidate@example.com", "delivery-secret"} {
		if strings.Contains(err.Error(), forbidden) || strings.Contains(lastErr, forbidden) || strings.Contains(formatAgentclubSchedulerRunSummary(summary), forbidden) {
			t.Fatalf("leaked %q err=%v state=%#v summary=%#v", forbidden, err, state, summary)
		}
	}
	if !strings.Contains(lastErr, "gateway HTTP 413") {
		t.Fatalf("last error = %q", lastErr)
	}
}

func writeAgentclubScheduleConfig(t *testing.T) string {
	t.Helper()
	return writeAgentclubTriggerConfig(t, agentclub.TriggerBindingConfig{
		ID:              "fixture.schedule",
		Kind:            agentclub.TriggerKindSchedule,
		Source:          "fixture",
		Capability:      "fixture.review",
		EventType:       "review_queue",
		Owner:           gatewayapi.SessionOwner{ClientType: "ingress", ClientID: "ingress:fixture:prod"},
		TargetSessionID: "session-1",
		Prompt:          "Review the scheduled fixture snapshot.",
		AuthMethod:      agentclub.TriggerAuthNone,
		Schedule: &agentclub.ScheduleConfig{
			Kind:       agentclub.ScheduleKindInterval,
			Every:      "30m",
			StartAtUTC: "2026-07-07T00:00:00Z",
			MaxCatchup: 1,
		},
		Enabled: true,
	})
}
