package jobs

import (
	"fmt"
	"strings"
	"time"
)

const (
	MinWorkers         = 1
	MaxWorkers         = 4
	MaxControlAttempts = 3
)

type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusWaiting   JobStatus = "waiting"
	JobStatusPaused    JobStatus = "paused"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

func (s JobStatus) Valid() bool {
	switch s {
	case JobStatusQueued, JobStatusRunning, JobStatusWaiting, JobStatusPaused,
		JobStatusCompleted, JobStatusFailed, JobStatusCancelled:
		return true
	default:
		return false
	}
}

func (s JobStatus) IsTerminal() bool {
	return s == JobStatusCompleted || s == JobStatusFailed || s == JobStatusCancelled
}

type TerminalReason string

const (
	TerminalReasonSuccess              TerminalReason = "success"
	TerminalReasonDeadline             TerminalReason = "deadline"
	TerminalReasonBudget               TerminalReason = "budget"
	TerminalReasonStagnation           TerminalReason = "stagnation"
	TerminalReasonBlocked              TerminalReason = "blocked"
	TerminalReasonUnrecoverable        TerminalReason = "unrecoverable"
	TerminalReasonOperatorCancellation TerminalReason = "operator_cancellation"
)

func (r TerminalReason) Valid() bool {
	switch r {
	case TerminalReasonSuccess, TerminalReasonDeadline, TerminalReasonBudget,
		TerminalReasonStagnation, TerminalReasonBlocked,
		TerminalReasonUnrecoverable, TerminalReasonOperatorCancellation:
		return true
	default:
		return false
	}
}

type Budget struct {
	MaxCycles     uint64 `json:"max_cycles"`
	MaxAttempts   uint64 `json:"max_attempts"`
	MaxModelCalls uint64 `json:"max_model_calls"`
	MaxTokens     uint64 `json:"max_tokens"`
}

func (b Budget) Validate() error {
	switch {
	case b.MaxCycles == 0:
		return fmt.Errorf("max_cycles must be greater than zero")
	case b.MaxAttempts == 0:
		return fmt.Errorf("max_attempts must be greater than zero")
	case b.MaxModelCalls == 0:
		return fmt.Errorf("max_model_calls must be greater than zero")
	case b.MaxTokens == 0:
		return fmt.Errorf("max_tokens must be greater than zero")
	default:
		return nil
	}
}

// ExceededBy reports whether a hard budget has been exhausted. Reaching a cap
// is terminal: callers must reserve capacity before starting more work.
func (b Budget) ExceededBy(u Usage) (bool, string) {
	switch {
	case u.Cycles >= b.MaxCycles:
		return true, "cycles"
	case u.Attempts >= b.MaxAttempts:
		return true, "attempts"
	case u.ModelCalls >= b.MaxModelCalls:
		return true, "model_calls"
	case u.TotalTokens() >= b.MaxTokens:
		return true, "tokens"
	default:
		return false, ""
	}
}

type Usage struct {
	Cycles       uint64 `json:"cycles"`
	Attempts     uint64 `json:"attempts"`
	ModelCalls   uint64 `json:"model_calls"`
	InputTokens  uint64 `json:"input_tokens"`
	OutputTokens uint64 `json:"output_tokens"`
}

