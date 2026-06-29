# Architecture Decomposition TODO

This document is the execution backlog for keeping billyharness fast and
extendable as it grows. It was created from six parallel architecture reviews
covering runtime/events, tools/web/MCP, TUI, Telegram/gateway UX,
config/provider/auth, and general repository hygiene.

The goal is not to make the codebase abstract for its own sake. The goal is to
stop the harness from turning into a pile of large cross-cutting files where
every new feature touches TUI, gateway, tools, provider, and config at once.

## Current Snapshot

Generated on 2026-06-29 from the current `main` branch.

- Total tracked Go lines: about 49k.
- Largest files:
  - `internal/tui/tui.go`: 4,945 LOC.
  - `internal/tools/tools.go`: 3,310 LOC.
  - `internal/tui/tui_test.go`: 2,431 LOC.
  - `internal/agent/agent_test.go`: 1,906 LOC.
  - `internal/agent/agent.go`: 1,644 LOC.
  - `internal/tools/tools_test.go`: 1,578 LOC.
  - `internal/gateway/gateway_test.go`: 1,505 LOC.
  - `internal/telegrambot/render.go`: 1,469 LOC.
  - `internal/gateway/gateway.go`: 1,390 LOC.
  - `internal/telegrambot/bot_test.go`: 1,373 LOC.
  - `internal/mcpclient/client.go`: 1,368 LOC.
  - `internal/bench/bench.go`: 1,288 LOC.
  - `internal/telegrambot/bot.go`: 1,240 LOC.
- Important dependency smells:
  - `internal/tui` imports `agent`, `gateway`, `provider`, and `tools`
    directly.
  - `internal/telegrambot` imports gateway server DTOs and has its own gateway
    client.
  - `internal/tools` imports `internal/provider` for web summarization.
  - Gateway session replay and benchmark trace replay validate event streams
    differently.
  - `config.Config` is a broad runtime bag used by provider, credentials,
    tools, gateway, doctor, TUI, Telegram, and benchmarks.

## Decomposition Principles

- Keep the single-binary UX. Refactors must not break `./bin/fast-agent-harness`,
  TUI autodiscovery, Telegram, DeepSeek, Codex OAuth, or dangerous solo mode.
- Prefer strangler-style slices. Add narrow packages beside old code, migrate
  callers, then delete old paths.
- Add behavioral tests before moving large blocks.
- Event semantics come before UI shape. TUI and Telegram should render the same
  durable event stream, not each invent its own truth.
- Keep runtime packages ignorant of presentation. Renderers can know about
  compact display; protocol/eventlog/runtime should not.
- Keep tools independent from provider construction. Model summarization should
  be injected through a small interface.
- Do not create a plugin platform, database, remote MCP stack, or enterprise
  policy system as part of this refactor.

## Target Package Direction

This is the desired direction. It can be reached gradually.

```text
cmd/fast-agent-harness
  -> adapters: gateway, tui, telegrambot, mcpserver
  -> clients: gatewayclient, clientux/projector
  -> runtime: agent/runtime, session, toolexec
  -> contracts: protocol, eventlog, gatewayapi
  -> providers/tools: provider, webtools, mcpclient, tooloutput
  -> config/auth: config, profiles, credentials, codexauth, modelinfo
```

Desired import rules:

- `protocol` imports no billyharness package.
- `eventlog` imports `protocol`, but not gateway, trace, TUI, Telegram, or
  agent.
- `gatewayapi` contains shared HTTP DTOs only. Server and client can both use it.
- `gatewayclient` imports `gatewayapi` and `protocol`, not `internal/gateway`.
- TUI and Telegram import `gatewayclient`, `clientux/projector`, `protocol`, and
  render/tool summary helpers, not gateway server, agent, provider, or tools.
- `tools` does not import `provider`; web/model summaries are injected through a
  narrow summarizer interface.
- `gateway` adapts HTTP to session/runtime and eventlog. It should not contain
  core lifecycle validation logic.
- `trace` uses `eventlog` for replay validation instead of maintaining a
  separate protocol validator.

## P0: Contract And Safety Before More Features

These are the highest-priority slices. They reduce bug risk and make later
decomposition safer.

### P0.1 Shared Eventlog And Lifecycle Validator

- [x] Create `internal/eventlog` for event envelope validation, lifecycle
  validation, JSONL append/replay helpers, and corruption diagnostics.
  - [x] Add `internal/eventlog` record validation for schema version, sequence,
        stream scope, event type, and optional protocol envelope validation.
  - [x] Add `internal/eventlog` lifecycle validation for run, turn, step,
        tool call, and tool attempt ordering.
  - [x] Move JSONL append/replay helpers into `eventlog`.
  - [x] Add structured corruption diagnostics shared by trace, gateway replay,
        and session inspection.
- [x] Move common validation currently split between `internal/protocol`,
  `internal/trace`, and `internal/gateway/session_store.go` into `eventlog`.
  - [x] Trace replay uses `eventlog.RecordValidator` for schema, sequence,
        run scope, event type, and strict envelope checks.
  - [x] Gateway session JSONL replay uses `eventlog.RecordValidator` for schema,
        sequence, session scope, and event type checks.
  - [x] Move or wrap `protocol.ValidateEventEnvelope` at the `eventlog`
        boundary once agent and trace lifecycle validation share the same path.
- [x] Validate run/turn/step/tool-attempt ordering:
  - [x] no completed run without started run;
  - [x] no orphan step completion;
  - [x] no tool result without matching `call_id`;
  - [x] no attempt finish without matching `attempt_id`;
  - [x] parallel child steps may complete out of order but must reference the
        correct batch/run/turn.
  - [x] Wire lifecycle validation into agent tests, trace replay, gateway JSONL
        replay, and session inspection.
- [x] Use the same validator in agent tests, trace replay, gateway JSONL replay,
  and session inspection.
  - [x] Shared record validator is used in trace replay.
  - [x] Shared record validator is used in gateway JSONL replay and session
        inspection.
  - [x] Shared lifecycle validator is used in agent tests, trace replay, gateway
        JSONL replay, and session inspection.
- [x] Document event identity rules:
  - [x] agent run id;
  - [x] gateway session id;
  - [x] session run sequence;
  - [x] event `seq` scope;
  - [x] persisted event shape.

Event identity rules:

- Agent run id: `agent.RunMessages` creates a `run-*` id and `submission-*`
  id for each runtime invocation. Agent protocol events use that run id for
  lifecycle validation; gateway persistence must not replace an existing agent
  `event.run_id`.
