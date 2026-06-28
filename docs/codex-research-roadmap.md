# Codex Research Roadmap

This document records the Codex architecture research pass and turns it into a billyharness implementation plan.

## Target Shape

Billyharness should stay a small, fast solo harness. The goal is not to clone the full Codex workspace. The useful target is:

- a lightweight `Session` and `Turn` runtime over the current agent loop;
- one event stream shared by TUI, Telegram, gateway, bench, and replay;
- JSONL-first run/session persistence with optional indexes later;
- DeepSeek API and Codex OAuth providers behind the same provider/auth contracts;
- native web/fs/shell tools plus lazy MCP discovery for Telegram, Telegram Parilka, GitHub, and Context7;
- typed TUI transcript cells and compact Telegram progress rendering;
- aggressive defaults for speed and context economy, with explicit opt-in for large outputs.

## What To Borrow From Codex

Borrow architecture patterns, not product weight:

- `Session -> Turn -> Step` separation for runtime state.
- Structured events: turn started/completed/aborted, item started/completed, tool started/completed, usage, compaction.
- Tool registry contracts: spec, exposure, search metadata, parallel policy, handler.
- Lazy/deferred discovery for large tool inventories.
- Auth layering: auth manager, auth provider, provider info, request transport.
- JSONL history as canonical storage; indexes are rebuildable.
- UI composition: typed history cells, live stream cell, bottom pane stack, footer modes.
- Small replay/trace bundles for debugging and benchmarks.

## What Not To Borrow

Do not pull in the full enterprise platform:

- Codex app-server JSON-RPC compatibility.
- Plugin marketplace and remote plugin sync.
- Full multi-agent persisted graph and inter-agent mailbox.
- Guardian/reviewer/enterprise permission stack.
- Remote environments, cloud tasks, realtime/audio, app-store surfaces.
- Full SQLite thread-store before JSONL replay is mature.

## 12-Agent Deep Dive Plan

When a full research pass is needed, split it like this:

1. Core runtime: session, turn, input queue, cancellation.
2. Provider/auth: DeepSeek, Codex OAuth, refresh, retry, request body reuse.
3. Tools core: `ToolExecutor`, namespaces, `tool_search`, parallel policies.
4. MCP lifecycle: allowed servers, status, reconnect, output caps.
5. Run/session store: manifest, events JSONL, payload refs, replay invariants.
6. Gateway API: sessions, run, subscribe, cancel, status, autoserve UX.
7. TUI architecture: cells, live tail, bottom pane stack, resume picker.
8. Telegram UX: rich messages, one progress message, commands, throttling.
9. Context compaction: 600k window, summary policy, usage semantics.
10. Performance: SSE, channel backpressure, web cache, request reuse.
11. Config/profiles: SOUL.md, AGENTS.md, model/reasoning defaults.
12. Permissions-lite: dangerous mode, risk labels, audit events.

Each agent should return target files, migration steps, tests, and things to leave out.

## Implementation Slices

Completed:

- Replayable bench trace bundles: manifest, sequenced events, payload refs, replay check.
- Codex auth refresh serialization: snapshot/mutex around refresh and request headers.
- Lightweight `tool_search`: native and hidden MCP discovery without exposing raw MCP tools.
- Smaller default web budgets: cheaper web fetch/crawl outputs unless explicitly expanded.
- Lightweight `internal/session`: history snapshots, active-run guard, cancellation.
- Gateway session locking/cancel over the session runner.
- Trace replay verifier compares event aggregates against result rows.
- Trace replay verifies payload ref files, byte counts, and SHA-256 hashes.
- Shared compact tool summary renderer for TUI and Telegram.
- Provider/model metadata layer for DeepSeek and Codex subscription models.
- Structured compaction telemetry: stable compaction IDs, trigger/threshold/message counts.
- Telegram `/cancel` explicitly cancels gateway sessions as well as local streams.
- Gateway subscribe/status stream over the session runner.
- TUI saved chats, `/resume`, `/fork`, active live assistant block, and newline-gated markdown streaming.
- Telegram single progress message for model/tool/thinking progress, compact tool rendering, context footer, profile switching, and user allowlist.
- MCP manager status/reconnect fields surfaced through gateway, TUI, and Telegram.
- Context window protected-prefix audit and compaction policy controls.
- JSONL session persistence for gateway sessions: manifest, `history.jsonl`, `events.jsonl`, replay, legacy snapshot fallback.
- Tool policy/audit events for dangerous local defaults.
- Parallel execution for read-only/network tool batches, capped by `FAST_AGENT_MAX_PARALLEL_TOOLS`.
- Terminal-Bench export/import adapter for local dataset workflows.
- Graceful systemd shutdown for gateway/Telegram service processes.
- Typed `turn.started`, `turn.completed`, `step.started`, and `step.completed` events over the current agent loop.
- Replay and bench counters for turns, steps, step errors, and parallel tool batches.
- Basic latency telemetry in step events and benchmarks: first streamed delta, model step duration, tool step duration, parallel batch duration.

## Active Backlog

P0 runtime correctness and speed:

- Promote typed `Turn` and `Step` events into an internal runtime model: input queue, cancellation, step storage, and replayable state transitions.
- Extend replay/bench timing metrics into distributions: p50/p95 first-delta, model latency, tool latency, compaction timing, and per-provider retry timing.
- Harden parallel tools with clearer policy metadata: why a tool can or cannot run in parallel, batch ids, limits, cancellation behavior.
- Add real long-loop benchmark runs: Terminal-Bench import/export smoke, local 50-100 turn loop, DeepSeek Flash/Pro comparison, Codex subscription comparison when available.

P1 context and web economy:

- Add optional external summarizer for web fetch/extract/crawl with a cheap configured model, while keeping current extractive summarizer as the free default.
- Add a web cache keyed by URL, query, extraction mode, and max budget, with TTL and cache metrics.
- Add context-growth guardrails that show which messages/tool outputs dominate the active context before compaction fires.

P1 UI and operator UX:

- Move TUI closer to typed transcript cells: assistant, user, reasoning, tool, audit, compaction, status, run summary.
- Add a richer resume picker and session inspector backed by gateway JSONL, not only local TUI session JSON.
- Surface MCP lifecycle changes as live transcript/status events instead of only `/mcp` snapshots.
- Make Telegram and TUI share the same compact run-summary renderer where possible.

P2 provider/auth:

- Keep DeepSeek and Codex OAuth behind the same provider/auth contracts, but add request-body reuse and explicit retry telemetry.
- Add provider capability metadata tests: parallel tool calls, reasoning modes, cache usage fields, token accounting fields.

P2 storage:

- Add optional rebuildable indexes over JSONL stores after replay invariants are stable.
- Add trace/session export bundles that include config, model metadata, profile hash, MCP status snapshot, and sanitized event payload refs.

## Acceptance Criteria For Next Pass

- `go test -count=1 ./...` stays green.
- Gateway and Telegram services restart without systemd timeout.
- Every new runtime feature emits replayable JSONL events.
- Every benchmark run can be replay-checked from its bundle without API access.
- Default web/tool behavior remains cheap; large raw outputs require explicit opt-in or output refs.

## Engineering Rules

- Keep Go package count low until an abstraction pays for itself.
- Add tests before broad UI rewrites.
- Preserve existing CLI/API compatibility unless the old UX is actively harmful.
- Prefer JSONL and typed replay before adding indexes.
- Keep dangerous local defaults available for solo use, but emit auditable events.
- Every long-run benchmark should produce a replayable bundle.
