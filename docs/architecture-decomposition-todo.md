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

- [ ] Create `internal/eventlog` for event envelope validation, lifecycle
  validation, JSONL append/replay helpers, and corruption diagnostics.
- [ ] Move common validation currently split between `internal/protocol`,
  `internal/trace`, and `internal/gateway/session_store.go` into `eventlog`.
- [ ] Validate run/turn/step/tool-attempt ordering:
  - [ ] no completed run without started run;
  - [ ] no orphan step completion;
  - [ ] no tool result without matching `call_id`;
  - [ ] no attempt finish without matching `attempt_id`;
  - [ ] parallel child steps may complete out of order but must reference the
        correct batch/run/turn.
- [ ] Use the same validator in agent tests, trace replay, gateway JSONL replay,
  and session inspection.
- [ ] Document event identity rules:
  - [ ] agent run id;
  - [ ] gateway session id;
  - [ ] session run sequence;
  - [ ] event `seq` scope;
  - [ ] persisted event shape.

Acceptance:

- [ ] A scripted agent run validates full lifecycle.
- [ ] A gateway session run persists events, replays after restart, and passes
  the same validator.
- [ ] Corrupt JSONL tests fail deterministically for seq gaps, missing IDs,
  orphan completions, and invalid event types.
- [ ] `go test -count=1 ./internal/protocol ./internal/trace ./internal/gateway ./internal/agent` passes.

### P0.2 Gateway Session Stream Contract

Problem: `/v1/sessions/{id}/run` currently records session events and streams
the original agent event. The recorded event may be enriched with session `seq`,
while TUI/Telegram cursors depend on `event.Seq`.

- [ ] Change session run streaming so it emits the same sequenced event shape
  that is stored for replay.
- [ ] Make `session.observeRunEvent` return the recorded/enriched event, or add a
  single record-and-stream helper.
- [ ] Add client-side cursor dedupe: ignore replay/live events with `Seq <= lastSeq`.
- [ ] Add tests proving replay after the final streamed seq returns no duplicate
  already-rendered events.

Acceptance:

- [ ] TUI and Telegram cursor tests cover run, replay, reconnect, and resume.
- [ ] Session stream events are monotonic and nonzero for stored gateway sessions.
- [ ] `go test -count=1 ./internal/gateway ./internal/tui ./internal/telegrambot` passes.

### P0.3 Central Tool Policy Boundary

Problem: dangerous checks live in the agent and in some handlers, but direct
registry/MCP-server callers can bypass handler-local assumptions.

- [ ] Add a `ToolPolicy` or `ToolExecutor` boundary used by agent, MCP server,
  and direct registry callers.
- [ ] Enforce risk before any handler runs.
- [ ] Remove ad hoc dangerous checks from individual handlers where the central
  policy now owns them.
- [ ] Ensure `web_cache_clear` and every `RiskWrite`/`RiskExecute` tool is denied
  when dangerous mode is disabled.

Acceptance:

- [ ] `AutoApproveDangerous=false` denies write/execute tools through agent,
  direct `Registry.Call`, and MCP server paths.
- [ ] Audit events still include permission source, risk, decision, and reason.
- [ ] `go test -count=1 ./internal/tools ./internal/agent ./internal/mcpserver` passes.

### P0.4 Web Public-Host Policy Bound To Actual Dial

Problem: web fetch validates public DNS before the request, then `http.Client`
dials normally. That leaves a DNS rebinding gap.

- [ ] Extract an `internal/webtools` HTTP client with injectable resolver and
  dialer.
- [ ] Enforce public-IP validation on the actual dial target, including redirects.
- [ ] Keep existing compact-output, cache, and output-ref behavior unchanged.
- [ ] Add fake resolver/dialer tests for:
  - [ ] public then private rebinding;
  - [ ] redirect to private IP;
  - [ ] localhost;
  - [ ] RFC1918/private ranges;
  - [ ] normal public host.

Acceptance:

- [ ] Web tools still return compact summaries/output refs by default.
- [ ] Private-network attempts fail before body fetch.
- [ ] `go test -count=1 ./internal/tools` passes before and after extraction.

### P0.5 Enforceable Package Boundary Map

- [ ] Add `docs/architecture.md` with every `internal/*` package, its
  responsibility, allowed imports, forbidden imports, and owner notes.
