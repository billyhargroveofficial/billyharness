package gatewayapi

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

const (
	// MaxJobDurationSeconds is the public API ceiling for one durable job.
	// Longer work must be split or explicitly continued as another bounded job.
	MaxJobDurationSeconds uint64 = 7 * 24 * 60 * 60

	// Keep mandatory inline goals inside the smallest supported context and the
	// default recoverable per-attempt token envelope. Larger note corpora belong
	// in explicitly authorized read roots/artifacts rather than being duplicated
	// into every provider invocation.
	MaxJobGoalBytes                  = 64 << 10
	MaxJobBudgetCycles        uint64 = 10_000
	MaxJobBudgetAttempts      uint64 = 1_000_000
	MaxJobBudgetModelCalls    uint64 = 1_000_000
	MaxJobBudgetTokens        uint64 = 1_000_000_000_000
	MaxJobAuthorityEntries           = 128
	MaxJobAuthorityValueBytes        = 4 << 10
)

// CreateJobRequest is the credential-free public input used to compile a
// built-in durable workflow. DurationSeconds and Deadline are alternative
// ways to express the same hard wall-clock bound.
type CreateJobRequest struct {
	// JobID is optional for direct API callers. When supplied, create is
	// idempotent for this ID: an identical retry returns the existing job while
	// a different request conflicts. First-party clients generate it up front.
	JobID     string              `json:"job_id,omitempty"`
	Goal      string              `json:"goal"`
	Preset    string              `json:"preset"`
	Workers   int                 `json:"workers"`
	MinCycles uint64              `json:"min_cycles,omitempty"`
	Route     jobs.ExecutionRoute `json:"route"`
	// DurationSeconds is the hard maximum runtime. MinRuntimeSeconds fixes the
	// earliest successful-completion wall clock relative to admission; queued,
	// paused, and offline time count, so it does not promise active compute.
	// CadenceSeconds durably paces each supervisor continue instead of keeping a
	// process or provider call open.
	DurationSeconds   uint64         `json:"duration_seconds,omitempty"`
	Deadline          *time.Time     `json:"deadline,omitempty"`
	MinRuntimeSeconds uint64         `json:"min_runtime_seconds,omitempty"`
	CadenceSeconds    uint64         `json:"cadence_seconds,omitempty"`
	Budget            jobs.Budget    `json:"budget"`
	Authority         jobs.Authority `json:"authority"`
	AutoStart         bool           `json:"auto_start"`
}