// ExecutionRoute is the immutable, credential-free provider selection for a
// durable job. A resumed job must use the same route; endpoints and secrets
// are resolved by the provider adapter at invocation time.
type ExecutionRoute struct {
	ProviderID      string `json:"provider_id"`
	ModelID         string `json:"model_id"`
	Thinking        string `json:"thinking,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

func (r ExecutionRoute) Validate() error {
	for _, field := range []struct {
		label    string
		value    string
		required bool
	}{
		{label: "provider_id", value: r.ProviderID, required: true},
		{label: "model_id", value: r.ModelID, required: true},
		{label: "thinking", value: r.Thinking},
		{label: "reasoning_effort", value: r.ReasoningEffort},
	} {
		if field.required && field.value == "" {
			return fmt.Errorf("%s is required", field.label)
		}
		if field.value != strings.TrimSpace(field.value) || len(field.value) > 128 || strings.ContainsAny(field.value, "\x00\r\n") {
			return fmt.Errorf("%s must be trimmed, single-line, and at most 128 bytes", field.label)
		}
	}
	return nil
}

const WorkflowControlVersion = 1

// WorkflowControl persists the execution-relevant parts of a compiled preset.
// StageOrder is authoritative for the durable cursor; declaration order is not.
type WorkflowControl struct {
	Version          int      `json:"version"`
	StageOrder       []string `json:"stage_order"`
	WorkerRoleIDs    []string `json:"worker_role_ids"`
	SupervisorRoleID string   `json:"supervisor_role_id"`
	ReducerRoleID    string   `json:"reducer_role_id"`
}

func WorkflowControlFromWorkflow(workflow WorkflowSpec) WorkflowControl {
	return WorkflowControl{
		Version:          WorkflowControlVersion,
		StageOrder:       append([]string(nil), workflow.StageOrder...),
		WorkerRoleIDs:    append([]string(nil), workflow.WorkerRoleIDs...),
		SupervisorRoleID: workflow.SupervisorRoleID,
		ReducerRoleID:    workflow.ReducerRoleID,
	}
}

func (w WorkflowControl) Validate(roles []RoleSpec, stages []StageSpec) error {
	if w.Version != WorkflowControlVersion {
		return fmt.Errorf("workflow control version must be %d", WorkflowControlVersion)
	}
	roleByID := make(map[string]RoleSpec, len(roles))
	for _, role := range roles {
		roleByID[role.ID] = role
	}
	if w.SupervisorRoleID == "" || w.ReducerRoleID == "" || w.SupervisorRoleID == w.ReducerRoleID {
		return fmt.Errorf("workflow control requires distinct reducer and supervisor roles")
	}
	for _, roleID := range []string{w.ReducerRoleID, w.SupervisorRoleID} {
		role, ok := roleByID[roleID]
		if !ok {
			return fmt.Errorf("workflow control references unknown role %q", roleID)
		}
		if role.Writer {
			return fmt.Errorf("workflow control role %q cannot be a writer", roleID)
		}
	}
	workerSeen := make(map[string]struct{}, len(w.WorkerRoleIDs))
	for _, roleID := range w.WorkerRoleIDs {
		if _, ok := roleByID[roleID]; !ok {
			return fmt.Errorf("workflow control references unknown worker role %q", roleID)
		}
		if roleID == w.ReducerRoleID || roleID == w.SupervisorRoleID {
			return fmt.Errorf("control role %q cannot be a worker role", roleID)
		}
		if _, duplicate := workerSeen[roleID]; duplicate {
			return fmt.Errorf("workflow control repeats worker role %q", roleID)
		}
		workerSeen[roleID] = struct{}{}
	}
	if len(workerSeen) == 0 {
		return fmt.Errorf("workflow control requires worker roles")
	}
	stageByID := make(map[string]StageSpec, len(stages))
	for _, stage := range stages {
		stageByID[stage.ID] = stage
	}
	if len(w.StageOrder) != len(stages) || len(w.StageOrder) < 3 {
		return fmt.Errorf("workflow stage order must reference all stages and include work, reduce, and supervise")
	}
	stageSeen := make(map[string]struct{}, len(w.StageOrder))
	for _, stageID := range w.StageOrder {
		if _, ok := stageByID[stageID]; !ok {
			return fmt.Errorf("workflow control references unknown stage %q", stageID)
		}
		if _, duplicate := stageSeen[stageID]; duplicate {
			return fmt.Errorf("workflow control repeats stage %q", stageID)
		}
		stageSeen[stageID] = struct{}{}
	}
	reducer := stageByID[w.StageOrder[len(w.StageOrder)-2]]
	supervisor := stageByID[w.StageOrder[len(w.StageOrder)-1]]
	if len(reducer.RoleIDs) != 1 || reducer.RoleIDs[0] != w.ReducerRoleID {
		return fmt.Errorf("penultimate stage must isolate reducer role %q", w.ReducerRoleID)
	}
	if len(supervisor.RoleIDs) != 1 || supervisor.RoleIDs[0] != w.SupervisorRoleID {
		return fmt.Errorf("final stage must isolate supervisor role %q", w.SupervisorRoleID)
	}
	return nil
}

func (u Usage) Validate() error { return nil }

func (u Usage) TotalTokens() uint64 {
	if ^uint64(0)-u.InputTokens < u.OutputTokens {
		return ^uint64(0)
	}
	return u.InputTokens + u.OutputTokens
}

type JobSpec struct {
	ID      string `json:"id"`
	Goal    string `json:"goal"`
	Preset  string `json:"preset"`
	Workers int    `json:"workers"`
	// CreateRequestHash binds an idempotent public create request to this
	// immutable spec. It is empty for strict/internal creates and a canonical
	// lowercase SHA-256 digest for client-ID based creates.
	CreateRequestHash string `json:"create_request_hash,omitempty"`
	// MinCycles is a quality-floor, not a wall-clock promise. Zero preserves
	// legacy/default behavior and means one complete workflow cycle.
	MinCycles uint64 `json:"min_cycles,omitempty"`
	// NotBeforeComplete is an optional absolute UTC earliest-success gate.
	// Successful completion is rejected before it; queueing, pauses, and daemon
	// downtime still advance this wall clock, so it is not a compute guarantee.
	// Deadline remains the independent hard wall-clock cap.
	NotBeforeComplete time.Time `json:"not_before_complete,omitzero"`
	// CycleCadenceSeconds is the durable minimum delay between a continue
	// decision and admission of the next cycle. Zero disables pacing.
	CycleCadenceSeconds uint64          `json:"cycle_cadence_seconds,omitempty"`
	Deadline            time.Time       `json:"deadline"`
	Budget              Budget          `json:"budget"`
	Route               ExecutionRoute  `json:"route"`
	Workflow            WorkflowControl `json:"workflow"`
	Authority           Authority       `json:"authority"`
	Roles               []RoleSpec      `json:"roles"`
	Stages              []StageSpec     `json:"stages"`
}

func (s JobSpec) Validate() error {
	if err := validateID("job id", s.ID); err != nil {
		return err
	}
	if s.CreateRequestHash != "" && !validLowerSHA256(s.CreateRequestHash) {
		return fmt.Errorf("create_request_hash must be a lowercase SHA-256 digest")
	}
	if strings.TrimSpace(s.Goal) == "" {
		return fmt.Errorf("goal is required")
	}
	if strings.TrimSpace(s.Preset) == "" {
		return fmt.Errorf("preset is required")
	}
	if s.Workers < MinWorkers || s.Workers > MaxWorkers {
		return fmt.Errorf("workers must be between %d and %d", MinWorkers, MaxWorkers)
	}
	if s.Deadline.IsZero() {
		return fmt.Errorf("deadline is required")
	}
	if s.Deadline.Location() != time.UTC {
		return fmt.Errorf("deadline must be UTC")
	}
	if s.CycleCadenceSeconds > uint64((1<<63-1)/int64(time.Second)) {
		return fmt.Errorf("cycle_cadence_seconds is too large")
	}
	if s.CycleCadenceSeconds > 0 && s.Budget.MaxCycles < 2 {
		return fmt.Errorf("cycle_cadence_seconds requires max_cycles of at least 2")
	}
	if !s.NotBeforeComplete.IsZero() {
		if s.NotBeforeComplete.Location() != time.UTC {
			return fmt.Errorf("not_before_complete must be UTC")
		}
		if !s.NotBeforeComplete.Before(s.Deadline) {
			return fmt.Errorf("not_before_complete must be before deadline")
		}
		if s.Deadline.Sub(s.NotBeforeComplete) < time.Second {
			return fmt.Errorf("not_before_complete must leave at least one second before deadline")
		}
		if s.CycleCadenceSeconds == 0 {
			return fmt.Errorf("cycle_cadence_seconds must be greater than zero when not_before_complete is set")
		}
	}
	if err := s.Budget.Validate(); err != nil {
		return fmt.Errorf("budget: %w", err)
	}
	if s.EffectiveMinCycles() > s.Budget.MaxCycles {
		return fmt.Errorf("min_cycles %d exceeds budget max_cycles %d", s.EffectiveMinCycles(), s.Budget.MaxCycles)
	}
	if err := s.Route.Validate(); err != nil {
		return fmt.Errorf("route: %w", err)
	}
	if err := s.Authority.Validate(); err != nil {
		return fmt.Errorf("authority: %w", err)
	}
	if len(s.Roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}
	roleIDs := make(map[string]struct{}, len(s.Roles))
	writerCount := 0
	for i, role := range s.Roles {
		if err := role.Validate(); err != nil {
			return fmt.Errorf("role %d: %w", i, err)
		}
		if _, exists := roleIDs[role.ID]; exists {
			return fmt.Errorf("duplicate role %q", role.ID)
		}
		roleIDs[role.ID] = struct{}{}
		if role.Writer {
			writerCount++
		}
	}
	if writerCount > 1 {
		return fmt.Errorf("job may declare at most one writer role")
	}
	if len(s.Stages) == 0 {
		return fmt.Errorf("at least one stage is required")
	}
	stageIDs := make(map[string]struct{}, len(s.Stages))
	for i, stage := range s.Stages {
		if err := stage.Validate(); err != nil {
			return fmt.Errorf("stage %d: %w", i, err)
		}
		if _, exists := stageIDs[stage.ID]; exists {
			return fmt.Errorf("duplicate stage %q", stage.ID)
		}
		stageIDs[stage.ID] = struct{}{}
		stageWriters := 0
		for _, roleID := range stage.RoleIDs {
			if _, exists := roleIDs[roleID]; !exists {
				return fmt.Errorf("stage %q references unknown role %q", stage.ID, roleID)
			}
			for _, role := range s.Roles {
				if role.ID == roleID && role.Writer {
					stageWriters++
				}
			}
		}
		if stageWriters > 0 && len(stage.RoleIDs) != 1 {
			return fmt.Errorf("stage %q must isolate its writer", stage.ID)
		}
	}
	if err := s.Workflow.Validate(s.Roles, s.Stages); err != nil {
		return fmt.Errorf("workflow: %w", err)
	}
	return nil
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (s JobSpec) EffectiveMinCycles() uint64 {
	if s.MinCycles == 0 {
		return 1
	}
	return s.MinCycles
}

type CompletedBatch struct {
	ID          string `json:"id"`
	StageID     string `json:"stage_id"`
	Cycle       uint64 `json:"cycle"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func (b CompletedBatch) Validate() error {
	if err := validateID("completed batch id", b.ID); err != nil {
		return err
	}
	if err := validateID("completed stage id", b.StageID); err != nil {
		return err
	}
	if b.Cycle == 0 {
		return fmt.Errorf("completed batch cycle must be greater than zero")
	}
	return nil
}

type JobState struct {
	Spec             JobSpec          `json:"spec"`
	Status           JobStatus        `json:"status"`
	TerminalReason   TerminalReason   `json:"terminal_reason,omitempty"`
	Revision         uint64           `json:"revision"`
	Cycle            uint64           `json:"cycle"`
	NextStageIndex   int              `json:"next_stage_index"`
	Usage            Usage            `json:"usage"`
	CurrentBatch     *WorkBatch       `json:"current_batch,omitempty"`
	Attempts         []Attempt        `json:"attempts,omitempty"`
	CompletedBatches []CompletedBatch `json:"completed_batches,omitempty"`
	Artifacts        []ArtifactRef    `json:"artifacts,omitempty"`
	LastDecision     *Decision        `json:"last_decision,omitempty"`
	// FinalResult is the canonical deliverable produced by the last successful
	// reducer when the supervisor accepts the job as complete. Attempt history
	// remains audit data; callers should not have to reverse-engineer it to find
	// the answer they asked the job to produce.
	FinalResult            string         `json:"final_result,omitempty"`
	StagnationFingerprints []string       `json:"stagnation_fingerprints,omitempty"`
	CancelRequested        bool           `json:"cancel_requested,omitempty"`
	PendingStop            TerminalReason `json:"pending_stop,omitempty"`
	WaitingReason          string         `json:"waiting_reason,omitempty"`
	// NextWakeAt identifies a scheduler-owned cadence wait. A model-requested
	// wait leaves it zero and requires explicit Resume, while the service still
	// watches the hard deadline. Operator pause may preserve a scheduled wake.
	NextWakeAt  time.Time `json:"next_wake_at,omitzero"`
	LastEventID string    `json:"last_event_id,omitempty"`
}

func (s JobState) Validate() error {
	if err := s.Spec.Validate(); err != nil {
		return fmt.Errorf("spec: %w", err)
	}
	if !s.Status.Valid() {
		return fmt.Errorf("invalid job status %q", s.Status)
	}
	if s.Status.IsTerminal() {
		if !s.TerminalReason.Valid() {
			return fmt.Errorf("terminal status requires a valid terminal reason")
		}
	} else if s.TerminalReason != "" {
		return fmt.Errorf("non-terminal status cannot have terminal reason %q", s.TerminalReason)
	}
	if s.PendingStop != "" {
		switch s.PendingStop {
		case TerminalReasonOperatorCancellation, TerminalReasonDeadline, TerminalReasonBudget, TerminalReasonUnrecoverable:
		default:
			return fmt.Errorf("invalid pending stop reason %q", s.PendingStop)
		}
		if s.Status.IsTerminal() {
			return fmt.Errorf("terminal job cannot retain pending stop %q", s.PendingStop)
		}
	}
	if !s.NextWakeAt.IsZero() {
		if s.NextWakeAt.Location() != time.UTC {
			return fmt.Errorf("next_wake_at must be UTC")
		}
		if s.Status != JobStatusWaiting && s.Status != JobStatusPaused {
			return fmt.Errorf("next_wake_at is only valid for waiting or paused jobs")
		}
		if s.NextWakeAt.After(s.Spec.Deadline) {
			return fmt.Errorf("next_wake_at cannot exceed deadline")
		}
	}
	if s.NextStageIndex < 0 || s.NextStageIndex > len(s.Spec.Workflow.StageOrder) {
		return fmt.Errorf("next stage index %d is outside workflow", s.NextStageIndex)
	}
	for index, batch := range s.CompletedBatches {
		if err := batch.Validate(); err != nil {
			return fmt.Errorf("completed batch %d: %w", index, err)
		}
	}
	if s.CurrentBatch != nil {
		if err := s.CurrentBatch.Validate(); err != nil {
			return fmt.Errorf("current batch: %w", err)
		}
		if s.Status != JobStatusRunning {
			return fmt.Errorf("only a running job may have a current batch")
		}
	}
	attemptIDs := make(map[string]struct{}, len(s.Attempts))
	running := 0
	for index, attempt := range s.Attempts {
		if err := attempt.Validate(); err != nil {
			return fmt.Errorf("attempt %d: %w", index, err)
		}
		if _, duplicate := attemptIDs[attempt.ID]; duplicate {
			return fmt.Errorf("duplicate attempt ID %q", attempt.ID)
		}
		attemptIDs[attempt.ID] = struct{}{}
		if attempt.Status == AttemptStatusRunning {
			running++
			if s.CurrentBatch == nil || attempt.BatchID != s.CurrentBatch.ID {
				return fmt.Errorf("running attempt %q is outside the current batch", attempt.ID)
			}
		}
	}
	if s.Status.IsTerminal() && running != 0 {
		return fmt.Errorf("terminal job cannot retain running attempts")
	}
	artifactIDs := make(map[string]struct{}, len(s.Artifacts))
	for index, artifact := range s.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("artifact %d: %w", index, err)
		}
		if _, duplicate := artifactIDs[artifact.ID]; duplicate {
			return fmt.Errorf("duplicate artifact ID %q", artifact.ID)
		}
		artifactIDs[artifact.ID] = struct{}{}
	}
	if s.LastDecision != nil {
		if err := s.LastDecision.Validate(); err != nil {
			return fmt.Errorf("last decision: %w", err)
		}
	}
	if s.FinalResult != "" && (s.Status != JobStatusCompleted || s.TerminalReason != TerminalReasonSuccess) {
		return fmt.Errorf("final_result is only valid for a successfully completed job")
	}
	return s.Usage.Validate()
}

