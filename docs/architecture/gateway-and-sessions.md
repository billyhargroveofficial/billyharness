# Gateway and Sessions Architecture

Status note: this document is written against the current worktree on
2026-07-03. The worktree already contains uncommitted gateway/security/client
changes. In particular, mutation-auth hardening and session-owner header
scoping are current worktree behavior, not something to assume is already in a
clean release commit.

Code anchors:

- `internal/gateway/gateway.go`: server construction, route registration,
  session handlers, run settings projection, session context responses.
- `internal/gateway/response.go`: NDJSON stream writer, live stream buffering,
  `gateway.stream_gap`, redacted JSON responses.
- `internal/gateway/session_events.go`: gateway session wrapper, event hub,
  status projection, recording and publishing of run events.
- `internal/gateway/session_store.go`: durable session manifest, history,
  event replay, legacy snapshot compatibility, private file permissions.
- `internal/gateway/session_inputs.go`: durable input admission and
  idempotency state.
- `internal/gateway/http_security.go`: HTTP bearer, mutation, origin, host,
  content-type, and privilege-clamping rules.
- `internal/gateway/session_authz.go`: session-owner header parsing and
  read/mutation authorization.
- `internal/gatewayapi/types.go`: shared request/response DTOs and owner header
  names.
- `internal/gatewayclient/client.go`: shared HTTP client helpers, owner header
  injection, session methods, NDJSON decoder, sequence-gap detection.
- `internal/gatewaybase/gatewaybase.go`: gateway URL normalization, auth-token
  environment lookup, readiness probe, unavailable hints.
- `internal/session/session.go`: presentation-neutral message state, single-run
  locking, cancellation, and rollback.
- `internal/clientux/context.go`: client-facing context/status projection from
  messages plus replayed events.

## Responsibilities

`internal/gateway` is the HTTP adapter that composes runtime pieces for local
and remote clients. It owns:

- gateway HTTP route registration and response encoding;
- session creation, listing, status, context, event replay, input admission,
  run, cancel, undo, redo, and user-input routes;
- durable session persistence when `ServerOptions.SessionStoreDir` is set;
- startup reload of durable sessions into the in-memory session map;
- event recording before live fanout for session runs;
- gateway-level security checks before handlers see mutating requests;
- owner/scope filtering for session reads and mutations in the current
  worktree;
- benchmark, tool, MCP, config, process, and auth inspection endpoints.

It does not own client rendering. Shared transport DTOs belong in
`internal/gatewayapi`; client request helpers belong in `internal/gatewayclient`;
URL/auth/readiness helpers belong in `internal/gatewaybase`; neutral projection
logic belongs in `internal/clientux`.

## HTTP Surface

Routes are registered in `Server.routes` in `internal/gateway/gateway.go`.
Future agents should update this section from that route table, not from memory.

| Route | Role |
| --- | --- |
| `GET /health` | unauthenticated liveness and active provider/model summary |
| `GET /v1/auth/status` | sanitized provider auth status |
| `POST /v1/auth/deepseek` | persist a DeepSeek API key through the credentials manager |
| `POST /v1/auth/codex/import` | import or save Codex auth JSON through the credentials manager |
| `GET /v1/config` | sanitized resolved config plus diagnostics |
| `GET /v1/benchmarks` | benchmark artifact listing |
| `GET /v1/tools` | registry tool specs |
| `GET /v1/mcp` | runtime MCP status, prompts, and server instructions |
| `GET /v1/processes` | managed shell-process status from the tools registry |
| `POST /v1/run` | stateless live NDJSON run stream; no durable session history |
| `GET /v1/sessions` | list sessions visible to the request actor |
| `POST /v1/sessions` | create a session and optionally persist owner metadata |
| `GET /v1/sessions/{id}` | fetch session metadata and messages |
| `GET /v1/sessions/{id}/status` | fetch current session status |
| `GET /v1/sessions/{id}/context` | project context, runtime, usage, prompt, and replay-derived metrics |
| `GET /v1/sessions/{id}/events` | NDJSON replay/follow stream for session events |
| `POST /v1/sessions/{id}/inputs` | admit an idempotent pending input without running it |
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

The gateway `Session` wraps `internal/session.Session`. The lower-level
`internal/session` package owns only message state, single-active-run locking,
and cancellation/rollback:

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
3. promotes the admitted input, runs the agent through the session thread,
   observes/persists/publishes run events, saves the resulting transcript, and
   completes the input record with a terminal status.

