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

Next:

1. `internal/session`: add a lightweight session runner around `agent.RunMessages`.
2. Gateway session locking/cancel/subscribe over the session runner.
3. Trace replay verifier that compares event counts against result rows.
4. Shared tool summary renderer for TUI and Telegram.
5. TUI transcript cells without changing saved session format.
6. Active live stream cell and newline-gated markdown streaming.
7. Telegram single progress message for tools/thinking.
8. Provider info layer for DeepSeek/OpenAI/Codex instead of model-prefix routing.
9. MCP manager status/reconnect events surfaced through gateway/TUI.
10. Context window state with explicit compaction IDs and protected prefix.

## Engineering Rules

- Keep Go package count low until an abstraction pays for itself.
- Add tests before broad UI rewrites.
- Preserve existing CLI/API compatibility unless the old UX is actively harmful.
- Prefer JSONL and typed replay before adding indexes.
- Keep dangerous local defaults available for solo use, but emit auditable events.
- Every long-run benchmark should produce a replayable bundle.