type RoleSpec struct {
	ID        string    `json:"id"`
	Purpose   string    `json:"purpose"`
	Authority Authority `json:"authority"`
	Writer    bool      `json:"writer,omitempty"`
}

func (r RoleSpec) Validate() error {
	if err := validateID("role id", r.ID); err != nil {
		return err
	}
	if strings.TrimSpace(r.Purpose) == "" {
		return fmt.Errorf("role %q purpose is required", r.ID)
	}
	if err := r.Authority.Validate(); err != nil {
		return fmt.Errorf("role %q authority: %w", r.ID, err)
	}
	return nil
}

type BarrierPolicy string

const BarrierAll BarrierPolicy = "all"

type StageSpec struct {
	ID         string        `json:"id"`
	RoleIDs    []string      `json:"role_ids"`
	MaxWorkers int           `json:"max_workers"`
	Barrier    BarrierPolicy `json:"barrier"`
}

func (s StageSpec) Validate() error {
	if err := validateID("stage id", s.ID); err != nil {
		return err
	}
	if s.MaxWorkers < MinWorkers || s.MaxWorkers > MaxWorkers {
		return fmt.Errorf("stage %q max_workers must be between %d and %d", s.ID, MinWorkers, MaxWorkers)
	}
	if s.Barrier != BarrierAll {
		return fmt.Errorf("stage %q barrier must be %q", s.ID, BarrierAll)
	}
	if len(s.RoleIDs) == 0 || len(s.RoleIDs) > s.MaxWorkers {
		return fmt.Errorf("stage %q must declare between 1 and max_workers role ids", s.ID)
	}
	seen := make(map[string]struct{}, len(s.RoleIDs))
	for _, roleID := range s.RoleIDs {
		if err := validateID("role id", roleID); err != nil {
			return fmt.Errorf("stage %q: %w", s.ID, err)
		}
		if _, exists := seen[roleID]; exists {
			return fmt.Errorf("stage %q contains duplicate role %q", s.ID, roleID)
		}
		seen[roleID] = struct{}{}
	}
	return nil
}