- [ ] Add a lightweight import-graph guard command or test.
- [ ] Encode at least these forbidden imports:
  - [ ] TUI must not import `internal/agent`, `internal/provider`,
        `internal/tools`, or gateway server internals after the gatewayclient
        migration.
  - [ ] Telegram must not import gateway server internals after DTO/client split.
  - [ ] Tools must not import `internal/provider` after summarizer injection.
  - [ ] Trace and gateway replay must use `internal/eventlog`.

Acceptance:

- [ ] Import guard runs locally and in the documented verification command.
- [ ] Exceptions are listed with a removal issue/TODO and target phase.

### P0.6 Remove Tools To Provider Coupling

- [ ] Define a narrow web summarizer interface in tools/webtools.
- [ ] Inject the summarizer from runtime wiring instead of storing `provider.New`
  inside `internal/tools`.
- [ ] Keep extractive summarization as the zero-provider default.
- [ ] Preserve `tool_summary_*` and `websum_*` metadata.

Acceptance:

- [ ] `internal/tools` no longer imports `internal/provider`.
- [ ] Existing model-summary tests pass using a fake summarizer.
- [ ] Web extractive mode still makes zero provider calls.

## P1: Structural Decomposition Slices

### P1.1 Runtime Loop Split

- [ ] Split `Agent.RunMessages` into:
  - [ ] runtime loop;
  - [ ] model-call step;
  - [ ] tool-attempt orchestration;
  - [ ] transcript mutation;
  - [ ] event builder.
- [ ] Decide whether `runstate.Run/Turn/Step` is authoritative lifecycle state
  or only snapshot metadata.
- [ ] Replace high-value map payloads with typed protocol structs:
  - [ ] model call data;
  - [ ] permission decision;
  - [ ] output ref;
  - [ ] hook summaries;
  - [ ] provider retry metadata.

Acceptance:

- [ ] `agent.go` drops below 1,200 LOC or has a documented split exception.
- [ ] Lifecycle tests cover model-only, tool, parallel-tool, denied-tool,
  aborted-tool, and compaction runs.

### P1.2 Gateway API DTOs And Shared Client

- [ ] Create `internal/gatewayapi` for HTTP request/response DTOs currently owned
  by the server package.
- [ ] Create `internal/gatewayclient` for:
  - [ ] auth headers;
  - [ ] URL/path escaping;
  - [ ] typed status errors;
  - [ ] `ErrSessionNotFound`;
  - [ ] NDJSON event decoding;
  - [ ] `RunSession` terminal-state reporting;
  - [ ] replay/follow helpers.
- [ ] Migrate Telegram and TUI from duplicate gateway code to `gatewayclient`.

Acceptance:

- [ ] Telegram no longer imports `internal/gateway`.
- [ ] TUI gateway calls use the same client as Telegram.
- [ ] Contract tests cover 404, auth, large NDJSON events, cursor replay, and
  run cancellation.

### P1.3 Shared Client UX Projector

- [ ] Create `internal/clientux/projector`.
- [ ] Project `protocol.Event` into a client-neutral run snapshot:
  - [ ] assistant text;
  - [ ] reasoning text;
  - [ ] tool items keyed by `call_id`;
  - [ ] run state;
  - [ ] usage and context counters;
  - [ ] web summary metrics;
  - [ ] model/tool totals;
  - [ ] errors;
  - [ ] last sequence.
- [ ] Move duplicated usage/tool/context accounting from TUI and Telegram into
  the projector.

Acceptance:

- [ ] Same event trace produces the same counts, context, tool summaries, and
  terminal state for TUI and Telegram tests.
- [ ] TUI and Telegram renderers become mostly presentation code over projector
  snapshots.

### P1.4 TUI Transcript Decomposition

- [ ] Create `internal/tui/transcript`:
  - [ ] `Cell`;
  - [ ] typed `CellType`;
  - [ ] `Projector.Apply(protocol.Event)`;
  - [ ] tool/call indexes;
  - [ ] run-summary cells;
  - [ ] canonical persistence DTOs.
- [ ] Create `internal/tui/render`:
  - [ ] `CellRenderer`;
  - [ ] markdown renderer;
  - [ ] activity/tool/status renderers;
  - [ ] render cache keys.
- [ ] Create `internal/tui/selection` for mouse coordinates, visible-cell line
  ranges, selected rendered text, OSC52, and clipboard adapter.
- [ ] Create `internal/tui/runtimeclient` so Bubble Tea state does not directly
  import agent/provider/tools for normal operation.