- Gateway session id: `/v1/sessions` creates the durable session id. Session
  history, manifest, and event JSONL records are scoped by this id and stored
  under the matching gateway session directory.
- Session run sequence: gateway `SessionStatus.RunSeq` increments once for each
  session run. Session event records persist this as `run_seq`; gateway-originated
  status events may use `<session_id>:run-<run_seq>` when no agent run id exists.
- Event `seq` scope: agent stream `seq` values are scoped to one agent run;
  benchmark trace `seq` values are scoped to one trace run; gateway persisted
  `seq` values are scoped to one session `events.jsonl`. Gateway replay cursors
  use the gateway session event `seq`.
- Persisted event shape: gateway session event JSONL stores
  `{schema_version, seq, session_id, run_seq, ts, event_type, event}`. The nested
  `event.seq` is set to the same gateway session sequence so replayed events and
  streamed session events share one cursor shape.

Acceptance:

- [x] A scripted agent run validates full lifecycle.
- [x] A gateway session run persists events, replays after restart, and passes
  the same validator.
- [x] Corrupt JSONL tests fail deterministically for seq gaps, missing IDs,
  orphan completions, and invalid event types.
  - [x] `internal/eventlog` corruption tests cover seq gaps, missing call/attempt
        IDs, orphan completions, and invalid event type mismatches.
  - [x] Gateway and trace corrupt JSONL tests exercise lifecycle validation
        through their replay paths.
  - [x] Shared JSONL replay returns structured `eventlog.CorruptionError`
        diagnostics from eventlog, trace replay, gateway replay, and session
        inspection paths.
- [x] `go test -count=1 ./internal/protocol ./internal/trace ./internal/gateway ./internal/agent` passes.

### P0.2 Gateway Session Stream Contract

Problem: `/v1/sessions/{id}/run` currently records session events and streams
the original agent event. The recorded event may be enriched with session `seq`,
while TUI/Telegram cursors depend on `event.Seq`.

- [x] Change session run streaming so it emits the same sequenced event shape
  that is stored for replay.
- [x] Make `session.observeRunEvent` return the recorded/enriched event, or add a
  single record-and-stream helper.
- [x] Add client-side cursor dedupe: ignore replay/live events with `Seq <= lastSeq`.
- [x] Add tests proving replay after the final streamed seq returns no duplicate
  already-rendered events.

Acceptance:

- [x] TUI and Telegram cursor tests cover run, replay, reconnect, and resume.
  - [x] TUI drops replay/live events at or before the current gateway cursor.
  - [x] Telegram drops stale replay events and duplicate live run events at or
        before the current gateway cursor.
  - [x] Add shared gatewayclient reconnect/follow cursor coverage after the
        gatewayclient split.
  - [x] Add end-to-end TUI/Telegram reconnect/resume flow coverage if those
        clients grow a background `FollowSessionEvents` path; current UI paths
        use replay-before-run plus direct run streaming.
    - [x] Blocked/not applicable as of 2026-06-29: `internal/gatewayclient`
          owns `FollowSessionEvents`, but `internal/tui` calls
          `gatewayclient.RunSessionResult` for live runs and `internal/telegrambot`
          exposes only `ReplaySessionEvents` plus `RunSession` through its
          harness interface. Add this end-to-end coverage when either UI client
          gains a background `FollowSessionEvents` path.
- [x] Session stream events are monotonic and nonzero for stored gateway sessions.
- [x] `go test -count=1 ./internal/gateway ./internal/tui ./internal/telegrambot` passes.

### P0.3 Central Tool Policy Boundary

Problem: dangerous checks live in the agent and in some handlers, but direct
registry/MCP-server callers can bypass handler-local assumptions.

- [x] Add a `ToolPolicy` or `ToolExecutor` boundary used by agent, MCP server,
  and direct registry callers.
- [x] Enforce risk before any handler runs.
- [x] Remove ad hoc dangerous checks from individual handlers where the central
  policy now owns them.
- [x] Ensure `web_cache_clear` and every `RiskWrite`/`RiskExecute` tool is denied
  when dangerous mode is disabled.

Acceptance:

- [x] `AutoApproveDangerous=false` denies write/execute tools through agent,
  direct `Registry.Call`, and MCP server paths.
- [x] Audit events still include permission source, risk, decision, and reason.
- [x] `go test -count=1 ./internal/tools ./internal/agent ./internal/mcpserver` passes.

### P0.4 Web Public-Host Policy Bound To Actual Dial

Problem: web fetch validates public DNS before the request, then `http.Client`
dials normally. That leaves a DNS rebinding gap.

- [x] Extract an `internal/webtools` HTTP client with injectable resolver and
  dialer.
- [x] Enforce public-IP validation on the actual dial target, including redirects.
- [x] Keep existing compact-output, cache, and output-ref behavior unchanged.
- [x] Add fake resolver/dialer tests for:
  - [x] public then private rebinding;
  - [x] redirect to private IP;
  - [x] localhost;
  - [x] RFC1918/private ranges;
  - [x] normal public host.

Acceptance:

- [x] Web tools still return compact summaries/output refs by default.
- [x] Private-network attempts fail before body fetch.
- [x] `go test -count=1 ./internal/tools` passes before and after extraction.

### P0.5 Enforceable Package Boundary Map

- [x] Add `docs/architecture.md` with every `internal/*` package, its
  responsibility, allowed imports, forbidden imports, and owner notes.
- [x] Add a lightweight import-graph guard command or test.
- [x] Encode at least these forbidden imports:
  - [x] TUI must not import `internal/agent`, `internal/provider`,
        `internal/tools`, or gateway server internals after the gatewayclient
        migration.
  - [x] Telegram must not import gateway server internals after DTO/client split.
  - [x] Tools must not import `internal/provider` after summarizer injection.
  - [x] Trace and gateway replay must use `internal/eventlog`.

Acceptance:

- [x] Import guard runs locally and in the documented verification command.
- [x] Exceptions are listed with a removal issue/TODO and target phase.

### P0.6 Remove Tools To Provider Coupling

- [x] Define a narrow web summarizer interface in tools/webtools.
- [x] Inject the summarizer from runtime wiring instead of storing `provider.New`
  inside `internal/tools`.
- [x] Keep extractive summarization as the zero-provider default.
- [x] Preserve `tool_summary_*` and `websum_*` metadata.

Acceptance:

- [x] `internal/tools` no longer imports `internal/provider`.
- [x] Existing model-summary tests pass using a fake summarizer.
- [x] Web extractive mode still makes zero provider calls.

## P1: Structural Decomposition Slices

