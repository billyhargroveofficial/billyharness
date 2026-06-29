# Competitive Architecture Analysis

This document summarizes a clean-room comparison of Billyharness with Codex,
OpenCode, and Claude Code. It extracts architecture ideas, contracts,
invariants, UX patterns, and test strategies. It does not copy code.

Billyharness is treated as a solo-owner harness: one fast local binary, TUI and
Telegram first, JSONL/event replay, compact native tools, simple MCP, bounded
web output, and high-signal tests. Enterprise SaaS, marketplace, RBAC,
multi-tenant compliance, and broad plugin platforms are out of scope.

## Executive Summary

Billyharness already has the right skeleton: typed protocol events, JSONL
event logs, shared client projection, compact tool output, out-of-band output
refs, lazy MCP discovery, web summary outside the main loop, local profiles,
and a small Go codebase.

The highest-value improvements are not "copy a bigger agent platform." They
are a set of narrow invariants:

1. Durable event follow must replay from the store until caught up instead of
   depending on a lossy live hub.
2. Gateway interruption must be authoritative, not just a Telegram-side race.
3. Web and tool outputs must have hard model-visible inline budgets.
4. Every provider turn must use an immutable tool snapshot.
5. The transcript must be validated for assistant tool-call/tool-result
   pairing before every model call.
6. Tool display must be a shared compact contract consumed by TUI and Telegram.
7. MCP catalog ownership must be explicit: the manager owns raw server tools,
   the registry owns only a derived gateway cache.
8. Memory should be local, file-based, summary-only in the prompt, and manual
   first.
9. Tests should use one canonical event trace replayed through eventlog,
   gateway, projector, TUI, and Telegram.
10. Architecture hygiene should keep the codebase boring: split by ownership,
    no new platform layers without a failing test or benchmark.

## Anti-Bloat Filter

Accept an idea only if at least one of these is true:

- It closes a concrete reliability bug.
- It lowers latency or token/context waste on the hot path.
- It removes code or simplifies ownership.
- It creates a contract used by at least two current callers.
- It adds a test or benchmark that catches a known expensive failure mode.

Reject by default:

- Plugin marketplace, extension store, or dynamic provider catalog.
- Enterprise RBAC, multi-tenant policy engines, SaaS telemetry, compliance
  theater.
- Remote execution planes, cloud task queues, generated SDKs, app-server
  protocols larger than current clients require.
- Headless browser web extraction unless a measured case proves it is needed.
- SQLite/event-sourcing migration while JSONL replay benchmarks remain fine.
- Background memory agents, vector DB, or auto-extraction as default behavior.
- UI framework rewrites for TUI/Telegram.

## Current Billyharness Baseline

Strong foundations:

- `internal/protocol` has typed lifecycle, model, tool, hook, usage, and
  context events.
- `internal/eventlog` validates seq ordering and lifecycle relationships.
- `internal/gateway` stores session JSONL and supports replay after seq.
- `internal/clientux/projector` gives a shared projection layer for clients.
- `internal/agent` already separates runtime loop, model call, tool attempts,
  transcript, compaction, and event builders.
- `internal/tooloutput` and web output refs already keep large data
  out-of-band.
- `internal/mcpclient` already has manager-owned stdio MCP lifecycle and
  status listeners.
- `internal/tools/discovery` and docs already push lazy MCP discovery instead
  of dumping every remote schema into the model.
- TUI and Telegram both consume typed event semantics rather than each owning
  their own runtime.

Main weaknesses to address:

- Durable event streaming can still depend too much on lossy live fanout.
- Gateway run replacement is not yet a first-class interrupt policy.
- Web output and recent tool tails need stricter inline/token budgets.
- Tool display is derived independently by multiple layers.
- Tool snapshots can drift if MCP/tools change between model advertisement and
  execution.
- Tests are strong locally but lack a single canonical trace that proves
  end-to-end replay across eventlog, projector, TUI, and Telegram.

## Competitive Patterns Worth Taking

### Codex

Useful ideas:

