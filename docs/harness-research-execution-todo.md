# Harness Research Execution TODO

This is the execution checklist distilled from the competitive harness research.
The long-form research remains the source of detail; this file is the working
task board for implementation.

Source documents:

- `/root/billyharness/docs/competitive-architecture-analysis.md`
- `/root/billyharness/docs/competitive-improvements-todo.md`
- `/root/billyharness/docs/architecture-decomposition-todo.md`
- `/root/billyharness/docs/decomposition-next-todo.md`

## Operating Rules

- Work in milestone order unless a task is blocked by an explicit dependency.
- Keep JSONL as the source of truth until benchmarks prove it cannot hold.
- Keep every feature local, bounded, and useful for a solo owner.
- Do not copy source code from Codex, OpenCode, Claude Code, or any other repo.
- Extract only clean-room ideas: invariants, interfaces, UX patterns, tests, and
  failure modes.
- Update this TODO when a task is completed, blocked, split, reprioritized, or
  moved to deferred.
- After each completed or explicitly blocked task, make one scoped git commit
  and push it to the configured upstream.
- Do not batch unrelated tasks into one commit.
- Do not start broad coding UX before Milestone 1 is green.

Status markers:

- `[ ]` open
- `[~]` in progress
- `[x]` done
- `[!]` blocked
- `[-]` intentionally deferred

## Explicit Non-Goals

- Plugin marketplace, extension store, or cloud plugin sync.
- Enterprise RBAC, org policy, compliance, SaaS telemetry, or audit theater.
- Heavy UI framework migration.
- Codex app-server compatibility layer.
- OpenCode Effect/SQLite platform as the default architecture.
- Claude-style large permission classifier.
- Hidden user-git commits, stash, branches, or `git reset`.
- Mandatory SQLite/FTS/vector DB for the first index.
- Full IDE/LSP platform before command diagnostics are proven.
- Team/cloud agents, nested agent swarms, or durable subagent queues.
- Auto-memory writes without user review.
- Shell-history scraping, `.env` value injection, or full README ingestion by
  default.

## Milestone 0 - Baseline And Safety

Goal: make the work reproducible before changing runtime behavior.

- [x] HR-00.1 Confirm current repo baseline.
  - source: current worktree and all source documents above.
  - target files: no required code files.
  - acceptance: `git status --short` is understood; unrelated dirty files are
    not reverted; current branch and upstream are known.
  - verification: `git status --short`, `git branch --show-current`,
    `git rev-parse --abbrev-ref --symbolic-full-name @{u}`.
  - status: completed 2026-07-01.
  - evidence: worktree clean; branch `main`; upstream `origin/main`; pushed
    baseline commit `16e96a791a4f67d97d0a234ebf2a094ad6a88c2d`; `HEAD` matches
    `@{u}`.
  - commit: pending.

- [x] HR-00.2 Build an internal implementation plan from this file.
  - source: this TODO.
  - target files: no code files.
  - acceptance: `update_plan` has exactly one task in progress; blocked tasks
    are recorded in this file with concrete reason and next action.
  - verification: plan state is visible in the current Codex turn.
  - status: completed 2026-07-01.
  - evidence: active `update_plan` mirrors Milestone 0 setup, HR-01.1 through
    HR-01.10 in priority order, Milestone 1 final verification, and later
    P0/P1 continuation; exactly one item is in progress.
  - commit: pending.

- [x] HR-00.3 Establish verification baseline for touched packages.
  - source: package-level commands in each task.
  - target files: no required code files.
  - acceptance: baseline failures, if any, are recorded before implementation.
  - verification: run the focused package tests needed by the first selected
    task.
  - status: completed 2026-07-01.
  - evidence: `/root/.local/go/bin/go test -count=1 ./internal/gateway
    ./internal/gatewayclient ./internal/eventlog ./internal/clientux/projector`
    passed before HR-01.1 implementation.
  - commit: pending.

## Milestone 1 - Reliability, Admission, Backpressure

Goal: make long Telegram/TUI runs durable, replayable, and hard to stall before
adding more powerful tools.

- [ ] HR-01.1 Durable gateway follow and replay.
  - maps to: `competitive-improvements-todo.md` P0-1.
  - target files: `internal/gateway/gateway.go`,
    `internal/gateway/session_events.go`,
    `internal/gatewayclient/client.go`,
    `internal/clientux/projector/projector.go`,
    `internal/eventlog/*`.
  - acceptance: follow subscribes before replay; gaps are detectable; replay
    reads `seq > cursor`; duplicate terminal lifecycle events are rejected.
  - verification: `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/eventlog ./internal/clientux/projector`.

