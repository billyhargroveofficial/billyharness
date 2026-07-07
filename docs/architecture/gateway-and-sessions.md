# Gateway and Sessions Architecture

Status note: this document is written against the current worktree on
2026-07-07. Mutation-auth hardening, session-owner header scoping,
stored-session projector inspection, fail-closed persistence behavior,
manifest-only session startup, and the neutral agent-club registry/discovery
ingress route are current worktree contracts, not assumptions from older
releases.

Code anchors:

- `internal/gateway/gateway.go`: server construction, session handlers, run
  settings projection, session context responses.
- `internal/gateway/routes.go`: gateway HTTP route table.
- `internal/gateway/status_routes.go`: health/readiness, auth/config, tool,
  MCP, and managed-process status handlers.
- `internal/gateway/response.go`: NDJSON stream writer, live stream buffering,
  `gateway.stream_gap`, redacted JSON responses.
- `internal/gateway/session_events.go`: gateway session wrapper, event hub,
  status projection, recording and publishing of run events.
- `internal/gateway/run_thread.go`: presentation-neutral message state,
  single-run locking, cancellation, and rollback for gateway sessions.
- `internal/gateway/session_store.go`: durable session manifest, history,
  lazy materialization, event replay, legacy snapshot compatibility, private
  file permissions.
- `internal/gateway/session_inputs.go`: durable input admission and
  idempotency state.
- `internal/gateway/ingress.go`: gateway-owned ingress admission helper and
  redacted append-only ingress audit ledger.
- `internal/gateway/agentclub_events.go`: agent-club event and capability
  discovery HTTP adapter that admits normalized external adapter events without
  dispatching a run.
- `internal/agentclub/`: neutral v0 event contract, capability metadata,
  trusted binding validation, discovery views, canonical payload hashing, and
  ingress event/rule conversion.
- `internal/ingress/`: pure external-event admission DTOs, deterministic input
  IDs, raw-body HMAC helpers, and payload metadata sanitization.
- `internal/gateway/http_security.go`: HTTP bearer, mutation, origin, host,
  content-type, and privilege-clamping rules.
- `internal/gateway/session_authz.go`: session-owner header parsing and
  read/mutation authorization.
- `internal/gatewayapi/types.go` and `net.go`: shared request/response DTOs,
  owner header names, URL/auth-token helpers, readiness probe, and unavailable
  hints.
- `internal/gatewayclient/client.go`: shared HTTP client helpers, owner header
  injection, session methods, NDJSON decoder, sequence-gap detection.
- `internal/clientux/context.go`: client-facing context/status projection from
  messages plus replayed events.

## Responsibilities

`internal/gateway` is the HTTP adapter that composes runtime pieces for local
and remote clients. It owns:

- gateway HTTP route registration and response encoding;
- session creation, listing, status, context, event replay, input admission,
  run, cancel, undo, redo, and user-input routes;
- gateway-owned external ingress admission into existing session input
  ledgers, with neutral adapter-event normalization plus optional trusted
  registry/binding checks before admission;
- durable session persistence when `ServerOptions.SessionStoreDir` is set;
- startup manifest indexing of durable sessions into the in-memory session map;
- event recording before live fanout for session runs;
- gateway-level security checks before handlers see mutating requests;
- owner/scope filtering for session reads and mutations in the current
  worktree;
- benchmark, tool, MCP, config, process, and auth inspection endpoints.

It does not own client rendering. Shared transport DTOs belong in
`internal/gatewayapi`; client request helpers belong in `internal/gatewayclient`;
URL/auth/readiness helpers belong in `internal/gatewayapi`; neutral projection
logic belongs in `internal/clientux`.

It also does not let external triggers run tools, MCP calls, provider
overrides, arbitrary commands, or schedulers directly. External ingress must
first become a gateway-admitted session input, and a separate gateway run must
later promote that input before the runtime sees it.

## HTTP Surface

