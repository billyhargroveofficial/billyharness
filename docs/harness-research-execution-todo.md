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

- [x] HR-01.1 Durable gateway follow and replay.
  - maps to: `competitive-improvements-todo.md` P0-1.
  - target files: `internal/gateway/gateway.go`,
    `internal/gateway/session_events.go`,
    `internal/gatewayclient/client.go`,
    `internal/clientux/projector/projector.go`,
    `internal/eventlog/*`.
  - acceptance: follow subscribes before replay; gaps are detectable; replay
    reads `seq > cursor`; duplicate terminal lifecycle events are rejected.
  - verification: `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/eventlog ./internal/clientux/projector`.
  - status: completed 2026-07-01.
  - evidence: gateway follow already subscribed before replay and replayed
    `seq > cursor`; added typed gateway-client `EventSeqGapError`, projector
    `SeqGap` snapshot state, and shared eventlog duplicate-terminal run
    rejection used by persisted session replay.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/gateway
    ./internal/gatewayclient ./internal/eventlog ./internal/clientux/projector`
    passed; `/root/.local/go/bin/go test -run
    'Test.*Replay.*|Test.*Seq.*|Test.*Lifecycle.*' -count=1
    ./internal/gateway ./internal/gatewayclient ./internal/eventlog
    ./internal/clientux/projector` passed.
  - commit: pending.

- [x] HR-01.2 Gateway interrupt and stale cleanup.
  - maps to: `competitive-improvements-todo.md` P0-2.
  - target files: `internal/gatewayapi/*`, `internal/gateway/*`,
    `internal/session/*`, `internal/telegrambot/runner.go`,
    `internal/telegrambot/render.go`, `internal/gatewayclient/*`.
  - acceptance: new input can cancel old run, wait briefly for idle, emit
    terminal run/tool cleanup, and start the new run without stale old-run UI.
  - verification: `go test -count=1 ./internal/gateway ./internal/session ./internal/telegrambot ./internal/gatewayclient`.
  - status: completed 2026-07-01.
  - evidence: added `interrupt_policy:"interrupt"` to gateway run requests,
    session `CancelAndWait`, gateway pre-run cancel-and-wait with persisted
    old-run terminal failure, and Telegram run requests that opt into gateway
    interrupt policy while retaining superseded-message UI guards.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/gateway
    ./internal/session ./internal/telegrambot ./internal/gatewayclient` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Interrupt.*|Test.*CancelAndWait.*|Test.*Stale.*Tool.*' -count=1
    ./internal/gateway ./internal/session ./internal/telegrambot
    ./internal/gatewayclient` passed.
  - commit: pending.

- [x] HR-01.3 Durable prompt admission/input inbox MVP.
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
  - status: completed 2026-07-01.
  - evidence: added per-session `inputs.jsonl`, `/v1/sessions/{id}/inputs`,
    idempotent/conflict-checked `input_id` admission, `/run` admission wrapper,
    pre-`Session.Run` promotion records, terminal completion records, gateway
    client admission helper, and restart-time ambiguous marking for promoted
    inputs without completion.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/gateway
    ./internal/gatewayclient ./internal/session` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Admission.*|Test.*InputInbox.*|Test.*Idempotent.*Prompt.*|Test.*Promot.*|Test.*Ambiguous.*'
    -count=1 ./internal/...` passed.
  - commit: pending.

- [x] HR-01.4 Telegram durable admission and offset safety.
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
  - status: completed 2026-07-01.
  - evidence: Telegram prompt updates now resolve/create their gateway session,
    admit deterministic `telegram-update-<id>` inputs through the gateway
    input inbox, append local admission/ignore JSONL evidence, and only then
    advance the Telegram offset. Duplicate promoted/completed/ambiguous inputs
    are acknowledged without rerunning, pending admitted inputs are visible in
    Telegram state/status, and admission failures leave the offset unchanged
    for retry.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/telegrambot
    ./internal/gatewayclient` passed; `/root/.local/go/bin/go test -run
    'Test.*Telegram.*(Admission|Offset|Duplicate|Interrupt|Superseded).*'
    -count=1 ./internal/telegrambot` passed.
  - commit: pending.

- [x] HR-01.5 Slow-client run/event decoupling.
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
  - status: completed 2026-07-01.
  - evidence: gateway `/run` streams now decouple active execution from the
    HTTP response writer with a bounded live channel, emit
    `gateway.stream_gap` hints when live progress drops under backpressure, and
    keep `/events?after_seq=...` as the durable replay recovery path. The shared
    gateway client records stream-gap counts, and TUI gateway mode replays after
    sequence-gap errors before fetching final messages.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/gateway
    ./internal/gatewayclient ./internal/tui ./internal/eventlog` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Slow.*Client.*|Test.*Backpressure.*|Test.*Replay.*Gap.*|Test.*Run.*Completes.*|Test.*Stream.*(Gap|Block).*'
    -count=1 ./internal/...` passed.
  - commit: pending.

- [x] HR-01.6 Batched TUI event apply and reflow.
  - maps to: `competitive-improvements-todo.md` B4.
  - target files: `internal/tui/tui.go`,
    `internal/tui/gateway_session.go`,
    `internal/tui/transcript/projector.go`,
    `internal/tui/tui_test.go`.
  - acceptance: event batches apply on a short tick; terminal/tool-boundary
    events flush immediately; 1000 deltas produce the same final transcript
    with far fewer reflows.
  - verification: `go test -count=1 ./internal/tui ./internal/tui/transcript`.
  - status: completed 2026-07-01.
  - evidence: TUI stream events now queue assistant/model deltas behind a short
    batch tick, flush immediately for terminal/tool-boundary events, and flush
    any pending batch before run completion/error handling. A 1000-delta
    regression proves the final assistant transcript matches direct apply while
    reflow happens once after the batch tick.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tui
    ./internal/tui/transcript` passed; `/root/.local/go/bin/go test -run
    'Test.*TUI.*(Batch|Reflow|Seq|Dedupe).*' -count=1 ./internal/tui
    ./internal/tui/transcript` passed.
  - commit: pending.

- [x] HR-01.7 Hard inline budgets for web and large tool outputs.
  - maps to: `competitive-improvements-todo.md` P0-3.
  - target files: `internal/tools/web_core.go`,
    `internal/tools/web_handlers.go`, `internal/tools/tools.go`,
    `internal/agent/*`, `internal/clientux/context.go`.
  - acceptance: web fetch/extract/crawl inline output cannot bypass configured
    budget; full text goes to `output_ref`; context diagnostics identify large
    inline sources.
  - verification: `go test -count=1 ./internal/tools ./internal/agent ./internal/clientux`.
  - status: completed 2026-07-01.
  - evidence: agent tool-result compaction now enforces
    `MaxToolOutputBytes` even when a handler pre-marks the result as truncated,
    and the returned preview plus truncation note fits inside the configured
    inline byte budget. Web crawl total inline text now clamps to the same hard
    inline token cap as fetch/extract, extreme `max_tokens`/`max_total_tokens`
    requests stay bounded, and web full extracted text remains available through
    `output_ref`. Context diagnostics now flag large inline tool contributors
    and count output-ref-backed sources.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tools ./internal/agent
    ./internal/clientux ./internal/config ./internal/gatewayclient` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Web.*Budget.*|Test.*OutputRef.*|Test.*Context.*Tool.*|Test.*Large.*Tool.*|Test.*Inline.*Budget.*|Test.*Truncated.*Tool.*|TestContextStatusClassifiesSourcesAndThresholds'
    -count=1 ./internal/tools ./internal/agent ./internal/clientux
    ./internal/gatewayclient` passed.
  - commit: pending.

- [x] HR-01.8 Turn-level tool snapshot and transcript pairing.
  - maps to: `competitive-improvements-todo.md` P0-4.
  - target files: `internal/tools/*`, `internal/agent/runtime_loop.go`,
    `internal/agent/model_call.go`, `internal/agent/tool_attempt.go`,
    `internal/agent/transcript_pairing.go`, `internal/runstate/runstate.go`.
  - acceptance: model-visible specs, handlers, metadata, and MCP catalog are
    frozen per provider turn; malformed tool-result pairing is rejected before
    provider calls.
  - verification: `go test -count=1 ./internal/agent ./internal/tools ./internal/mcpclient`.
  - status: completed 2026-07-01.
  - evidence: added a per-provider-turn `tools.ToolSet` snapshot that clones
    model-visible specs, handler maps, parallel metadata, policy/risk lookup,
    and the dynamic MCP catalog/status mirror; the agent now uses the snapshot
    for model requests, tool execution, permission decisions, audits,
    parallel batching, and rate buckets. Snapshot MCP gateway handlers are
    rebound to the frozen mirror, so live MCP catalog changes do not affect
    `tool_search`, `mcp_list_tools`, or `mcp_call` inside an active provider
    turn. Added pre-provider transcript pairing validation that rejects
    orphan, duplicate, or missing tool results before `model.call_started`.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/agent ./internal/tools
    ./internal/mcpclient ./internal/runstate` passed; `/root/.local/go/bin/go
    test -run
    'Test.*ToolSnapshot.*|Test.*TranscriptPairing.*|Test.*ToolResult.*'
    -count=1 ./internal/agent ./internal/tools` passed.
  - commit: pending.

- [x] HR-01.9 Canonical end-to-end event trace.
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
  - status: completed 2026-07-01.
  - evidence: added a canonical JSONL trace fixture plus shared testkit loader
    covering assistant content/reasoning deltas, provider usage, context
    threshold and compaction, a parallel web/MCP tool batch, web output-ref
    metadata, tool-summary usage, terminal turn completion, and terminal run
    completion. Trace replay, the shared client projector, TUI projection, and
    Telegram rendering now all consume the same fixture and assert consistent
    counts, usage, context, tools, assistant text, and completion state.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/testkit
    ./internal/trace ./internal/clientux/projector ./internal/tui
    ./internal/telegrambot` passed; `/root/.local/go/bin/go test -run
    'Test.*Golden.*Trace.*' -count=1 ./internal/...` passed.
  - commit: pending.

- [x] HR-01.10 Bounded file reads with line windows.
  - maps to: `competitive-improvements-todo.md` A1.
  - target files: `internal/tools/tools.go`, `internal/tools/fs_read.go`,
    `internal/tools/tools_test.go`, `internal/toolrender/toolrender.go`.
  - acceptance: legacy `{"path":...}` stays compatible; optional
    `offset/limit` returns numbered lines, truncation metadata, `next_offset`,
    `total_lines`, and safe binary/symlink/sensitive-path behavior.
  - verification: `go test -count=1 ./internal/tools ./internal/toolrender`.
  - status: completed 2026-07-01.
  - evidence: moved `fs_read_file` into a focused `fs_read.go` handler and kept
    bare `{"path":...}` on the legacy full-file path. Supplying `offset` or
    `limit` now enables a bounded 1-indexed line window with numbered lines,
    clamped limits, UTF-8/NUL binary rejection, long-line truncation, visible
    `next_offset`, and metadata for total lines, line bounds, truncation, and
    skipped long lines. Existing sensitive-path and symlink escape checks are
    reused before either full or windowed reads, and TUI/Telegram tool call
    lines summarize requested line windows.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tools
    ./internal/toolrender` passed; `/root/.local/go/bin/go test -run
    'Test.*FSRead.*(Offset|Limit|Line|Sensitive|Symlink|Legacy)' -count=1
    ./internal/tools` passed.
  - commit: pending.

Milestone 1 final verification:

- [x] Run focused reliability suite:
  - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/session ./internal/telegrambot ./internal/tui ./internal/eventlog ./internal/tools ./internal/agent ./internal/clientux/projector`
  - status: completed 2026-07-01.
  - evidence: `/root/.local/go/bin/go test -count=1 ./internal/gateway
    ./internal/gatewayclient ./internal/session ./internal/telegrambot
    ./internal/tui ./internal/eventlog ./internal/tools ./internal/agent
    ./internal/clientux/projector` passed on pushed `HEAD`
    `25380f31e7a00ceb840b5fa2495b539426f54274`.
- [x] Run targeted regex suite:
  - `go test -run 'Test.*Replay.*|Test.*Seq.*|Test.*Interrupt.*|Test.*Admission.*|Test.*InputInbox.*|Test.*Telegram.*(Admission|Offset).*|Test.*Slow.*Client.*|Test.*Backpressure.*|Test.*TUI.*(Batch|Reflow).*|Test.*FSRead.*|Test.*ToolSnapshot.*|Test.*TranscriptPairing.*|Test.*Golden.*Trace.*' -count=1 ./internal/...`
  - status: completed 2026-07-01.
  - evidence: first run failed in `internal/bench` with
    `TestLocalLoopBenchmarkGeneratesReplayableFiftyTurnSuite: duplicate
    terminal run event for "20260701T135222Z": got run.completed after
    run.completed`; fixed by pushed commit
    `25380f31e7a00ceb840b5fa2495b539426f54274`, which preserves nested agent
    run ids in aggregate trace records. Rerun of the exact command passed.
- [x] Commit and push a milestone summary update after all HR-01 tasks are
  complete.
  - status: completed 2026-07-01.
  - evidence: this summary update records the completed Milestone 1
    implementation and verification state.
  - commit: pending.

## Milestone 2 - Reversibility And Display Contracts

Goal: add rollback and compact display invariants before expanding edit power.

- [x] HR-02.1 Provider binding denylist and auth-source discipline.
  - maps to: `competitive-improvements-todo.md` P0-6.
  - acceptance: project config cannot redirect secrets or mix Codex/DeepSeek
    credential paths.
  - verification: `go test -count=1 ./internal/config ./internal/provider ./internal/credentials ./internal/codexauth ./internal/modelinfo`.
  - status: completed 2026-07-01.
  - evidence: project `.billyharness/config.toml` now ignores provider/auth
    authority keys for DeepSeek and Codex routing, including base URLs,
    API-key env names, credential files, Codex auth files, refresh/auth URLs,
    client ids, and originator metadata, while still allowing non-sensitive
    runtime preferences. Environment, CLI, and gateway overrides remain trusted
    sources for those keys. Provider/model conflicts now resolve through the
    existing model-routing rule with an explicit warning, and provider
    construction tests prove DeepSeek and Codex credential paths stay isolated.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Denylist.*|Test.*ProviderBinding.*|Test.*Credentials.*' -count=1
    ./internal/config ./internal/provider ./internal/credentials` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/config
    ./internal/provider ./internal/credentials ./internal/codexauth
    ./internal/modelinfo` passed.
  - commit: pending.

- [x] HR-02.2 Turn-scoped checkpoints, diff, preview, and undo.
  - maps to: `competitive-improvements-todo.md` A2.
  - acceptance: no hidden commits/stash/reset/user `.git` writes; mutating file
    and shell steps produce turn-change events; preview writes nothing; undo is
    conflict-safe and denied during active runs.
  - verification: `go test -count=1 ./internal/checkpoint ./internal/agent ./internal/eventlog ./internal/gateway ./internal/tui ./internal/telegrambot`.
  - status: completed 2026-07-01.
  - evidence: added a leaf `internal/checkpoint` package that snapshots
    workspace files/directories before and after mutating `fs_write_file`,
    `fs_make_dir`, and `shell_exec` tool attempts, stores capped reversible
    patch records in Billy output-ref storage, and never writes user `.git`
    state. Agent tool orchestration now emits durable
    `turn.change_recorded` events with compact file/stat metadata and a patch
    output ref when a mutating step changes the workspace. Gateway session
    `/undo` supports preview-only and restore modes, replays JSONL to find the
    latest or named change, denies undo during active runs, records
    `turn.change_reverted`, and performs a full conflict precheck before any
    restore write.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/checkpoint
    ./internal/agent ./internal/eventlog ./internal/gateway ./internal/tui
    ./internal/telegrambot` passed; `/root/.local/go/bin/go test -run
    'Test.*Checkpoint.*|Test.*Turn.*Diff.*|Test.*Rollback.*|Test.*Undo.*|Test.*Preview.*|Test.*Conflict.*|Test.*Shell.*Changed.*'
    -count=1 ./internal/...` passed; `/root/.local/go/bin/go test -count=1
    ./internal/architecture` passed.
  - commit: pending.

- [x] HR-02.3 Minimal turn diff display and revert preview UX.
  - maps to: `competitive-improvements-todo.md` B11.
  - acceptance: transcript shows compact file/stat summary; full patch is
    available by output ref/API/command; Telegram/TUI can request preview.
  - verification: `go test -count=1 ./internal/clientux/projector ./internal/tui ./internal/telegrambot ./internal/toolrender ./internal/checkpoint`.
  - status: completed 2026-07-01.
  - evidence: added shared turn-change rendering for compact file/stat
    summaries, including additions/deletions, binary/large markers, shell
    change hints, and patch output refs. The client projector now reconstructs
    turn-change state from durable `turn.change_recorded` and
    `turn.change_reverted` events, TUI transcript replay shows `CHANGES` and
    `REVERTED` cells, Telegram progress shows the same compact summaries, and
    TUI/Telegram `/diff [change_id]` request gateway preview-only undo output
    without restoring files.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/clientux/projector
    ./internal/tui ./internal/telegrambot ./internal/toolrender
    ./internal/checkpoint` passed; `/root/.local/go/bin/go test -run
    'Test.*Turn.*Diff.*Display.*|Test.*Revert.*Preview.*|Test.*Patch.*OutputRef.*'
    -count=1 ./internal/...` passed; `/root/.local/go/bin/go test -count=1
    ./internal/architecture ./internal/tui/transcript` passed.
  - commit: pending.

- [x] HR-02.4 Shared ToolCompact display contract.
  - maps to: `competitive-improvements-todo.md` P1-7.
  - acceptance: TUI and Telegram render one bounded compact tool state with no
    raw JSON fallback.
  - verification: `go test -count=1 ./internal/toolrender ./internal/tooloutput ./internal/agent ./internal/clientux/projector ./internal/telegrambot ./internal/tui/transcript`.
  - status: completed 2026-07-01.
  - evidence: added an optional protocol `ToolCompact` display contract on tool
    result, progress, and output-ref events; agent tool orchestration now
    populates compact identity, lifecycle, summary, output-ref, metric, error,
    truncation, and hint fields without importing presentation packages. Shared
    `toolrender` helpers render compact result/progress/output-ref/permission
    lines, unknown Telegram tool calls no longer fall back to raw JSON
    arguments, the client projector stores one compact tool state by call id,
    and TUI/Telegram tests prove compact display is used without leaking raw
    payloads.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/toolrender
    ./internal/tooloutput ./internal/agent ./internal/clientux/projector
    ./internal/telegrambot ./internal/tui/transcript` passed;
    `/root/.local/go/bin/go test -run
    'Test.*ToolCompact.*|Test.*NoRaw.*JSON.*|Test.*OutputRef.*' -count=1
    ./internal/...` passed; `/root/.local/go/bin/go test -count=1
    ./internal/protocol ./internal/architecture` passed.
  - commit: pending.

- [x] HR-02.5 MCP catalog change lifecycle.
  - maps to: `competitive-improvements-todo.md` P1-8.
  - acceptance: manager owns raw catalog; relist atomically swaps tools;
    crashed/closed servers remove active specs; stale tools fail as unknown.
  - verification: `go test -count=1 ./internal/mcpclient ./internal/tools ./internal/config`.
  - status: completed 2026-07-01.
  - evidence: manager-owned MCP catalog snapshots already drove registry
    mirrors, reconnect refreshes, and `tools/list_changed` relists. This slice
    closes the stale-spec lifecycle gap: managed servers now clear their active
    specs and publish catalog changes when a client crashes, reconnect fails,
    restart begins, startup closes, or the manager closes. Registry mirrors
    receive the same catalog change and stale `mcp_call` requests now fail
    validation as unknown instead of reaching a dead or removed server tool.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*MCP.*ListChanged.*|Test.*MCP.*Reconnect.*|Test.*Stale.*Tool.*'
    -count=1 ./internal/mcpclient ./internal/tools` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/mcpclient
    ./internal/tools ./internal/config` passed.
  - commit: pending.

## Milestone 3 - Core Coding Tools

Goal: make Billy useful as a coding agent without full-file rewrites or shell
abuse.

- [x] HR-03.1 Agent work plan state.
  - maps to: `competitive-improvements-todo.md` A3.
  - acceptance: `todo_write` has bounded todos, valid statuses, one
    `in_progress`, deterministic compact rendering, and replayable state.
  - verification: `go test -count=1 ./internal/tools ./internal/clientux/projector ./internal/toolrender ./internal/tui/transcript ./internal/telegrambot`.
  - status: completed 2026-07-01.
  - evidence: added native `todo_write` with bounded full-list replacement,
    schema-enforced statuses/priorities, handler-level duplicate and
    single-`in_progress` validation, deterministic plan summaries, and
    `protocol.TodoState` metadata for replay. The shared client projector now
    reconstructs todo state from durable tool-result metadata and preserves it
    across run starts; shared tool rendering, TUI transcript projection, and
    Telegram rendering show compact plan counts/current work without raw todo
    JSON arguments.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tools
    ./internal/clientux/projector ./internal/toolrender
    ./internal/tui/transcript ./internal/telegrambot` passed;
    `/root/.local/go/bin/go test -run 'Test.*Todo.*|Test.*PlanState.*'
    -count=1 ./internal/...` passed; `/root/.local/go/bin/go test -count=1
    ./internal/protocol ./internal/agent ./internal/architecture` passed.
  - commit: pending.

- [x] HR-03.2 Native grep and glob tools.
  - maps to: `competitive-improvements-todo.md` A5.
  - acceptance: bounded regex/glob search with context, limits, binary skip,
    sensitive-path skip, deterministic truncation, and compact rendering.
  - verification: `go test -count=1 ./internal/tools ./internal/toolrender ./internal/tui/transcript`.
  - status: completed 2026-07-01.
  - evidence: added native read-only `fs_grep` and `fs_glob` tools with
    workspace/symlink safety, sensitive-path skips, bounded regex search,
    include globs, `content`/`files_with_matches`/`count` output modes,
    context lines, offsets, limits, binary and large-file skips, recursive glob
    matching, file/dir/both filters, name/modified sorting, deterministic
    truncation markers, metadata summaries, read-only parallel metadata, and
    compact TUI/Telegram call rendering without raw JSON argument fallback.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'TestFS(Grep|Glob).*|TestFSSearchToolsParallelMetadata' -count=1
    ./internal/tools` passed; `/root/.local/go/bin/go test -run
    'Test.*ToolRender.*(Grep|Glob)' -count=1 ./internal/toolrender
    ./internal/tui/transcript` passed; `/root/.local/go/bin/go test -count=1
    ./internal/tools ./internal/toolrender ./internal/tui/transcript` passed.
  - commit: pending.

- [x] HR-03.3 Fuzzy file resolver and TUI `@file`.
  - maps to: `competitive-improvements-todo.md` A6.
  - acceptance: ranked file search uses git/rg/walk fallbacks; TUI inserts exact
    paths without auto-injecting file content.
  - verification: `go test -count=1 ./internal/filesearch ./internal/tools ./internal/tui`.
  - status: completed 2026-07-01.
  - evidence: added `internal/filesearch` as an on-demand fuzzy relative-path
    resolver with workspace-root safety, sensitive-path skips, lightweight
    TTL/signature caching, exact-basename-before-path ranking, pagination, and
    git `ls-files` / `rg --files` / `WalkDir` fallbacks. Added native
    read-only `fs_find_files` with ranked path/type/score/source output,
    truncation metadata, compact display metadata, and parallel-safe metadata.
    TUI normal prompt input now opens an `@` file popup outside slash mode,
    ignores stale async search results, inserts only exact relative paths on
    `Tab`/`Enter`, and never reads or injects file content.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/filesearch
    ./internal/tools ./internal/tui` passed; `/root/.local/go/bin/go test -run
    'Test.*File(Search|Resolver|FindFiles).*|TestTUI.*(AtFile|FileMention|Popup)'
    -count=1 ./internal/filesearch ./internal/tools ./internal/tui` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/toolrender
    ./internal/tui/transcript ./internal/architecture` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/filesearch
    ./internal/tools ./internal/tui ./internal/toolrender
    ./internal/tui/transcript ./internal/architecture ./internal/telegrambot`
    passed.
  - commit: pending.

- [x] HR-03.4 Run access modes and plan mode.
  - maps to: `competitive-improvements-todo.md` A7.
  - acceptance: `build|guarded|plan`; plan mode filters visible tools and
    hard-denies write/execute/external tools.
  - verification: `go test -count=1 ./internal/config ./internal/agent ./internal/tools ./internal/gateway ./internal/tui ./internal/telegrambot`.
  - status: completed 2026-07-01.
  - evidence: added normalized `access_mode=build|guarded|plan` config,
    runtime override, gateway request/status, run snapshot, and model-call
    event metadata. Agent tool snapshots now apply the per-run tool policy, so
    plan-mode provider calls advertise only read/search tools while execution
    still hard-denies write, shell/execute, and external MCP calls even when
    dangerous auto-approval is enabled. Guarded mode denies write/execute tools
    while leaving normal tool visibility intact. TUI/CLI/Telegram can start
    plan-mode runs through `-access-mode`, TUI `/mode`, Telegram `/mode`, and
    gateway JSON `access_mode`; docs now clarify that `todo_write` remains a
    progress tracker, not access-policy escalation.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/config ./internal/agent
    ./internal/tools ./internal/gateway ./internal/tui ./internal/telegrambot`
    passed; `/root/.local/go/bin/go test -run
    'Test.*AccessMode.*|Test.*PlanMode.*|Test.*ReadOnly.*|Test.*Dangerous.*Denied.*'
    -count=1 ./internal/...` passed; `/root/.local/go/bin/go test -count=1
    ./internal/config ./internal/tools ./internal/agent ./internal/gateway
    ./internal/tui ./internal/telegrambot ./cmd/fast-agent-harness` passed.
  - commit: pending.

- [x] HR-03.5 Exact file edit tool.
  - maps to: `competitive-improvements-todo.md` A4.
  - dependency: HR-02.2.
  - acceptance: exact-only `fs_edit_file`; no fuzzy match; atomic multi-edit;
    optional `expected_sha256`; no full file result.
  - verification: `go test -count=1 ./internal/tools ./internal/agent ./internal/toolrender`.
  - status: completed 2026-07-01.
  - evidence: added native write-risk `fs_edit_file` with exact string
    replacements only, bounded sequential multi-edit validation, optional
    `expected_sha256`, UTF-8/binary and workspace safety checks, and a single
    temp-file rename after every edit verifies in memory. Ambiguous matches
    fail unless `replace_all=true`, missing/no-op/hash-mismatch failures leave
    the file unchanged, result output returns only summary and before/after
    hashes, and checkpoint tracking records edit turn diffs for undo/preview.
    Plan/guarded/dangerous policy treats the edit tool like other write tools,
    and TUI/Telegram compact call rendering avoids raw edit JSON.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tools ./internal/agent
    ./internal/toolrender` passed; `/root/.local/go/bin/go test -count=1
    ./internal/tools ./internal/agent ./internal/toolrender
    ./internal/checkpoint` passed;
    `/root/.local/go/bin/go test -run
    'Test.*FSEdit.*|Test.*Exact.*Replacement.*|Test.*DangerousTools.*|Test.*ParallelMetadata.*|Test.*Turn.*Diff.*'
    -count=1 ./internal/tools ./internal/agent ./internal/toolrender
    ./internal/checkpoint` passed.
  - commit: pending.

- [-] HR-03.6 Structured patch tool.
  - maps to: `competitive-improvements-todo.md` A13.
  - dependency: HR-02.2 and HR-03.5.
  - acceptance: clean-room parser; all hunks verify before write; invalid patch
    never mutates files.
  - verification: `go test -count=1 ./internal/tools`.
  - status: deferred 2026-07-01.
  - blocker: A13 is marked P2 in `competitive-improvements-todo.md`, and the
    active execution goal says to defer P2 unless explicitly unblocked and
    justified by tests or benchmarks. HR-03.5 now provides exact atomic edits,
    so there is no failing test or benchmark that requires a structured patch
    parser ahead of the remaining P1 Milestone 3 items.
  - next action: revisit after HR-03.7 through HR-03.10, or earlier only if a
    focused test/benchmark demonstrates exact edits are insufficient for a
    required workflow.
  - commit: pending.

- [x] HR-03.7 Managed shell runtime for dev servers.
  - maps to: `competitive-improvements-todo.md` A8.
  - dependency: HR-02.2.
  - acceptance: background process ids are Billy-owned; output is bounded and
    cursor/ref based; kill is scoped; registry cleanup prevents leaks.
  - verification: `go test -count=1 ./internal/tools ./internal/tooloutput ./internal/agent`.
  - status: completed 2026-07-01.
  - evidence: `shell_exec` now supports `background=true` for Billy-owned
    in-memory process ids, with registry-scoped max-live-process checks,
    process-group termination, and `Registry.Close()` cleanup. Added
    `shell_output` for bounded cursor/tail reads with output-ref metadata and
    `shell_kill` for scoped termination by opaque process id; all shell tools
    remain execute-risk and unavailable in plan/guarded access modes. Compact
    TUI/Telegram call rendering covers background start, output polling, and
    kill without raw JSON, and setup docs show the managed-shell JSON flow.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tools
    ./internal/tooloutput ./internal/agent` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Shell.*(Background|Output|Kill|ProcessGroup|OutputRef|RegistryClose|MaxLive).*|TestRunMessagesShellExecBackground.*'
    -count=1 ./internal/tools ./internal/agent` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/toolrender` passed.
  - commit: pending.

- [x] HR-03.8 Command-based diagnostics feedback.
  - maps to: `competitive-improvements-todo.md` A9.
  - acceptance: configured commands produce structured bounded diagnostics with
    raw output refs and no full LSP platform.
  - verification: `go test -count=1 ./internal/diagnostics ./internal/tools ./internal/agent ./internal/toolrender ./internal/tui/transcript ./internal/telegrambot ./internal/config`.
  - status: completed 2026-07-01.
  - evidence: added a command-based `diagnostics_run` tool backed by
    diagnostics settings and an optional `diagnostics.config.toml`; the tool
    only runs named configured/default commands, captures combined stdout/stderr
    into a bounded buffer, stores raw output with `output_ref`, parses common
    `file:line:col` and `file(line,col)` diagnostics with raw fallback, caps
    issues globally and per file, sorts errors before warnings, and returns a
    compact `<diagnostics>` block plus bounded metadata. The implementation is
    local command feedback only; no LSP, watcher, scheduler, auto-install, or
    provider call was added.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/diagnostics
    ./internal/tools ./internal/agent ./internal/toolrender
    ./internal/tui/transcript ./internal/telegrambot ./internal/config`
    passed; `/root/.local/go/bin/go test -count=1 ./internal/tui
    ./internal/gateway ./internal/architecture` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Diagnostics.*|Test.*Compiler.*|Test.*OutputRef.*' -count=1
    ./internal/...` passed.
  - commit: pending.

- [x] HR-03.9 Local custom slash prompt commands.
  - maps to: `competitive-improvements-todo.md` A10.
  - acceptance: local markdown commands load from Billy home and workspace;
    built-ins cannot be shadowed; placeholders expand deterministically.
  - verification: `go test -count=1 ./internal/tui ./internal/promptcommands`.
  - status: completed 2026-07-01.
  - evidence: added `internal/promptcommands` for local Markdown prompt-command
    loading from `$BILLYHARNESS_HOME/commands/*.md` and
    `<workspace>/.billyharness/commands/*.md`, with simple frontmatter
    (`description`, `argument_hint`), filename-derived command names,
    deterministic `$ARGUMENTS` and `$1`..`$9` expansion, expansion byte caps,
    and built-in/alias shadow protection. TUI now appends custom commands to the
    slash popup and model-aware `/help`, and custom command invocation expands
    back into the normal prompt send path so busy/gateway checks still apply.
    Custom prompt command runs carry durable input metadata for the original
    slash command, command source/scope, expanded prompt byte length, and
    expanded prompt SHA-256 through gateway `/run` admission. No shell
    interpolation, marketplace, model override, or allowed-tools frontmatter
    was added.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tui
    ./internal/promptcommands ./internal/gateway ./internal/gatewayapi
    ./internal/gatewayclient` passed; `/root/.local/go/bin/go test -count=1
    ./internal/promptcommands ./internal/tui ./internal/gateway
    ./internal/gatewayapi ./internal/gatewayclient ./internal/architecture`
    passed; `/root/.local/go/bin/go test -run
    'Test.*Custom.*Slash.*|Test.*CommandTemplate.*|Test.*Slash.*Popup.*|Test.*PromptCommand.*|Test.*SessionInputRequestFromRun.*'
    -count=1 ./internal/...` passed.
  - commit: pending.

- [x] HR-03.10 `user_prompt_submit` hook.
  - maps to: `competitive-improvements-todo.md` A11.
  - acceptance: hook runs before model call; can block or add bounded context;
    blocked prompts do not reach provider history.
  - verification: `go test -count=1 ./internal/config ./internal/hooks ./internal/agent ./internal/session ./internal/gateway`.
  - status: completed 2026-07-01.
  - evidence: added `user_prompt_submit` as a valid local hook event that runs
    after `session_start` and before the first model call. Hook payloads include
    prompt, cwd, model/profile, submission/run ids, source, access mode, durable
    run metadata, and slash-command metadata when present. JSON stdout can
    block with a bounded reason, add bounded model-visible context, or replace
    the submitted prompt with a bounded `updated_prompt`; non-JSON stdout is
    treated only as bounded additional context. Blocked prompts return an
    explicit prompt-blocked error, skip provider calls and turn/model events,
    and are discarded from session history even when the pre-run transcript is
    empty. Hook events record decision, block reason, context length, updated
    prompt length/hash, and cap values. Gateway and TUI local runs now pass
    source and prompt-command metadata through the hook path.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/config ./internal/hooks
    ./internal/agent ./internal/session ./internal/gateway` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/config ./internal/hooks
    ./internal/agent ./internal/session ./internal/gateway
    ./internal/tui/runtimeclient` passed; `/root/.local/go/bin/go test
    -count=1 ./internal/config ./internal/hooks ./internal/agent
    ./internal/session ./internal/gateway ./internal/gatewayapi
    ./internal/gatewayclient ./internal/tui ./internal/tui/runtimeclient`
    passed; `/root/.local/go/bin/go test -run
    'Test.*UserPromptSubmit.*|Test.*PromptHook.*|Test.*Hook.*Block.*|Test.*AdditionalContext.*'
    -count=1 ./internal/...` passed.
  - commit: pending.

## Milestone 4 - Long-Run Productivity

Goal: make many sessions and projects manageable without turning Billy into a
platform.

- [x] HR-04.1 Rebuildable session search and diagnostics side index.
  - maps to: `competitive-improvements-todo.md` B5.
  - acceptance: rebuild emits text/tool/error/run/usage rows from JSONL; corrupt
    index never breaks session list/inspect; no mandatory SQLite/FTS/vector.
  - verification: `go test -count=1 ./internal/gateway ./internal/clientux/projector ./internal/trace`.
  - status: completed 2026-07-01.
  - evidence: added rebuild-only `diagnostics.json` session side index under the
    existing optional `index/` directory. Rebuild reads canonical session
    history/events JSONL, emits capped visible user/assistant text rows,
    tool rows, error rows, run rows, and cumulative-usage rows, and leaves
    session list/inspect paths independent of the side index even when the
    diagnostics index is corrupt. Usage aggregation now matches the shared
    projector semantics and trace replay also avoids double-counting cumulative
    provider usage updates. No SQLite, FTS, vector store, daemon, or live
    write path was added.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'TestStoredSessionDiagnosticsIndexUsageCumulativeMatchesProjector'
    -count=1 ./internal/gateway` passed; `/root/.local/go/bin/go test -run
    'TestReplayEventsAggregatesUsageCumulativeAndEventCounters' -count=1
    ./internal/trace` passed; `/root/.local/go/bin/go test -count=1
    ./internal/gateway ./internal/clientux/projector ./internal/trace` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Session.*Index.*|Test.*Index.*(Tool|Error|Usage|Search).*|Test.*Usage.*Cumulative.*'
    -count=1 ./internal/...` passed.
  - commit: pending.

- [x] HR-04.2 CLI session queries.
  - maps to: `competitive-improvements-todo.md` B6.
  - acceptance: `sessions search/tools/errors/usage/runs` support human and
    JSON output with limits.
  - verification: `go test -count=1 ./cmd/fast-agent-harness ./internal/gateway`.
  - status: completed 2026-07-01.
  - evidence: added read-only `sessions search`, `sessions tools`,
    `sessions errors`, `sessions usage`, and `sessions runs` commands over the
    rebuildable diagnostics side index. Each command supports bounded human and
    JSON output, `-limit`, `-session` filtering, and focused filters where
    useful (`-name`, `-status`, `-query`). Missing or corrupt diagnostics index
    reads return explicit rebuild guidance, and `sessions index rebuild` now
    refreshes the diagnostics side index as a side effect while preserving the
    existing session-list index output contract.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'TestSessionsSearchToolsErrorsUsageRunsCommands' -count=1
    ./cmd/fast-agent-harness` passed; `/root/.local/go/bin/go test -count=1
    ./cmd/fast-agent-harness ./internal/gateway` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Sessions.*(Search|Tools|Errors|Usage|Runs).*' -count=1
    ./cmd/fast-agent-harness ./internal/gateway` passed.
  - commit: pending.

- [x] HR-04.3 Interactive `ask_user` MVP.
  - maps to: `competitive-improvements-todo.md` B7.
  - acceptance: one pending request per session; TUI/Telegram answers resume the
    same run; Telegram answers do not interrupt pending question runs.
  - verification: `go test -count=1 ./internal/protocol ./internal/agent ./internal/tools ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot`.
  - status: completed 2026-07-01.
  - evidence: added model-visible bounded `ask_user`, typed
    `user_input.requested`, `user_input.answered`, and
    `user_input.rejected` events with run/turn/tool correlation, gateway
    answer/reject endpoints, gatewayclient helpers, TUI pending-answer submit
    flow, and Telegram pending-question handling that answers the same
    session request before durable prompt admission or run interruption. Pending
    gateway/TUI/Telegram state clears on answer/reject/cancel/terminal paths,
    and restart-loaded Telegram pending requests expire by clearing on missing
    gateway sessions.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*AskUser.*|Test.*UserInput.*(Requested|Answered|Rejected).*|Test.*Telegram.*Pending.*Question.*'
    -count=1 ./internal/...` passed; `/root/.local/go/bin/go test -count=1
    ./internal/protocol ./internal/agent ./internal/tools ./internal/gateway
    ./internal/gatewayclient ./internal/tui ./internal/telegrambot` passed.
  - commit: pending.

- [x] HR-04.4 Minimal project context registry.
  - maps to: `competitive-improvements-todo.md` B8.
  - acceptance: bounded snapshot of cwd, roots, package manager, likely
    commands, instruction metadata, env names without values, and cap flags.
  - verification: `go test -count=1 ./internal/projectcontext ./internal/agent ./internal/clientux ./internal/config ./internal/instructions`.
  - status: completed 2026-07-01.
  - evidence: added `internal/projectcontext` for a bounded local snapshot with
    cwd, workspace roots, git root, package-manager hints, likely test/build
    commands, instruction source byte/hash metadata, `.env*` variable names
    without values, and explicit cap flags. The agent now injects the capped
    project-context fragment after profile/SOUL and before AGENTS/project
    instructions, compaction protects that prefix, and `/context` reports a
    separate `project_context` source bucket.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*ProjectContext.*|Test.*Env.*Hints.*|Test.*Instruction.*Metadata.*|Test.*Context.*Project.*'
    -count=1 ./internal/...` passed; `/root/.local/go/bin/go test -count=1
    ./internal/projectcontext ./internal/agent ./internal/clientux
    ./internal/config ./internal/instructions` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: pending.

- [x] HR-04.5 Context epoch reconcile.
  - maps to: `competitive-improvements-todo.md` B9.
  - acceptance: unchanged project context injects nothing; changed context
    injects one bounded update; restart reuses stored epoch.
  - verification: `go test -count=1 ./internal/runstate ./internal/session ./internal/gateway ./internal/agent ./internal/clientux`.
  - status: completed 2026-07-01.
  - evidence: project context reconciliation now hashes the rendered
    `<PROJECT_CONTEXT>` body already present in the transcript, preserves the
    current epoch when unchanged, and replaces it with one bounded
    `# Project context updated` fragment when metadata changes. Disabled or
    failed observation leaves the prior fragment untouched. Because the active
    epoch lives in persisted session messages, gateway restart/load reuses it
    without adding another update, while runstate turn metadata now hashes
    protected user instruction/context fragments so AGENTS and project-context
    changes are visible in `profile_instruction_hash`.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/projectcontext
    ./internal/agent ./internal/runstate ./internal/gateway` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/runstate
    ./internal/session ./internal/gateway ./internal/agent
    ./internal/clientux` passed; `/root/.local/go/bin/go test -run
    'Test.*ContextEpoch.*|Test.*ProjectContext.*|Test.*Instruction.*Hash.*'
    -count=1 ./internal/...` passed.
  - commit: pending.

- [x] HR-04.6 Delta coalescing before durable append and render.
  - maps to: `competitive-improvements-todo.md` B10.
  - acceptance: final assistant text is identical; first visible delta latency
    is bounded; replay order remains valid.
  - verification: `go test -count=1 ./internal/agent ./internal/gateway ./internal/clientux/projector ./internal/telegrambot`.
  - status: completed 2026-07-01.
  - evidence: provider content/reasoning streams now emit the first delta of
    each consecutive visible stream immediately, coalesce subsequent same-type
    deltas up to a bounded byte/time threshold, and flush before tool-call,
    usage, request-metadata, done, and channel-close boundaries. The protocol
    event types and string payload contract stay unchanged, so existing
    projectors and renderers concatenate replayed coalesced deltas into the
    same assistant/reasoning text. A 2000-chunk gateway session regression
    verifies far fewer persisted assistant delta events, monotonic replay seqs,
    completed replay projection, and exact final assistant text.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/agent
    ./internal/gateway ./internal/clientux/projector ./internal/telegrambot`
    passed; `/root/.local/go/bin/go test -run
    'Test.*Delta.*Coalesc.*|Test.*Assistant.*Final.*Text.*|Test.*Replay.*Coalesc.*|Test.*Coalesced.*|Test.*First.*Delta.*'
    -count=1 ./internal/...` passed. Note: the first package-suite attempt hit
    transient Telegram timing in
    `TestClientSerializesSameChatRateLimitReservations`
    (`request 1 arrived after 10.654818ms, want serialized pacing`); an
    immediate `/root/.local/go/bin/go test -count=1 ./internal/telegrambot`
    rerun and the full package-suite rerun both passed.
  - commit: pending.

- [x] HR-04.7 JSONL scale benchmark and sparse seq index gate.
  - maps to: `competitive-improvements-todo.md` B12.
  - acceptance: 10k/100k append and tail-replay metrics exist; sparse `.idx`
    only if measured full scan misses target.
  - verification: `go test -run '^$' -bench 'Benchmark(SessionJSONL|EventJSONL|ReplayAfterSeq)' -benchmem ./internal/gateway ./internal/eventlog`.
  - status: completed 2026-07-01.
  - evidence: added gateway session and raw eventlog JSONL benchmarks covering
    warmed 10k/100k appends, high-seq tail replay, output-ref-heavy records,
    and coalesced/uncoalesced assistant streams. Added `scripts/bench-smoke.sh`
    with stable `METRIC name value unit` output and documented the sparse
    seq-offset gate in `docs/benchmarks.md`: keep JSONL full-scan replay while
    the 100k tail replay benchmarks remain below 1s/op on repeated local smoke
    runs. No sparse `.idx` was added because measured full scans stayed below
    the gate.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/gateway
    ./internal/eventlog` passed; `./scripts/bench-smoke.sh` passed and emitted
    `METRIC` lines including `initial_events`, `log_events`, and
    `tail_events`; `/root/.local/go/bin/go test -run '^$' -bench
    'Benchmark(SessionJSONL|EventJSONL|ReplayAfterSeq)' -benchmem
    ./internal/gateway ./internal/eventlog` passed with
    `BenchmarkReplayAfterSeq/deltas_100000_tail100` at about 626ms/op and
    `BenchmarkEventJSONLReplay/deltas_100000_tail100` at about 642ms/op.
  - commit: pending.

- [-] HR-04.8 Cross-adapter slow-client stress harness.
  - maps to: `competitive-improvements-todo.md` B15.
  - acceptance: fake provider 10k chunks, blocked TUI channel, Telegram 429,
    replay after drop, and final state correctness are tested.
  - verification: `go test -count=1 ./internal/testkit ./internal/gateway ./internal/tui ./internal/telegrambot ./internal/provider`.
  - status: deferred 2026-07-01.
  - blocker: B15 is marked P2 in `competitive-improvements-todo.md`, and the
    active execution goal says to defer P2 unless explicitly unblocked and
    justified by tests or benchmarks. HR-04.7 added JSONL scale benchmarks and
    did not expose a replay/index failure that requires a broader
    cross-adapter stress harness before the remaining P1 work.
  - next action: revisit after the remaining unblocked P1 execution TODO items,
    or earlier only if a focused TUI/Telegram/provider regression or benchmark
    demonstrates that existing slow-client/backpressure coverage is
    insufficient.
  - verification evidence: source-priority check only; no code changed for this
    deferral.
  - commit: pending.

## Milestone 5 - Deferred Bounded Capabilities

Goal: implement only after the smaller contracts are stable and tests prove the
need.

- [x] HR-05.1 Redo state and destructive git command guardrails.
  - maps to: `competitive-improvements-todo.md` A12.
  - dependency: HR-02.2.
  - acceptance: one linear redo stack; destructive git shell commands are
    blocked or require explicit approval.
  - verification: `go test -count=1 ./internal/checkpoint ./internal/session ./internal/gateway ./internal/tui ./internal/telegrambot ./internal/tools ./internal/toolrender`.
  - status: completed 2026-07-01.
  - evidence: added direction-aware checkpoint redo that restores recorded
    `After` state only when the current workspace still matches the reverted
    `Before` state. Gateway undo now skips already-reverted changes, `/redo`
    restores the last undone patch, emits a redone `turn.change_recorded`
    event to clear the redo candidate, and session inspection exposes
    turn-change status/files/stats plus redo availability. TUI and Telegram now
    expose `/undo [change_id]` and `/redo` over the gateway, with shared
    turn-change status rendering. `shell_exec` blocks recognizable destructive
    git commands (`reset --hard`, `clean -f`, workspace `checkout`/`restore`,
    `stash drop|clear`, and force push) before foreground or background
    execution while allowing harmless `git status`.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Redo.*|Test.*Destructive.*Git.*|Test.*Git.*Guardrail.*' -count=1
    ./internal/...` passed; `/root/.local/go/bin/go test -count=1
    ./internal/checkpoint ./internal/session ./internal/gateway ./internal/tui
    ./internal/telegrambot ./internal/tools ./internal/toolrender
    ./internal/gatewayclient ./internal/clientux` passed. During verification,
    the package suite exposed a stale TUI profile-switch assertion that assumed
    project context was absent; the test was updated to assert no profile
    fragment is injected while allowing the existing project-context message.
  - commit: pending.

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