- [ ] HR-01.2 Gateway interrupt and stale cleanup.
  - maps to: `competitive-improvements-todo.md` P0-2.
  - target files: `internal/gatewayapi/*`, `internal/gateway/*`,
    `internal/session/*`, `internal/telegrambot/runner.go`,
    `internal/telegrambot/render.go`, `internal/gatewayclient/*`.
  - acceptance: new input can cancel old run, wait briefly for idle, emit
    terminal run/tool cleanup, and start the new run without stale old-run UI.
  - verification: `go test -count=1 ./internal/gateway ./internal/session ./internal/telegrambot ./internal/gatewayclient`.

- [ ] HR-01.3 Durable prompt admission/input inbox MVP.
  - maps to: `competitive-improvements-todo.md` B1.
  - target files: `internal/gateway/session_store.go`,
    `internal/gateway/session_inputs.go`, `internal/gatewayapi/types.go`,
    `internal/gateway/gateway.go`, `internal/gatewayclient/client.go`,
    `internal/session/session.go`.
  - acceptance: per-session `inputs.jsonl`; idempotent `input_id`; fsynced
    admission before execution; promotion recorded before `Session.Run`;
    unpromoted inputs survive restart; promoted incomplete inputs are marked
    ambiguous, not silently replayed.
  - verification: `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/session`.

- [ ] HR-01.4 Telegram durable admission and offset safety.
  - maps to: `competitive-improvements-todo.md` B2.
  - target files: `internal/telegrambot/poller.go`,
    `internal/telegrambot/runner.go`,
    `internal/telegrambot/state_runtime.go`,
    `internal/telegrambot/store.go`,
    `internal/telegrambot/admission_store.go`,
    `internal/gatewayclient/client.go`, `docs/telegram.md`.
  - acceptance: Telegram offset advances only after durable admission or
    intentional ignore; duplicate updates do not duplicate runs; interrupt and
    supersede semantics remain intact.
  - verification: `go test -count=1 ./internal/telegrambot ./internal/gatewayclient`.

- [ ] HR-01.5 Slow-client run/event decoupling.
  - maps to: `competitive-improvements-todo.md` B3.
  - target files: `internal/gateway/gateway.go`,
    `internal/gateway/session_events.go`,
    `internal/gatewayclient/client.go`,
    `internal/tui/gateway_session.go`,
    `internal/eventlog/*`, `docs/architecture.md`.
  - acceptance: blocked live response writers cannot stall active execution;
    clients receive gap/drop/reconnect hints and can recover via
    `/events?after_seq=last_seq`.
  - verification: `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/eventlog`.

- [ ] HR-01.6 Batched TUI event apply and reflow.
  - maps to: `competitive-improvements-todo.md` B4.
  - target files: `internal/tui/tui.go`,
    `internal/tui/gateway_session.go`,
    `internal/tui/transcript/projector.go`,
    `internal/tui/tui_test.go`.
  - acceptance: event batches apply on a short tick; terminal/tool-boundary
    events flush immediately; 1000 deltas produce the same final transcript
    with far fewer reflows.
  - verification: `go test -count=1 ./internal/tui ./internal/tui/transcript`.

- [ ] HR-01.7 Hard inline budgets for web and large tool outputs.
  - maps to: `competitive-improvements-todo.md` P0-3.
  - target files: `internal/tools/web_core.go`,
    `internal/tools/web_handlers.go`, `internal/tools/tools.go`,
    `internal/agent/*`, `internal/clientux/context.go`.
  - acceptance: web fetch/extract/crawl inline output cannot bypass configured
    budget; full text goes to `output_ref`; context diagnostics identify large
    inline sources.
  - verification: `go test -count=1 ./internal/tools ./internal/agent ./internal/clientux`.

- [ ] HR-01.8 Turn-level tool snapshot and transcript pairing.
  - maps to: `competitive-improvements-todo.md` P0-4.
  - target files: `internal/tools/*`, `internal/agent/runtime_loop.go`,
    `internal/agent/model_call.go`, `internal/agent/tool_attempt.go`,
    `internal/agent/transcript_pairing.go`, `internal/runstate/runstate.go`.
  - acceptance: model-visible specs, handlers, metadata, and MCP catalog are
    frozen per provider turn; malformed tool-result pairing is rejected before
    provider calls.
  - verification: `go test -count=1 ./internal/agent ./internal/tools ./internal/mcpclient`.

