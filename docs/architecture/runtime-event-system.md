# Runtime Event System

This document describes the implemented runtime/event contract for the core
agent loop, event protocol, JSONL replay, and benchmark trace replay. Gateway,
TUI, Telegram, and tool-specific presentation contracts are documented
elsewhere; this file only names them when they depend on the shared event model.

Status note: this document was reviewed against the dirty current worktree on
2026-07-03. Claims describe this checkout, not necessarily a clean release
commit.

The source-of-truth decision is recorded in
[ADR 0001](../adr/0001-jsonl-event-log-source-of-truth.md).

## Core Ownership

- [internal/protocol](../../internal/protocol/types.go) owns event names,
  payload structs, messages, tool calls, and shared envelope fields.
- [internal/protocol/envelope.go](../../internal/protocol/envelope.go) enriches
  events with schema version, sequence, source, timestamps, run/submission IDs,
  profile hash, and correlation IDs copied from typed payloads.
- [internal/eventlog](../../internal/eventlog/eventlog.go) owns record
  validation, envelope validation forwarding, lifecycle validation, and JSONL
  corruption diagnostics. It must stay independent of runtime callers.
- [internal/agent](../../internal/agent/runtime_loop.go) owns runtime event
  emission for runs, turns, model calls, tools, context thresholds, compaction,
  and liveness hints.
- [internal/session](../../internal/session/session.go) owns message state,
  active-run locking, cancellation, and rollback policy after interrupted runs.
- [internal/runstate](../../internal/runstate/runstate.go) owns snapshot hashes
  and prompt-cache diagnostics attached to model-call events.
- [internal/trace](../../internal/trace/trace.go) owns benchmark event records,
  payload refs, replay summaries, output-ref audits, and timeline projection.

## Source Of Truth