type WorkBatch struct {
	ID      string        `json:"id"`
	StageID string        `json:"stage_id"`
	Cycle   uint64        `json:"cycle"`
	Barrier BarrierPolicy `json:"barrier"`
	Items   []WorkItem    `json:"items"`
}

func (b WorkBatch) Validate() error {
	if err := validateID("batch id", b.ID); err != nil {
		return err
	}
	if err := validateID("stage id", b.StageID); err != nil {
		return err
	}
	if b.Cycle == 0 {
		return fmt.Errorf("batch %q cycle must be greater than zero", b.ID)
	}
	if b.Barrier != BarrierAll {
		return fmt.Errorf("batch %q barrier must be %q", b.ID, BarrierAll)
	}
	if len(b.Items) < MinWorkers || len(b.Items) > MaxWorkers {
		return fmt.Errorf("batch %q must contain between %d and %d items", b.ID, MinWorkers, MaxWorkers)
	}
	seen := make(map[string]struct{}, len(b.Items))
	previous := ""
	for i, item := range b.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("batch %q item %d: %w", b.ID, i, err)
		}
		if _, exists := seen[item.ID]; exists {
			return fmt.Errorf("batch %q contains duplicate item %q", b.ID, item.ID)
		}
		if previous != "" && item.ID < previous {
			return fmt.Errorf("batch %q items must be ordered by id", b.ID)
		}
		seen[item.ID] = struct{}{}
		previous = item.ID
	}
	return nil
}