- [ ] HR-01.9 Canonical end-to-end event trace.
  - maps to: `competitive-improvements-todo.md` P0-5.
  - target files: `internal/testkit/*`,
    `internal/testkit/testdata/traces/agent_loop_full.jsonl`,
    `internal/trace/trace_test.go`,
    `internal/clientux/projector/projector_test.go`,
    `internal/tui/tui_test.go`,
    `internal/telegrambot/render_test.go`.
  - acceptance: one fixture covers deltas, parallel tools, web output ref, MCP,
    usage, context threshold, compaction, and terminal completion; all clients
    project it consistently.
  - verification: `go test -count=1 ./internal/testkit ./internal/trace ./internal/clientux/projector ./internal/tui ./internal/telegrambot`.

- [ ] HR-01.10 Bounded file reads with line windows.
  - maps to: `competitive-improvements-todo.md` A1.
  - target files: `internal/tools/tools.go`, `internal/tools/fs_read.go`,
    `internal/tools/tools_test.go`, `internal/toolrender/toolrender.go`.
  - acceptance: legacy `{"path":...}` stays compatible; optional
    `offset/limit` returns numbered lines, truncation metadata, `next_offset`,
    `total_lines`, and safe binary/symlink/sensitive-path behavior.
  - verification: `go test -count=1 ./internal/tools ./internal/toolrender`.

Milestone 1 final verification:

- [ ] Run focused reliability suite:
  - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/session ./internal/telegrambot ./internal/tui ./internal/eventlog ./internal/tools ./internal/agent ./internal/clientux/projector`
- [ ] Run targeted regex suite:
  - `go test -run 'Test.*Replay.*|Test.*Seq.*|Test.*Interrupt.*|Test.*Admission.*|Test.*InputInbox.*|Test.*Telegram.*(Admission|Offset).*|Test.*Slow.*Client.*|Test.*Backpressure.*|Test.*TUI.*(Batch|Reflow).*|Test.*FSRead.*|Test.*ToolSnapshot.*|Test.*TranscriptPairing.*|Test.*Golden.*Trace.*' -count=1 ./internal/...`
- [ ] Commit and push a milestone summary update after all HR-01 tasks are
  complete.

## Milestone 2 - Reversibility And Display Contracts

Goal: add rollback and compact display invariants before expanding edit power.

- [ ] HR-02.1 Provider binding denylist and auth-source discipline.
  - maps to: `competitive-improvements-todo.md` P0-6.
  - acceptance: project config cannot redirect secrets or mix Codex/DeepSeek
    credential paths.
  - verification: `go test -count=1 ./internal/config ./internal/provider ./internal/credentials ./internal/codexauth ./internal/modelinfo`.

- [ ] HR-02.2 Turn-scoped checkpoints, diff, preview, and undo.
  - maps to: `competitive-improvements-todo.md` A2.
  - acceptance: no hidden commits/stash/reset/user `.git` writes; mutating file
    and shell steps produce turn-change events; preview writes nothing; undo is
    conflict-safe and denied during active runs.
  - verification: `go test -count=1 ./internal/checkpoint ./internal/agent ./internal/eventlog ./internal/gateway ./internal/tui ./internal/telegrambot`.

- [ ] HR-02.3 Minimal turn diff display and revert preview UX.
  - maps to: `competitive-improvements-todo.md` B11.
  - acceptance: transcript shows compact file/stat summary; full patch is
    available by output ref/API/command; Telegram/TUI can request preview.
  - verification: `go test -count=1 ./internal/clientux/projector ./internal/tui ./internal/telegrambot ./internal/toolrender ./internal/checkpoint`.

- [ ] HR-02.4 Shared ToolCompact display contract.
  - maps to: `competitive-improvements-todo.md` P1-7.
  - acceptance: TUI and Telegram render one bounded compact tool state with no
    raw JSON fallback.
  - verification: `go test -count=1 ./internal/toolrender ./internal/tooloutput ./internal/agent ./internal/clientux/projector ./internal/telegrambot ./internal/tui/transcript`.

- [ ] HR-02.5 MCP catalog change lifecycle.
  - maps to: `competitive-improvements-todo.md` P1-8.
  - acceptance: manager owns raw catalog; relist atomically swaps tools;
    crashed/closed servers remove active specs; stale tools fail as unknown.
  - verification: `go test -count=1 ./internal/mcpclient ./internal/tools ./internal/config`.

## Milestone 3 - Core Coding Tools

Goal: make Billy useful as a coding agent without full-file rewrites or shell
abuse.

- [ ] HR-03.1 Agent work plan state.
  - maps to: `competitive-improvements-todo.md` A3.
  - acceptance: `todo_write` has bounded todos, valid statuses, one
    `in_progress`, deterministic compact rendering, and replayable state.
  - verification: `go test -count=1 ./internal/tools ./internal/clientux/projector ./internal/toolrender ./internal/tui/transcript ./internal/telegrambot`.

- [ ] HR-03.2 Native grep and glob tools.
  - maps to: `competitive-improvements-todo.md` A5.
  - acceptance: bounded regex/glob search with context, limits, binary skip,
    sensitive-path skip, deterministic truncation, and compact rendering.
  - verification: `go test -count=1 ./internal/tools ./internal/toolrender ./internal/tui/transcript`.

- [ ] HR-03.3 Fuzzy file resolver and TUI `@file`.
  - maps to: `competitive-improvements-todo.md` A6.
  - acceptance: ranked file search uses git/rg/walk fallbacks; TUI inserts exact
    paths without auto-injecting file content.
  - verification: `go test -count=1 ./internal/filesearch ./internal/tools ./internal/tui`.

- [ ] HR-03.4 Run access modes and plan mode.
  - maps to: `competitive-improvements-todo.md` A7.
  - acceptance: `build|guarded|plan`; plan mode filters visible tools and
    hard-denies write/execute/external tools.
  - verification: `go test -count=1 ./internal/config ./internal/agent ./internal/tools ./internal/gateway ./internal/tui ./internal/telegrambot`.

- [ ] HR-03.5 Exact file edit tool.
  - maps to: `competitive-improvements-todo.md` A4.
  - dependency: HR-02.2.
  - acceptance: exact-only `fs_edit_file`; no fuzzy match; atomic multi-edit;
    optional `expected_sha256`; no full file result.
  - verification: `go test -count=1 ./internal/tools ./internal/agent ./internal/toolrender`.

- [ ] HR-03.6 Structured patch tool.
  - maps to: `competitive-improvements-todo.md` A13.
  - dependency: HR-02.2 and HR-03.5.
  - acceptance: clean-room parser; all hunks verify before write; invalid patch
    never mutates files.
  - verification: `go test -count=1 ./internal/tools`.

- [ ] HR-03.7 Managed shell runtime for dev servers.
  - maps to: `competitive-improvements-todo.md` A8.
  - dependency: HR-02.2.
  - acceptance: background process ids are Billy-owned; output is bounded and
    cursor/ref based; kill is scoped; registry cleanup prevents leaks.
  - verification: `go test -count=1 ./internal/tools ./internal/tooloutput ./internal/agent`.

- [ ] HR-03.8 Command-based diagnostics feedback.
  - maps to: `competitive-improvements-todo.md` A9.
  - acceptance: configured commands produce structured bounded diagnostics with
    raw output refs and no full LSP platform.
  - verification: `go test -count=1 ./internal/diagnostics ./internal/tools ./internal/agent ./internal/toolrender ./internal/tui/transcript ./internal/telegrambot ./internal/config`.

- [ ] HR-03.9 Local custom slash prompt commands.
  - maps to: `competitive-improvements-todo.md` A10.
  - acceptance: local markdown commands load from Billy home and workspace;
    built-ins cannot be shadowed; placeholders expand deterministically.
  - verification: `go test -count=1 ./internal/tui ./internal/promptcommands`.

- [ ] HR-03.10 `user_prompt_submit` hook.
  - maps to: `competitive-improvements-todo.md` A11.
  - acceptance: hook runs before model call; can block or add bounded context;
    blocked prompts do not reach provider history.
  - verification: `go test -count=1 ./internal/config ./internal/hooks ./internal/agent ./internal/session ./internal/gateway`.

## Milestone 4 - Long-Run Productivity

Goal: make many sessions and projects manageable without turning Billy into a
platform.

- [ ] HR-04.1 Rebuildable session search and diagnostics side index.
  - maps to: `competitive-improvements-todo.md` B5.
  - acceptance: rebuild emits text/tool/error/run/usage rows from JSONL; corrupt
    index never breaks session list/inspect; no mandatory SQLite/FTS/vector.
  - verification: `go test -count=1 ./internal/gateway ./internal/clientux/projector ./internal/trace`.

- [ ] HR-04.2 CLI session queries.
  - maps to: `competitive-improvements-todo.md` B6.
  - acceptance: `sessions search/tools/errors/usage/runs` support human and
    JSON output with limits.
  - verification: `go test -count=1 ./cmd/fast-agent-harness ./internal/gateway`.

- [ ] HR-04.3 Interactive `ask_user` MVP.
  - maps to: `competitive-improvements-todo.md` B7.
  - acceptance: one pending request per session; TUI/Telegram answers resume the
    same run; Telegram answers do not interrupt pending question runs.
  - verification: `go test -count=1 ./internal/protocol ./internal/agent ./internal/tools ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot`.

- [ ] HR-04.4 Minimal project context registry.
  - maps to: `competitive-improvements-todo.md` B8.
  - acceptance: bounded snapshot of cwd, roots, package manager, likely
    commands, instruction metadata, env names without values, and cap flags.
  - verification: `go test -count=1 ./internal/projectcontext ./internal/agent ./internal/clientux ./internal/config ./internal/instructions`.

- [ ] HR-04.5 Context epoch reconcile.
  - maps to: `competitive-improvements-todo.md` B9.
  - acceptance: unchanged project context injects nothing; changed context
    injects one bounded update; restart reuses stored epoch.
  - verification: `go test -count=1 ./internal/runstate ./internal/session ./internal/gateway ./internal/agent ./internal/clientux`.

- [ ] HR-04.6 Delta coalescing before durable append and render.
  - maps to: `competitive-improvements-todo.md` B10.
  - acceptance: final assistant text is identical; first visible delta latency
    is bounded; replay order remains valid.
  - verification: `go test -count=1 ./internal/agent ./internal/gateway ./internal/clientux/projector ./internal/telegrambot`.

- [ ] HR-04.7 JSONL scale benchmark and sparse seq index gate.
  - maps to: `competitive-improvements-todo.md` B12.
  - acceptance: 10k/100k append and tail-replay metrics exist; sparse `.idx`
    only if measured full scan misses target.
  - verification: `go test -run '^$' -bench 'Benchmark(SessionJSONL|EventJSONL|ReplayAfterSeq)' -benchmem ./internal/gateway ./internal/eventlog`.

- [ ] HR-04.8 Cross-adapter slow-client stress harness.
  - maps to: `competitive-improvements-todo.md` B15.
  - acceptance: fake provider 10k chunks, blocked TUI channel, Telegram 429,
    replay after drop, and final state correctness are tested.
  - verification: `go test -count=1 ./internal/testkit ./internal/gateway ./internal/tui ./internal/telegrambot ./internal/provider`.

## Milestone 5 - Deferred Bounded Capabilities

Goal: implement only after the smaller contracts are stable and tests prove the
need.

- [ ] HR-05.1 Redo state and destructive git command guardrails.
  - maps to: `competitive-improvements-todo.md` A12.
  - dependency: HR-02.2.
  - acceptance: one linear redo stack; destructive git shell commands are
    blocked or require explicit approval.

- [ ] HR-05.2 Minimal stateless subagent tools.
  - maps to: `competitive-improvements-todo.md` A14.
  - dependency: Milestone 1 and ToolCompact.
  - acceptance: depth 1, max 3 workers, explorer read-only, compact output ref,
    parent cancel cancels children.

- [ ] HR-05.3 Opt-in AI memory candidate extraction.
  - maps to: `competitive-improvements-todo.md` A15.
  - dependency: manual memory MVP.
  - acceptance: disabled by default; candidate-only; user approval required
    before canonical memory mutation.

- [ ] HR-05.4 MCP elicitation bridge.
  - maps to: `competitive-improvements-todo.md` B13.
  - dependency: HR-04.3.
  - acceptance: simple MCP elicitation maps to common pending input events;
    unsupported forms decline cleanly.

- [ ] HR-05.5 Last Billy commands project context source.
  - maps to: `competitive-improvements-todo.md` B14.
  - dependency: HR-04.4.
  - acceptance: only last N Billy-run shell commands, no raw logs, no shell
    history scraping.

- [ ] HR-05.6 Deferred diagnostics, LSP, memory, and backup extensions.
  - maps to: `competitive-improvements-todo.md` A16.
  - acceptance: each feature stays opt-in and behind a proven interface.

- [ ] HR-05.7 Early tool execution and input-aware parallelism.
  - maps to: `competitive-improvements-todo.md` P2-15 and P2-16.
  - dependency: transcript pairing, tool snapshots, interrupt cleanup.
  - acceptance: latency improves without violating model-call order or
    cancellation semantics.

## Completion Criteria

- Milestone 1 is complete before broad coding UX begins.
- Each completed or blocked task has a scoped commit and push.
- This TODO is updated with status, commit hash, verification command, and any
  remaining blocker.
- Full milestone verification passes, or failures are documented with exact
  command output and next action.
- No explicit non-goal has been introduced.