Routes are registered in `Server.routes` in `internal/gateway/routes.go`.
Future agents should update this section from that route table, not from memory.

| Route | Role |
| --- | --- |
| `GET /health` | unauthenticated liveness and active provider/model summary |
| `GET /ready` | unauthenticated bounded readiness summary for effective config, native tool catalog, MCP catalog state, and startup session-store health |
| `GET /v1/auth/status` | sanitized provider auth status |
| `POST /v1/auth/deepseek` | persist a DeepSeek API key through the credentials manager |
| `POST /v1/auth/codex/import` | import or save Codex auth JSON through the credentials manager |
| `GET /v1/config` | sanitized resolved config plus diagnostics |
| `GET /v1/tools` | registry tool specs |
| `GET /v1/mcp` | runtime MCP status, prompts, and server instructions |
| `GET /v1/processes` | managed shell-process status from the tools registry |
| `GET /v1/agentclub/capabilities` | list enabled agent-club capability descriptors and safe binding metadata visible to the request actor |
| `POST /v1/run` | stateless live NDJSON run stream; no durable session history |
| `GET /v1/sessions` | list sessions visible to the request actor |
| `POST /v1/sessions` | create a session and optionally persist owner metadata |
| `GET /v1/sessions/{id}` | fetch session metadata and messages |
| `GET /v1/sessions/{id}/status` | fetch current session status |
| `GET /v1/sessions/{id}/context` | project context, runtime, usage, prompt, and replay-derived metrics |
| `GET /v1/sessions/{id}/inspect` | return redacted live/durable session inspection and replay readiness |
| `GET /v1/sessions/{id}/events` | NDJSON replay/follow stream for session events |
| `POST /v1/sessions/{id}/agentclub/events` | admit one normalized agent-club adapter event as an ingress input without running the session |
| `POST /v1/sessions/{id}/inputs` | admit an idempotent pending input without running it |
| `POST /v1/sessions/{id}/inputs/{input_id}/complete` | terminally complete an admitted input with optional failure evidence |
| `POST /v1/sessions/{id}/run` | admit/promote an input and run it through the session thread |
| `POST /v1/sessions/{id}/user_input/{request_id}/answer` | answer a pending user-input request |
| `POST /v1/sessions/{id}/user_input/{request_id}/reject` | reject a pending user-input request |
| `POST /v1/sessions/{id}/undo` | preview or restore a recorded checkpoint change |
| `POST /v1/sessions/{id}/redo` | redo the last reverted checkpoint change |
| `POST /v1/sessions/{id}/cancel` | interrupt the active session run and wait briefly |

The gateway returns JSON for ordinary responses and `application/x-ndjson` for
run and event streams. Response encoding runs through `marshalRedactedJSON`, so
secret-looking strings are redacted before they leave the gateway.

## Session Model

The gateway `Session` wraps a gateway-local `runThread`. The lower-level thread
owns only message state, single-active-run locking, and cancellation/rollback:

- `RunMessage` clones the pre-run message list, appends the user message for
  the runner, and stores the runner result on success.
- interrupted runs restore the pre-run transcript, so the canceled prompt and
  any late runner messages do not become canonical session messages;
- `CancelAndWait` cancels the active run and waits for the session to become
  idle or for the caller context to expire.

The gateway layer adds HTTP identity, event delivery, durability, and status:

- `Session.Owner` records client ownership metadata from create requests or
  owner headers;
- `Session.status` stores run sequence, running state, provider/model/profile,
  access mode, counts, last event, and last error;
- `Session.events` is an in-memory live hub for current subscribers;
- `Session.eventRecorder` appends events to the durable store when a store is
  configured.

`POST /v1/sessions/{id}/run` performs three coupled actions:

1. admits the input through `session_inputs.go`, generating or validating an
   `input_id`;
2. applies the only supported interrupt policy, `interrupt`, by canceling and
   waiting for any active run;