type WorkItem struct {
	ID        string    `json:"id"`
	RoleID    string    `json:"role_id"`
	Objective string    `json:"objective"`
	Authority Authority `json:"authority"`
}

func (w WorkItem) Validate() error {
	if err := validateID("work item id", w.ID); err != nil {
		return err
	}
	if err := validateID("role id", w.RoleID); err != nil {
		return err
	}
	if strings.TrimSpace(w.Objective) == "" {
		return fmt.Errorf("work item %q objective is required", w.ID)
	}
	if err := w.Authority.Validate(); err != nil {
		return fmt.Errorf("work item %q authority: %w", w.ID, err)
	}
	return nil
}

type AttemptStatus string

const (
	AttemptStatusQueued    AttemptStatus = "queued"
	AttemptStatusRunning   AttemptStatus = "running"
	AttemptStatusSucceeded AttemptStatus = "succeeded"
	AttemptStatusFailed    AttemptStatus = "failed"
	AttemptStatusCancelled AttemptStatus = "cancelled"
	AttemptStatusAbandoned AttemptStatus = "abandoned"
	AttemptStatusAmbiguous AttemptStatus = "ambiguous"
)

func (s AttemptStatus) Valid() bool {
	switch s {
	case AttemptStatusQueued, AttemptStatusRunning, AttemptStatusSucceeded,
		AttemptStatusFailed, AttemptStatusCancelled, AttemptStatusAbandoned,
		AttemptStatusAmbiguous:
		return true
	default:
		return false
	}
}

