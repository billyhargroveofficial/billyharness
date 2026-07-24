// Package jobruntime composes durable jobs with provider-neutral invocation
// adapters. Provider transports and model-specific payloads stay behind
// Invoker.
package jobruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

const (
	MaxInvocationGoalBytes           = 256 << 10
	MaxInvocationTextBytes           = 64 << 10
	MaxInvocationAuthorityEntries    = 256
	MaxInvocationAuthorityEntryBytes = 8 << 10
	MaxInvocationPriorAttempts       = 128
	MaxInvocationArtifacts           = 128
	MaxInvocationPriorResultBytes    = 512 << 10
	MaxInvocationPriorPayloadBytes   = 1 << 20
	MaxInvocationResultBytes         = 256 << 10
	MaxInvocationErrorBytes          = 16 << 10
	MaxInvocationResultArtifacts     = 64
	MaxInvocationArtifactFieldBytes  = 8 << 10
	MaxSupervisorProposalBytes       = 64 << 10
	MaxSupervisorReasonBytes         = 8 << 10
	MaxSupervisorObjectiveBytes      = 32 << 10
	MaxSupervisorProposalObjectives  = jobs.MaxWorkers
)

// InvocationKind identifies the role an invocation plays in one workflow
// cycle. It is runtime structure, not a provider or model identifier.
type InvocationKind string

const (
	InvocationKindWorker     InvocationKind = "worker"
	InvocationKindReducer    InvocationKind = "reducer"
	InvocationKindSupervisor InvocationKind = "supervisor"
)

func (k InvocationKind) Valid() bool {
	switch k {
	case InvocationKindWorker, InvocationKindReducer, InvocationKindSupervisor:
		return true
	default:
		return false
	}
}

// Invoker executes one already-authorized, bounded model attempt. Implementors
// normalize provider-specific termination and usage before returning.
type Invoker interface {
	Invoke(context.Context, Invocation) (InvocationResult, error)
}

// DispatchProvenance describes whether an Invoker crossed the external
// provider boundary before failing. The runtime treats an untyped error as
// dispatched because retrying an unknown outcome must fail closed.
type DispatchProvenance string

const (
	DispatchNotDispatched DispatchProvenance = "not_dispatched"
	DispatchDispatched    DispatchProvenance = "dispatched"
)

// UsageProvenance describes whether InvocationResult.Usage is the complete,
// factual usage for a failed dispatched invocation. Unknown usage is charged
// conservatively from the durable reservation.
type UsageProvenance string

const (
	UsageUnknown UsageProvenance = "unknown"
	UsageFactual UsageProvenance = "factual"
	// UsageNoGeneration means a provider request was explicitly rejected by an
	// HTTP response before a model stream or local tool execution. A generic
	// transport failure is not enough: request bytes may have reached the remote
	// service, so its generation and billing remain unknown. ModelCalls records
	// rejected requests while locally observed token usage is zero.
	UsageNoGeneration UsageProvenance = "no_generation"
)

// InvocationFailure carries failure provenance across the provider-neutral
// Invoker boundary. Cause remains available through errors.Is/errors.As.
//
// Adapters must use DispatchNotDispatched only when they can prove that no
// provider request was attempted. DispatchDispatched+UsageFactual promises
// that InvocationResult.Usage is complete rather than a streamed prefix.
type InvocationFailure struct {
	Cause      error
	Dispatch   DispatchProvenance
	Usage      UsageProvenance
	Transient  bool
	Fatal      bool
	RetryAfter time.Duration
}

func (e *InvocationFailure) Error() string {
	if e == nil || e.Cause == nil {
		return "invocation failed"
	}
	return e.Cause.Error()
}