`POST /v1/sessions/{id}/inputs` is intentionally separate from run execution.
It gives clients a durable idempotency point before a run is promoted.

## Durable Store

Durability is enabled only when `ServerOptions.SessionStoreDir` is non-empty.
`DefaultSessionStoreDir()` resolves to `config.BillyHomeDir()/gateway-sessions`,
but construction decides whether the server actually uses a store.

When a store is configured, `NewServerWithOptionsFromSettings` loads existing
session directories, restores status from replayed events when possible, marks
promoted-but-not-completed inputs as ambiguous after restart, attaches the
session event recorder, and places sessions in the in-memory map.

Current store layout:

```text
<store>/
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
  sequence numbers, message count, owner, and history hash.
- `history.jsonl` stores full message snapshots, not individual message deltas.
  A new record is appended when the message hash changes.
- `events.jsonl` stores protocol events enriched by the gateway with monotonic
  `seq`, source `gateway`, `run_id`, timestamp, event type, session id, and run
  sequence.
- `inputs.jsonl` stores input admission, promotion, completion, and
  restart-ambiguity records for idempotency.
- snapshot JSON files capture selected runtime/config/model/MCP state for
  offline inspection.
- store directories are forced to `0700`; manifest, snapshots, legacy
  snapshots, and JSONL append targets are written privately.

Replay is strict. `replaySessionHistory`, `lastSessionEventSeq`,
`replaySessionStatus`, `replaySessionEventsAfter`, and `replaySessionInputs`
validate schema version, monotonic sequence, expected `session_id`, event type,
and lifecycle where applicable through `internal/eventlog`.

Offline helpers in `session_export.go` and `session_inspect.go` load transcripts,
list stored sessions, inspect manifests/files/event types/output refs/turn
changes, and fall back to legacy snapshots when no session directory exists.

Undo/redo restore is fail-closed at the gateway boundary. The gateway loads the
checkpoint patch artifact through the `patch_output_ref` recorded on the
`turn.change_recorded` event and verifies the recorded
`patch_output_ref_sha256` before preview, undo, or redo. Before writing files
back, `internal/checkpoint` rechecks that every restored path is inside the
configured workspace roots and that existing symlink ancestry does not escape
those roots. Tampered, moved, symlinked, non-regular, out-of-root, or conflicting
patches fail before workspace mutation.

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
`gateway.stream_gap` with dropped-event count and a replay cursor hint. That
hint is useful for session runs; stateless `/v1/run` has no session replay path.

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

For compaction replay, the context response keeps the latest compaction ID,
event sequence, `context_epoch`, before/after token estimates, reason/strategy,
and audit hashes such as post-history hash. `gatewayclient.FormatSessionContext`
prints the epoch and a short post-history hash so operators can line up context
status with durable `context.compacted` events.

The response also includes a compact diagnostics index: current epoch,
compaction/threshold/tool/helper counts, protected-prefix versus body token
split, context-window and compaction margins, and shortable hashes for memory,
project, AGENTS, MCP instructions, prompt inventory, and the latest compaction
post-history state. These fields are derived from message snapshots and durable
events; they are an operator/debug projection, not separate runtime state.

## API and Client Boundary

`internal/gatewayapi` is the shared contract package. It contains DTOs for
requests/responses, session status/list/owner/context/undo/user-input/benchmark
payloads, and the `X-Billyharness-Session-*` header names. It must not import
the gateway server or client packages.

`internal/gatewayclient` is the shared HTTP client package for TUI, Telegram,
CLI helpers, and future client surfaces. It:

- normalizes base URLs through `gatewaybase`;
- injects bearer auth from `BILLYHARNESS_GATEWAY_AUTH_TOKEN` or legacy
  `FAST_AGENT_GATEWAY_AUTH_TOKEN`;
- retries once around readiness on connection refused;
- exposes typed helpers for create/list/get/status/context/run/follow/replay/
  input/cancel/user-input/undo/redo;
- injects owner scope headers from `WithSessionOwner(ctx, owner)`;
- decodes NDJSON protocol events and reports sequence gaps or run failures as
  typed errors.

Client surfaces should import `gatewayapi`, `gatewayclient`, `gatewaybase`, and
`clientux` as needed. They should not import `internal/gateway` server internals.

## Security and Scope

`Server.Handler` always wraps the mux with `httpSecurityMiddleware`.

Current security behavior:

- `/health` bypasses gateway auth so readiness probes can work.
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