### P1.1 Runtime Loop Split

- [x] Split `Agent.RunMessages` into:
  - [x] runtime loop;
    - [x] Move `Run`, `RunMessages`, transcript append helpers, failed-turn
          lifecycle handling, and top-level tool-call dispatch into
          `internal/agent/runtime_loop.go`; `agent.go` now holds agent
          construction and lower-level helper surfaces.
  - [x] model-call step;
    - [x] Extract provider stream collection, assistant/reasoning delta
          emission, usage updates, provider retry hook payloads, and tool-call
          accumulation into a dedicated model-call stream helper.
    - [x] Extract model-call step lifecycle into a dedicated helper that emits
          model step started/finished/completed events and returns content,
          reasoning, prompt token usage, tool calls, or step errors to the
          runtime loop.
    - [x] Move model-call step helpers into `internal/agent/model_call.go`;
          `agent.go` no longer owns provider stream collection or model-step
          lifecycle implementation details.
  - [x] tool-attempt orchestration;
    - [x] Move tool-attempt permission, attempt lifecycle, hook payload,
          execution progress, and permission decision event helpers into
          `internal/agent/tool_attempt.go`; `agent.go` now owns scheduling and
          runtime-loop wiring, not the attempt orchestration machinery.
  - [x] transcript mutation;
    - [x] Extract assistant response and tool-result transcript appends into
          dedicated helpers so the runtime loop no longer constructs those
          protocol messages inline.
    - [x] Move initial transcript construction, MCP instruction insertion,
          assistant/tool result appends, and reasoning-storage selection into
          `internal/agent/transcript.go`.
  - [x] event builder.
    - [x] Extract failed-turn/session-done/run-failed event emission into a
          helper so model-step failure handling does not build the lifecycle
          event sequence inline.
    - [x] Move run/turn lifecycle event constructors plus successful, failed,
          and max-tool-round terminal paths into `internal/agent/event_builder.go`.
- [x] Decide whether `runstate.Run/Turn/Step` is authoritative lifecycle state
  or only snapshot metadata.
  - [x] Decision: lifecycle authority stays in the protocol event stream plus
        `internal/eventlog` validation. `runstate.Snapshot` is the durable
        per-turn metadata snapshot; `runstate.Run` is currently an ID/status
        envelope helper for agent events and hooks; `runstate.Turn` and
        `runstate.Step` are not runtime transition authority. Do not add a
        second mutable lifecycle state machine in `runstate` without replacing
        the eventlog authority explicitly.
  - [x] Evidence: Go usages are limited to agent run/submission IDs, MCP hook
        payloads, model-call metadata, and `runstate.NewSnapshot` calls in
        agent, gateway, and benchmark setup; no runtime code consumes
        `runstate.Turn` or `runstate.Step` as authoritative state.
- [x] Replace high-value map payloads with typed protocol structs:
  - [x] model call data;
    - [x] Add `protocol.ModelCallEvent` for `model.call_started` and
          `model.call_finished`, including snapshot, usage, latency, and error
          fields while preserving the persisted JSON shape.
  - [x] permission decision;
    - [x] Add `protocol.ToolPermissionEvent`, use it for
          `tool.permission_requested` and `tool.permission_decided`, and teach
          event enrichment to copy `call_id` from the typed payload.
  - [x] output ref;
    - [x] Add `protocol.ToolOutputRefEvent`, use it for
          `tool.output_ref_created`, and teach event enrichment to copy
          `call_id` and `attempt_id` from the typed payload.
  - [x] hook summaries;
    - [x] Add `protocol.HookEvent`, use it for `hook.started`,
          `hook.finished`, and `hook.failed`, and teach event enrichment to
          copy hook turn/step/call/attempt IDs and duration from the typed
          payload.
  - [x] provider retry metadata.
    - [x] `protocol.ModelCallEvent` now carries typed provider request,
          attempts, retries, and status-code fields for model-call events;
          hook summary payloads remain tracked separately.

Acceptance:

- [x] `agent.go` drops below 1,200 LOC or has a documented split exception.
  - [x] `internal/agent/agent.go` is 867 LOC after extracting runtime-loop,
        transcript, model-call, and tool-attempt helpers into dedicated files.
- [x] Lifecycle tests cover model-only, tool, parallel-tool, denied-tool,
  aborted-tool, and compaction runs.
  - [x] Agent tests now run `eventlog.ValidateLifecycle` over model-only,
        compaction, normal tool, denied tool, parallel tool, out-of-order
        parallel completion, and canceled/aborted tool scenarios.

### P1.2 Gateway API DTOs And Shared Client

- [x] Create `internal/gatewayapi` for HTTP request/response DTOs currently owned
  by the server package.
  - [x] Gateway server keeps compatibility aliases while DTO ownership moves to
        `gatewayapi`.
  - [x] Telegram and TUI context/session DTO references use `gatewayapi` instead
        of gateway server-owned types where behavior helpers are not required.
- [x] Create `internal/gatewayclient` for:
  - [x] auth headers;
  - [x] URL/path escaping;
  - [x] typed status errors;
  - [x] `ErrSessionNotFound`;
  - [x] NDJSON event decoding;
  - [x] `RunSession` terminal-state reporting;
  - [x] replay/follow helpers.
  - [x] one-shot session replay helper with client-side cursor dedupe.
  - [x] raw streaming `Do` path for clients that need to scan session run
        events directly.
- [x] Migrate Telegram and TUI from duplicate gateway code to `gatewayclient`.
  - [x] Telegram session client wrapper delegates to `gatewayclient` and no
        longer imports gateway server internals.
  - [x] TUI HTTP gateway calls use `gatewayclient` URL/auth/retry behavior.
  - [x] TUI session run streaming uses `gatewayclient.RunSessionResult` instead
        of a local NDJSON scanner.
  - [x] Move TUI local context/report helpers off gateway server imports.

Acceptance:

- [x] Telegram no longer imports `internal/gateway`.
- [x] TUI no longer imports `internal/gateway`.
- [x] TUI gateway calls use the same client as Telegram.
- [x] Contract tests cover 404, auth, large NDJSON events, cursor replay, and
  run cancellation.
  - [x] `gatewayclient` tests cover auth header propagation, typed 404
        `ErrSessionNotFound`, and cursor replay dedupe.
  - [x] `gatewayclient` tests cover large NDJSON events, follow/reconnect cursor
        dedupe, terminal run-state reporting, and cancellation.

### P1.3 Shared Client UX Projector