func (e *InvocationFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewInvocationFailure annotates an Invoker error with externally meaningful
// provenance. Invalid combinations fail closed as a plain contract error.
func NewInvocationFailure(cause error, dispatch DispatchProvenance, usage UsageProvenance) error {
	if cause == nil {
		return nil
	}
	switch dispatch {
	case DispatchNotDispatched:
		if usage != UsageUnknown {
			return fmt.Errorf("invalid invocation failure provenance: undispatched failure cannot have factual usage: %w", cause)
		}
	case DispatchDispatched:
		if usage != UsageUnknown && usage != UsageFactual && usage != UsageNoGeneration {
			return fmt.Errorf("invalid invocation failure usage provenance %q: %w", usage, cause)
		}
	default:
		return fmt.Errorf("invalid invocation failure dispatch provenance %q: %w", dispatch, cause)
	}
	return &InvocationFailure{Cause: cause, Dispatch: dispatch, Usage: usage}
}

// NewTransientInvocationFailure marks a provider-declared retryable dispatched
// failure. No-generation failures retain only the rejected request count.
// Unknown-usage transport/stream failures burn the full reservation; the
// runtime may retry only read-only work, while writers remain fail-closed.
func NewTransientInvocationFailure(cause error, dispatch DispatchProvenance, usage UsageProvenance, retryAfter time.Duration) error {
	err := NewInvocationFailure(cause, dispatch, usage)
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure == nil {
		return err
	}
	if dispatch != DispatchDispatched || (usage != UsageNoGeneration && usage != UsageUnknown) {
		return fmt.Errorf("transient invocation failure requires dispatched no-generation or unknown-usage provenance: %w", cause)
	}
	failure.Transient = true
	if retryAfter > 0 {
		failure.RetryAfter = retryAfter
	}
	return failure
}

// NewFatalPreflightFailure identifies a deterministic adapter/configuration
// rejection proven to occur before provider dispatch. Retrying the same
// immutable invocation cannot help, so the runtime fails with the real cause
// instead of burning the attempt budget in a hot loop.
func NewFatalPreflightFailure(cause error) error {
	err := NewInvocationFailure(cause, DispatchNotDispatched, UsageUnknown)
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure == nil {
		return err
	}
	failure.Fatal = true
	return failure
}

// InvocationFailureFromError returns the outer typed provenance annotation,
// including when the cause was joined with other errors.
func InvocationFailureFromError(err error) (DispatchProvenance, UsageProvenance, bool) {
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure == nil {
		return "", "", false
	}
	if failure.Dispatch == DispatchNotDispatched && failure.Usage == UsageUnknown {
		return failure.Dispatch, failure.Usage, true
	}
	if failure.Dispatch == DispatchDispatched && (failure.Usage == UsageUnknown || failure.Usage == UsageFactual || failure.Usage == UsageNoGeneration) {
		return failure.Dispatch, failure.Usage, true
	}
	return "", "", false
}

func TransientInvocationFailureFromError(err error) (time.Duration, bool) {
	var failure *InvocationFailure
	if !errors.As(err, &failure) || failure == nil || !failure.Transient ||
		failure.Dispatch != DispatchDispatched || (failure.Usage != UsageNoGeneration && failure.Usage != UsageUnknown) {
		return 0, false
	}
	return failure.RetryAfter, true
}

func FatalPreflightFailureFromError(err error) bool {
	var failure *InvocationFailure
	return errors.As(err, &failure) && failure != nil && failure.Fatal &&
		failure.Dispatch == DispatchNotDispatched && failure.Usage == UsageUnknown
}

// RemainingLimits is the runtime-owned capacity reserved for one invocation.
// Provider adapters must apply MaxOutputTokens to every model call they make.
type RemainingLimits struct {
	ModelCalls      uint64 `json:"remaining_model_calls"`
	Tokens          uint64 `json:"remaining_tokens"`
	MaxOutputTokens uint64 `json:"max_output_tokens"`
}

// JobRemainingBudget is the provider-neutral capacity left after durable
// usage and every currently running attempt reservation. It excludes the
// current invocation's already-reserved capacity, which remains available via
// RemainingLimits.
type JobRemainingBudget struct {
	Cycles     uint64 `json:"cycles"`
	Attempts   uint64 `json:"attempts"`
	ModelCalls uint64 `json:"model_calls"`
	Tokens     uint64 `json:"tokens"`
}

func (l RemainingLimits) Validate() error {
	switch {
	case l.ModelCalls == 0:
		return errors.New("remaining model calls must be greater than zero")
	case l.Tokens == 0:
		return errors.New("remaining tokens must be greater than zero")
	case l.MaxOutputTokens == 0:
		return errors.New("max output tokens must be greater than zero")
	case l.MaxOutputTokens > l.Tokens:
		return errors.New("max output tokens cannot exceed remaining tokens")
	default:
		return nil
	}
}

// Invocation is the provider-neutral input for one durable attempt. The
// runtime, never model output, supplies identity, route, and authority.
type Invocation struct {
	JobID               string              `json:"job_id"`
	AttemptID           string              `json:"attempt_id"`
	AttemptNo           uint64              `json:"attempt_no"`
	BatchID             string              `json:"batch_id"`
	WorkItemID          string              `json:"work_item_id"`
	Cycle               uint64              `json:"cycle"`
	MinimumCycles       uint64              `json:"minimum_cycles,omitempty"`
	MaximumCycles       uint64              `json:"maximum_cycles,omitempty"`
	StageID             string              `json:"stage_id"`
	RoleID              string              `json:"role_id"`
	Kind                InvocationKind      `json:"kind"`
	Writer              bool                `json:"writer,omitempty"`
	Goal                string              `json:"goal"`
	Objective           string              `json:"objective"`
	RolePurpose         string              `json:"role_purpose"`
	Route               jobs.ExecutionRoute `json:"route"`
	Authority           jobs.Authority      `json:"authority"`
	ObservedAt          time.Time           `json:"observed_at"`
	NotBeforeComplete   time.Time           `json:"not_before_complete,omitzero"`
	CycleCadenceSeconds uint64              `json:"cycle_cadence_seconds,omitempty"`
	Deadline            time.Time           `json:"deadline"`
	JobRemainingBudget  JobRemainingBudget  `json:"job_remaining_budget"`
	Limits              RemainingLimits     `json:"limits"`
	PriorAttempts       []jobs.Attempt      `json:"prior_attempts,omitempty"`
	Artifacts           []jobs.ArtifactRef  `json:"artifacts,omitempty"`
	AllowedNextRoleIDs  []string            `json:"allowed_next_role_ids,omitempty"`
}

func (i Invocation) Validate() error {
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "job id", value: i.JobID},
		{label: "attempt id", value: i.AttemptID},
		{label: "batch id", value: i.BatchID},
		{label: "work item id", value: i.WorkItemID},
		{label: "stage id", value: i.StageID},
		{label: "role id", value: i.RoleID},
	} {
		if err := validatePortableID(field.label, field.value); err != nil {
			return err
		}
	}
	if i.AttemptNo == 0 {
		return errors.New("attempt number must be greater than zero")
	}
	if i.Cycle == 0 {
		return errors.New("invocation cycle must be greater than zero")
	}
	if i.MaximumCycles != 0 {
		if i.MaximumCycles < i.EffectiveMinimumCycles() {
			return errors.New("invocation maximum cycles cannot be lower than minimum cycles")
		}
		if i.Cycle > i.MaximumCycles {
			return errors.New("invocation cycle exceeds maximum cycles")
		}
	}
	if !i.Kind.Valid() {
		return fmt.Errorf("invalid invocation kind %q", i.Kind)
	}
	if i.Writer && i.Kind != InvocationKindWorker {
		return fmt.Errorf("%s invocation cannot be a writer", i.Kind)
	}
	if err := validateBoundedText("goal", i.Goal, MaxInvocationGoalBytes, false); err != nil {
		return err
	}
	if err := validateBoundedText("objective", i.Objective, MaxInvocationTextBytes, false); err != nil {
		return err
	}
	if err := validateBoundedText("role purpose", i.RolePurpose, MaxInvocationTextBytes, false); err != nil {
		return err
	}
	if err := i.Route.Validate(); err != nil {
		return fmt.Errorf("execution route: %w", err)
	}
	if err := i.Authority.Validate(); err != nil {
		return fmt.Errorf("effective authority: %w", err)
	}
	if err := validateBoundedAuthority(i.Authority); err != nil {
		return fmt.Errorf("effective authority: %w", err)
	}
	if !authorityAllowsProvider(i.Authority, i.Route.ProviderID) {
		return fmt.Errorf("effective authority does not allow persisted route provider %q", i.Route.ProviderID)
	}
	if len(i.Authority.Providers) != 1 || i.Authority.Providers[0] != i.Route.ProviderID {
		return errors.New("effective authority must be narrowed to the persisted route provider")
	}
	if !i.Writer && len(i.Authority.WriteRoots) != 0 {
		return errors.New("non-writer invocation cannot receive write roots")
	}
	if i.Deadline.IsZero() {
		return errors.New("invocation deadline is required")
	}
	if i.ObservedAt.IsZero() {
		return errors.New("invocation observed_at is required")
	}
	if !i.NotBeforeComplete.IsZero() && !i.NotBeforeComplete.Before(i.Deadline) {
		return errors.New("invocation not_before_complete must be before deadline")
	}
	if err := i.Limits.Validate(); err != nil {
		return fmt.Errorf("remaining limits: %w", err)
	}
	if len(i.PriorAttempts) > MaxInvocationPriorAttempts {
		return fmt.Errorf("prior attempts exceed limit %d", MaxInvocationPriorAttempts)
	}
	priorBytes := 0
	priorArtifacts := 0
	for index, attempt := range i.PriorAttempts {
		if !terminalInvocationAttemptStatus(attempt.Status) {
			return fmt.Errorf("prior attempt %q is not terminal", attempt.ID)
		}
		if err := attempt.Validate(); err != nil {
			return fmt.Errorf("prior attempt %d: %w", index, err)
		}
		priorBytes += len(attempt.Result) + len(attempt.Error)
		if priorBytes > MaxInvocationPriorResultBytes {
			return fmt.Errorf("prior attempt text exceeds limit %d", MaxInvocationPriorResultBytes)
		}
		priorArtifacts += len(attempt.Artifacts)
		if priorArtifacts > MaxInvocationArtifacts {
			return fmt.Errorf("prior attempt artifacts exceed limit %d", MaxInvocationArtifacts)
		}
		if err := validateArtifactRefs(attempt.Artifacts); err != nil {
			return fmt.Errorf("prior attempt %d artifacts: %w", index, err)
		}
	}
	priorPayload, err := json.Marshal(i.PriorAttempts)
	if err != nil {
		return fmt.Errorf("encode prior attempts: %w", err)
	}
	if len(priorPayload) > MaxInvocationPriorPayloadBytes {
		return fmt.Errorf("prior attempt payload exceeds limit %d", MaxInvocationPriorPayloadBytes)
	}
	if len(i.Artifacts) > MaxInvocationArtifacts {
		return fmt.Errorf("invocation artifacts exceed limit %d", MaxInvocationArtifacts)
	}
	if err := validateArtifactRefs(i.Artifacts); err != nil {
		return fmt.Errorf("invocation artifacts: %w", err)
	}
	if i.Kind == InvocationKindSupervisor {
		if err := validateAllowedRoleIDs(i.AllowedNextRoleIDs); err != nil {
			return fmt.Errorf("allowed next roles: %w", err)
		}
	} else if len(i.AllowedNextRoleIDs) != 0 {
		return fmt.Errorf("%s invocation cannot declare next-stage roles", i.Kind)
	}
	return nil
}

