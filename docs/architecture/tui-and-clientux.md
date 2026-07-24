# TUI And Client UX Architecture

This document records the durable architecture for the terminal UI, transcript
projection, client UX metadata, rendering, selection/copy behavior, saved local
chat state, and the local runtime adapter.

Status note: this document was reviewed against the dirty current worktree on
2026-07-05. Claims describe this checkout, not necessarily a clean release
commit.

The main rule is that UI surfaces are projections of protocol events and saved
client state. They are not alternate owners of agent execution, provider
construction, gateway server behavior, or tool policy.

## Package Ownership

`internal/tui` owns the Bubble Tea model, input handling, local chat state,
gateway-session client calls, event application, reflow, and the concrete TUI
action handlers. It may import shared DTO/client packages such as
`internal/gatewayapi` and `internal/gatewayclient`, but it must not import the
gateway server package, `internal/agent`, `internal/provider`, or
`internal/tools` directly.

`internal/tui/runtimeclient` is the only TUI-owned package that composes the
local runtime. It imports `internal/agent`, `internal/provider`, and
`internal/tools` so local TUI mode can run without a gateway. Keep Bubble Tea
state, transcript blocks, rendering, selection, and slash command handling out
of this adapter.

`internal/tui/jobclient` is the separate durable-job control-plane adapter. It
wraps `internal/gatewayclient` calls in asynchronous Bubble Tea commands and
returns typed create/list/show/action/attempt/artifact messages. It imports only
`internal/gatewayapi` and `internal/gatewayclient`; it does not import the pure
job domain, gateway server, scheduler, store, service, provider, tools, or TUI
model. Request construction and view state stay in `internal/tui`, while the
gateway remains the only owner of execution and durable state.

`internal/clientux` owns shared client-facing metadata and context projection
helpers. `internal/clientux/actions.go` defines frontend-neutral action
metadata; runtime handlers stay in concrete clients such as
`internal/tui/actions.go`. `internal/clientux/context.go` builds shared context
responses from messages/events and runtime metadata.

`internal/clientux/projector` owns the presentation-neutral event projector.
`internal/clientux/projector/projector.go` accumulates a `Snapshot` with run
state, sequence state, assistant/reasoning text, tool items, usage counters,
context thresholds, turn changes, and todo state. It does not render terminal
text. `internal/clientux/projector/presentation.go` declares which protocol
events affect transcript, compact progress, status, footer, context reports,
low-level lifecycle display, and immediate flushing.

`internal/tui/transcript` owns TUI transcript cells and the protocol-event to
cell projector. It may use `internal/protocol`, `internal/displayfmt`, and
`internal/toolrender`, but it must not import Bubble Tea, rendering packages,
gateway server/runtime packages, provider, tools, agent, or Telegram.

`internal/tui/render` owns terminal rendering helpers and render cache keys. It
has no Billyharness runtime imports. Rendering is downstream of transcript
cells.

`internal/tui/selection` owns viewport-coordinate selection, ANSI-aware text
copying/highlighting, clipboard writes, and OSC52 fallback. It has no
Billyharness runtime imports and does not know about protocol events.

`internal/toolrender` owns compact tool labels and summaries shared by clients.
TUI transcript projection uses it for stable tool titles and raw-copy summaries
without importing tool execution code.

These boundaries are also represented in `docs/architecture.md` and checked by
`internal/architecture/architecture_test.go`.

## Event Flow

Both runtime modes feed the same TUI event application path:

```text
local runtime or gateway stream
  -> protocol.Event
  -> internal/clientux/projector Snapshot
  -> internal/tui/transcript Projector cells
  -> internal/tui/render terminal text
  -> internal/tui/selection viewport copy/highlight
```

In local mode, `internal/tui/tui.go` calls `Model.runLocal`, which delegates to
`internal/tui/runtimeclient.RunLocal`. The adapter constructs the provider,
tool registry, agent, initial user message, and local MCP status from projected
runtime settings, then emits `protocol.Event` values back through the TUI event
channel.

In gateway mode, `internal/tui/gateway_session.go` uses `internal/gatewayclient`
and `internal/gatewayapi` DTOs. It creates gateway sessions with a TUI owner,
runs `/v1/sessions/{id}/run`, receives streamed events, replays durable events
after detected stream gaps, and fetches final messages from the gateway session.
The TUI talks to gateway transport/client packages, not the gateway server
internals.

Durable-job control is a parallel control-plane flow rather than a protocol
event stream:

```text
TUI /jobs dashboard, detail, or wizard
  -> internal/tui/jobclient Bubble Tea command
  -> internal/gatewayclient typed /v1/jobs request
  -> gateway-owned jobservice/runtime/store
  -> typed Bubble Tea result message
  -> TUI projection
```

No provider invocation or workflow loop lives in the TUI process. Closing the
control center or the whole TUI therefore does not cancel a job. Reopening it
lists and fetches canonical gateway state again. A stopped gateway resumes
recoverable work from its durable store when restarted; a TUI session file is
never used as a job checkpoint.

`SessionOwner` metadata identifies the requesting frontend for diagnostics; it
does not partition durable jobs. `/v1/jobs` is an operator-global collection,
and possession of the gateway bearer token permits listing and controlling all
jobs on that gateway. Deploy separate gateway/auth boundaries when operators
must not share this control plane.

`internal/tui/transcript_runtime.go` is where incoming events become UI state.
`Model.applyEvent` ignores already-seen sequenced events, applies the
`clientux/projector` accounting snapshot, uses
`projector.EventPresentationPolicy` to decide whether an event should affect
the transcript, updates status/failure-summary state, and leaves rendering for
the later reflow step.

## Durable Jobs Control Center

`/jobs` opens a full-screen control center with three related surfaces:

- The dashboard lists canonical gateway job summaries and keeps selection by
  job ID rather than by a volatile list index.
- The detail view fetches one job, displays lifecycle/terminal state, cycles,
  model/token/attempt budgets, active stage, final result, artifacts, and a
  bounded latest-attempt tail so worker/reducer/supervisor role progress is
  visible without placing unbounded history in one response.
- The creation wizard builds one typed `gatewayapi.CreateJobRequest`, reviews
  it, and submits it through `jobclient.CreateCmd`. The client allocates a
  stable job ID before POST so a lost acknowledgement can be retried without
  creating a second durable job.

The keyboard flow is dashboard selection (`Up`/`Down`, then `Enter`) into
detail, `Esc` back, and `n` from the dashboard into the wizard. Detail actions
are state-gated: pause, resume, cancellation, and refresh are offered only when
their transition is meaningful. Cancellation requires explicit confirmation.
Job work remains background gateway work and must not set the ordinary chat
composer's `busy` flag.

The wizard exposes all eight immutable workflow presets: `general`,
`research`, `coding`, `debug`, `review`, `planning`, `writing`, and `compare`.
It also captures provider/model/reasoning, one to four worker roles, hard
duration, minimum and maximum cycles, optional minimum runtime/cadence,
attempt/model-call/token budgets, and the explicit tool/read/write/network/
provider authority envelope. Authority is fail-closed and the server computes
the effective intersection; the TUI cannot grant ambient process permissions.

Built-in provider choices come from the shared model catalog. The optional
custom choice comes from the TUI process's one resolved OpenAI-compatible
binding and is offered only with an explicit base URL and model; the gateway
still validates it against the gateway process's exact configured binding.
The jobs API currently has no route-capabilities endpoint, so a TUI connected
to a differently configured remote gateway cannot discover that gateway's
custom provider/model pair. The UI must not present local custom configuration
as remotely discovered capability.

Worker count describes the number of predeclared roles in eligible parallel
workflow stages. It is not the number of provider calls guaranteed to run at
once. The gateway's process-wide `-job-concurrency` semaphore defaults to `1`
and caps durable provider invocations across every job. A four-worker preset
therefore executes serially under the default unless the operator deliberately
raises the gateway cap and the provider permits it.

Timing controls have deliberately different meanings. `duration` is the hard
wall-clock stop. `min_cycles` forces complete worker/reducer/supervisor passes
before success and is the primary iteration-depth floor. `min_runtime` is only
an admission-relative earliest-success wall-clock gate: queueing, operator
pause, gateway downtime, and cadence waits count, so it never promises active
compute. The immutable job spec also persists the gateway's UTC admission
instant, allowing the projection to show canonical elapsed wall time after a
TUI or gateway restart. Legacy specs with no timestamp remain valid and render
elapsed as unavailable. The durable scheduler, not UI polling, owns all three
rules.

