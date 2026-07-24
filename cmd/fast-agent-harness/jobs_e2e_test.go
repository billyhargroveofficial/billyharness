package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
)

func TestJobsCLILoopbackGatewayMockWorkflowCompletes(t *testing.T) {
	setJobsTestConfig(t)
	workspace := t.TempDir()
	cfg := config.BuiltIn()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.Thinking = "disabled"
	cfg.ReasoningEffort = "off"
	cfg.WorkspaceRoots = []string{workspace}
	cfg.ApplyModelProviderDefaults()

	registry := newToolRegistryNoMCP(cfg)
	t.Cleanup(registry.Close)
	stack, err := newDurableJobStack(
		context.Background(),
		cfg,
		t.TempDir(),
		registry,
		withDurableJobMaxConcurrency(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if stopped, shutdownErr := stack.shutdown(shutdownCtx); shutdownErr != nil || !stopped {
			t.Errorf("shutdown durable job stack: stopped=%t err=%v", stopped, shutdownErr)
		}
	})

	jobGateway := gateway.NewServerWithOptions(
		cfg,
		provider.Mock{},
		registry,
		gateway.ServerOptions{
			JobController: stack.manager,
			JobAuthority:  stack.authority,
		},
	)
	server := httptest.NewServer(jobGateway.Handler())
	t.Cleanup(server.Close)

	var createOut bytes.Buffer
	if err := jobsCommand([]string{
		"create",
		"-gateway", server.URL,
		"-json",
		"-provider", "mock",
		"-model", "mock",
		"-thinking", "disabled",
		"-reasoning", "off",
		"-preset", "general",
		"-workers", "2",
		"-min-cycles", "1",
		"-duration", "1m",
		"-max-cycles", "1",
		"-max-attempts", "16",
		"-max-model-calls", "64",
		"-max-tokens", "1000000",
		"Complete the deterministic loopback multi-agent smoke test.",
	}, &createOut); err != nil {
		t.Fatal(err)
	}
	var created gatewayapi.JobResponse
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatalf("decode create output: %v\n%s", err, createOut.String())
	}
	if created.State.Spec.ID == "" || created.State.Status != jobs.JobStatusRunning || !created.Active {
		t.Fatalf("created response = %#v", created)
	}

	deadline := time.Now().Add(5 * time.Second)
	var completed gatewayapi.JobResponse
	for time.Now().Before(deadline) {
		var showOut bytes.Buffer
		if err := jobsCommand([]string{
			"show", "-gateway", server.URL, "-json", created.State.Spec.ID,
		}, &showOut); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(showOut.Bytes(), &completed); err != nil {
			t.Fatalf("decode show output: %v\n%s", err, showOut.String())
		}
		if completed.State.Status.IsTerminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.State.Status != jobs.JobStatusCompleted || completed.State.TerminalReason != jobs.TerminalReasonSuccess {
		t.Fatalf("terminal response = %#v", completed)
	}
	if completed.State.Usage.Attempts != 4 || completed.State.Usage.ModelCalls != 4 || completed.State.Usage.TotalTokens() == 0 {
		t.Fatalf("usage = %#v, want two workers plus reducer and supervisor with factual tokens", completed.State.Usage)
	}
	if completed.History.Attempts != 4 || completed.History.CompletedBatches != 3 {
		t.Fatalf("history = %#v", completed.History)
	}
	if !strings.Contains(completed.State.FinalResult, "Mock durable-job result for `control.reducer`") {
		t.Fatalf("final result = %q", completed.State.FinalResult)
	}
}