type Attempt struct {
	ID          string             `json:"id"`
	BatchID     string             `json:"batch_id"`
	WorkItemID  string             `json:"work_item_id"`
	RoleID      string             `json:"role_id"`
	AttemptNo   uint64             `json:"attempt_no"`
	Cycle       uint64             `json:"cycle"`
	StageID     string             `json:"stage_id"`
	Reservation AttemptReservation `json:"reservation"`
	// Dispatched distinguishes a terminal outcome observed after the external
	// invoker was admitted from a finish that the live runtime proved never
	// crossed the dispatch boundary. A persisted running attempt is always
	// false; crash recovery treats its dispatch state as unknown and therefore
	// finishes it conservatively as dispatched.
	Dispatched  bool          `json:"dispatched"`
	Status      AttemptStatus `json:"status"`
	Result      string        `json:"result,omitempty"`
	Fingerprint string        `json:"fingerprint,omitempty"`
	Artifacts   []ArtifactRef `json:"artifacts,omitempty"`
	Usage       Usage         `json:"usage"`
	Error       string        `json:"error,omitempty"`
	Decision    *Decision     `json:"decision,omitempty"`
}

// AttemptReservation is persisted before dispatch so parallel attempts cannot
// each spend the same remaining model/token budget.
type AttemptReservation struct {
	ModelCalls      uint64 `json:"model_calls"`
	Tokens          uint64 `json:"tokens"`
	MaxOutputTokens uint64 `json:"max_output_tokens"`
}