- [x] Create `internal/clientux/projector`.
- [x] Project `protocol.Event` into a client-neutral run snapshot:
  - [x] assistant text;
  - [x] reasoning text;
  - [x] tool items keyed by `call_id`;
  - [x] run state;
  - [x] usage and context counters;
  - [x] web summary metrics;
  - [x] model/tool totals;
  - [x] errors;
  - [x] last sequence.
- [x] Move duplicated usage/tool/context accounting from TUI and Telegram into
  the projector.
  - [x] Telegram renderer sources model/tool totals, usage counters, web summary
        metrics, and terminal state from `clientux/projector`.
  - [x] TUI event accounting now syncs model/tool counts, provider usage
        counters, last context usage, terminal state, and tool-summary token
        totals from `clientux/projector`; the old TUI usage accumulator and
        local tool-summary parser were removed.

Acceptance:

- [x] Same event trace produces the same counts, context, tool summaries, and
  terminal state for TUI and Telegram tests.
  - [x] TUI has a projector parity test over one event trace for model/tool
        counts, context usage, tool-summary tokens, and completed terminal
        state; Telegram renderer tests cover the same projector-backed usage
        and context accounting path.
- [x] TUI and Telegram renderers become mostly presentation code over projector
  snapshots.
  - [x] TUI transcript event application delegates assistant streams, tool
        calls/results/audits, tool batches, and live-cell finalization to
        `internal/tui/transcript.Projector`.
    - [x] `Model.applyEvent` now routes those event types through
          `transcript.Projector` and keeps the TUI switch focused on status,
          grouping, collapse, and run-summary presentation.
    - [x] Removed the duplicate TUI-local assistant/tool/audit/tool-batch block
          mutation helpers; compact TUI tool/audit cell text now lives in the
          transcript projector.
    - [x] Added a TUI/projector parity regression over assistant streams,
          reasoning, audits, tool calls/results, tool batches, and run
          completion.
  - [x] TUI accounting/state presentation reads model/tool counts, usage,
        context, tool-summary tokens, and terminal state from
        `clientux/projector` snapshots.
  - [x] Telegram renderer reads model/tool counts, usage, context,
        tool-summary tokens, and terminal state from `clientux/projector`
        snapshots.
  - [x] Telegram final/status message rendering consumes projected assistant,
        reasoning, and tool-item snapshots instead of renderer-owned content
        and tool-summary maps.
    - [x] `clientux/projector` now owns assistant turn separation and stores the
          projected tool call on each tool item.
    - [x] Telegram final and live progress text read assistant content from the
          projector snapshot, with only a legacy `Content` fallback for direct
          test/manual construction paths.
    - [x] Telegram finished-tool summaries use the projected tool item as their
          base instead of a renderer-local `toolSummaries` map; reasoning
          visibility remains snapshot-derived via `ReasoningText` length.
  - [x] TUI context threshold/compaction cells move from renderer-local text
        projection to transcript projector cells without changing displayed
        diagnostics.
    - [x] `internal/tui/transcript` owns context compaction and threshold cell
          text via projector-applied `COMPACT`/`CONTEXT` cells.
    - [x] TUI keeps only status-line updates for context events; block creation
          now comes from `transcript.Projector`.
    - [x] Added transcript projector context diagnostic tests and extended TUI
          projector parity coverage to include context compaction/threshold
          events.

### P1.4 TUI Transcript Decomposition

- [x] Create `internal/tui/transcript`:
  - [x] `Cell`;
  - [x] typed `CellType`;
  - [x] `Projector.Apply(protocol.Event)`;
  - [x] tool/call indexes;
  - [x] run-summary cells;
  - [x] canonical persistence DTOs.
  - [x] Event identity enrichment for tool/call/attempt metadata now lives in
        `internal/tui/transcript` and is covered without rendering imports.
  - [x] `transcript.BuildIndex` owns tool call, step, and latest run-summary
        lookup rules; TUI no longer maintains a renderer-local call-id cache.
  - [x] `transcript.NewRunSummaryCell` owns run-summary title/body/copy
        construction; TUI supplies the run snapshot and upserts the returned
        cell.
  - [x] `transcript.Projector.Apply` projects assistant/reasoning streams, tool
        calls/results, tool-batch steps, context status cells, and live-cell
        finalization from protocol events with focused package tests.
- [x] Create `internal/tui/render`:
  - [x] `CellRenderer`;
  - [x] markdown renderer;
  - [x] activity/tool/status renderers;
  - [x] render cache keys.
  - [x] `internal/tui/render` owns transcript and rich terminal cache key
        construction, plus rich terminal cache hit/miss behavior through
        `render.CellRenderer`.
  - [x] `internal/tui/render` owns terminal-safe markdown rendering,
        live-stream markdown holdback, markdown table parsing, and fenced-code
        extraction; TUI now passes markdown styles and calls renderer entry
        points instead of owning the parser.
  - [x] `internal/tui/render` owns activity/tool/status title normalization,
        guide-line wrapping, and compact activity block rendering; TUI keeps
        transcript visibility/collapse decisions and adapts its private block
        fields into renderer inputs.
- [x] Create `internal/tui/selection` for mouse coordinates, visible-cell line
  ranges, selected rendered text, OSC52, and clipboard adapter.
  - [x] `internal/tui/selection` owns ANSI-aware mouse point clamping, selected
        rendered text, byte ranges, highlight ranges, clipboard writes, and
        OSC52 fallback; TUI keeps only Bubble Tea message adaptation.
- [x] Create `internal/tui/runtimeclient` so Bubble Tea state does not directly
  import agent/provider/tools for normal operation.
  - [x] Local initial messages, local agent runs, and local MCP status now go
        through `internal/tui/runtimeclient`; `internal/tui` no longer imports
        `internal/agent`, `internal/provider`, or `internal/tools`, and the
        architecture guard enforces those direct imports stay out.
- [x] Stop classifying context tools from rendered title strings; use structured
  tool name/args/metadata.
  - [x] TUI tool blocks now persist structured `tool_name` metadata from
        protocol tool events, and context-tool grouping classifies by that tool
        name instead of parsing rendered titles. A regression test scrambles
        display titles and still expects web context grouping to work.

Acceptance:

- [x] `internal/tui/transcript` has no Bubble Tea, lipgloss, gateway, provider,
  tools, or agent imports.
  - [x] Architecture guard enforces `internal/tui/transcript` imports only
        `internal/protocol`.
- [x] Hidden thinking/tool cells cannot be selected/copied through visible-cell
  navigation.
  - [x] `TestTranscriptSelectionCannotCopyHiddenThinkingOrTools` selects across
        the rendered viewport with thinking/tool views hidden and verifies
        hidden reasoning and tool output are absent from copied and highlighted
        text.
