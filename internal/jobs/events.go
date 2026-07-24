package jobs

import "time"

// EventType identifies a state-machine input. Events are intentionally
// provider-neutral: provider stop reasons and transport errors must be
// normalized before they reach this package.
type EventType string

const (
	EventJobStarted      EventType = "job_started"
	EventBatchStarted    EventType = "batch_started"
	EventAttemptStarted  EventType = "attempt_started"
	EventAttemptFinished EventType = "attempt_finished"
	// EventAttemptRecorded is retained only for replay compatibility with the
	// pre-two-phase schema. New runtimes must use started/finished.
	EventAttemptRecorded       EventType = "attempt_recorded"
	EventBatchCompleted        EventType = "batch_completed"
	EventUsageRecorded         EventType = "usage_recorded"
	EventDecisionMade          EventType = "decision_made"
	EventJobPaused             EventType = "job_paused"
	EventJobResumed            EventType = "job_resumed"
	EventCancellationRequested EventType = "cancellation_requested"
	EventJobCancelled          EventType = "job_cancelled"
	EventJobFailed             EventType = "job_failed"
	EventDeadlineExceeded      EventType = "deadline_exceeded"
)

// Event is the explicit input contract for Reduce. Payload fields are typed
// rather than carried in an open-ended metadata map so a replay cannot acquire
// new authority or behavior through provider-controlled data.
//
// ID is required and must be unique within a job. Replaying the most recently
// accepted event is idempotent. After terminal emission, that exact replay is
// the only accepted event; any different event is rejected.
type Event struct {
	ID        string         `json:"id"`
	Type      EventType      `json:"type"`
	At        time.Time      `json:"at"`
	Batch     *WorkBatch     `json:"batch,omitempty"`
	BatchID   string         `json:"batch_id,omitempty"`
	Attempt   *Attempt       `json:"attempt,omitempty"`
	Decision  *Decision      `json:"decision,omitempty"`
	Usage     Usage          `json:"usage,omitempty"`
	Artifacts []ArtifactRef  `json:"artifacts,omitempty"`
	Reason    TerminalReason `json:"reason,omitempty"`
}