- Turn-scoped snapshots for tools, config, context, and MCP status.
- Active-turn cancellation owned by the session/runtime, not the UI.
- Early tool start once a complete tool call is observed in provider streaming,
  with results drained before the next model call.
- Lossless versus best-effort event classes, with explicit lag instead of
  silent stream corruption.
- Raw/rich TUI transcript modes and robust terminal clipboard handling.
- Window/epoch metadata for compaction and cached-prefix reasoning.

Do not take:

- Cloud tasks, app-server JSON-RPC surface, reviewer/guardian stack, realtime
  audio, workspace platform features, enterprise policy layers.
- Full TUI architecture or remote protocols when current Go/Bubble Tea and
  NDJSON gateway are enough.

### OpenCode

Useful ideas:

- Aggregate-scoped durable seq and subscribe-before-read wake loops.
- Context epochs and compaction as durable state replacement, not loss of
  original JSONL transcript.
- Tool laws: one executor, durable invocation identity, output bounding,
  stale tool rejection.
- MCP `tools/list_changed` handled as full relist plus atomic catalog swap.
- Fake provider/HTTP replay tests and machine-readable benchmark metrics.

Do not take:

- Effect/layer runtime, full SQLite/event-sourcing stack, plugin lifecycle,
  SDK/server platform, organization/managed configuration.

### Claude Code

Useful ideas:

- File-based memory taxonomy: user, feedback, project, reference.
- Memory index plus topic files, with small summary injection and bounded read.
- Tool interface traits: display summary, concurrency safety, interrupt
  behavior, output refs, progress, and conservative collapse.
- Remote UI as transport adapter rather than owner of execution.
- Clipboard/selection polish and semantic copy of assistant/code blocks.
- Transcript pairing repair/validation before model calls.

Do not take:

- Team memory, analytics, managed policies, React/Ink UI stack, cloud relay,
  marketplace, multi-agent teammates, mobile/Slack/CCR surfaces.
- Auto memory extraction by default.

## Findings By Area

### 1. Agent Loop Architecture

Billy should keep the current simple loop, but harden its invariants.

Recommended changes:

- Add a turn-level `ToolSet` snapshot in `internal/tools` and use it in
  `internal/agent/runtime_loop.go`, `model_call.go`, and `tool_attempt.go`.
- Add transcript pairing validation in `internal/agent` before every provider
  request.
- Emit structured provider retry events before retry sleep.
- Replace hot-path string concatenation in stream collection with builders.
- Remove fake "retry decision skipped" tool phases unless real retries exist.
- Add tool interrupt behavior as a small trait: `cancel`, `block`, or
  `wait_cleanup`.
- Defer early tool execution until snapshot and pairing invariants are tested.

Key invariants:

- A run has one terminal event.
- A model call has exactly one finished/failed/aborted event.
- A tool attempt has exactly one finished/failed/aborted event.
- A model-visible tool spec is executable from the same turn snapshot.
- No assistant message with tool calls is followed by another assistant before
  all corresponding tool results exist.
- Early tools may overlap the model stream, but cannot feed the next model call
  until the stream closes and all in-flight tools drain.

### 2. Event Log And Streaming Contracts

Billy's protocol is already small and typed. The main fix is delivery
semantics.

Recommended changes:

- In gateway follow mode, treat JSONL store as source of truth: subscribe to a
  wake signal, replay after cursor until caught up, then wait and repeat.
- Make live hub payloads optional for durable events; wake-only is enough.
- Add optional client seq gap detection in `gatewayclient`.
- Make projector stale-drop stricter and record visible gap state.
- Reject duplicate terminal lifecycle events in `eventlog`.

Key invariant:

Persisted session events are lossless. If a client misses live progress, it can
replay from the last durable seq and recover without corrupt projection state.

### 3. TUI Architecture And UX

Do not rewrite TUI around another framework. Improve current Bubble Tea model
and shared transcript renderer.

Recommended changes:

- Add raw/rich transcript mode for reliable copy/debug.
- Harden OSC52/tmux/SSH clipboard support and UTF-8 trimming.
- Normalize streaming markdown for fenced tables and unclosed fences.
- Improve command palette filtering, suggested commands, and keybinding display.
- Extract status rendering into a small renderer.
- Keep collapsed tool previews bounded and semantic.

Key invariant:

TUI rendering is a view over `clientux`/transcript state, not a second runtime
or event interpreter.

### 4. Telegram And Gateway Runtime

Telegram should remain a remote UI. Gateway should own run replacement,
session isolation, and durable event semantics.

Recommended changes:

- Extend `gatewayapi.RunRequest` with client IDs and `InterruptPolicy`.
- Add `CancelAndWait` or `WaitIdle` to session runtime.
- Gateway `interrupt` policy cancels active run, emits terminal cleanup, waits
  briefly for idle, then starts the replacement.
- Track active tool attempts and emit abort cleanup before run terminal events.
- Add lag marker or replay signal for slow subscribers.
- Add stream headers that disable proxy buffering.
- Enforce owner checks on get/run/cancel/events/list endpoints.

Key invariant:

One active run per session. A new Telegram message can replace the old run
without `ErrBusy`, and stale tool rows cannot survive past a terminal run.

### 5. Memory, Profiles, Instructions

Billy should add local memory, but not a memory platform.

Recommended changes:

- Add `internal/memory` with file-based `memory_summary.md`, `MEMORY.md`
  manifest, and topic `.md` files.
- Inject only a capped memory summary into initial messages.
- Expose bounded manual operations: search, read, add note, status.
- Do not auto-extract memories by default.
- Keep memory lower priority than current user prompt, profile, and project
  instructions.
- Add context bucket accounting for memory.

Key invariant:

Memory may be stale and must not claim live code facts. Code/file facts should
be verified from the workspace.

### 6. Context, Compaction, Summarization

This is the biggest token-waste risk. Web and tool output must be bounded
before it reaches the transcript.

Recommended changes:

- Enforce hard inline budget for default `web_fetch`, `web_extract`, and
  `web_crawl` outputs.
- Source-aware tool result compaction: `web_*` outputs above budget always use
  output refs.
- Make compaction keep recent user turns by token budget, not only message
  count.
- Preserve assistant tool-call/tool-result adjacency during compaction.
- Add diagnostics for largest inline tool outputs and raw web text presence.
- Add body-after-protected-prefix counters for cached prefix strategy.

Key invariant:

No default web call can add hundreds of thousands of tokens to the model-visible
transcript. Full page text lives behind `output_ref`.

### 7. Tools And Tool Rendering

Billy needs one compact tool display contract.

Recommended changes:

- Add optional `ToolCompact`/`ToolDisplay` data to protocol events.
- Split `toolrender` into derive functions and render functions.
- Feed compact display through agent, projector, TUI, and Telegram.
- Make output refs first-class in compact display.
- Keep old render helpers as wrappers during migration.

Key invariant:

Compact display is markup-free, bounded, deterministic, and never falls back to
raw JSON arguments for unknown tools.

### 8. MCP Architecture

Billy's lazy MCP gateway direction is correct. The missing piece is live catalog
correctness.

Recommended changes:

- `mcpclient.Manager` remains the only raw MCP catalog owner.
- `tools.Registry` keeps a derived cache and listens to manager catalog changes.
- Handle `notifications/tools/list_changed` using a reader pump, full relist,
  and atomic server catalog swap.
- On close/crash/failure, remove or mark server tools inactive and rebuild the
  global catalog.
- Keep Billy-owned MCP config. Do not import Codex/Claude/OpenCode configs by
  default.

Key invariant:

Removed MCP tools become unknown until rediscovered. Model-visible gateway
tools stay stable; raw MCP tools are discovered lazily through `tool_search` and
`mcp_list_tools`.

### 9. Web Search, Fetch, Crawl

Keep web local, browserless, bounded, and cheap.

Recommended changes:

- Add a reusable capped reader in `internal/webtools`.
- Replace ad hoc HTML regex extraction with a small tokenizer helper.
- Make `web_search` return structured cached output with provider, query,
  result count, timing, cache hit, and no-results metadata.