- [x] `/toolview current`, grouped context tools, and out-of-order tool updates
  still pass tests.
- [x] Ordinary printable input does not re-render the full transcript.
  - [x] `TestPrintableInputDoesNotReflowTranscript` verifies a normal typed key
        updates the textarea while preserving existing transcript viewport
        content.

### P1.5 TUI Action Registry

- [x] Move slash commands, keybindings, command palette metadata, aliases, and
  argument providers into one action registry.
  - [x] Slash commands, slash aliases, command palette metadata, summaries, and
        argument providers are defined by `internal/tui`'s action registry.
  - [x] Keybindings moved from the main `Update` switch into the same action
        registry; registry key actions now cover send/newline, command palette,
        model/reasoning cycling, thinking visibility, gateway reconnect,
        viewport navigation, selected-block navigation, collapse/expand, and
        quit.
- [x] `Update` should dispatch actions rather than contain all command logic.
  - [x] `Update` now delegates key commands through registry key dispatch after
        slash navigation; slash commands already dispatch through registry
        actions. Non-action Bubble Tea message handling remains in `Update`.
- [x] Help text, slash popup, and Telegram command metadata should derive from
  shared action definitions where practical.
  - [x] TUI help text, keybinding help, and slash popup derive command metadata
        from the action registry.
  - [x] Telegram command metadata consumes shared `internal/clientux` action
        definitions for aliases, usage strings, and summaries; tests verify
        every action with Telegram aliases is present in the Telegram command
        table.

Acceptance:

- [x] Adding an action does not require editing the main TUI `Update` switch.
  - [x] New slash or key actions are registered in `actionRegistry`; `Update`
        only calls registry dispatch.
- [x] Slash popup, keybinding help, and command validation use one source of
  metadata.
  - [x] Slash popup and slash command validation use registry metadata.
  - [x] Keybinding help is generated from the registry's keybinding metadata.

### P1.6 Telegram Package Split

- [x] Split `telegrambot` into:
  - [x] poller/update loop;
  - [x] authz/allowlist;
  - [x] state and sessions;
  - [x] command dispatch;
  - [x] runner/gateway bridge;
  - [x] progress throttler;
  - [x] delivery/send/edit/delete;
  - [x] render/chunking.
    - [x] Move shared Telegram UTF-16 length, trimming, escaping, and raw chunk
          helpers into `internal/telegrambot/chunking.go`.
    - [x] Move remaining markdown/rich chunk splitting out of
          `internal/telegrambot/render.go`.
- [x] Replace hand-written command switch with shared command metadata plus
  Telegram-specific handlers.
  - [x] Telegram command metadata now drives dispatch, help text, and active-run
        bypass checks.
- [x] Add fake-clock progress tests for burst deltas, final flush, retry-after,
  and UTF-16 Telegram limits.

Acceptance:

- [x] `bot.go` drops below 900 LOC or has a documented split exception.
- [x] Per-user isolation, scoped `/cancel`, `/resume`, `/fork`, and rich fallback
  tests still pass.

### P1.7 Session Ownership Metadata

- [x] Add owner metadata to gateway sessions:
  - [x] client type;
  - [x] Telegram chat id;
  - [x] Telegram thread id;
  - [x] Telegram user id;
  - [x] TUI chat id;
  - [x] profile/model at creation.
- [x] Filter Telegram `/resume` and `/fork` by owner unless explicitly
  admin/global.
  - [x] Owner-scoped filters hide other Telegram users' sessions for list,
        resume, and fork.
  - [x] Ownerless legacy/solo sessions remain visible.
  - [x] Explicit admin/global override semantics are defined in
        `docs/telegram.md`: there is currently no Telegram admin/global
        session-owner override, and `AllowAllChats` admits chats without
        bypassing per-user session ownership.
- [x] Keep solo mode ergonomic with clear override/admin behavior.
  - [x] Ownerless solo session lists remain accessible.
  - [x] Admin/global override behavior is intentionally absent until a separate
        Telegram admin/global mode is introduced; ownerless legacy/solo
        sessions remain the compatibility path.

Acceptance:

- [x] Two allowed Telegram users cannot accidentally resume/fork each other's
  sessions unless global/admin mode is used.
- [x] Existing solo session list remains accessible to the owner.

### P1.8 Tool Discovery And Dynamic MCP Catalog

- [x] Move native/MCP discovery filtering into `internal/tools/discovery`.
- [x] Make `tool_search` and `mcp_list_tools` use the same query engine.
- [x] Make MCP catalog manager-owned and refreshed on successful start/reconnect.
- [x] Add collision handling and change events.

Acceptance:

- [x] Optional MCP server failing at startup and later reconnecting updates
  `mcp_list_tools`, `tool_search`, and `mcp_call` validation.
- [x] Query, namespace, risk, alias, limit, and schema-budget tests are shared.

### P1.9 Output Ref Service

- [x] Create `internal/tooloutput` for:
  - [x] safe artifact names;
  - [x] private directories;
  - [x] `0600` writes;
  - [x] byte/hash metadata;
  - [x] existence checks;
  - [x] future retention hooks.
- [x] Migrate web output refs and generic oversized tool refs to the same
  service.

Acceptance:

- [x] Web and generic tool refs have identical metadata semantics.
- [x] Cache misses if an output ref file is deleted.
- [x] No duplicate path/hash/chmod logic remains in agent and tools.

### P1.10 Webtools Split

- [x] Move web code out of `internal/tools/tools.go` into thin handlers plus:
  - [x] fetch;
  - [x] extract;
  - [x] crawl;
  - [x] compact;
  - [x] summary;
  - [x] cache;
  - [x] metadata.
- [x] Preserve the public tool JSON contract.
- [x] Add golden JSON tests for `web_fetch`, `web_extract`, and `web_crawl`.

Acceptance:

- [x] `internal/tools/tools.go` drops below 1,500 LOC or only contains registry
  and thin native handlers.
- [x] Existing web summary/cache/output-ref tests pass after moves.

### P1.11 Config/Auth/Provider Projections

- [x] Add narrow projections on `config.Config`:
  - [x] `AuthSettings`;
  - [x] `ProviderSelection`;
  - [x] `ModelSelection`;
  - [x] `ProfileSelection`;
  - [x] `RuntimeLimits`;
  - [x] `ToolPolicySettings`;
  - [x] `MCPSettings`;
  - [x] `HookSettings`;
  - [x] `InstructionSettings`.