func (i Invocation) EffectiveMinimumCycles() uint64 {
	if i.MinimumCycles == 0 {
		return 1
	}
	return i.MinimumCycles
}

// InvocationResult is the normalized terminal result of one invocation.
// Provider transport errors are returned through Invoker's error result;
// provider-declared failed/cancelled attempts use Status and Error here.
type InvocationResult struct {
	Status      jobs.AttemptStatus  `json:"status"`
	Result      string              `json:"result,omitempty"`
	Fingerprint string              `json:"fingerprint,omitempty"`
	Artifacts   []jobs.ArtifactRef  `json:"artifacts,omitempty"`
	Usage       jobs.Usage          `json:"usage"`
	Proposal    *SupervisorProposal `json:"proposal,omitempty"`
	Error       string              `json:"error,omitempty"`
}

// ValidateFor validates a normalized result against runtime-owned invocation
// metadata. It never accepts recovery-only attempt statuses from an Invoker.
func (r InvocationResult) ValidateFor(invocation Invocation) error {
	return r.validateFor(invocation, true)
}

// validateFor optionally separates structural result conformance from factual
// usage that exceeded a reservation. The latter is a budget event, not a
// malformed Invoker result, and must remain accountably representable.
func (r InvocationResult) validateFor(invocation Invocation, enforceLimits bool) error {
	if err := invocation.Validate(); err != nil {
		return fmt.Errorf("invocation: %w", err)
	}
	switch r.Status {
	case jobs.AttemptStatusSucceeded:
		if r.Error != "" {
			return errors.New("succeeded invocation result cannot contain error")
		}
	case jobs.AttemptStatusFailed, jobs.AttemptStatusCancelled:
		if err := validateBoundedText("invocation error", r.Error, MaxInvocationErrorBytes, false); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invoker returned non-terminal or recovery-only status %q", r.Status)
	}
	if len(r.Result) > MaxInvocationResultBytes {
		return fmt.Errorf("invocation result exceeds limit %d", MaxInvocationResultBytes)
	}
	if !utf8.ValidString(r.Result) {
		return errors.New("invocation result is not valid UTF-8")
	}
	if len(r.Fingerprint) > 256 || containsControl(r.Fingerprint) {
		return errors.New("invocation fingerprint is invalid or exceeds 256 bytes")
	}
	if len(r.Artifacts) > MaxInvocationResultArtifacts {
		return fmt.Errorf("result artifacts exceed limit %d", MaxInvocationResultArtifacts)
	}
	if err := validateArtifactRefs(r.Artifacts); err != nil {
		return fmt.Errorf("result artifacts: %w", err)
	}
	if r.Usage.Cycles != 0 || r.Usage.Attempts != 0 {
		return errors.New("invocation usage cannot report cycles or attempts")
	}
	if r.Usage.ModelCalls == 0 {
		return errors.New("invocation usage must report at least one model call")
	}
	if enforceLimits && r.Usage.ModelCalls > invocation.Limits.ModelCalls {
		return errors.New("invocation model call usage exceeds remaining limit")
	}
	if invocationUsageTokenOverflow(r.Usage) {
		return errors.New("invocation token usage overflows")
	}
	if r.Usage.TotalTokens() == 0 {
		return errors.New("invocation usage must report token counts")
	}
	if enforceLimits && r.Usage.TotalTokens() > invocation.Limits.Tokens {
		return errors.New("invocation token usage exceeds remaining limit")
	}
	if enforceLimits && !withinPerCallOutputLimit(r.Usage.OutputTokens, r.Usage.ModelCalls, invocation.Limits.MaxOutputTokens) {
		return errors.New("invocation output token usage exceeds per-call limit")
	}
	if invocation.Kind == InvocationKindSupervisor && r.Status == jobs.AttemptStatusSucceeded {
		if r.Proposal == nil {
			return errors.New("successful supervisor invocation requires proposal")
		}
		if err := r.Proposal.Validate(invocation.AllowedNextRoleIDs); err != nil {
			return fmt.Errorf("supervisor proposal: %w", err)
		}
	} else if r.Proposal != nil {
		return errors.New("only a successful supervisor invocation may return proposal")
	}
	return nil
}