- Add search cache keys with schema/parser versions.
- Keep model summarization outside the main loop and skip it for tiny/direct
  pages.
- Consider lower concurrency for crawls after measurement.

Key invariant:

Search output never includes fetched page text. Fetch/extract/crawl outputs are
bounded and carry truncation/output-ref metadata.

### 10. Config, Auth, Provider Routing

Billy should keep a narrow provider set and make binding explicit.

Recommended changes:

- Make `ProviderBinding` the resolved contract between config and runtime.
- Add project-local config denylist for sensitive provider/auth fields.
- Validate provider/model/auth consistency before provider construction.
- Use one DeepSeek key resolver path.
- Make Codex auth reject unsupported auth modes explicitly.
- Add diagnostics that show auth source labels without secrets.

Key invariant:

Codex provider code never reads DeepSeek credentials, and DeepSeek provider
code never reads Codex auth.

### 11. Tests, Benchmarks, Reliability

Billy has many useful package tests, but needs a shared scenario.

Recommended changes:

- Add a canonical event JSONL fixture covering model deltas, reasoning, parallel
  tools, web output ref, MCP call, usage, context threshold, compaction, and
  clean completion.
- Replay that fixture through `eventlog`, `trace`, `clientux/projector`, TUI,
  and Telegram.
- Add a fake provider/SSE harness for retry, slow chunks, hangs, resets, and
  request capture.
- Add web SSRF/output-ref tests at handler level.
- Add MCP fault and catalog-change tests.
- Add benchmark smoke script that emits stable `METRIC name value` lines.

Key invariant:

The same durable events reconstruct the same user-visible state in every client.

### 12. Project Architecture And Anti-Bloat

Recommended principles:

- One binary.
- JSONL is the source of truth until benchmarks prove otherwise.
- UIs are projectors.
- Snapshot per provider turn.
- Large data is lazy and out-of-band.
- Boring Go structs/functions before frameworks.
- Split by ownership, not abstraction fashion.
- Speed and token discipline before platform features.

Immediate architecture hygiene:

- Fix missing tracked TUI files and runtime artifact policy.
- Split large TUI files without changing architecture.
- Keep `internal/tui/transcript.Projector` canonical for TUI transcript state.
- Split `internal/tools/tools.go` only by ownership while keeping one registry.

## Second-Pass Tool Gap Findings

A separate subagent pass over the attached tool-gap notes found that Billy is
not missing a platform. It is missing a few small, high-leverage coding harness
primitives.

Useful additions:

- Bounded file reads with line windows and stable anchors.
- Turn-scoped checkpoint/diff/undo records before broader mutating tool work.
- Agent-visible work plan state, equivalent to a local `todo_write`.
- Native read-only grep/glob and fuzzy file resolution, plus TUI `@file`
  insertion.
- Exact string file edits, with no fuzzy matching and no partial writes.
- Managed background shell runs for dev servers and long tasks.
- Command-based compiler diagnostics before any full LSP investment.
- Local slash prompt commands and a bounded `user_prompt_submit` hook.
- Later, only if the base is proven: structured patching, stateless subagents,
  reviewed memory candidate extraction, and deferred diagnostics.

The important constraint is ordering: checkpoint/undo and bounded reads should
land before larger editing ergonomics, shell management, or autonomous
subagents. Otherwise Billy gains power before it gains enough reversibility and
context discipline.

## Third-Pass Runtime Productivity Findings

The additional architecture note reframed the gap: Billy already has a solid
runtime base, but a solo coding harness also needs admission, search,
backpressure, and project-context primitives that keep long Telegram/TUI work
usable.

Useful additions:

- Durable prompt admission with per-session `inputs.jsonl`, idempotent input
  ids, promotion records, and explicit ambiguous-input handling after restart.
- Telegram offset safety tied to durable admission, so user intent is not lost
  between polling and gateway execution.
- Slow-client backpressure hardening: live `/run` streams are progress channels,
  while JSONL replay is the recovery path.