- [x] Migrate provider, credentials, doctor, gateway, tools, and command code to
  projections before splitting the underlying struct.
  - [x] Provider factory accepts `config.ProviderBinding`; legacy
        `provider.New(config.Config)` delegate removed after all callers moved
        to the binding path.
  - [x] Credentials manager stores `config.AuthSettings`; provider Codex auth
        resolution/refresh helpers consume auth settings instead of full config.
  - [x] Doctor config status embeds `config.ProviderAuthSnapshot`.
  - [x] Gateway `/v1/config` and CLI `config inspect -json` include
        `config.DiagnosticSnapshot` for provider/auth fields.
  - [x] Doctor config status embeds `config.RuntimeToolSnapshot`.
  - [x] Tools registry stores `config.ToolPolicySettings` and
        `config.MCPSettings`; policy, workspace, web-cache, and MCP startup
        decisions read those projections instead of full config fields.
  - [x] Gateway auth manager, health response, MCP status, and session
        config/MCP snapshots read auth, provider/model, runtime, tool, and MCP
        projections for static settings.
  - [x] CLI, gateway override runs, TUI local mode, and benchmark provider
        construction call `provider.NewFromBinding(config.ProviderBinding)`.
  - [x] Web summarizer construction accepts `config.ProviderBinding` plus
        `config.ToolPolicySettings`; CLI, TUI, and benchmark registry setup
        inject the summarizer from projections, and tools web summary/cache
        request metadata read projected web-summary settings. The full-config
        web summarizer wrapper has been removed.
  - [x] Credentials status/import/save expose `config.AuthSettings` entry
        points; TUI local auth and credentials tests use them, and the
        full-config credentials wrappers have been removed.
  - [x] Agent model-compaction summary provider construction calls
        `provider.NewFromBinding(config.ProviderBinding)` instead of a
        full-config provider factory.
  - [x] Tools registry no longer stores or exposes full `config.Config`;
        gateway and TUI MCP status paths read `config.MCPSettings` through a
        projection accessor on the registry.
  - [x] Agent caches `config.ProviderBinding`, `config.RuntimeLimits`, and
        `config.ToolPolicySettings`; model metadata, model requests, max tool
        rounds, parallel-tool limits, dangerous audit flags, tool-output
        truncation, and reasoning-content storage read those projections.
  - [x] Gateway server caches base `config.ProviderAuthSnapshot`,
        `config.ProviderBinding`, and `config.ProfileSelection`; health and
        default session owner fields read projections while request override
        paths continue to resolve per-run configs.
  - [x] Shared client context response builder accepts `config.RuntimeLimits`
        instead of full `config.Config`; gateway and TUI context status pass
        runtime projections.
  - [x] TUI model state caches `config.ProviderBinding`,
        `config.ProfileSelection`, `config.RuntimeLimits`, and
        `config.ToolPolicySettings`; read-only status/context/provider/profile
        paths use projections while `currentConfig` remains the full-config
        operation builder.
  - [x] Agent context threshold and deterministic/model compaction helpers read
        `config.RuntimeLimits`, `config.ProviderBinding`, and
        `config.ToolPolicySettings` instead of full `config.Config`.
  - [x] `runstate.NewSnapshot` accepts a projection-only
        `runstate.SnapshotInput`; agent, gateway session snapshots, benchmark
        profile hashes, and runstate tests build snapshot metadata from
        projections.
  - [x] Hook runner construction accepts `config.HookSettings`; agent caches
        hook settings and no longer passes full `config.Config` into hooks.
  - [x] Instruction loading accepts `config.InstructionSettings`; agent caches
        instruction settings and no longer stores full `config.Config`.
  - [x] Gateway session config and MCP snapshot map builders accept projection
        values.
  - [x] Gateway default session messages use cached `config.InstructionSettings`
        and profile override projection instead of cloning full server config.
  - [x] Gateway `sessionSnapshotConfig` was replaced by projection-based
        session snapshot metadata; only run override builders and `/v1/config`
        runtime diffing still assemble full configs.
  - [x] Gateway run override mutation is centralized in `configForRunRequest`;
        `/v1/run` and session runs build the override config once and derive
        agent construction plus run status metadata from it.
  - [x] TUI local auth actions and initial-message resets use cached
        `config.AuthSettings` and `config.InstructionSettings` projections.
  - [x] TUI gateway run request construction is isolated in a projection-backed
        helper; it uses cached provider/profile projections and UI selection
        state instead of `currentConfig`.
  - [x] MCP client and tools registry expose projection constructors; TUI local
        runs and local MCP status use cached provider/tool/MCP projections
        instead of assembling `currentConfig`.
  - [x] Agent construction has a projection settings entry point; TUI local
        runs pass cached provider/profile/runtime/tool/MCP/hook/instruction
        projections into the agent.
  - [x] Gateway run handlers convert `configForRunRequest` output into
        projection run settings; provider construction, agent construction, and
        run status metadata no longer consume full config directly.
  - [x] Config runtime diff reporting has a projection bridge; gateway
        `/v1/config` and TUI local `/config` diff reporting pass
        `config.RuntimeDiffSettings` instead of comparing caller-owned full
        configs directly.
  - [x] Split remaining TUI full-config operation builder: `currentConfig` was
        removed, local runs/MCP status/config diffing use projection settings,
        and profile metadata loading remains an explicit profile-selection
        operation.
  - [x] Split remaining `gateway.Server.cfg` override/snapshot paths:
        gateway no longer stores full `config.Config`; run overrides resolve
        through `config.RuntimeDiffSettingsWithRunOverrides`, and
        `/v1/config` runtime diff reporting uses `config.RuntimeDiffSettings`.
- [x] Extract shared Codex auth parser/status/refresh metadata into a
  `codexauth` package.
- [x] Make DeepSeek credential save respect configured `APIKeyEnv`, or reject
  non-default save targets with a clear diagnostic.
- [x] Make config/profile reading side-effect-free; default file creation should
  be explicit initialization.
  - [x] `Resolve`/`Default` and `LoadProfileMetadata` no longer create default
        Billy profile files; built-in Billy metadata is applied from memory.
  - [x] `LoadDefaultMCPServers` reads existing default MCP config files only;
        `EnsureDefaultMCPConfigFile` remains the explicit initialization path.