func (r AttemptReservation) Validate() error {
	if r.ModelCalls == 0 || r.Tokens == 0 || r.MaxOutputTokens == 0 {
		return fmt.Errorf("attempt reservation limits must be positive")
	}
	if r.MaxOutputTokens > r.Tokens {
		return fmt.Errorf("attempt max output tokens cannot exceed reserved tokens")
	}
	return nil
}

func (a Attempt) Validate() error {
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "attempt id", value: a.ID},
		{label: "batch id", value: a.BatchID},
		{label: "work item id", value: a.WorkItemID},
		{label: "role id", value: a.RoleID},
	} {
		if err := validateID(field.label, field.value); err != nil {
			return err
		}
	}
	if !a.Status.Valid() {
		return fmt.Errorf("attempt %q has invalid status %q", a.ID, a.Status)
	}
	if a.AttemptNo == 0 || a.Cycle == 0 {
		return fmt.Errorf("attempt %q requires positive attempt_no and cycle", a.ID)
	}
	if err := validateID("stage id", a.StageID); err != nil {
		return err
	}
	if err := a.Reservation.Validate(); err != nil {
		return fmt.Errorf("attempt %q reservation: %w", a.ID, err)
	}
	if a.Usage.Cycles != 0 || a.Usage.Attempts != 0 {
		return fmt.Errorf("attempt %q usage cannot record cycles or attempts", a.ID)
	}
	if a.Status == AttemptStatusRunning {
		if a.Dispatched || a.Result != "" || a.Fingerprint != "" || len(a.Artifacts) != 0 || a.Error != "" || a.Decision != nil || a.Usage != (Usage{}) {
			return fmt.Errorf("running attempt %q cannot contain terminal output", a.ID)
		}
	} else if a.Status == AttemptStatusQueued {
		return fmt.Errorf("attempt %q cannot be persisted as queued", a.ID)
	}
	if !a.Dispatched && a.Usage != (Usage{}) {
		return fmt.Errorf("undispatched attempt %q cannot report provider usage", a.ID)
	}
	if a.Status == AttemptStatusSucceeded && !a.Dispatched {
		return fmt.Errorf("successful attempt %q must have crossed dispatch", a.ID)
	}
	if a.Status == AttemptStatusAmbiguous && !a.Dispatched {
		return fmt.Errorf("ambiguous attempt %q must have crossed dispatch", a.ID)
	}
	if a.Decision != nil {
		if a.Status != AttemptStatusSucceeded {
			return fmt.Errorf("only a successful attempt may carry a decision")
		}
		if err := a.Decision.Validate(); err != nil {
			return fmt.Errorf("attempt decision: %w", err)
		}
	}
	if a.Usage.ModelCalls > a.Reservation.ModelCalls || a.Usage.TotalTokens() > a.Reservation.Tokens {
		return fmt.Errorf("attempt %q usage exceeds its reservation", a.ID)
	}
	maxOutput := ^uint64(0)
	if a.Usage.ModelCalls == 0 {
		maxOutput = 0
	} else if a.Reservation.MaxOutputTokens <= ^uint64(0)/a.Usage.ModelCalls {
		maxOutput = a.Reservation.MaxOutputTokens * a.Usage.ModelCalls
	}
	if a.Usage.OutputTokens > maxOutput {
		return fmt.Errorf("attempt %q output usage exceeds its per-call reservation", a.ID)
	}
	for i, artifact := range a.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("attempt %q artifact %d: %w", a.ID, i, err)
		}
	}
	return a.Usage.Validate()
}

type ArtifactRef struct {
	ID                 string `json:"id"`
	URI                string `json:"uri"`
	SHA256             string `json:"sha256,omitempty"`
	MediaType          string `json:"media_type,omitempty"`
	CreatedByAttemptID string `json:"created_by_attempt_id,omitempty"`
}

func (a ArtifactRef) Validate() error {
	if err := validateID("artifact id", a.ID); err != nil {
		return err
	}
	if strings.TrimSpace(a.URI) == "" {
		return fmt.Errorf("artifact %q uri is required", a.ID)
	}
	if a.CreatedByAttemptID != "" {
		if err := validateID("created_by_attempt_id", a.CreatedByAttemptID); err != nil {
			return err
		}
	}
	return nil
}

type DecisionKind string

const (
	DecisionContinue DecisionKind = "continue"
	DecisionComplete DecisionKind = "complete"
	DecisionWait     DecisionKind = "wait"
	DecisionBlocked  DecisionKind = "blocked"
)