// Validate checks provider-neutral request shape and hard API bounds. Use
// ResolveDeadline at admission time to apply the relative deadline and check
// an absolute deadline against the server clock.
func (r CreateJobRequest) Validate() error {
	if r.JobID != "" {
		if !validPortableJobID(r.JobID) {
			return fmt.Errorf("job_id must be a portable identifier")
		}
	}
	if strings.TrimSpace(r.Goal) == "" {
		return fmt.Errorf("goal is required")
	}
	if len(r.Goal) > MaxJobGoalBytes {
		return fmt.Errorf("goal exceeds %d bytes", MaxJobGoalBytes)
	}
	if r.Preset == "" || r.Preset != strings.TrimSpace(r.Preset) ||
		!slices.Contains(jobs.BuiltInPresetNames(), r.Preset) {
		return fmt.Errorf("preset must be a built-in preset")
	}
	if r.Workers < jobs.MinWorkers || r.Workers > jobs.MaxWorkers {
		return fmt.Errorf("workers must be between %d and %d", jobs.MinWorkers, jobs.MaxWorkers)
	}
	if err := r.Route.Validate(); err != nil {
		return fmt.Errorf("route: %w", err)
	}

	hasDuration := r.DurationSeconds != 0
	hasDeadline := r.Deadline != nil
	if hasDuration == hasDeadline {
		return fmt.Errorf("exactly one of duration_seconds or deadline is required")
	}
	if hasDuration && r.DurationSeconds > MaxJobDurationSeconds {
		return fmt.Errorf("duration_seconds must be at most %d", MaxJobDurationSeconds)
	}
	if hasDeadline && r.Deadline.IsZero() {
		return fmt.Errorf("deadline must not be zero")
	}
	if r.MinRuntimeSeconds > MaxJobDurationSeconds {
		return fmt.Errorf("min_runtime_seconds must be at most %d", MaxJobDurationSeconds)
	}
	if r.CadenceSeconds > MaxJobDurationSeconds {
		return fmt.Errorf("cadence_seconds must be at most %d", MaxJobDurationSeconds)
	}

	if err := r.Budget.Validate(); err != nil {
		return fmt.Errorf("budget: %w", err)
	}
	requiredCycles := r.MinCycles
	if requiredCycles == 0 {
		requiredCycles = 1
	}
	if requiredCycles > r.Budget.MaxCycles {
		return fmt.Errorf("min_cycles %d exceeds budget max_cycles %d", requiredCycles, r.Budget.MaxCycles)
	}
	if r.CadenceSeconds > 0 && r.Budget.MaxCycles < 2 {
		return fmt.Errorf("cadence_seconds requires budget max_cycles of at least 2")
	}
	if r.MinRuntimeSeconds > 0 {
		if r.CadenceSeconds == 0 {
			return fmt.Errorf("cadence_seconds must be greater than zero when min_runtime_seconds is set")
		}
		if r.Budget.MaxCycles < 2 {
			return fmt.Errorf("min_runtime_seconds requires budget max_cycles of at least 2")
		}
		intervals := r.Budget.MaxCycles - 1
		minimumCadence := r.MinRuntimeSeconds / intervals
		if r.MinRuntimeSeconds%intervals != 0 {
			minimumCadence++
		}
		if r.CadenceSeconds < minimumCadence {
			return fmt.Errorf(
				"cadence_seconds must be at least %d so the max_cycles schedule can span min_runtime_seconds",
				minimumCadence,
			)
		}
		scheduledCycles, ok := checkedAddUint64(1, ceilDivUint64(r.MinRuntimeSeconds, r.CadenceSeconds))
		if !ok {
			return fmt.Errorf("minimum runtime cycle requirement overflows")
		}
		if scheduledCycles > requiredCycles {
			requiredCycles = scheduledCycles
		}
	}
	if requiredCycles > r.Budget.MaxCycles {
		return fmt.Errorf("required cycles %d exceeds budget max_cycles %d", requiredCycles, r.Budget.MaxCycles)
	}
	if r.Budget.MaxCycles > MaxJobBudgetCycles {
		return fmt.Errorf("budget max_cycles must be at most %d", MaxJobBudgetCycles)
	}
	if r.Budget.MaxAttempts > MaxJobBudgetAttempts {
		return fmt.Errorf("budget max_attempts must be at most %d", MaxJobBudgetAttempts)
	}
	if r.Budget.MaxModelCalls > MaxJobBudgetModelCalls {
		return fmt.Errorf("budget max_model_calls must be at most %d", MaxJobBudgetModelCalls)
	}
	if r.Budget.MaxTokens > MaxJobBudgetTokens {
		return fmt.Errorf("budget max_tokens must be at most %d", MaxJobBudgetTokens)
	}
	workflow, err := jobs.CompilePreset(r.Preset, r.Workers)
	if err != nil {
		return fmt.Errorf("compile preset: %w", err)
	}
	// This is only a necessary arithmetic lower bound: one attempt, one model
	// call, and one token per stage-role invocation. Provider context and prompt
	// fit remain runtime concerns and are not implied by passing admission.
	invocationsPerCycle := uint64(0)
	for _, stage := range workflow.Stages {
		var ok bool
		invocationsPerCycle, ok = checkedAddUint64(invocationsPerCycle, uint64(len(stage.RoleIDs)))
		if !ok {
			return fmt.Errorf("preset invocation lower bound overflows")
		}
	}
	requiredInvocations, ok := checkedMulUint64(requiredCycles, invocationsPerCycle)
	if !ok {
		return fmt.Errorf("required invocation lower bound overflows")
	}
	for _, dimension := range []struct {
		name  string
		value uint64
	}{
		{name: "max_attempts", value: r.Budget.MaxAttempts},
		{name: "max_model_calls", value: r.Budget.MaxModelCalls},
		{name: "max_tokens", value: r.Budget.MaxTokens},
	} {
		if dimension.value < requiredInvocations {
			return fmt.Errorf(
				"budget %s %d cannot cover the minimum %d stage-role invocations",
				dimension.name,
				dimension.value,
				requiredInvocations,
			)
		}
	}
	if err := r.Authority.Validate(); err != nil {
		return fmt.Errorf("authority: %w", err)
	}
	if err := validateJobAuthorityBounds(r.Authority); err != nil {
		return err
	}
	return nil
}

// ceilDivUint64 returns ceil(numerator/denominator). Validate ensures the
// denominator is non-zero before schedule admission reaches this helper.
func ceilDivUint64(numerator, denominator uint64) uint64 {
	quotient := numerator / denominator
	if numerator%denominator != 0 {
		quotient++
	}
	return quotient
}

func checkedAddUint64(left, right uint64) (uint64, bool) {
	if ^uint64(0)-left < right {
		return 0, false
	}
	return left + right, true
}

func checkedMulUint64(left, right uint64) (uint64, bool) {
	if left != 0 && right > ^uint64(0)/left {
		return 0, false
	}
	return left * right, true
}

// validPortableJobID mirrors the public portion of the store grammar without
// coupling gateway DTOs to persistence. The store validates the boundary again
// before using the value as a path component.
func validPortableJobID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 128 || strings.HasSuffix(value, ".") {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(index > 0 && character >= '0' && character <= '9') ||
			(index > 0 && (character == '-' || character == '_' || character == '.')) {
			continue
		}
		return false
	}
	base := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CLOCK$" {
		return false
	}
	return len(base) != 4 || base[3] < '1' || base[3] > '9' || (base[:3] != "COM" && base[:3] != "LPT")
}

