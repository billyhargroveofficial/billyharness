package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

type agentclubSchedulerRunOptions struct {
	gatewayURL string
	configPath string
	statePath  string
	once       bool
	tick       time.Duration
	jsonOut    bool
	now        time.Time
}

type agentclubSchedulerRunSummary struct {
	SchemaVersion  int                               `json:"schema_version"`
	StatePath      string                            `json:"state_path"`
	ConfigFiles    []string                          `json:"config_files,omitempty"`
	RunID          string                            `json:"run_id"`
	ScheduleCount  int                               `json:"schedule_count"`
	DueCount       int                               `json:"due_count"`
	DeliveredCount int                               `json:"delivered_count"`
	ErrorCount     int                               `json:"error_count"`
	RunDispatched  bool                              `json:"run_dispatched"`
	Results        []agentclubSchedulerTriggerResult `json:"results,omitempty"`
}

type agentclubSchedulerTriggerResult struct {
	TriggerID      string `json:"trigger_id"`
	ScheduledAtUTC string `json:"scheduled_at_utc"`
	State          string `json:"state"`
	InputID        string `json:"input_id,omitempty"`
	Duplicate      bool   `json:"duplicate,omitempty"`
	Error          string `json:"error,omitempty"`
}

type agentclubSchedulerStatusView struct {
	SchemaVersion int                               `json:"schema_version"`
	StatePath     string                            `json:"state_path"`
	ConfigFiles   []string                          `json:"config_files,omitempty"`
	ScheduleCount int                               `json:"schedule_count"`
	EnabledCount  int                               `json:"enabled_count"`
	DueCount      int                               `json:"due_count"`
	State         agentclub.SchedulerState          `json:"state"`
	Schedules     []agentclubSchedulerScheduleState `json:"schedules,omitempty"`
}

type agentclubSchedulerScheduleState struct {
	TriggerID          string `json:"trigger_id"`
	Every              string `json:"every,omitempty"`
	Jitter             string `json:"jitter,omitempty"`
	StartAtUTC         string `json:"start_at_utc,omitempty"`
	MaxCatchup         int    `json:"max_catchup,omitempty"`
	DueCount           int    `json:"due_count"`
	LastScheduledAtUTC string `json:"last_scheduled_at_utc,omitempty"`
	LastSuccessAtUTC   string `json:"last_success_at_utc,omitempty"`
	LastErrorAtUTC     string `json:"last_error_at_utc,omitempty"`
	LastError          string `json:"last_error,omitempty"`
}

func agentclubSchedulerCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		agentclubSchedulerUsage(out)
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "run":
		return agentclubSchedulerRunCommand(args[1:], out)
	case "status":
		return agentclubSchedulerStatusCommand(args[1:], out)
	case "help", "-h", "--help":
		agentclubSchedulerUsage(out)
		return nil
	default:
		agentclubSchedulerUsage(out)
		return fmt.Errorf("unknown agentclub scheduler command %q", args[0])
	}
}

func agentclubSchedulerRunCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub scheduler run", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL")
	configPath := fs.String("path", "", "agent-club config file path override")
	statePath := fs.String("state", defaultAgentclubSchedulerStatePath(), "scheduler state JSON path")
	once := fs.Bool("once", false, "evaluate once and exit")
	tickSeconds := fs.Int("tick", 60, "tick interval in seconds for long-running mode")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub scheduler run [-gateway URL] [-path CONFIG] [-once] [-state PATH] [-tick SECONDS] [-json]")
	}
	if *tickSeconds <= 0 {
		return fmt.Errorf("-tick must be positive")
	}
	opts := agentclubSchedulerRunOptions{
		gatewayURL: *gatewayURL,
		configPath: *configPath,
		statePath:  *statePath,
		once:       *once,
		tick:       time.Duration(*tickSeconds) * time.Second,
		jsonOut:    *jsonOut,
	}
	if opts.once {
		summary, err := agentclubSchedulerRunOnce(context.Background(), opts)
		if *jsonOut {
			if writeErr := writeRedactedJSON(out, summary); writeErr != nil {
				return writeErr
			}
		} else {
			fmt.Fprint(out, formatAgentclubSchedulerRunSummary(summary))
		}
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		summary, err := agentclubSchedulerRunOnce(ctx, opts)
		if *jsonOut {
			_ = writeRedactedJSON(out, summary)
		} else {
			fmt.Fprint(out, formatAgentclubSchedulerRunSummary(summary))
		}
		if err != nil {
			fmt.Fprintf(out, "agent-club scheduler error: %s\n", secrets.Redact(err.Error()))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(opts.tick):
		}
	}
}

func agentclubSchedulerStatusCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub scheduler status", flag.ExitOnError)
	configPath := fs.String("path", "", "agent-club config file path override")
	statePath := fs.String("state", defaultAgentclubSchedulerStatePath(), "scheduler state JSON path")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub scheduler status [-path CONFIG] [-state PATH] [-json]")
	}
	status, err := buildAgentclubSchedulerStatus(*configPath, *statePath, time.Now().UTC())
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, status)
	}
	fmt.Fprint(out, formatAgentclubSchedulerStatus(status))
	return nil
}

func agentclubSchedulerRunOnce(ctx context.Context, opts agentclubSchedulerRunOptions) (agentclubSchedulerRunSummary, error) {
	now := opts.now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	loaded, files, err := loadAgentClubLocalConfig(opts.configPath)
	if err != nil {
		return agentclubSchedulerRunSummary{}, err
	}
	state, err := agentclub.LoadSchedulerState(opts.statePath)
	if err != nil {
		return agentclubSchedulerRunSummary{}, err
	}
	state.RunCount++
	runID := "scheduler-" + now.UTC().Format("20060102T150405.000000000Z")
	triggers := agentclub.EnabledScheduleTriggers(loaded.Config)
	summary := agentclubSchedulerRunSummary{
		SchemaVersion: agentclub.SchemaVersion,
		StatePath:     opts.statePath,
		ConfigFiles:   files,
		RunID:         runID,
		ScheduleCount: len(triggers),
		RunDispatched: false,
	}
	var client *gatewayclient.Client
	var firstErr error
	for _, trigger := range triggers {
		last := agentclub.SchedulerLastScheduledAt(state, trigger.ID)
		if trigger.Schedule == nil {
			continue
		}
		ticks, err := agentclub.DueScheduleTicks(trigger.ID, *trigger.Schedule, last, now)
		if err != nil {
			recordSchedulerCommandError(&state, &summary, trigger.ID, "", err, now)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		summary.DueCount += len(ticks)
		for _, scheduledAt := range ticks {
			if client == nil {
				client, err = agentclubGatewayClient(opts.gatewayURL)
				if err != nil {
					recordSchedulerCommandError(&state, &summary, trigger.ID, scheduledAt.Format(time.RFC3339Nano), err, now)
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
			}
			resp, err := deliverScheduledAgentclubTrigger(ctx, client, trigger.ID, scheduledAt, runID)
			if err != nil {
				safeErr := safeAgentClubTriggerDeliveryError(err)
				recordSchedulerCommandError(&state, &summary, trigger.ID, scheduledAt.Format(time.RFC3339Nano), safeErr, now)
				if firstErr == nil {
					firstErr = safeErr
				}
				continue
			}
			agentclub.RecordSchedulerSuccess(&state, trigger.ID, scheduledAt, now)
			summary.DeliveredCount++
			summary.Results = append(summary.Results, agentclubSchedulerTriggerResult{
				TriggerID:      trigger.ID,
				ScheduledAtUTC: scheduledAt.Format(time.RFC3339Nano),
				State:          resp.State,
				InputID:        resp.InputID,
				Duplicate:      resp.Duplicate,
			})
		}
	}
	if err := agentclub.SaveSchedulerState(opts.statePath, state); err != nil {
		return summary, err
	}
	if firstErr != nil {
		return summary, fmt.Errorf("agentclub scheduler completed with %d error(s): %w", summary.ErrorCount, firstErr)
	}
	return summary, nil
}

func deliverScheduledAgentclubTrigger(ctx context.Context, client *gatewayclient.Client, triggerID string, scheduledAt time.Time, runID string) (agentclub.TriggerDeliveryResponse, error) {
	payload, err := json.Marshal(map[string]string{
		"scheduler":        "agentclub",
		"scheduler_run_id": runID,
		"scheduled_at_utc": scheduledAt.UTC().Format(time.RFC3339Nano),
		"trigger_id":       triggerID,
	})
	if err != nil {
		return agentclub.TriggerDeliveryResponse{}, err
	}
	body, err := json.Marshal(agentclub.TriggerDeliveryRequest{
		SchemaVersion:  agentclub.SchemaVersion,
		ScheduledAtUTC: scheduledAt.UTC().Format(time.RFC3339Nano),
		Payload:        payload,
		Metadata: map[string]string{
			"scheduler":        "agentclub",
			"scheduler_run_id": runID,
		},
	})
	if err != nil {
		return agentclub.TriggerDeliveryResponse{}, err
	}
	return client.DeliverAgentClubTrigger(ctx, gatewayclient.AgentClubTriggerDelivery{
		BindingID: triggerID,
		Body:      body,
	})
}

func buildAgentclubSchedulerStatus(configPath, statePath string, now time.Time) (agentclubSchedulerStatusView, error) {
	loaded, files, err := loadAgentClubLocalConfig(configPath)
	if err != nil {
		return agentclubSchedulerStatusView{}, err
	}
	state, err := agentclub.LoadSchedulerState(statePath)
	if err != nil {
		return agentclubSchedulerStatusView{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	triggers := agentclub.EnabledScheduleTriggers(loaded.Config)
	status := agentclubSchedulerStatusView{
		SchemaVersion: agentclub.SchemaVersion,
		StatePath:     statePath,
		ConfigFiles:   files,
		ScheduleCount: len(loaded.Config.Triggers),
		EnabledCount:  len(triggers),
		State:         state,
	}
	for _, trigger := range triggers {
		entry := agentclubSchedulerScheduleState{TriggerID: trigger.ID}
		if trigger.Schedule != nil {
			entry.Every = trigger.Schedule.Every
			entry.Jitter = trigger.Schedule.Jitter
			entry.StartAtUTC = trigger.Schedule.StartAtUTC
			entry.MaxCatchup = trigger.Schedule.MaxCatchup
			last := agentclub.SchedulerLastScheduledAt(state, trigger.ID)
			ticks, err := agentclub.DueScheduleTicks(trigger.ID, *trigger.Schedule, last, now)
			if err != nil {
				entry.LastError = secrets.Redact(err.Error())
			} else {
				entry.DueCount = len(ticks)
				status.DueCount += len(ticks)
			}
		}
		if item, ok := state.Triggers[trigger.ID]; ok {
			entry.LastScheduledAtUTC = item.LastScheduledAtUTC
			entry.LastSuccessAtUTC = item.LastSuccessAtUTC
			entry.LastErrorAtUTC = item.LastErrorAtUTC
			entry.LastError = secrets.Redact(firstNonEmptyAgentclubScheduler(entry.LastError, item.LastError))
		}
		status.Schedules = append(status.Schedules, entry)
	}
	return status, nil
}

func recordSchedulerCommandError(state *agentclub.SchedulerState, summary *agentclubSchedulerRunSummary, triggerID, scheduledAt string, err error, now time.Time) {
	agentclub.RecordSchedulerError(state, triggerID, err, now)
	summary.ErrorCount++
	summary.Results = append(summary.Results, agentclubSchedulerTriggerResult{
		TriggerID:      triggerID,
		ScheduledAtUTC: scheduledAt,
		State:          "error",
		Error:          secrets.Redact(err.Error()),
	})
}

func defaultAgentclubSchedulerStatePath() string {
	return filepath.Join(config.BillyHomeDir(), "agentclub-scheduler-state.json")
}

func formatAgentclubSchedulerRunSummary(summary agentclubSchedulerRunSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent-club scheduler run: schedules=%d due=%d delivered=%d errors=%d run_dispatched=%t\n",
		summary.ScheduleCount,
		summary.DueCount,
		summary.DeliveredCount,
		summary.ErrorCount,
		summary.RunDispatched,
	)
	fmt.Fprintf(&b, "state=%s run=%s\n", summary.StatePath, secrets.Redact(summary.RunID))
	for _, result := range summary.Results {
		fmt.Fprintf(&b, "- %s scheduled_at=%s state=%s",
			secrets.Redact(result.TriggerID),
			result.ScheduledAtUTC,
			result.State,
		)
		if result.InputID != "" {
			fmt.Fprintf(&b, " input=%s", secrets.Redact(result.InputID))
		}
		if result.Duplicate {
			b.WriteString(" duplicate=true")
		}
		if result.Error != "" {
			fmt.Fprintf(&b, " error=%s", secrets.Redact(result.Error))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatAgentclubSchedulerStatus(status agentclubSchedulerStatusView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent-club scheduler: schedules=%d enabled=%d due=%d state=%s\n",
		status.ScheduleCount,
		status.EnabledCount,
		status.DueCount,
		status.StatePath,
	)
	fmt.Fprintf(&b, "runs=%d delivered=%d errors=%d updated=%s\n",
		status.State.RunCount,
		status.State.DeliveryCount,
		status.State.ErrorCount,
		status.State.UpdatedAtUTC,
	)
	for _, schedule := range status.Schedules {
		fmt.Fprintf(&b, "- %s every=%s due=%d last=%s",
			secrets.Redact(schedule.TriggerID),
			schedule.Every,
			schedule.DueCount,
			schedule.LastScheduledAtUTC,
		)
		if schedule.LastError != "" {
			fmt.Fprintf(&b, " last_error=%s", secrets.Redact(schedule.LastError))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func agentclubSchedulerUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: fast-agent-harness agentclub scheduler <command> [args]")
	fmt.Fprintln(out, "  agentclub scheduler run [-gateway URL] [-path CONFIG] [-once] [-state PATH] [-tick SECONDS] [-json]")
	fmt.Fprintln(out, "  agentclub scheduler status [-path CONFIG] [-state PATH] [-json]")
}

func firstNonEmptyAgentclubScheduler(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