- [ ] Stop classifying context tools from rendered title strings; use structured
  tool name/args/metadata.

Acceptance:

- [ ] `internal/tui/transcript` has no Bubble Tea, lipgloss, gateway, provider,
  tools, or agent imports.
- [ ] Hidden thinking/tool cells cannot be selected/copied through visible-cell
  navigation.
- [ ] `/toolview current`, grouped context tools, and out-of-order tool updates
  still pass tests.
- [ ] Ordinary printable input does not re-render the full transcript.

### P1.5 TUI Action Registry

- [ ] Move slash commands, keybindings, command palette metadata, aliases, and
  argument providers into one action registry.
- [ ] `Update` should dispatch actions rather than contain all command logic.
- [ ] Help text, slash popup, and Telegram command metadata should derive from
  shared action definitions where practical.

Acceptance:

- [ ] Adding an action does not require editing the main TUI `Update` switch.
- [ ] Slash popup, keybinding help, and command validation use one source of
  metadata.

### P1.6 Telegram Package Split

- [ ] Split `telegrambot` into:
  - [ ] poller/update loop;
  - [ ] authz/allowlist;
  - [ ] state and sessions;
  - [ ] command dispatch;
  - [ ] runner/gateway bridge;
  - [ ] progress throttler;
  - [ ] delivery/send/edit/delete;
  - [ ] render/chunking.
- [ ] Replace hand-written command switch with shared command metadata plus
  Telegram-specific handlers.
- [ ] Add fake-clock progress tests for burst deltas, final flush, retry-after,
  and UTF-16 Telegram limits.

Acceptance:

- [ ] `bot.go` drops below 900 LOC or has a documented split exception.
- [ ] Per-user isolation, scoped `/cancel`, `/resume`, `/fork`, and rich fallback
  tests still pass.

### P1.7 Session Ownership Metadata

- [ ] Add owner metadata to gateway sessions:
  - [ ] client type;
  - [ ] Telegram chat id;
  - [ ] Telegram thread id;
  - [ ] Telegram user id;
  - [ ] TUI chat id;
  - [ ] profile/model at creation.
- [ ] Filter Telegram `/resume` and `/fork` by owner unless explicitly
  admin/global.
- [ ] Keep solo mode ergonomic with clear override/admin behavior.

Acceptance:

- [ ] Two allowed Telegram users cannot accidentally resume/fork each other's
  sessions unless global/admin mode is used.
- [ ] Existing solo session list remains accessible to the owner.

### P1.8 Tool Discovery And Dynamic MCP Catalog

- [ ] Move native/MCP discovery filtering into `internal/tools/discovery`.
- [ ] Make `tool_search` and `mcp_list_tools` use the same query engine.
- [ ] Make MCP catalog manager-owned and refreshed on successful start/reconnect.
- [ ] Add collision handling and change events.

Acceptance:

- [ ] Optional MCP server failing at startup and later reconnecting updates
  `mcp_list_tools`, `tool_search`, and `mcp_call` validation.
- [ ] Query, namespace, risk, alias, limit, and schema-budget tests are shared.

### P1.9 Output Ref Service

- [ ] Create `internal/tooloutput` for:
  - [ ] safe artifact names;
  - [ ] private directories;
  - [ ] `0600` writes;
  - [ ] byte/hash metadata;
  - [ ] existence checks;
  - [ ] future retention hooks.
- [ ] Migrate web output refs and generic oversized tool refs to the same
  service.

Acceptance:

- [ ] Web and generic tool refs have identical metadata semantics.
- [ ] Cache misses if an output ref file is deleted.
- [ ] No duplicate path/hash/chmod logic remains in agent and tools.

### P1.10 Webtools Split

- [ ] Move web code out of `internal/tools/tools.go` into thin handlers plus:
  - [ ] fetch;
  - [ ] extract;
  - [ ] crawl;
  - [ ] compact;
  - [ ] summary;
  - [ ] cache;
  - [ ] metadata.
- [ ] Preserve the public tool JSON contract.
- [ ] Add golden JSON tests for `web_fetch`, `web_extract`, and `web_crawl`.

Acceptance:

- [ ] `internal/tools/tools.go` drops below 1,500 LOC or only contains registry
  and thin native handlers.
- [ ] Existing web summary/cache/output-ref tests pass after moves.

### P1.11 Config/Auth/Provider Projections