Persisted JSONL event records are the durable source of truth. Live run streams
are progress views: they can be replayed from durable sequence numbers if a
client drops events. The project-wide delivery rule is in
[docs/architecture.md](../architecture.md#runtime-event-delivery).

The shared JSONL helper in
[internal/eventlog/jsonl.go](../../internal/eventlog/jsonl.go) appends one JSON
record per line, fsyncs the file, uses private file modes, and replays by
scanning records in order. Replay failures are surfaced as `CorruptionError`
values with path, line, record number, kind, and wrapped cause.

`eventlog.RecordValidator` checks record schema version, contiguous `seq`,
stable scope IDs, `event_type` versus `event.type`, and optionally the protocol
envelope. The validator is scope-neutral: trace replay uses run scope in
[internal/trace/trace.go](../../internal/trace/trace.go), and other stores can
use their own scope names while sharing the same corruption behavior.

Strict append paths validate before persistence. Gateway session events and
benchmark trace events enrich an event, validate the v1 nested envelope, clone
and advance lifecycle state, and only then write JSONL. Validation failures do
not consume a durable sequence number and do not leave a partial event record.
Legacy replay/import mode is explicit: callers can omit `RequireEnvelope` only
for old records that predate schema-versioned envelopes; new durable writers
must emit schema version `1`.

## Protocol Envelope

`protocol.Event` has these durable identity fields:

- `schema_version`: current value is `1`.
- `seq`: monotonic within the writer/scope that persists or streams the event.
- `source`: one of the shared `EventSource` constants such as `agent`,
  `gateway`, `tool`, `provider`, `bench`, `tui`, or `telegram`.
- `ts`: RFC3339Nano UTC timestamp.
- `submission_id`, `run_id`, `turn_id`, `step_id`, `call_id`, `attempt_id`,
  `parent_step_id`, and `profile_hash`: correlation fields for replay,
  projection, auditing, and trace summaries.
- `duration_ms`: copied from typed payloads where available.
- `type` and `data`: event name and typed or map payload.

`EventEnricher` serializes callbacks under a mutex and assigns contiguous
sequence numbers for an enriched stream. It also copies IDs from known payload
types such as `TurnEvent`, `StepEvent`, `ToolResult`, `ToolProgressEvent`,
`ToolOutputRefEvent`, `ProviderHelperUsageEvent`, user-input events, hook
events, raw JSON, and maps.

Envelope validation is intentionally event-specific. For example, run events
require `run_id`; turn events require `run_id` and `turn_id`; model events
require `run_id`, `turn_id`, and `step_id`; terminal tool-attempt events require
`run_id`, `call_id`, and `attempt_id`; user-input events require the full
run/turn/step/call/attempt correlation.

## Runtime Lifecycle

The agent runtime in [internal/agent/runtime_loop.go](../../internal/agent/runtime_loop.go)
emits a run as:

1. `run.started`
2. zero or more turns
3. exactly one terminal run event from the normal paths: `run.completed` or
   `run.failed`

Each turn has a deterministic ID from `agentTurnID`, currently `turn-001`,
`turn-002`, and so on. A turn starts with `turn.started` and ends with
`turn.completed`. A failed turn is represented as `turn.completed` with status
`failed` and stop reason `error`; successful turns use stop reason
`final_answer` or `tool_results`.

Each model call is a `model_call` step:

- `step.started`
- `model.call_started`
- zero or more `assistant.content_delta` and `assistant.reasoning_delta`
  events, coalesced by
  [internal/agent/model_call.go](../../internal/agent/model_call.go)
- zero or more cumulative `provider.usage` updates
- `model.call_finished`
- `step.completed`

The model-call payload includes provider/model IDs, request metadata, retry
metadata, token usage, latency fields, prompt inventory, prompt-cache break
diagnostics, tool snapshot hash, MCP status hash, profile instruction hash, and
access/permission mode metadata.

Tool calls are requested before execution through
[internal/agent/tool_attempt.go](../../internal/agent/tool_attempt.go):

- `tool.call_requested`
- `tool.call_progress` for prepare and permission phases
- `tool.permission_requested`
- `tool.permission_decided`
- optional `tool.audit` for write/execute/external risk
- `step.started` for the tool-call step
- `tool.call_started`
- `tool.call_progress` while executing
- `step.completed`
- optional `tool.output_ref_created`
- optional `provider.helper_usage` for helper model/web backend work
- one terminal tool-attempt event: `tool.call_finished`, `tool.call_failed`, or
  `tool.call_aborted`
- final `tool.call_progress` entries for retry/finalize metadata

Tool attempts currently use deterministic IDs from `agentAttemptID`, for
example `turn-001:tool-call-001:attempt-001`. The current agent implementation
does not configure real tool retries; retry-decision progress events are emitted
with `retries_not_configured`.

Parallel-safe tools can be grouped under a parent `tool_batch` step. The batch
step starts before child tool steps and completes after all child results arrive.
Child tool steps may complete out of order; lifecycle validation only requires
that a child `parent_step_id` reference a known started step.

## Lifecycle Validation

[eventlog.LifecycleValidator](../../internal/eventlog/eventlog.go) currently
enforces these replay rules:

- `run.started` must carry `run_id`; terminal run events require a started run
  and reject duplicate terminal run events for the same `run_id`;
- turn events require a started run; `turn.completed` requires a started turn
  and rejects duplicate terminal turn events for the same run/turn pair;
- step events require a started run and turn; `step.completed` requires a
  started step and rejects duplicate terminal step events for the same
  run/turn/step tuple;
- `step.started` with `parent_step_id` requires the parent step to have been
  started first;
- model-call, assistant-delta, assistant-reasoning, and provider-usage events
  require the enclosing run, turn, and step to have started;
- context threshold, compaction, and provider-helper usage events require a
  started run;
- `tool.call_started` requires a previous `tool.call_requested` with the same
  run/call key and a non-empty attempt ID;
- `tool.call_finished`, `tool.call_failed`, and `tool.call_aborted` require a
  previous call request and attempt start, bind `attempt_id` to the original
  `call_id`, and reject duplicate terminal attempt events for the same
  run/attempt key;
- `tool.call_progress` events that carry an `attempt_id` must reference the
  same call as the started attempt, and `attempt_started` progress binds the
  attempt to its first call before the terminal start event arrives.

Trace replay in [internal/trace/trace.go](../../internal/trace/trace.go) runs
both `RecordValidator` and `LifecycleValidator` before it builds counters or
timeline rows. `trace.EventWriter` uses the same envelope/lifecycle checks
before encoding a record and leaves the writer sequence unchanged when
validation fails. Benchmark verification in
[internal/bench/bench.go](../../internal/bench/bench.go) treats finished,
failed, and aborted tool attempts as terminal outcomes.

## Context And Compaction Events

The runtime emits `context.threshold` when the estimated prompt reaches 50, 70,
85, or 95 percent of the configured context window. The estimator is currently
character-count based (`chars_div_4`) in
[internal/agent/context_threshold.go](../../internal/agent/context_threshold.go).
Threshold events are emitted once per percent per context epoch and include the
epoch, `threshold_key`, and stage such as `initial`, `before_turn`,
`after_tool_results`, or `after_final_answer`. After `context.compacted`
advances the epoch, threshold tracking resets so stale pre-compaction warnings
do not suppress warnings for the new active context.

Before each turn, the agent may compact messages. Deterministic compaction keeps
the protected system/context prefix and recent messages, replaces older body
messages with a system summary, and emits `context.compacted` with a
`compactionReport` from [internal/agent/compaction.go](../../internal/agent/compaction.go).
The report carries the new `context_epoch`, previous epoch, cut/replacement
indexes, trigger source/tokens, summary strategy, input-span hash, replacement
hash, summary hash, and pre/post history sequence/hash fields. These hashes are
audit breadcrumbs only; they let replay and incident tools compare boundaries
without copying full compacted text into status displays.
If the configured strategy uses a helper model, the runtime updates the report
and emits `provider.helper_usage` with kind `context_compact` from
[internal/agent/model_compaction.go](../../internal/agent/model_compaction.go).

`stream.still_running` is a liveness hint emitted by
[internal/agent/liveness.go](../../internal/agent/liveness.go) when a run is
active but no events have appeared for half the stream idle timeout. It carries
the latest known run/turn/step/call/attempt IDs and phase, but it is not a
terminal lifecycle event.

## Output Refs And Payload Refs

Tool output refs are runtime metadata for large or truncated tool results. When
a `ToolResult` carries `OutputRef`, the agent emits `tool.output_ref_created`
with path, ID, byte count, optional SHA-256, permissions, plaintext flag, and
truncation state. This event is not terminal for the attempt; terminal state is
still one of finished, failed, or aborted.

Trace payload refs are separate benchmark persistence metadata. `trace.EventWriter`
can write selected large protocol events to a payload directory, replace the
inline event body with a slim event, and record `PayloadRef` entries on the
trace record. `trace.ReplayEvents` verifies payload ref readability, byte
counts, and hashes before summarizing the event stream.

## Run State Snapshots

[internal/runstate](../../internal/runstate/runstate.go) creates per-turn
snapshots that are attached to turn/model events through metadata:

- provider, model, reasoning mode, context budget, access mode, and dangerous
  permission mode;
- tool schema hash and MCP status snapshot hash;
- profile instruction hash;
- prompt inventory over stable prompt sections and tool schemas;
- prompt-cache break diagnostics comparing the current turn snapshot to the
  previous turn.

Prompt inventory intentionally omits arbitrary user prompt text. The snapshot
hashes are for diagnostics and replay comparison, not for reconstructing the
full prompt.

Memory drift diagnostics reuse the same hash-only posture. A session keeps the
memory context it was created with; context/status projections compare the
locked memory hash with the current rendered memory hash when the live gateway
can load memory, and expose no memory topic body text in the drift fields.

Context diagnostics are rebuilt as projections over snapshots and events. The
index surfaces epoch, compaction and threshold counts, helper/tool counts,
protected-prefix/body token split, compaction/window margins, and stable prompt
section hashes so operators can correlate active context with replay without
reading full prompt sections.

## Session State Boundary

[internal/session/session.go](../../internal/session/session.go) stores message
history and serializes runs per session. Its cancellation rollback policy
restores the pre-run transcript when a run is interrupted, while the event
stream remains durable. Callers are responsible for emitting or synthesizing a
terminal event so replay stays valid after interruption.

[internal/session/importer.go](../../internal/session/importer.go) can import
JSON-like or Markdown transcripts into messages and emits a `session.imported`
marker event. It does not infer tool-call history as executable runtime state.

## Current Limits

These are current implementation boundaries, not desired behavior:

- Lifecycle validation does not prove every semantic relationship. It does not
  currently require `model.call_finished` to follow `model.call_started`, and
  treats hook/session/import/gateway hint events mostly through envelope
  validation and replay counters.
- Incremental append and replay do not require every started run/turn/step or
  attempt to be terminal by end of file, because live session logs can be open.
  Closed artifacts can opt into `eventlog.ValidateClosedLifecycle`.
- `eventlog.ReplayJSONL` is a full scan. Benchmark files include replay-after
  sequence benchmarks, but there is no shared sparse index in `internal/eventlog`.
- Tool retries are not implemented as multiple attempts in the agent runtime;
  attempt IDs currently end in `attempt-001`.
- Gateway session storage has its own record shape and live-stream API. This
  document relies only on the shared event contract and the delivery rule in
  `docs/architecture.md`; gateway API details belong in gateway documentation.