- [x] Centralize diagnostic snapshots for doctor, gateway `/v1/config`, and CLI
  `config inspect`.
  - [x] Provider/auth diagnostics use `config.DiagnosticSnapshot` across doctor,
        gateway `/v1/config`, and CLI `config inspect`.
  - [x] Runtime/tool/web/MCP diagnostics use `config.RuntimeToolSnapshot` across
        doctor, gateway `/v1/config`, and CLI `config inspect`.

Acceptance:

- [x] Provider factory can be called from a resolved provider binding rather than
  full `config.Config`.
- [x] Codex auth parsing is not duplicated between credentials and provider.
- [x] Config inspect and doctor cannot drift on provider/auth fields.
- [x] Tests cover auth precedence, custom `APIKeyEnv`, model routing, Spark
  disablement, redaction, and config read purity.
  - [x] Projection/provider/credentials tests cover custom `APIKeyEnv`, model
        routing, and Spark disablement for the new binding path.
  - [x] Provider auth tests cover Codex env-before-file precedence and token
        redaction through the `AuthSettings` path.
  - [x] Config tests cover `Resolve` preserving built-in Billy profile defaults
        without creating `profile.toml` or `SOUL.md`.
  - [x] Config tests cover default MCP server loading without creating
        `mcp.config.toml`.

### P1.12 Shared Testkit And Fixture Hygiene

- [x] Consolidate duplicated test helpers:
  - [x] scripted provider;
    - [x] No cross-package duplicate remains for the exact helper concept:
          `rg '^type scriptedProvider' --glob '*_test.go'` reports only
          `internal/agent/agent_test.go`, so it stays package-local.
  - [x] test JWT;
    - [x] Add `internal/testkit.JWT` and replace duplicated JWT helpers in
          credentials, codexauth, and provider tests.
  - [x] round trip function;
    - [x] Add `internal/testkit.RoundTripFunc` and replace duplicate local HTTP
          transport adapters in gateway and Telegram tests.
  - [x] gateway fake client/server helpers;
    - [x] Add `internal/testkit.NewRouteServer`, `DecodeJSON`,
          `WriteJSON`, and `WriteJSONLines` for gateway-style HTTP JSON and
          NDJSON tests; gatewayclient, Telegram gateway-client wrapper, and TUI
          gateway tests now share the helper instead of repeating routed
          `httptest` servers.
  - [x] Telegram fake API helpers.
    - [x] Add a package-local Telegram API test server/client helper that
          decodes request payloads, routes by Telegram API method, and writes
          standard success/error envelopes; Telegram client tests and command
          tests now use it instead of repeating `httptest` path and JSON
          boilerplate.
- [x] Prefer package-local helpers unless multiple packages truly need them; use
  `internal/testkit` only for cross-package helper concepts.
  - [x] `internal/testkit` now contains only cross-package helper concepts
        (`JWT`, `RoundTripFunc`, and the generic HTTP route/JSON helpers);
        Telegram API helpers stay package-local because they depend on Telegram
        DTOs and method semantics.
- [x] Add benchmark fixture ownership rules to `benchmarks/README.md`.
- [x] Add generated-output and duplicate-fixture policy.
  - [x] `benchmarks/README.md` now names benchmark fixture owners, keeps
        generated run bundles out of `benchmarks/`, and allowlists the small
        duplicate `package.json` files that keep local smoke workspaces
        self-contained.

Acceptance:

- [x] `rg '^type scriptedProvider|^func testJWT|^type roundTripFunc'` shows one
  canonical helper per concept or a documented exception.
  - [x] The command now reports only the package-local
        `internal/agent/agent_test.go` scripted provider; JWT and round-trip
        helpers use `internal/testkit`.
- [x] Benchmark fixture duplicates are either removed, templated, or allowlisted.

## P2: Cleanup And Long-Term Hygiene

### P2.1 MCP Client File Split

- [x] Split `internal/mcpclient/client.go` into:
  - [x] manager;
  - [x] catalog;
  - [x] server lifecycle;
  - [x] stdio transport;
  - [x] JSON-RPC;
  - [x] content rendering;
  - [x] env/secrets;
  - [x] reconnect/status fanout.
  - [x] `internal/mcpclient/manager.go` now owns manager construction,
        config projection cloning, listener registration/fanout, status/tool
        snapshots, refresh, close, and catalog rebuild orchestration.
  - [x] `internal/mcpclient/catalog.go` now owns catalog construction,
        external MCP tool naming, enabled/disabled tool filtering, and catalog
        collision handling.
  - [x] `internal/mcpclient/server.go` now owns managed server lifecycle,
        static errors, reconnect startup, close, catalog snapshots, and status
        publication hooks.
  - [x] `internal/mcpclient/stdio.go` now owns stdio process startup, workspace
        cwd validation, process close/watch lifecycle, stderr tails, shell
        blocking, and bounded stderr buffers.
  - [x] `internal/mcpclient/jsonrpc.go` now owns JSON-RPC request/notify/write
        and response read limits, tool calls, oversized response errors, and
        transport stderr wrapping.
  - [x] `internal/mcpclient/content.go` now owns MCP tool content rendering,
        output caps, truncation notes, and UTF-8-safe trimming.
  - [x] `internal/mcpclient/env.go` now owns inherited env selection,
        configured env lookup, deterministic env ordering, and secret discovery
        for redaction.
  - [x] `internal/mcpclient/status.go` now owns status cloning, catalog-change
        cloning, reconnect backoff, status-change comparison, timestamp helpers,
        and redacted server errors.
  - [x] `internal/mcpclient/client.go` is now the shared MCP contract/type file
        and all MCP client implementation files are under the source file-size
        target.

Acceptance:

- [x] Existing lifecycle tests pass.
  - [x] `go test ./internal/mcpclient ./internal/tools ./internal/mcpserver`
        passes after the catalog/content/env split.
- [x] Narrow tests cover output caps, env redaction, status fanout, and process
  cleanup.
  - [x] Existing MCP tests cover capped tool output, oversized raw response
        failure, dotenv env lookup, redaction of server/env secrets, status
        listeners, reconnect/backoff status, and process termination on close.

### P2.2 Tool Rendering Registry

- [x] Replace hard-coded tool-name switches in `toolrender` with a small renderer
  registry/table keyed by namespace/tool family.
- [x] Keep it simple. Do not build a full display DSL.
  - [x] `internal/toolrender` now routes known call-line rendering through a
        compact `callRenderers` table with TUI/Telegram render callbacks and
        keeps the existing generic fallback for unknown tools.

Acceptance:

- [x] Snapshot tests cover TUI and Telegram call/result lines.
  - [x] `TestCallLineSnapshotsCommonTools` pins exact TUI and Telegram call
        lines for shell, filesystem, web, and MCP calls; existing result-line
        tests continue to cover compact result metadata.