3. promotes the admitted input under the input ledger lock, assigns the next run
   sequence from durable session/input state, runs the agent through the session
   thread, observes/persists/publishes run events, saves the resulting
   transcript, and completes the input record with a terminal status.

If preflight work fails after admission but before promotion, the gateway marks
the input terminal in `inputs.jsonl` with `terminal_status=preflight_failed` and
a redacted failure reason. Clients that admitted input separately can also call
`POST /v1/sessions/{id}/inputs/{input_id}/complete` to terminally close an
admitted input with replayable failure evidence.

Run persistence is fail-closed around durable event truth. Event append failures
stop the run stream with a non-durable `run.failed` surface and mark in-memory
status as `persistence_failed`. If event append succeeds but the final session
snapshot save fails after a visible run transition, the stream emits a
non-durable `session.status` with `last_event=persistence_failed`, marks the
input completion as failed, and returns that persistence error from the run
closure. When `interrupt` cancels an active run before starting a replacement,
the gateway must save the interrupted session state before the replacement run
continues; save failure aborts the replacement stream. `POST
/v1/sessions/{id}/cancel` likewise returns an error instead of claiming
`cancelled=true` when the post-cancel session save fails.

`POST /v1/sessions/{id}/inputs` is intentionally separate from run execution.
It gives clients a durable idempotency point before a run is promoted.

## External Ingress And Agent-Club

Ingress is admission-first. It does not let external triggers dispatch runs,
call tools, call MCP, or pick provider/runtime overrides. The current HTTP
adapter surface has one neutral route for project adapters:
`POST /v1/sessions/{id}/agentclub/events`. There is no public webhook route,
scheduler, UI, generic project command runner, raw API caller, raw SQL caller,
browser auth bridge, or project-specific behavior in Billyharness core.

`internal/ingress` owns the pure admission contract:

- `IngressEvent`, `IngressRule`, and `AdmissionDecision` describe an external
  event, a local allowlist rule, and the sanitized decision;
- deterministic input IDs are derived from rule id, source, external event id,
  raw payload SHA-256, and target session id;
- raw-body HMAC helpers verify SHA-256 signatures with constant-time compare
  and optional timestamp skew checks;
- payload metadata keys that look like provider/model/access-mode/tool/MCP or
  shell overrides are rejected before a `SessionInputRequest` is built;
- rule/static metadata is treated as trusted local configuration, not external
  payload authority.

`Server.AdmitIngressEvent` in `internal/gateway/ingress.go` is the gateway-owned
bridge. It asks `internal/ingress` to build a `SessionInputRequest`, authorizes
the target session with the same owner-scope checks as HTTP routes, appends a
redacted `received` audit record, and then calls the existing input admission
path. The final `admitted` or `rejected` audit record follows input admission,
so a durable input cannot be written without prior ingress audit evidence. It
does not promote the input, start a run, call tools, call MCP, or shell out.

`internal/agentclub` is a small v0 contract layer for external project
adapters. It uses four layers:

1. a capability descriptor that says what exists;
2. a trusted binding that says which ingress owner may submit it;
3. an admitted event that becomes a gateway input;
4. a later normal gateway run, started separately by a client or operator.

An adapter submits one normalized event with `schema_version=1`, `source`,
`capability`, `event_type`, `external_event_id`, `prompt`, a JSON `payload`,
and safe string metadata. The gateway route requires session-owner headers
before it decodes authority from anywhere else:

```text
client_type=ingress
client_id=ingress:<adapter-id>:<profile-or-env>
```

The route requires a non-empty actor with `client_type=ingress`, authorizes
that actor against the target session, maps the event to `IngressEvent` plus a
local `IngressRule` whose owner is that actor, and admits the resulting
session input. Payload authority stays inert: request JSON cannot set owner,
provider, model, reasoning, access mode, tool, MCP, shell, command, env, raw
SQL, browser auth, or dispatch behavior. Metadata still passes through the
existing ingress unsafe-key sanitizer.