func (k DecisionKind) Valid() bool {
	switch k {
	case DecisionContinue, DecisionComplete, DecisionWait, DecisionBlocked:
		return true
	default:
		return false
	}
}

type Decision struct {
	Kind        DecisionKind `json:"kind"`
	Reason      string       `json:"reason"`
	NextBatch   *WorkBatch   `json:"next_batch,omitempty"`
	Fingerprint string       `json:"fingerprint,omitempty"`
}

func (d Decision) Validate() error {
	if !d.Kind.Valid() {
		return fmt.Errorf("invalid decision kind %q", d.Kind)
	}
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("decision reason is required")
	}
	if d.Kind == DecisionContinue {
		if d.NextBatch == nil {
			return fmt.Errorf("continue decision requires next_batch")
		}
		if err := d.NextBatch.Validate(); err != nil {
			return fmt.Errorf("next_batch: %w", err)
		}
		if strings.TrimSpace(d.Fingerprint) == "" {
			return fmt.Errorf("continue decision requires fingerprint")
		}
	} else if d.NextBatch != nil {
		return fmt.Errorf("%s decision cannot include next_batch", d.Kind)
	}
	return nil
}

// ValidateBatchForSpec proves that a supervisor-proposed batch stays inside
// the immutable job catalog, concurrency cap, cycle cap, and authority.
func ValidateBatchForSpec(spec JobSpec, batch WorkBatch) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("job spec: %w", err)
	}
	if err := batch.Validate(); err != nil {
		return err
	}
	if len(batch.Items) > spec.Workers {
		return fmt.Errorf("batch %q has %d items, exceeding job worker cap %d", batch.ID, len(batch.Items), spec.Workers)
	}
	if batch.Cycle > spec.Budget.MaxCycles {
		return fmt.Errorf("batch %q cycle %d exceeds job cycle cap %d", batch.ID, batch.Cycle, spec.Budget.MaxCycles)
	}

	var stage *StageSpec
	for i := range spec.Stages {
		if spec.Stages[i].ID == batch.StageID {
			stage = &spec.Stages[i]
			break
		}
	}
	if stage == nil {
		return fmt.Errorf("batch %q references undeclared stage %q", batch.ID, batch.StageID)
	}
	if len(batch.Items) > stage.MaxWorkers {
		return fmt.Errorf("batch %q exceeds stage %q worker cap %d", batch.ID, stage.ID, stage.MaxWorkers)
	}

	roles := make(map[string]RoleSpec, len(spec.Roles))
	for _, role := range spec.Roles {
		roles[role.ID] = role
	}
	stageRoles := make(map[string]struct{}, len(stage.RoleIDs))
	for _, roleID := range stage.RoleIDs {
		stageRoles[roleID] = struct{}{}
	}
	usedRoles := make(map[string]struct{}, len(batch.Items))
	writers := 0
	for _, item := range batch.Items {
		role, exists := roles[item.RoleID]
		if !exists {
			return fmt.Errorf("work item %q references undeclared role %q", item.ID, item.RoleID)
		}
		if _, allowed := stageRoles[item.RoleID]; !allowed {
			return fmt.Errorf("work item %q role %q is not declared for stage %q", item.ID, item.RoleID, stage.ID)
		}
		if _, duplicate := usedRoles[item.RoleID]; duplicate {
			return fmt.Errorf("batch %q repeats role %q", batch.ID, item.RoleID)
		}
		usedRoles[item.RoleID] = struct{}{}
		roleAuthority, err := IntersectAuthority(spec.Authority, role.Authority)
		if err != nil {
			return fmt.Errorf("work item %q role authority: %w", item.ID, err)
		}
		if err := ValidateChildAuthority(roleAuthority, item.Authority); err != nil {
			return fmt.Errorf("work item %q: %w", item.ID, err)
		}
		if role.Writer {
			writers++
		}
	}
	if writers > 1 {
		return fmt.Errorf("batch %q contains concurrent writers", batch.ID)
	}
	if writers == 1 && len(batch.Items) != 1 {
		return fmt.Errorf("batch %q must isolate its writer", batch.ID)
	}
	return nil
}

func validateID(label, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 128 {
		return fmt.Errorf("%s must be a non-empty portable identifier of at most 128 bytes", label)
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9') ||
			(i > 0 && (r == '-' || r == '_' || r == '.' || r == ':')) {
			continue
		}
		return fmt.Errorf("%s %q is not a portable identifier", label, value)
	}
	return nil
}