The control center requires gateway mode even though normal chat supports a
local runtime. A local-only TUI must report that `/jobs` needs a reachable
gateway rather than starting an in-process scheduler. Operators should start
the gateway from the intended workspace root before opening the TUI; that
working directory is the default root unless explicit configuration replaces
it. Server workspace roots are not exposed through the current jobs API, and
wizard authority can only narrow—not discover or widen—the server boundary.
The wizard consequently parses roots using the TUI host's path grammar. This is
fail-closed for symlink/case mismatches, but a cross-OS TUI cannot author a path
which is invalid locally but valid on the remote gateway; use a compatible-host
CLI/API until gateway capabilities expose remote path semantics.

Selecting the built-in Qwen Token Plan route requires an explicit unattended-
use warning confirmation before create. This is an operator guard, not a terms
override: confirmation does not make automated scripts, backends, or
non-interactive batch execution permissible. An unattended job must use a
configured endpoint and plan whose terms actually permit automation.

## Projection Versus Rendering

Projection decides what a client knows. Rendering decides how the terminal
shows it.

`internal/clientux/projector` is projection-neutral across clients. It tracks
run state, model/tool counts, token deltas, helper usage, tool status by
call ID, sequence gaps, context thresholds, turn changes, and todo state. It
also separates event presentation policy from concrete rendering; for example,
low-level tool progress can affect the status line without necessarily adding
transcript cells.

`internal/tui/transcript.Projector` turns selected protocol events into
`transcript.Cell` values. Assistant and reasoning deltas append to live cells;
run start/completion/failure finalizes live cells; tool results are matched by
call ID; tool batch step events are matched by step ID; compaction, context,
turn-change, audit, and failed run summaries become typed status/tool cells.
Routine run start/completion status stays in the status/footer projections so
successful runs do not leave persistent transcript noise.

`internal/tui/render` receives already-derived cell text and view settings. It
does not inspect protocol events, invoke tools, or query runtime state.
`internal/tui/transcript_runtime.go` adapts a `transcript.Cell` into render
inputs, applies tool/thinking visibility modes, uses render cache keys, and
sets the viewport content.

This separation keeps replay and gateway/local parity tractable: a future
client can consume the same event stream and projector data without copying TUI
rendering rules.

## Transcript Cells

`internal/tui/transcript/cell.go` defines the durable TUI transcript shape:

- `Kind` is the broad display family: user, assistant, reasoning, tool, status,
  audit, or error.
- `CellType` is the typed behavior category, such as `assistant_stream`,
  `assistant_final`, `tool_call`, `tool_batch`, `tool_group`, `compaction`,
  `mcp_status`, `run_summary`, or `error`.
- `Title` and `Content` are display inputs.
- `RawCopy` is the semantic copy/export text and may differ from rich rendered
  text.
- `EventType`, `TurnID`, `StepID`, `CallID`, `AttemptID`, `ParentStepID`, and
  `ToolName` preserve event identity for upserts, grouping, copying, and tests.
- `Collapsed` and `CollapseSet` persist explicit/default collapse state.
- `RenderCacheKey` is derived from cell fields by `internal/tui/render/cache.go`
  and is used by the TUI render cache.

`EncodeCells` and `DecodeCells` convert cells to the saved-session DTO. The
persisted DTO intentionally stores the semantic fields and collapse state, not
runtime-only timestamps or live-render state.

`internal/tui/transcript/index.go` builds lookup maps for tool call IDs,
step IDs, and the diagnostic run-summary cell. Projectors use this to upsert
cells rather than append duplicate lifecycle noise. The TUI filters routine
successful run summaries when saving or restoring local transcript blocks.

## Saved Sessions

TUI saved sessions are local UI state, not the gateway event log. The saved
session shape in `internal/tui/sessions.go` stores a local chat ID, title,
creation/update times, model/profile/reasoning choices, optional gateway
session ID and last gateway event sequence, messages, encoded transcript cells,
token/call counters, and projected usage.

Settings and sessions live under `BILLYHARNESS_HOME` when set, otherwise under
the user's `billyharness` directory. `internal/tui/settings.go` creates the
settings file and `sessions` directory, normalizes view modes, and persists
settings with owner-only file permissions.

Saved-chat lookup keeps ID-prefix behavior authoritative. `/resume` and `/fork`
first match exact or prefix session IDs; ambiguous prefixes remain errors. Only
when no ID prefix matches do they fall back to case-insensitive title/message
text search and render snippet results for multi-match queries.

Current resume behavior is split by runtime mode:

- Local mode restores saved messages, cells, view/accounting state, and runtime
  selections from the local session file.
- Gateway mode restores the local session file and, when a saved gateway session
  ID exists, replays gateway events after the saved sequence. If replay cannot
  be used and fallback is allowed, the TUI creates a new gateway session for the
  restored local chat.