The response is intentionally small and redacted: schema version, admitted
state, duplicate state, input id, target session id, source, capability, event
type, payload SHA-256, external event id hash, metadata keys, and
`run_dispatched=false`. It does not include raw prompt, raw payload, external
event id, client id, metadata values, or adapter-specific command details.

Agent-club capability descriptors are metadata only in this slice. A descriptor
may declare `id`, `title`, `description`, `kind`, `risk`, `input_schema`,
`output_schema`, `dispatch=admit_only`, approval semantics, and version.
Conservative risk values are `read_only`, `local_read`, `network_read`,
`local_write`, `network_write`, `external_mutation`, `execute`,
`secret_access`, and `unknown`; approval is `none` or `required`.

Trusted bindings link a descriptor to `client_type=ingress`, a concrete
`client_id`, optional source restrictions, optional event-type restrictions,
optional safe metadata keys, and an enabled flag. They are gateway-owned local
policy, not project-provided authority. When a registry is configured, the
event route rejects unknown capabilities, disabled capabilities, source/event
mismatches, and disallowed metadata keys before writing ingress audit or
session inputs. When no registry is configured, the lower-level normalized
event route remains permissive for local tests and direct operator-owned
adapters.

`GET /v1/agentclub/capabilities` is read-only discovery. It returns enabled
descriptors and safe binding metadata visible to the current actor. It does
not include secrets, raw prompts, payloads, environment variables, command
lines, or metadata values.

The gateway does not load project manifests, read `.billyharness` integration
files, execute capabilities directly, or run schedules in this slice. A later
configuration slice can add a trusted gateway config file under the operator's
Billyharness home, but project-local manifests need a separate install/hash
story first.

Generic owner scoping now includes `SessionOwner.ClientID` plus
`X-Billyharness-Session-Client-ID`. Client ID can scope non-Telegram/non-TUI
owners such as `client_type=ingress` rules. If a stored owner has `client_id`,
the actor must present the same client ID before list/read/mutation access is
allowed.

## Durable Store

Durability is enabled only when `ServerOptions.SessionStoreDir` is non-empty.
`DefaultSessionStoreDir()` resolves to `config.BillyHomeDir()/gateway-sessions`,
but construction decides whether the server actually uses a store.

When a store is configured, `NewServerWithOptionsFromSettings` loads only
`manifest.json` from each existing session directory, marks promoted-but-not-
completed inputs as ambiguous after restart, attaches the session event
recorder, and places manifest-only stubs in the in-memory map. Startup and
`GET /v1/sessions` do not replay `history.jsonl` or `events.jsonl`; routes that
need messages or full live state materialize exactly one session through the
`s.session` choke point by replaying that session's history and status files.

Current store layout:

```text
<store>/
  ingress-audit.jsonl
  <session-id>/
    manifest.json
    history.jsonl
    events.jsonl
    inputs.jsonl
    config.snapshot.json
    model_provider.snapshot.json
    mcp.snapshot.json
  <session-id>.json
```

The session directory is the canonical current format. The root
`<session-id>.json` file is still written and read as a legacy compatibility
snapshot.

Store semantics:

- `manifest.json` records schema version, session id, file names, history/event
  sequence numbers, message count, attachment/image counts, owner, history
  hash, and the latest listing/status fields needed for `GET /v1/sessions`
  (provider/model/profile/reasoning/access mode, run sequence, last event,
  last error, model/tool calls, dropped events).
- `history.jsonl` stores full message snapshots, not individual message deltas.
  A new record is appended when the message hash changes.
- `events.jsonl` stores protocol events enriched by the gateway with monotonic
  `seq`, source `gateway`, `run_id`, timestamp, event type, session id, and run
  sequence. The store validates the session record, v1 protocol envelope, and
  lifecycle ordering before appending; rejected events do not consume sequence
  numbers or become durable history.
- `inputs.jsonl` stores input admission, promotion, completion, and
  restart-ambiguity records for idempotency. Completion records can include
  terminal status and failure reason.