func invocationUsageTokenOverflow(usage jobs.Usage) bool {
	return ^uint64(0)-usage.InputTokens < usage.OutputTokens
}

// SupervisorProposal is deliberately narrower than jobs.Decision. A model may
// suggest only a disposition and objectives for predeclared roles. Runtime
// code owns stage/batch/item IDs, authority, route, deadline, budget, and the
// stagnation fingerprint.
type SupervisorProposal struct {
	Kind           jobs.DecisionKind `json:"kind"`
	Reason         string            `json:"reason"`
	NextObjectives map[string]string `json:"next_objectives,omitempty"`
}

func (p SupervisorProposal) Validate(allowedRoleIDs []string) error {
	if err := validateBoundedText("proposal reason", p.Reason, MaxSupervisorReasonBytes, true); err != nil {
		return err
	}
	switch p.Kind {
	case jobs.DecisionContinue:
		if err := validateAllowedRoleIDs(allowedRoleIDs); err != nil {
			return fmt.Errorf("allowed roles: %w", err)
		}
		allowed := make(map[string]struct{}, len(allowedRoleIDs))
		for _, roleID := range allowedRoleIDs {
			allowed[roleID] = struct{}{}
		}
		for roleID, objective := range p.NextObjectives {
			if _, ok := allowed[roleID]; !ok {
				return fmt.Errorf("proposal objective uses undeclared next-stage role %q", roleID)
			}
			if err := validateBoundedText("proposal objective for "+roleID, objective, MaxSupervisorObjectiveBytes, true); err != nil {
				return err
			}
		}
		if len(p.NextObjectives) != len(allowedRoleIDs) {
			return errors.New("continue proposal must contain exactly one objective for every allowed role")
		}
	case jobs.DecisionComplete, jobs.DecisionWait, jobs.DecisionBlocked:
		if len(p.NextObjectives) != 0 {
			return fmt.Errorf("%s proposal cannot contain next objectives", p.Kind)
		}
	default:
		return fmt.Errorf("invalid supervisor proposal kind %q", p.Kind)
	}
	return nil
}