- Forking a chat copies the local state into a new local chat ID, clears gateway
  session identity, and creates a new gateway session when gateway mode is
  active.

Because the gateway JSONL/session store remains the durable source of truth for
gateway runs, client code should treat saved TUI cells as a cached client
projection and rehydrate from gateway events when available.

## Rendering

The TUI reflow path is in `internal/tui/transcript_runtime.go`. `Model.reflow`
filters cells according to `thinkView` and `toolView`, hides grouped context
tool details when appropriate, renders each visible cell through
`renderBlockCached`, records which viewport lines are selectable, and writes the
unhighlighted transcript to `viewportContent`.

Transcript find is a viewport feature layered on top of that unhighlighted
content. `/find` stores the current query and byte-range matches on the TUI
model, while `ctrl+f` / `alt+f` navigate the viewport's built-in highlights.
Because `viewport.SetContent` clears highlights during every reflow,
`Model.reflow` must reapply an active find query after writing
`viewportContent`.

Rich rendering and raw rendering are different views of the same cells:

- Raw mode uses `RawCopy`, then `Content`, then `Title`.
- Rich mode renders assistant markdown with
  `internal/tui/render.RenderAssistantMarkdown`, renders user/assistant blocks
  as content, and renders tool/status/reasoning/error blocks through
  `internal/tui/render.RenderActivityBlock`.
- User and assistant cells add compact role markers in the TUI render layer
  (`you` and `assistant`) so dialogue turns are visually separable without
  changing saved cell content, raw copy, or transcript exports.
- Tool/thinking collapsed and hidden modes are rendering and selection choices;
  they do not alter the underlying transcript cells.

Theme styles intentionally set deterministic foreground colors without painting
ordinary terminal backgrounds. This keeps light/dark Billyharness themes from
fighting the user's terminal theme; the explicit yellow mouse-selection
highlight is the normal exception.

The render cache key includes cell identity/content, live state, event
identity, collapse state, terminal width, theme, and view modes. That keeps
terminal rendering deterministic without coupling the renderer to protocol
events.

The normal inline status line is intentionally dense and Claude-like: workspace,
git branch/diff, model/reasoning, and active context only. Detailed cache,
helper API, cost, version, profile, and session counters stay in `/status`,
`/context`, exports, or debug surfaces rather than the always-visible footer.
When the selected provider is the Codex/OpenAI subscription path, the status
line may append Codex quota windows from the Codex app-server account rate-limit
read method: the primary window label/percent/reset first, then the secondary
percent/reset. That quota segment is optional and drops before the core
workspace/git/model/context segment at narrow terminal widths.
The transient run strip above the input is even smaller: a spinner,
`working`, and elapsed time, with tool names and lifecycle details kept in
transcript/status surfaces.

## Selection, Copy, And Export Boundaries

Mouse selection works over rendered viewport coordinates, not source cell
offsets. `internal/tui/selection` maps mouse positions through viewport offset,
strips ANSI for selected text and byte ranges, highlights display-column ranges
in the rendered content, and copies through the system clipboard with OSC52 as
a fallback.

`Model.reflow` builds `viewportSelectableLines` alongside rendered content.
Status/audit/run-summary/compaction/MCP lines, collapsed tool details, grouped
tool summaries, and collapsed reasoning can be excluded from mouse copy even
when they are visible. This is why selection lives downstream of rendering but
still receives a line filter from the TUI model.

Semantic copy is TUI behavior in `internal/tui/selection_runtime.go`.
`/copy selected` uses the selected cell's `RawCopy`; `/copy last` searches the
last assistant cell; `/copy tool` uses the selected or latest tool cell raw
output; `/copy transcript` and `/copy transcript-rich` format all current
cells; `/copy code` extracts the last fenced code block from raw copy; and
`/copy command` uses the input line.

`internal/tui/transcript/export.go` is a formatter boundary. It can format
cells, messages, events, or a message-plus-event session as raw or rich text.
Generated raw output-reference records quote path-like values such as
`output_ref` and `preview` so incident artifacts do not contain ambiguous
unquoted paths.

The current TUI uses these helpers for semantic transcript copy and for
`/export`. Semantic copy remains body-only. `/export` wraps the body in an
incident-grade artifact header from `internal/clientux/transcript_export.go`.
The header records source store, source mode (`cells`, `messages`, `events`,
or `combined`), transcript mode, runtime mode, local/gateway session IDs, last
known gateway sequence, sequence range, model/profile/access mode, export time,
redaction mode, body hash/size, and warnings. TUI event exports are explicit
about being projected client state rather than durable gateway JSONL replay.