- `ingress-audit.jsonl` is a top-level gateway-store ledger for external
  ingress decisions. It records only redacted data: sequence, timestamp,
  admitted/rejected decision, reason, rule/source, external event id hash,
  payload hash, target session id, admitted input id, duplicate marker, client
  id hash, client type, and metadata keys. It does not store raw bodies,
  prompts, metadata values, or external ids.
- snapshot JSON files capture selected runtime/config/model/MCP state for
  offline inspection.
- store directories are forced to `0700`; manifest, snapshots, legacy
  snapshots, and JSONL append targets are written privately.

Replay is strict when a session is materialized, inspected, streamed, or
mutated. `replaySessionHistory`, `lastSessionEventSeq`, `replaySessionStatus`,
`replaySessionEventsAfter`, and `replaySessionInputs` validate schema version,
monotonic sequence, expected `session_id`, event type, and lifecycle where
applicable through `internal/eventlog`. Startup session listing intentionally
defers history/event corruption to the first route that needs that specific
session's replay.
Ingress audit replay validates schema version, gapless sequence, decision
vocabulary, and hash shapes. It is separate from session event replay because
rejected ingress can happen before any session run exists, and admitted ingress
is not itself runtime execution.
Session event replay allows open active runs, turns, steps, and tool attempts;
closed-lifecycle checks are reserved for callers that know an artifact is
complete.

Input ledger corruption is quarantined separately from the session event log.
When startup can otherwise load a session but `inputs.jsonl` fails strict replay,
the gateway renames the input ledger to `inputs.jsonl.corrupt-<timestamp>` and
continues loading the session with a fresh input ledger instead of hiding the
usable session.

Offline helpers in `session_export.go` and `session_inspect.go` load transcripts,
list stored sessions, inspect manifests/files/event types/output refs/turn
changes, and fall back to legacy snapshots when no session directory exists.
Stored-session inspection feeds replayed events through the shared
`internal/clientux/projector` path, compares raw lifecycle counts to the
projected snapshot, and reports sequence range, last event identity, projection
hash, and mismatch reasons when projector parity fails.

Stored-session readiness separates message snapshots from event replay. A legacy
or JSONL history snapshot can be `message_snapshot_ready` while
`event_replay_ready` stays false when event JSONL is missing, corrupt, or only a
partial/open lifecycle. The compatibility `offline_replay_ready` field follows
`event_replay_ready`; it does not mean "messages can be loaded."

`GET /v1/sessions/{id}/inspect` exposes the same redacted inspection for a live
gateway session, subject to the normal session read authority boundary. It
prefers durable store inspection and reports owner scope, lifecycle open/closed
counts, projector parity, output-ref checks, input-ledger state counts, and
replay readiness. If a session exists only in memory, it returns a warning
instead of claiming durable replay truth.

Undo/redo restore is fail-closed at the gateway boundary. The gateway loads the
checkpoint patch artifact through the `patch_output_ref` recorded on the
`turn.change_recorded` event and verifies the recorded
`patch_output_ref_sha256` before preview, undo, or redo. Before writing files
back, `internal/checkpoint` rechecks that every restored path is inside the
configured workspace roots and that existing symlink ancestry does not escape
those roots. Tampered, moved, symlinked, non-regular, out-of-root, or conflicting
patches fail before workspace mutation.

After a successful checkpoint restore, the gateway must append the corresponding
durable event before returning success: `turn.change_reverted` for undo and
`turn.change_recorded` with status `redone` for redo. If that append fails after
files were restored, the route immediately applies the opposite checkpoint
operation as rollback and returns HTTP 500. The event log therefore never claims
an undo/redo happened unless the workspace mutation is also represented in
durable replay; if rollback itself fails, the response reports both the rollback
failure and the original persistence failure.

## Replay and Live Streams

Gateway session JSONL is the durable source of truth. Live HTTP streams are
progress channels; clients should keep the last durable `seq` and recover by
replaying `/v1/sessions/{id}/events?after_seq=<seq>&follow=false` or by
following from that cursor.