- [x] Adding a native tool requires adding one renderer entry, not editing a
  large switch.
  - [x] A search for old `switch call.Name`, `tuiCallLine`, and
        `telegramCallLine` paths in `internal/toolrender/toolrender.go` finds
        no tool-name switches or style-specific switch functions; new known
        tools are added to `callRenderers`.

### P2.3 File Size Budget

- [x] Adopt file-size targets:
  - [x] handwritten `.go` files under 1,500 LOC;
  - [x] `_test.go` files under 1,200 LOC;
  - [x] exceptions documented in `docs/architecture.md` with owner and split
        plan.
  - [x] `docs/architecture.md` now records the file-size targets and current
        exceptions for large source/test files with decomposition owners and
        split plans.
- [x] Add a hygiene command using `git ls-files`, not raw filesystem traversal.
  - [x] `fast-agent-harness hygiene` collects tracked Go source files through
        `git ls-files -- '*.go'`, supports `-json` and `-strict`, and has
        focused command tests using a fake Git runner.
- [x] Report ignored runtime artifact size separately.
  - [x] The hygiene report has a separate runtime artifact section for ignored
        paths such as `gateway-sessions`, `bench-runs`, `tool-output`, `auth`,
        logs, config, cache, and binaries.

Acceptance:

- [x] Hygiene command flags large files and ignored artifact growth without
  counting `gateway-sessions`, `bench-runs`, `tool-output`, auth files, or
  binaries as source.
  - [x] `go run ./cmd/fast-agent-harness hygiene -repo /root/billyharness`
        reports large tracked source files and separate runtime artifact sizes;
        in the current dirty tree it also warns about tracked files deleted by
        the in-progress TUI file move.

### P2.4 Dependency Metadata Hygiene

- [x] Pin CI/local verification to the Go version in `go.mod`.
  - [x] `scripts/verify-deps.sh` reads the `go` directive from `go.mod` and
        fails when the active `GO_BIN` reports a different `go env GOVERSION`;
        README/setup docs call it with `/root/.local/go/bin/go`.
- [x] Ensure `go mod tidy` produces no diff.
  - [x] `scripts/verify-deps.sh` runs `go mod tidy`, reports any `go.mod` or
        `go.sum` diff, and restores the original files on failure.
- [x] Keep direct source imports in a direct `require` block, not hidden as
  indirect dependencies.
  - [x] `go.mod` now has a direct `require` block for source-imported external
        modules (`bubbles`, `bubbletea`, `lipgloss`, `toml`, `clipboard`, and
        `x/ansi`) and a separate indirect block for transitive modules; the
        dependency verifier compares source/test imports with direct
        requirements.

Acceptance:

- [x] Dependency hygiene check is part of documented verification.
  - [x] README work protocol and `docs/setup.md` include
        `GO_BIN=/root/.local/go/bin/go ./scripts/verify-deps.sh`.

### P2.5 Optional Storage Indexes Stay Optional

- [x] Keep JSONL canonical.
  - [x] Gateway session list/inspect paths replay the canonical manifest,
        history JSONL, and events JSONL; corrupt index files do not affect
        `ListStoredSessions`.
- [x] Add rebuildable indexes only when concrete UX or perf needs exist:
  - [x] session search deferred until a concrete query UX exists; the current
        optional index is only a rebuildable session-list cache.
  - [x] tool calls deferred until a concrete inspector needs it.
  - [x] cost/usage deferred until a concrete analytics view needs it.
  - [x] errors deferred until a concrete diagnostics view needs it.
  - [x] `sessions index rebuild|show|delete` exposes the optional session-list
        index without making it part of the live runtime path.

Acceptance:

- [x] Deleting indexes and rebuilding restores the same visible state.
  - [x] Gateway storage tests delete the session index, verify it is gone, then
        rebuild/read it back from canonical session files; CLI tests cover
        `sessions index rebuild`, `show -json`, and `delete`.
- [x] No runtime path treats an index as canonical session data.
  - [x] Gateway storage tests write a corrupt index and still expect
        `ListStoredSessions` to return the same canonical session list.

## Recommended Execution Order

Use this order unless a production bug forces a narrower emergency fix:

1. P0.2 gateway session stream contract and client cursor dedupe.
2. P0.1 shared lifecycle validator and eventlog validation.
3. P0.3 central tool policy boundary.
4. P0.4 web dial-bound public-host policy.
5. P0.5 architecture map and import guard.
6. P0.6 remove tools-to-provider coupling.
7. P1.2 gateway API DTOs and shared gateway client.
8. P1.3 shared client UX projector.
9. P1.4 TUI transcript decomposition.
10. P1.6 Telegram package split.
11. P1.8 dynamic MCP catalog and shared discovery.
12. P1.9 output ref service.
13. P1.10 webtools split.
14. P1.11 config/auth/provider projections.
15. P1.1 runtime loop split.
16. P1.12 testkit and fixture hygiene.
17. P2 cleanup items.

The reason P0.2 comes before P0.1 in execution is pragmatic: if clients are
currently applying unsequenced streamed events, that can cause visible duplicate
rendering on resume. Fixing that narrow contract first gives the lifecycle
validator a cleaner target.

## Per-Slice Verification Checklist

Before each slice:

- [ ] Identify exactly which item from this document the slice advances.
- [ ] Capture current import/line-count state if the slice is decomposition work.
- [ ] Add a regression test that would fail on the current bad boundary or bug.
- [ ] Keep public CLI/TUI/Telegram behavior compatible unless the old behavior is
  actively harmful.

After each slice:

- [ ] Run focused tests for touched packages.
- [ ] Run `go test -count=1 ./...` for eventlog, tool policy, gateway client,
  runtime, provider, or shared projector changes.
- [ ] Rebuild `bin/fast-agent-harness` if runtime/CLI code changed.
- [ ] Restart gateway/Telegram services if deployed runtime behavior changed.
- [ ] Update this document when a checkbox is completed or a better split is
  discovered.
- [ ] Commit and push coherent verified work.

## Explicit Non-Goals

- [ ] Do not rewrite the whole harness in one architecture pass.
- [ ] Do not add a plugin marketplace as part of decomposition.
- [ ] Do not replace JSONL with a canonical database.
- [ ] Do not build remote MCP OAuth while local URL MCP is intentionally deferred.
- [ ] Do not replace native web extraction with a browser crawler unless a real
  benchmark proves the need.
- [ ] Do not optimize for multi-user SaaS at the cost of solo local speed.
