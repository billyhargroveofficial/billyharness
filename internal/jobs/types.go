package jobs

import (
	"fmt"
	"strings"
	"time"
)

const (
	MinWorkers = 1
	MaxWorkers = 4
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

func (u Usage) Validate() error { return nil }

func (u Usage) TotalTokens() uint64 {
	if ^uint64(0)-u.InputTokens < u.OutputTokens {
		return ^uint64(0)
	}
	return u.InputTokens + u.OutputTokens
}

type JobSpec struct {
	ID        string      `json:"id"`
	Goal      string      `json:"goal"`
	Preset    string      `json:"preset"`
	Workers   int         `json:"workers"`
	Deadline  time.Time   `json:"deadline"`
	Budget    Budget      `json:"budget"`
	Authority Authority   `json:"authority"`
	Roles     []RoleSpec  `json:"roles"`
	Stages    []StageSpec `json:"stages"`
}

func (s JobSpec) Validate() error {
	if err := validateID("job id", s.ID); err != nil {
		return err
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
	if err := s.Budget.Validate(); err != nil {
		return fmt.Errorf("budget: %w", err)
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
		for _, roleID := range stage.RoleIDs {
			if _, exists := roleIDs[roleID]; !exists {
				return fmt.Errorf("stage %q references unknown role %q", stage.ID, roleID)
			}
		}
	}
	return nil
}

type JobState struct {
	Spec                   JobSpec        `json:"spec"`
	Status                 JobStatus      `json:"status"`
	TerminalReason         TerminalReason `json:"terminal_reason,omitempty"`
	Revision               uint64         `json:"revision"`
	Cycle                  uint64         `json:"cycle"`
	Usage                  Usage          `json:"usage"`
	CurrentBatch           *WorkBatch     `json:"current_batch,omitempty"`
	Attempts               []Attempt      `json:"attempts,omitempty"`
	Artifacts              []ArtifactRef  `json:"artifacts,omitempty"`
	LastDecision           *Decision      `json:"last_decision,omitempty"`
	StagnationFingerprints []string       `json:"stagnation_fingerprints,omitempty"`
	CancelRequested        bool           `json:"cancel_requested,omitempty"`
	WaitingReason          string         `json:"waiting_reason,omitempty"`
	LastEventID            string         `json:"last_event_id,omitempty"`
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
)

func (s AttemptStatus) Valid() bool {
	switch s {
	case AttemptStatusQueued, AttemptStatusRunning, AttemptStatusSucceeded,
		AttemptStatusFailed, AttemptStatusCancelled:
		return true
	default:
		return false
	}
}

type Attempt struct {
	ID          string        `json:"id"`
	BatchID     string        `json:"batch_id"`
	WorkItemID  string        `json:"work_item_id"`
	RoleID      string        `json:"role_id"`
	Status      AttemptStatus `json:"status"`
	Result      string        `json:"result,omitempty"`
	Fingerprint string        `json:"fingerprint,omitempty"`
	Artifacts   []ArtifactRef `json:"artifacts,omitempty"`
	Usage       Usage         `json:"usage"`
	Error       string        `json:"error,omitempty"`
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