`POST /v1/run` is stateless. It streams NDJSON with the same stream writer, but
it does not create a session or durable event log.

`POST /v1/sessions/{id}/run` streams live run events while the session recorder
persists observed events. The stream writer has a bounded buffer
(`liveRunStreamBuffer`), and if the writer falls behind it emits
`gateway.stream_gap` with dropped-event count and a replay cursor hint when it
can enqueue that hint without blocking the handler. Final gap emission and
writer drain are bounded so a dead or stalled client cannot pin a run handler.
The hint is useful for session runs; stateless `/v1/run` has no session replay
path.

`GET /v1/sessions/{id}/events` has two query parameters:

- `after_seq`: optional non-negative durable cursor;
- `follow`: optional boolean, defaulting to `true`.

Behavior by mode:

- with `after_seq` and a configured store, the handler first replays durable
  events after the cursor;
- with `follow=false`, it returns after the initial replay/status emission;
- with `follow=true`, `after_seq`, and a store, the live hub is used mainly as a
  wake signal and a ticker also polls durable storage. Each wake replays from
  the last emitted durable `seq`;
- if a live event has `seq == 0`, the handler may emit it as a non-durable live
  event after replaying durable events. This covers failures that could not be
  appended;
- without `after_seq`, the handler emits a current `session.status` event and
  then follows live hub events;
- without a store, `after_seq` can only filter live events already seen by the
  process. It cannot provide catch-up after disconnect or restart.

Client-side `gatewayclient.decodeEvents` skips already-seen events, errors on
durable sequence gaps, tracks terminal run state, and counts
`gateway.stream_gap` events.

## Context Projection

`GET /v1/sessions/{id}/context` combines the session transcript with replayed
events when the store is available. `internal/clientux.BuildContextResponse` and
`BuildContextResponseWithOptions` estimate active context from messages,
summarize sources and largest contributors, and derive runtime, usage,
compaction, prompt inventory, and output-ref metrics from events. If replay is
unavailable, the gateway returns the message-derived context report with a
warning.

Memory context is session-locked. The live gateway context response compares
the memory hash captured in the session transcript with the currently rendered
memory index for the session profile and reports only status, policy, hashes,
counts, cap state, and generic load errors. It does not promote new memory into
an existing session implicitly. Offline stored-session context reports the
locked session memory hash only because it cannot safely prove the current
memory root/profile state from the durable session bundle alone.

Run admission records a broader session-locked context epoch before the agent
run is persisted. The epoch stores only hashes: effective config, tool catalog,
MCP catalog/status, profile instructions, prompt inventory, AGENTS, memory,
project context, and promoted MCP instructions when present. `session.status`
and `run.started` both carry the run epoch plus `context_epoch_drift`; follow-up
runs compare the first locked epoch with a freshly rendered ambient epoch and
emit changed-field warnings instead of silently mixing AGENTS, memory, MCP, or
config drift.
Stored-session context rebuilds the same drift state from `events.jsonl`, so
offline replay/fork inspection does not need to trust the current filesystem.

For compaction replay, the context response keeps the latest compaction ID,
event sequence, `context_epoch`, before/after token estimates, reason/strategy,
and audit hashes such as post-history hash. `gatewayclient.FormatSessionContext`
prints the epoch and a short post-history hash so operators can line up context
status with durable `context.compacted` events.

The response also includes a compact diagnostics index: current epoch,
compaction/threshold/tool/helper counts, protected-prefix versus body token
split, context-window and compaction margins, context epoch status, and
shortable hashes for config, tools, MCP, docs index, memory, project, AGENTS,
MCP instructions, prompt inventory, and the latest compaction post-history
state. These fields are derived from message snapshots and durable events; they
are an operator/debug projection, not separate runtime state.

## API and Client Boundary