func TestJobsCLILoopbackGatewayScheduledWaitSurvivesStackRestart(t *testing.T) {
	setJobsTestConfig(t)
	workspace := t.TempDir()
	storeRoot := t.TempDir()
	cfg := config.BuiltIn()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	cfg.Thinking = "disabled"
	cfg.ReasoningEffort = "off"
	cfg.WorkspaceRoots = []string{workspace}
	cfg.ApplyModelProviderDefaults()

	registry := newToolRegistryNoMCP(cfg)
	t.Cleanup(registry.Close)
	stack1, err := newDurableJobStack(
		context.Background(),
		cfg,
		storeRoot,
		registry,
		withDurableJobMaxConcurrency(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if stopped, shutdownErr := stack1.shutdown(shutdownCtx); shutdownErr != nil || !stopped {
			t.Errorf("shutdown first durable job stack: stopped=%t err=%v", stopped, shutdownErr)
		}
	})

	firstGateway := gateway.NewServerWithOptions(
		cfg,
		provider.Mock{},
		registry,
		gateway.ServerOptions{
			JobController: stack1.manager,
			JobAuthority:  stack1.authority,
		},
	)
	firstServer := httptest.NewServer(firstGateway.Handler())
	t.Cleanup(firstServer.Close)

	var createOut bytes.Buffer
	if err := jobsCommand([]string{
		"create",
		"-gateway", firstServer.URL,
		"-json",
		"-job-id", "scheduled-restart-e2e",
		"-provider", "mock",
		"-model", "mock",
		"-thinking", "disabled",
		"-reasoning", "off",
		"-preset", "general",
		"-workers", "1",
		"-min-cycles", "1",
		"-duration", "10s",
		"-min-runtime", "2s",
		"-cadence", "2s",
		"-max-cycles", "2",
		"-max-attempts", "16",
		"-max-model-calls", "16",
		"-max-tokens", "1000000",
		"Complete two deterministic mock cycles across a durable scheduler restart.",
	}, &createOut); err != nil {
		t.Fatal(err)
	}
	var created gatewayapi.JobResponse
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatalf("decode create output: %v\n%s", err, createOut.String())
	}
	jobID := created.State.Spec.ID
	if jobID != "scheduled-restart-e2e" || created.State.Status != jobs.JobStatusRunning || !created.Active {
		t.Fatalf("created response = %#v", created)
	}

	beforeRestart := waitForJobResponse(t, firstServer.URL, jobID, 5*time.Second, func(response gatewayapi.JobResponse) bool {
		return response.State.Status == jobs.JobStatusWaiting && !response.State.NextWakeAt.IsZero()
	})
	if !beforeRestart.Active || beforeRestart.State.WaitingReason != "scheduled cycle cadence" ||
		beforeRestart.State.NextWakeAt.Before(time.Now().UTC()) || beforeRestart.State.Spec.NotBeforeComplete.IsZero() {
		t.Fatalf("scheduled wait before restart = %#v", beforeRestart)
	}
	if beforeRestart.State.Usage.Attempts != 3 || beforeRestart.State.Usage.ModelCalls != 3 ||
		beforeRestart.State.Usage.TotalTokens() == 0 || beforeRestart.History.Attempts != 3 ||
		beforeRestart.History.CompletedBatches != 3 {
		t.Fatalf("first-cycle accounting before restart: usage=%#v history=%#v", beforeRestart.State.Usage, beforeRestart.History)
	}
	persistedWake := beforeRestart.State.NextWakeAt
	persistedRevision := beforeRestart.State.Revision

	firstServer.Close()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	stopped, shutdownErr := stack1.shutdown(shutdownCtx)
	cancelShutdown()
	if shutdownErr != nil || !stopped {
		t.Fatalf("shutdown first durable job stack: stopped=%t err=%v", stopped, shutdownErr)
	}

	stack2, err := newDurableJobStack(
		context.Background(),
		cfg,
		storeRoot,
		registry,
		withDurableJobMaxConcurrency(1),
	)
	if err != nil {
		t.Fatalf("reopen and recover durable job stack: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if stopped, shutdownErr := stack2.shutdown(shutdownCtx); shutdownErr != nil || !stopped {
			t.Errorf("shutdown recovered durable job stack: stopped=%t err=%v", stopped, shutdownErr)
		}
	})
	recovered, err := stack2.manager.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State.Status != jobs.JobStatusWaiting || !recovered.Active ||
		!recovered.State.NextWakeAt.Equal(persistedWake) || recovered.State.Revision != persistedRevision ||
		recovered.State.Usage != beforeRestart.State.Usage {
		t.Fatalf("recovered wait = %#v, want persisted state %#v", recovered, beforeRestart.State)
	}

	secondGateway := gateway.NewServerWithOptions(
		cfg,
		provider.Mock{},
		registry,
		gateway.ServerOptions{
			JobController: stack2.manager,
			JobAuthority:  stack2.authority,
		},
	)
	secondServer := httptest.NewServer(secondGateway.Handler())
	t.Cleanup(secondServer.Close)
	completed := waitForJobResponse(t, secondServer.URL, jobID, 8*time.Second, func(response gatewayapi.JobResponse) bool {
		return response.State.Status.IsTerminal()
	})
	if completed.State.Status != jobs.JobStatusCompleted || completed.State.TerminalReason != jobs.TerminalReasonSuccess || completed.Active {
		t.Fatalf("terminal response after restart = %#v", completed)
	}
	if completed.State.Cycle != 2 || completed.State.Usage.Cycles != 2 || completed.State.Usage.Attempts != 6 ||
		completed.State.Usage.ModelCalls != 6 || completed.State.Usage.TotalTokens() <= beforeRestart.State.Usage.TotalTokens() {
		t.Fatalf("two-cycle usage = %#v, first cycle = %#v", completed.State.Usage, beforeRestart.State.Usage)
	}
	if completed.History.Attempts != 6 || completed.History.CompletedBatches != 6 || !completed.State.NextWakeAt.IsZero() {
		t.Fatalf("terminal history/schedule: history=%#v next_wake_at=%s", completed.History, completed.State.NextWakeAt)
	}
	if !strings.Contains(completed.State.FinalResult, "Mock durable-job result for `control.reducer`") {
		t.Fatalf("final result = %q", completed.State.FinalResult)
	}
}

func waitForJobResponse(t *testing.T, gatewayURL, jobID string, timeout time.Duration, ready func(gatewayapi.JobResponse) bool) gatewayapi.JobResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var response gatewayapi.JobResponse
	for time.Now().Before(deadline) {
		var showOut bytes.Buffer
		if err := jobsCommand([]string{"show", "-gateway", gatewayURL, "-json", jobID}, &showOut); err != nil {
			t.Fatal(err)
		}
		response = gatewayapi.JobResponse{}
		if err := json.Unmarshal(showOut.Bytes(), &response); err != nil {
			t.Fatalf("decode show output: %v\n%s", err, showOut.String())
		}
		if ready(response) {
			return response
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach expected state within %s; last response = %#v", jobID, timeout, response)
	return gatewayapi.JobResponse{}
}