- Batched TUI event apply/reflow and optional delta coalescing to avoid one
  render/fsync per provider chunk.
- Rebuildable session search/index rows for text, tools, errors, runs, and
  usage, with JSONL remaining canonical.
- Interactive `ask_user` as a bounded clarification tool for TUI and Telegram.
- Minimal project context registry with git root, package manager, likely test
  commands, instruction metadata, env names without values, and context epochs.
- Per-turn diff display and revert preview built from the checkpoint/diff
  recorder, not from hidden user-git state.

These are productivity primitives, not platform features. They should stay
local, rebuildable, bounded, and easy to delete.

## What Not To Copy

- Codex: cloud tasks, app-server surface, guardian/reviewer stack, remote
  workspace orchestration, marketplace/extension precedence.
- OpenCode: Effect runtime, SQLite event store, plugin lifecycle, generated SDK,
  organization config, large server platform.
- Claude Code: React/Ink UI stack, team memory, managed policy sync, analytics,
  remote relay, mobile/Slack/CCR integrations, multi-agent teammates.
- Any source: enterprise RBAC, SaaS telemetry, compliance machinery, policy
  language, auto-memory extraction by default, headless browser web pipeline.
- Any source: general job schedulers for prompt admission, hidden user-git
  commits/stash/reset, mandatory SQLite/FTS/vector search, shell-history
  scraping, `.env` value injection, full README ingestion by default, rich MCP
  elicitation forms before simple `ask_user` works.

## Recommended Implementation Order

1. Fix durable event follow/replay semantics and gap detection.
2. Add gateway-level interrupt policy with cancel-and-wait and stale tool
   cleanup.
3. Add durable prompt admission/input inbox and Telegram offset safety.
4. Decouple slow live clients from active execution and make replay the recovery
   path.
5. Batch TUI event apply/reflow so high-volume streams stay responsive.
6. Enforce hard web/tool inline budgets and output refs.
7. Add turn-level tool snapshots and transcript pairing validation.
8. Add canonical event trace tests across eventlog, gateway, projector, TUI, and
   Telegram.
9. Add bounded `fs_read_file` windows with line numbering, truncation metadata,
   and output-safe rendering.
10. Add turn-scoped checkpoint/diff/undo records for mutating tool steps before
    expanding write/edit/shell ergonomics.
11. Add shared `ToolCompact` display contract.
12. Fix MCP catalog change/stale tool lifecycle.
13. Add rebuildable session search/index rows and CLI queries.
14. Add delta coalescing only after replay/gap tests prove final transcript
    equality.
15. Add agent work plan state and compact TUI/Telegram rendering.
16. Add native grep/glob, fuzzy file resolution, and TUI `@file` insertion.
17. Add exact string edit and structured patch tools only after checkpoint/undo
    is in place.
18. Add interactive `ask_user` clarification for TUI/Telegram.
19. Add minimal project context registry and context epochs.
20. Add managed background shell lifecycle for dev servers and long commands.
21. Add command-based diagnostics feedback; defer full LSP until this proves
    useful.
22. Harden TUI copy/raw/markdown and command palette.
23. Add structured cached web search and tokenizer extraction.
24. Add local manual memory summary system.
25. Add provider binding denylist/auth-source cleanup.
26. Only then consider sparse seq indexes, early tool execution, input-aware
    concurrency, stateless subagents, and reviewed AI memory candidates.

## Proof Strategy

Useful changes must prove one of these:

- Lower active context tokens after large web/tool outputs.
- Lower latency in model/tool overlap or provider streaming.
- No duplicate/missing events after reconnect.
- No stale Telegram/TUI tool state after interrupt.
- No admitted Telegram/TUI prompt lost across restart or gateway failure.
- Slow live clients cannot stall an active run.
- No tool execution from a tool schema not advertised in that turn.
- Same event trace renders consistently in all clients.
- Session search/index rows can be rebuilt from JSONL without changing truth.
- Benchmarks show JSONL remains sufficient before adding storage complexity.