`internal/gatewayapi` is the shared contract package. It contains DTOs for
requests/responses, session status/list/owner/context/undo/user-input/benchmark
payloads, and the `X-Billyharness-Session-*` header names. It must not import
the gateway server or client packages.

`internal/gatewayclient` is the shared HTTP client package for TUI, Telegram,
CLI helpers, and future client surfaces. It:

- normalizes base URLs through `gatewayapi`;
- injects bearer auth from `BILLYHARNESS_GATEWAY_AUTH_TOKEN` or legacy
  `FAST_AGENT_GATEWAY_AUTH_TOKEN`;
- retries once around readiness on connection refused;
- exposes helpers for create/list/get/status/context/inspect/run/follow/replay/
  input/cancel/user-input/undo/redo. Most are typed; inspect currently returns
  raw JSON so CLI/debug surfaces can reuse the gateway inspection shape without
  moving store-only DTOs into the client package;
- injects owner scope headers from `WithSessionOwner(ctx, owner)`;
- decodes NDJSON protocol events and reports sequence gaps or run failures as
  typed errors.

Client surfaces should import `gatewayapi`, `gatewayclient`, and `clientux` as
needed. They should not import `internal/gateway` server internals.

## Security and Scope

`Server.Handler` always wraps the mux with `httpSecurityMiddleware`.

Current security behavior:

- `/health` bypasses gateway auth for cheap process liveness.
- `/ready` bypasses gateway auth for bounded readiness. It returns counts and
  redacted state for effective config, visible tools, MCP catalog health, and
  session-store startup diagnostics, but not raw MCP metadata, schemas, prompts,
  or store paths.
- All `/v1/` routes are treated as browser-reachable protected gateway
  surfaces. Before handlers run, loopback requests must use an allowed loopback
  host, and any `Origin` or `Referer` header must match the gateway host.
- When a gateway bearer token is configured, `/v1/` requests require a matching
  bearer token even from loopback remote addresses. This includes state-bearing
  read routes such as sessions, events, config status, auth status, MCP status,
  tool catalogs, and process summaries.
- When `ServerOptions.RequireMutationAuth` is true, mutating `/v1/` requests
  must also pass `application/json` content-type checks when a body is present.
- If mutation auth is required but no bearer token is configured, mutating
  requests receive `503`.
- `DevAllowUnauthenticatedLoopbackMutations` is an explicit development bypass
  for loopback mutations. It does not bypass configured-token protection for
  `/v1/` read routes.
- Provider/model/thinking/reasoning overrides are accepted only from bearer-
  authenticated mutation requests when mutation auth is required. Other mutation
  requests keep the server provider/model defaults.
- `max_tool_rounds` is capped at the configured maximum, and `access_mode`
  cannot be escalated above the configured gateway access mode.

Current worktree owner/scope behavior:

- clients can send session actor metadata through the shared
  `X-Billyharness-Session-*` headers;
- `SessionOwner.ClientID` and `X-Billyharness-Session-Client-ID` provide a
  generic owner principal for ingress and future clients that do not have
  Telegram/TUI-specific IDs;
- `gatewayclient.WithSessionOwner` is the intended way for clients to attach
  that metadata;
- create requests may include `owner` in the JSON body. If the request also has
  scoped owner headers, the body owner must match the actor. If the body owner
  is empty and headers are present, the gateway stores the actor as owner;
- session list responses are filtered for scoped actors;
- scoped actors may read their own sessions and legacy unowned sessions;
- scoped actors may not mutate legacy unowned sessions;
- cross-owner reads and mutations are denied with `403`;
- unscoped local callers are still treated as unscoped gateway operators by the
  current code.

Owner headers are not a cryptographic identity by themselves. They are a
gateway-enforced scoping claim inside the HTTP security boundary. Do not treat
them as a substitute for bearer auth or network exposure controls.

See `docs/adr/0002-gateway-owns-session-authority.md` for the durable decision
behind this authority boundary.
See `docs/adr/0009-external-ingress-is-gateway-admission.md` for the external
ingress invariant.