// withinPerCallOutputLimit compares output <= calls*perCall without allowing
// the multiplication itself to overflow.
func withinPerCallOutputLimit(output, calls, perCall uint64) bool {
	quotient, remainder := output/calls, output%calls
	return quotient < perCall || quotient == perCall && remainder == 0
}

// ParseSupervisorProposal strictly decodes bounded model output. Unknown
// fields are rejected so a model cannot smuggle runtime-owned identities,
// authority, budgets, route/provider choices, or deadlines into a proposal.
func ParseSupervisorProposal(body []byte, allowedRoleIDs []string) (SupervisorProposal, error) {
	if len(body) == 0 {
		return SupervisorProposal{}, errors.New("supervisor proposal is empty")
	}
	if len(body) > MaxSupervisorProposalBytes {
		return SupervisorProposal{}, fmt.Errorf("supervisor proposal exceeds limit %d", MaxSupervisorProposalBytes)
	}
	if err := rejectDuplicateJSONFields(body); err != nil {
		return SupervisorProposal{}, fmt.Errorf("decode supervisor proposal: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var proposal SupervisorProposal
	if err := decoder.Decode(&proposal); err != nil {
		return SupervisorProposal{}, fmt.Errorf("decode supervisor proposal: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return SupervisorProposal{}, fmt.Errorf("decode supervisor proposal: %w", err)
	}
	if err := proposal.Validate(allowedRoleIDs); err != nil {
		return SupervisorProposal{}, err
	}
	return proposal, nil
}

// rejectDuplicateJSONFields closes the gap left by encoding/json's normal
// "last value wins" behavior. A supervisor proposal is a control message, so
// an object with conflicting duplicate keys is ambiguous and must fail closed.
func rejectDuplicateJSONFields(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := consumeUniqueJSONValue(decoder); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func consumeUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object field name is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("invalid JSON object terminator")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("invalid JSON array terminator")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func validateAllowedRoleIDs(roleIDs []string) error {
	if len(roleIDs) == 0 || len(roleIDs) > jobs.MaxWorkers {
		return fmt.Errorf("must contain between 1 and %d role IDs", jobs.MaxWorkers)
	}
	seen := make(map[string]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		if err := validatePortableID("role id", roleID); err != nil {
			return err
		}
		if _, duplicate := seen[roleID]; duplicate {
			return fmt.Errorf("duplicate role ID %q", roleID)
		}
		seen[roleID] = struct{}{}
	}
	return nil
}

func validateArtifactRefs(artifacts []jobs.ArtifactRef) error {
	seen := make(map[string]struct{}, len(artifacts))
	for index, artifact := range artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("artifact %d: %w", index, err)
		}
		for _, field := range []struct {
			label string
			value string
		}{
			{label: "uri", value: artifact.URI},
			{label: "sha256", value: artifact.SHA256},
			{label: "media type", value: artifact.MediaType},
		} {
			if len(field.value) > MaxInvocationArtifactFieldBytes || !utf8.ValidString(field.value) || containsControl(field.value) {
				return fmt.Errorf("artifact %q %s is invalid or exceeds limit %d", artifact.ID, field.label, MaxInvocationArtifactFieldBytes)
			}
		}
		if _, duplicate := seen[artifact.ID]; duplicate {
			return fmt.Errorf("duplicate artifact %q", artifact.ID)
		}
		seen[artifact.ID] = struct{}{}
	}
	return nil
}

func validateBoundedAuthority(authority jobs.Authority) error {
	dimensions := []struct {
		label  string
		values []string
	}{
		{label: "tools", values: authority.Tools},
		{label: "read roots", values: authority.ReadRoots},
		{label: "write roots", values: authority.WriteRoots},
		{label: "network hosts", values: authority.NetworkHosts},
		{label: "providers", values: authority.Providers},
	}
	total := 0
	for _, dimension := range dimensions {
		total += len(dimension.values)
		if total > MaxInvocationAuthorityEntries {
			return fmt.Errorf("authority entries exceed limit %d", MaxInvocationAuthorityEntries)
		}
		for _, value := range dimension.values {
			if len(value) > MaxInvocationAuthorityEntryBytes || !utf8.ValidString(value) || containsControl(value) {
				return fmt.Errorf("%s entry is invalid or exceeds limit %d", dimension.label, MaxInvocationAuthorityEntryBytes)
			}
		}
	}
	return nil
}

func validatePortableID(label, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 128 {
		return fmt.Errorf("%s must be a non-empty portable identifier of at most 128 bytes", label)
	}
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(index > 0 && r >= '0' && r <= '9') ||
			(index > 0 && (r == '-' || r == '_' || r == '.' || r == ':')) {
			continue
		}
		return fmt.Errorf("%s %q is not a portable identifier", label, value)
	}
	return nil
}

func validateBoundedText(label, value string, maxBytes int, rejectAllControls bool) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", label)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds limit %d", label, maxBytes)
	}
	for _, r := range value {
		if !unicode.IsControl(r) {
			continue
		}
		if !rejectAllControls && (r == '\n' || r == '\r' || r == '\t') {
			continue
		}
		return fmt.Errorf("%s contains control characters", label)
	}
	return nil
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func terminalInvocationAttemptStatus(status jobs.AttemptStatus) bool {
	switch string(status) {
	case string(jobs.AttemptStatusSucceeded), string(jobs.AttemptStatusFailed), string(jobs.AttemptStatusCancelled), "abandoned", "ambiguous":
		return true
	default:
		return false
	}
}