- [ ] Add narrow projections on `config.Config`:
  - [ ] `AuthSettings`;
  - [ ] `ProviderSelection`;
  - [ ] `ModelSelection`;
  - [ ] `ProfileSelection`;
  - [ ] `RuntimeLimits`;
  - [ ] `ToolPolicySettings`;
  - [ ] `MCPSettings`.
- [ ] Migrate provider, credentials, doctor, gateway, tools, and command code to
  projections before splitting the underlying struct.
- [ ] Extract shared Codex auth parser/status/refresh metadata into a
  `codexauth` package.
- [ ] Make DeepSeek credential save respect configured `APIKeyEnv`, or reject
  non-default save targets with a clear diagnostic.
- [ ] Make config/profile reading side-effect-free; default file creation should
  be explicit initialization.
- [ ] Centralize diagnostic snapshots for doctor, gateway `/v1/config`, and CLI
  `config inspect`.

Acceptance:

- [ ] Provider factory can be called from a resolved provider binding rather than
  full `config.Config`.
- [ ] Codex auth parsing is not duplicated between credentials and provider.
- [ ] Config inspect and doctor cannot drift on provider/auth fields.
- [ ] Tests cover auth precedence, custom `APIKeyEnv`, model routing, Spark
  disablement, redaction, and config read purity.

### P1.12 Shared Testkit And Fixture Hygiene

- [ ] Consolidate duplicated test helpers:
  - [ ] scripted provider;
  - [ ] test JWT;
  - [ ] round trip function;
  - [ ] gateway fake client/server helpers;
  - [ ] Telegram fake API helpers.
- [ ] Prefer package-local helpers unless multiple packages truly need them; use
  `internal/testkit` only for cross-package helper concepts.
- [ ] Add benchmark fixture ownership rules to `benchmarks/README.md`.
- [ ] Add generated-output and duplicate-fixture policy.

Acceptance:

- [ ] `rg '^type scriptedProvider|^func testJWT|^type roundTripFunc'` shows one
  canonical helper per concept or a documented exception.
- [ ] Benchmark fixture duplicates are either removed, templated, or allowlisted.

## P2: Cleanup And Long-Term Hygiene

### P2.1 MCP Client File Split

- [ ] Split `internal/mcpclient/client.go` into:
  - [ ] manager;
  - [ ] catalog;
  - [ ] server lifecycle;
  - [ ] stdio transport;
  - [ ] JSON-RPC;
  - [ ] content rendering;
  - [ ] env/secrets;
  - [ ] reconnect/status fanout.

Acceptance:

- [ ] Existing lifecycle tests pass.
- [ ] Narrow tests cover output caps, env redaction, status fanout, and process
  cleanup.

### P2.2 Tool Rendering Registry

- [ ] Replace hard-coded tool-name switches in `toolrender` with a small renderer
  registry/table keyed by namespace/tool family.
- [ ] Keep it simple. Do not build a full display DSL.

Acceptance:

- [ ] Snapshot tests cover TUI and Telegram call/result lines.
- [ ] Adding a native tool requires adding one renderer entry, not editing a
  large switch.

### P2.3 File Size Budget

- [ ] Adopt file-size targets:
  - [ ] handwritten `.go` files under 1,500 LOC;
  - [ ] `_test.go` files under 1,200 LOC;
  - [ ] exceptions documented in `docs/architecture.md` with owner and split
        plan.
- [ ] Add a hygiene command using `git ls-files`, not raw filesystem traversal.
- [ ] Report ignored runtime artifact size separately.

Acceptance:

- [ ] Hygiene command flags large files and ignored artifact growth without
  counting `gateway-sessions`, `bench-runs`, `tool-output`, auth files, or
  binaries as source.

### P2.4 Dependency Metadata Hygiene

- [ ] Pin CI/local verification to the Go version in `go.mod`.
- [ ] Ensure `go mod tidy` produces no diff.
- [ ] Keep direct source imports in a direct `require` block, not hidden as
  indirect dependencies.

Acceptance:

- [ ] Dependency hygiene check is part of documented verification.

### P2.5 Optional Storage Indexes Stay Optional

- [ ] Keep JSONL canonical.
- [ ] Add rebuildable indexes only when concrete UX or perf needs exist:
  - [ ] session search;
  - [ ] tool calls;
  - [ ] cost/usage;
  - [ ] errors.

Acceptance:

- [ ] Deleting indexes and rebuilding restores the same visible state.
- [ ] No runtime path treats an index as canonical session data.

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