## Client UX Metadata

Shared action metadata belongs in `internal/clientux/actions.go`. It describes
stable IDs, slash names, aliases, categories, summaries, and Telegram metadata.
It must stay frontend-neutral: no command execution, gateway calls, file IO, or
Bubble Tea state.

The TUI turns that metadata into concrete behavior in `internal/tui/actions.go`
and `internal/tui/commands.go`. That includes key bindings, slash arguments,
command palette behavior, settings mutation, chat resume/fork/new behavior,
copy commands, transcript find, gateway reconnect, and viewport/block
navigation.

Image input is also a TUI input concern. `/attach PATH` imports a local image
file, while `Alt+V` reads raw image bytes from the system clipboard, stores them
as a private attachment ref, and leaves `Ctrl+V` for normal text paste.

The command registry composes shared action metadata with prompt commands,
profile metadata, and MCP prompt metadata. The TUI may display/search that
registry, but client UX metadata should remain reusable by Telegram and future
clients.

### Debug Snapshot Contract

`internal/clientux/debug_snapshot.go` defines the frontend-neutral
`TUIDebugSnapshot` schema. The TUI exposes it through `/debug` and the
compatibility path `/status debug`, both rendered as a redacted info block.
The snapshot includes local chat identity, gateway session identity, last
gateway event sequence, runtime mode/settings, stream queue state, client UX
projector state, viewport/selection coordinates, transcript/export byte counts
and hashes, stale flags, block/cell counts, and diagnostic hints.

Snapshot content must stay incident-safe: raw transcript text, selected
viewport text, provider secrets, bearer tokens, secret-bearing URLs, local
settings paths passed as redaction inputs, and secret-like errors are not
printed. Transcript, viewport, selection, and export bodies are represented by
lengths and hashes so an operator can compare states without leaking the
conversation.

## Runtime Boundary

The local runtime adapter exists to keep `internal/tui` from becoming another
runtime composition root.

`internal/tui/runtimeclient/runtimeclient.go` owns:

- building initial instruction messages via agent settings;
- constructing the provider from `config.ProviderBinding`;
- constructing the tool registry and MCP-backed tools from projected settings;
- wiring provider-backed web summarization for tools;
- running the local agent with `PromptSubmitOptions{Source: "tui"}`;
- loading local MCP status for `/mcp` when no gateway is configured.

`internal/tui/runtime_config.go` projects the selected TUI config into
`runtimeclient.Settings`. The main TUI model can change model, reasoning,
profile, access mode, and view settings, but local execution still crosses the
`runtimeclient` adapter before it reaches agent/provider/tools code.

Gateway mode is the other runtime boundary. It crosses `internal/gatewayclient`
and `internal/gatewayapi`, not `internal/gateway`. TUI-only features that need
gateway-owned state, such as undo/redo diff application, process status, gateway
context, or answering gateway user-input requests, should stay on typed gateway
client endpoints.

## Current Hardening And Future Boundaries

Current hardening:

- `docs/architecture.md` records the import boundaries, and
  `internal/architecture/architecture_test.go` enforces them.
- `internal/clientux/projector` ignores stale sequenced events and records
  sequence gaps in snapshots.
- Gateway runs replay durable events after detected stream gaps before final
  message fetch.
- `internal/clientux/projector/presentation.go` prevents every low-level event
  from becoming transcript noise.
- `internal/clientux/debug_snapshot.go` provides the redacted TUI debug
  snapshot used by `/debug` and `/status debug`; it reports hashes and state
  counters instead of raw transcript, selection, or export bodies.
- `internal/clientux/transcript_export.go` provides the metadata header for
  incident-grade transcript exports, while transcript body formatting remains
  under `internal/tui/transcript`.
- `internal/tui/transcript_render_test.go`,
  `internal/tui/cross_surface_consistency_test.go`,
  `internal/tui/presentation_policy_test.go`, and package tests under
  `internal/tui/transcript`, `internal/tui/render`, `internal/tui/selection`,
  and `internal/clientux/projector` cover projection/render/copy parity.

Future hardening should preserve these ownership lines. Runtime hardening should
strengthen gateway/eventlog/runtimeclient seams rather than importing gateway,
agent, provider, or tools into `internal/tui`.