// ResolveDeadline validates the request and returns its absolute UTC deadline
// using now as the admission clock.
func (r CreateJobRequest) ResolveDeadline(now time.Time) (time.Time, error) {
	if err := r.Validate(); err != nil {
		return time.Time{}, err
	}
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("admission time is required")
	}
	now = now.UTC()
	if r.DurationSeconds != 0 {
		return now.Add(time.Duration(r.DurationSeconds) * time.Second), nil
	}
	deadline := r.Deadline.UTC()
	if !deadline.After(now) {
		return time.Time{}, fmt.Errorf("deadline must be in the future")
	}
	if deadline.After(now.Add(time.Duration(MaxJobDurationSeconds) * time.Second)) {
		return time.Time{}, fmt.Errorf("deadline must be within %d seconds", MaxJobDurationSeconds)
	}
	return deadline, nil
}

// ResolveSchedule compiles admission-relative public timing into absolute UTC
// policy. One second is reserved after the earliest-success gate so the hard
// deadline can remain an independent terminal bound rather than the only
// possible wake. Elapsed wall time counts regardless of useful computation.
func (r CreateJobRequest) ResolveSchedule(now time.Time) (time.Time, time.Time, error) {
	deadline, err := r.ResolveDeadline(now)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	now = now.UTC()
	notBeforeComplete := time.Time{}
	if r.MinRuntimeSeconds > 0 {
		notBeforeComplete = now.Add(time.Duration(r.MinRuntimeSeconds) * time.Second)
		if notBeforeComplete.Add(time.Second).After(deadline) {
			return time.Time{}, time.Time{}, fmt.Errorf("min_runtime_seconds must leave at least one second before the hard deadline")
		}
	}
	if r.CadenceSeconds > 0 {
		firstWake := now.Add(time.Duration(r.CadenceSeconds) * time.Second)
		if firstWake.Add(time.Second).After(deadline) {
			return time.Time{}, time.Time{}, fmt.Errorf("cadence_seconds must leave at least one second before the hard deadline")
		}
	}
	return deadline, notBeforeComplete, nil
}

func validateJobAuthorityBounds(authority jobs.Authority) error {
	for _, dimension := range []struct {
		name   string
		values []string
	}{
		{name: "tools", values: authority.Tools},
		{name: "read_roots", values: authority.ReadRoots},
		{name: "write_roots", values: authority.WriteRoots},
		{name: "network_hosts", values: authority.NetworkHosts},
		{name: "providers", values: authority.Providers},
	} {
		if len(dimension.values) > MaxJobAuthorityEntries {
			return fmt.Errorf("authority %s exceeds %d entries", dimension.name, MaxJobAuthorityEntries)
		}
		for _, value := range dimension.values {
			if len(value) > MaxJobAuthorityValueBytes {
				return fmt.Errorf("authority %s entry exceeds %d bytes", dimension.name, MaxJobAuthorityValueBytes)
			}
		}
	}
	return nil
}

// JobResponse combines the canonical durable state with ephemeral executor
// ownership. LastError must already be redacted at the gateway boundary.
type JobResponse struct {
	State     jobs.JobState     `json:"state"`
	Active    bool              `json:"active"`
	LastError string            `json:"last_error,omitempty"`
	History   JobHistorySummary `json:"history"`
}

// JobHistorySummary describes audit records intentionally omitted from the
// bounded control-plane response. Full attempts and artifacts are paged by
// their dedicated endpoints, so a long job cannot make pause/cancel/show fail
// merely because its accumulated transcript exceeds the client body limit.
type JobHistorySummary struct {
	Attempts         uint64 `json:"attempts"`
	CompletedBatches uint64 `json:"completed_batches"`
	Artifacts        uint64 `json:"artifacts"`
}

type JobSummaryResponse struct {
	ID             string              `json:"id"`
	Goal           string              `json:"goal"`
	Preset         string              `json:"preset"`
	Status         jobs.JobStatus      `json:"status"`
	TerminalReason jobs.TerminalReason `json:"terminal_reason,omitempty"`
	Revision       uint64              `json:"revision"`
	Cycle          uint64              `json:"cycle"`
	Usage          jobs.Usage          `json:"usage"`
	Deadline       time.Time           `json:"deadline"`
	Active         bool                `json:"active"`
	LastError      string              `json:"last_error,omitempty"`
}

type JobListResponse struct {
	Jobs       []JobSummaryResponse `json:"jobs"`
	Offset     int                  `json:"offset"`
	Limit      int                  `json:"limit"`
	Total      int                  `json:"total"`
	NextOffset *int                 `json:"next_offset,omitempty"`
}

type JobAttemptPage struct {
	JobID      string         `json:"job_id"`
	Offset     int            `json:"offset"`
	Limit      int            `json:"limit"`
	Total      int            `json:"total"`
	NextOffset *int           `json:"next_offset,omitempty"`
	Attempts   []jobs.Attempt `json:"attempts"`
}

type JobArtifactPage struct {
	JobID      string             `json:"job_id"`
	Offset     int                `json:"offset"`
	Limit      int                `json:"limit"`
	Total      int                `json:"total"`
	NextOffset *int               `json:"next_offset,omitempty"`
	Artifacts  []jobs.ArtifactRef `json:"artifacts"`
}
