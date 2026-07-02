# Solo Harness Next Hardening TODO

Date: 2026-07-02
Status: new source-of-truth roadmap after the consistency audit and
post-competitive cleanup pass.

This roadmap is for the next reliability/productivity layer of Billyharness.
It folds together the latest subagent reports, the known deployed smoke
blockers, and fresh upstream documentation checks. The goal is not to turn the
project into Codex, Claude Code, or OpenCode. The goal is to keep the solo
harness fast, inspectable, recoverable, and pleasant under real Telegram/TUI
use.

## Source Inputs

- `/root/billyharness/docs/post-competitive-hardening-todo.md`
- `/root/billyharness/docs/solo-harness-competitive-todo.md`
- `/root/billyharness/docs/consistency-audit-todo.md`
- `/root/billyharness/docs/architecture.md`
- `/root/billyharness/docs/memory-systems-research.md`
- Subagent reliability report: interrupt/replay/cancel/backpressure.
- Subagent context report: compaction, cache stability, memory, web summaries.
- Subagent Telegram report: rich-message streaming, throttling, finalization.
- Recovered Codex subagent rollout files from `/root/.codex/sessions/2026/07/02`:
  - completed: Bacon reliability
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-46-04-019f23b9-2841-7b21-beca-c55e27956982.jsonl`.
  - completed: Boole Telegram streaming
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-46-16-019f23b9-53d9-7b62-bf4a-9ba49d0af139.jsonl`.
  - completed: Sagan context/compaction/memory
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-46-25-019f23b9-7a71-7d53-b0ad-b5353415665b.jsonl`.
  - completed: Parfit MCP lifecycle/config
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-47-01-019f23b9-f9db-7833-b2b6-9c14447a3d6c.jsonl`.
  - completed: Turing solo safety/permissions
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-47-43-019f23ba-a8b4-7df1-a2ef-2b836186d6da.jsonl`.
  - completed: Hume observability/debug bundles
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-48-06-019f23ba-ea53-7051-972b-4df9a4e64ebd.jsonl`.
  - completed: Faraday benchmarks/regression
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-52-09-019f23be-b71a-7b43-8a16-964be77357ff.jsonl`.
  - incomplete: Averroes tools/edit/shell/contracts
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-46-35-019f23b9-9fb5-7ad1-9886-da7ae97942c2.jsonl`.
  - incomplete: Mill architecture/decomposition
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-52-23-019f23be-eff3-77c0-91c3-280095cf2be4.jsonl`.
  - incomplete: Erdos TUI/terminal UX
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-52-35-019f23bf-1f0b-7a82-86dc-427f02045389.jsonl`.
  - incomplete: Schrodinger web/search/extract
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-53-55-019f23c0-559b-7fe0-b873-4bd036c10d33.jsonl`.
  - incomplete: Carver solo product/roadmap
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-54-42-019f23c1-0c0e-71f0-8d73-7db47599e1cc.jsonl`.
- Upstream docs checked 2026-07-02:
  - OpenAI Codex non-interactive JSONL event stream:
    `https://developers.openai.com/codex/noninteractive`
  - Claude Code prompt caching:
    `https://code.claude.com/docs/en/prompt-caching`
  - Claude API compaction and cache breakpoints:
    `https://platform.claude.com/docs/en/build-with-claude/compaction`
  - OpenCode CLI/server/attach/run and agent model:
    `https://opencode.ai/docs/cli/`,
    `https://opencode.ai/docs/agents/`
  - OpenCode compaction source:
    `https://github.com/sst/opencode/blob/dev/packages/opencode/src/session/compaction.ts`
  - Telegram Bot API Rich Messages and Bot API:
    `https://core.telegram.org/bots/api`

## Solo Harness Filter

Accept only work that improves at least one of:

- run correctness under interruption, crashes, slow clients, or reconnects;
- context/cache/compaction predictability and token spend visibility;
- Telegram/TUI liveness without stale tool state or dead progress messages;
- replayability from JSONL as the durable source of truth;
- local solo ergonomics without adding platform bloat;
- architecture clarity already reflected in `docs/architecture.md`.

Reject by default:

- job schedulers, mandatory databases, hidden user-git state, cloud queues,
  team workflows, plugin stores, organization policy/RBAC, broad web/IDE
  platforms, React/Ink rewrites, copied competitor code, and telemetry.

## Current Known Blockers

- Deployed gateway interruption can fail when a replacement prompt arrives
  while the active run is inside a long cancellable shell tool. The first run
  cancels, but the replacement path may inherit a canceled context and fail
  before producing the new answer.
- Telegram live smoke still needs either an explicit safe chat/thread target or
  a fake Bot API harness. Current real-user testing has exposed stale progress,
  long silent periods, and duplicate-input/idempotency errors.
- Context accounting is now clearer than earlier raw input/output counters, but
  compaction, web-summary helper use, cache-break reasons, and context epochs
  still need stronger recovery and regression tests.

## Milestone 0 - Recovered Research Reconciliation (P0)

Goal: make sure the recovered Codex subagent rollout files are not lost, and
that incomplete agents are either rerun, mapped to concrete work, or explicitly
closed as no final report.

- [x] NH-00.1 Reconcile completed and incomplete subagent traces.
  - target files: this document, optionally the relevant roadmap sections.
  - acceptance: completed reports are mapped to existing NH tasks; incomplete
    traces are inspected through their last `agent_message` and either rerun
    or recorded as "no final report, no additional action".
  - evidence already recovered:
    - Averroes produced no final report; last state was switching from sparse
      web search to direct `curl` reads for tools/edit/shell/contracts.
    - Mill produced no final report; last state noted strict hygiene and
      architecture guard pass, but tracked Go files had grown to 315, so new
      pressure points should be reviewed before broad decomposition work.
    - Erdos produced no final report; last state noted existing TUI primitives
      such as Bubble Tea/Lip Gloss, `/toolview`, `/copy`, transcript export,
      command registry, and `internal/tui`, so future TUI work should avoid
      duplicating shipped behavior.
    - Schrodinger produced no final report; last state noted web-tool coverage
      is already good for DNS rebinding, redirects, auth mapping, retries,
      output refs, summary accounting, and cache invalidation, but missing
      product-level tests remain around provider query options,
      evidence/citation shape, markdown/readability quality, and multi-backend
      failover policy.
    - Carver produced no final report; last state said it was using the
      `openai-docs` skill for Codex-specific product/roadmap claims.
  - verification, 2026-07-02:
    - incomplete trace extraction was completed with `jq -s` and recorded in
      `docs/recovered-subagent-followup-todo.md` RS-00.1 through RS-00.3.
    - accepted findings were mapped to RS-01 through RS-04; Carver was closed
      with no additional action because no final product finding survived the
      trace beyond the existing solo harness filter.
  - follow-up closure, 2026-07-02:
    - reconciliation and mapping were committed in `0436175`.
    - compact recoverable tool-call schema errors were committed in
      `42c852a`; mutating tool display/replay contracts were committed in
      `6dc62f0`.
    - the fresh architecture pressure baseline and no-split decision were
      committed in `593289a`.
    - the TUI primitive audit and gateway Enter regression were committed in
      `83dc223`.
    - web/search/extract product tests, search options, readability fallback,
      and configured-backend-to-native search failover were committed in
      `3281198`.
    - the closeout pass split oversized focused tests back under strict
      hygiene limits and recorded final verification in
      `docs/recovered-subagent-followup-todo.md`.
    - no platform, marketplace, team, cloud, or broad framework features were
      accepted from the recovered traces.
  - commit: 5f9e9fa.
  - status: completed.

## Milestone 1 - Durable Run Termination And Replay Recovery (P0)

Goal: make every active run end in a deterministic state, and make every client
able to recover from durable JSONL replay after interruption, crash, reconnect,
or slow-consumer drops.

- [ ] NH-01.1 Fix interrupt replacement under long tools.
  - target areas: `internal/session`, `internal/gateway`, `internal/agent`,
    `internal/tools`, `internal/protocol`, deployed smoke scripts or tests.
  - acceptance: `interrupt_policy:"interrupt"` cancels the old run, drains the
    active tool/process, writes terminal events for the old run, and starts the
    replacement run with a fresh context that is not already canceled.
  - acceptance: no stale tool progress from the old run appears in the new
    run's projected Telegram/TUI state.
  - verification: fake long cancellable tool test; deployed-style gateway test
    where first prompt runs `sleep`, second prompt interrupts, and second
    assistant answer completes.
  - status: open.

- [ ] NH-01.2 Add crash-tail repair for incomplete JSONL runs.
  - target areas: `internal/eventlog`, `internal/session`,
    `internal/gateway/session_store*`, replay tests.
  - acceptance: on session load/replay, detect `run.started`, `step.started`,
    `tool.call_started`, or assistant draft events without terminal pairs and
    append synthetic repair events with `recovery:true`; never mutate existing
    JSONL records.
  - acceptance: repaired sessions replay as idle or failed, not forever
    running.
  - verification: corrupt/incomplete JSONL fixtures for run, step, tool, and
    assistant draft tails.
  - status: open.

- [ ] NH-01.3 Emit explicit stream-gap events for all live follow clients.
  - target areas: `internal/gateway` event hub, `/run` streaming,
    `/events?follow=true`, `internal/gatewayclient`, TUI/Telegram projectors.
  - acceptance: when a live stream drops events under backpressure, clients see
    a `gateway.stream_gap` hint with enough cursor data to replay from durable
    JSONL and converge.
  - acceptance: slow follow clients cannot block active execution.
  - verification: blocked writer/follow-client tests with replay convergence.
  - status: open.

- [ ] NH-01.4 Preserve partial assistant output across cancel/crash.
  - target areas: `internal/agent`, `internal/protocol`, `internal/eventlog`,
    projector tests.
  - acceptance: coalesced assistant deltas are persisted as draft segments and
    marked `partial:true` if the run is canceled or fails before finalization.
  - acceptance: Telegram/TUI can show the partial text as partial history
    without merging it into the next assistant answer.
  - verification: provider stream interrupted after text deltas; replay shows
    partial assistant block and new run starts cleanly.
  - status: open.

- [ ] NH-01.5 Define a terminal run/session state contract.
  - target areas: `internal/protocol`, `internal/clientux/projector`,
    `internal/gatewayapi`, TUI/Telegram status rendering.
  - acceptance: clients consume explicit states such as `running`,
    `interrupting`, `completed`, `failed`, `repairing`, and `waiting_for_user`
    instead of inferring from ad hoc event combinations.
  - verification: projector golden tests for normal run, interrupt, crash
    repair, and failed tool.
  - status: open.

## Milestone 2 - Context, Compaction, Cache, And Web Summary Safety (P0)

Goal: make context growth reversible and explainable, while keeping expensive
web/tool material outside the main loop unless the model explicitly asks for
bounded details.

- [ ] NH-02.1 Implement reversible compaction checkpoints.
  - target areas: `internal/agent`, `internal/runstate`,
    `internal/protocol`, `internal/eventlog`, `internal/session`.
  - acceptance: every compaction stores pre-compaction transcript refs,
    summary refs, context epoch, and token deltas; raw history remains
    recoverable.
  - acceptance: replay and `/context` can distinguish raw transcript,
    compacted transcript, summary, and current epoch.
  - verification: fake compaction test; replay full pre-compact transcript;
    undo/rewind or recovery path restores accounting.
  - status: open.

- [ ] NH-02.2 Add cache-stability preflight and diagnostics.
  - target areas: `internal/runstate`, `internal/modelinfo`,
    `internal/config`, `internal/agent/model_call.go`, `/context` formatter.
  - acceptance: before model, reasoning, tool schema, MCP catalog, profile, or
    prompt-section changes, compute whether the next call will likely bust the
    provider prefix cache and record the reason.
  - acceptance: status/context surfaces explain cache misses as stable prefix
    break, model switch, tool schema change, dynamic context change, or
    provider missing data.
  - verification: identical turns keep stable hashes; model/effort/tool change
    reports a cache break; deferred tool changes do not rewrite stable prefix.
  - status: open.

- [ ] NH-02.3 Move web/extract summarization to a non-blocking helper lane.
  - target areas: `internal/tools`, `internal/webtools`, `internal/provider`,
    `internal/tooloutput`, `internal/protocol`.
  - acceptance: `web_fetch`, `web_extract`, and `web_crawl` never inject large
    raw bodies into the main loop; helper summaries run with timeout/retry,
    strict token budgets, separate usage accounting, and deterministic fallback
    snippets when the helper fails.
  - acceptance: raw fetched data is stored in output refs, and the agent sees a
    bounded summary plus read/search instructions.
  - verification: helper timeout test, helper failure test, huge HTML/body
    test, per-tool `websum in->out` metrics, no context explosion.
  - status: open.

- [ ] NH-02.4 Reserve context budget before compaction becomes unrecoverable.
  - target areas: `internal/agent`, `internal/runstate`, `internal/context`
    projection if present, status/context formatting.
  - acceptance: keep explicit reserved budgets for the next assistant answer,
    next tool burst, compaction prompt, and compaction output; trim/refuse
    before reaching a provider hard limit.
  - verification: synthetic near-window session triggers preflight
    compaction/refusal, not provider overflow.
  - status: open.

- [ ] NH-02.5 Add compaction-time memory snapshot without automatic writes.
  - target areas: `internal/memory`, `internal/agent`, `internal/protocol`,
    `/memory` and `/context`.
  - acceptance: compaction can include reviewable task/touched-files/blockers
    snapshots and optional `/memory add`, but no background auto-memory writes.
  - verification: compaction snapshot fixture; memory remains user-approved.
  - status: open.

## Milestone 3 - Telegram And TUI Streaming Correctness (P0)

Goal: make Telegram and TUI projections feel alive without lying, duplicating
old tools, or getting rate-limited into silence.

- [ ] NH-03.1 Introduce run-id guarded render state for Telegram and TUI.
  - target areas: `internal/telegrambot`, `internal/tui`,
    `internal/clientux/projector`, `internal/protocol`.
  - acceptance: every progress edit, typing pulse, delayed retry, tool row, and
    final message is guarded by run id/input id; stale work from an older run
    cannot mutate the current progress message.
  - verification: two prompts in quick succession; old delayed edit ignored;
    current run finalizes once.
  - status: open.

- [ ] NH-03.2 Coalesce Telegram edits with meaningful-change throttling.
  - target areas: `internal/telegrambot` progress renderer/sender.
  - acceptance: do not edit solely for timer ticks. Edit on new tool state,
    sentence/paragraph boundary, meaningful text delta, final state, or a
    long-run heartbeat. Respect `429 retry_after` by pausing edits and keeping
    only the latest render state.
  - acceptance: final edit bypasses normal throttle but remains idempotent.
  - verification: fake Bot API with edit count assertions, `retry_after`,
    duplicate text suppression, transient network errors, and permanent
    `message not found/cannot edit` fallback.
  - status: open.

- [ ] NH-03.3 Split live progress and final rich-message rendering.
  - target areas: Telegram renderer, rich-message/entity builder, Markdown/HTML
    sanitizer, long-message splitter.
  - acceptance: live progress stays compact and robust; final answer gets full
    Telegram-supported rich formatting, LaTeX-safe code/math handling, and
    4096-char splitting without broken entities.
  - acceptance: tool progress remains inline/compact in final summaries, not
    expanded tables unless explicitly requested.
  - verification: golden messages for headings, bold/italic, code blocks,
    links, blockquotes, lists, tables fallback, LaTeX text, and split messages.
  - status: open.

- [ ] NH-03.4 Add typing lifecycle tied to real liveness.
  - target areas: Telegram polling/runner.
  - acceptance: send typing after a short delay when no visible output exists,
    refresh about every 4s while the run is alive, stop after first visible
    progress/final state, and preserve `message_thread_id`.
  - verification: fake Bot API chat-action lifecycle tests.
  - status: open.

- [ ] NH-03.5 Build cross-surface stale-tool regression tests.
  - target areas: `internal/clientux/projector`, `internal/telegrambot`,
    `internal/tui`.
  - acceptance: a new user input starts with an empty per-turn tool snapshot
    while cumulative chat metrics continue accumulating.
  - verification: replay fixture with old tool rows, new input, interrupt, and
    final answer; Telegram/TUI projections agree.
  - status: open.

## Milestone 4 - Observability, Recovery Bundles, And Stress Harnesses (P1)

Goal: when something feels wrong in production, collect the exact state needed
to reproduce it locally without reading random logs by hand.

- [ ] NH-04.1 Add canonical end-to-end event trace fixtures.
  - target areas: `internal/trace`, `internal/eventlog`, projectors.
  - acceptance: one fixture covers user input, assistant stream, tool call,
    tool result, web summary, compaction, interrupt, recovery, and final state.
  - verification: trace replay golden output for CLI/TUI/Telegram projections.
  - status: open.

- [ ] NH-04.2 Add slow-client and high-churn stress harnesses.
  - target areas: gateway streaming, gatewayclient, Telegram fake API, TUI
    reflow benchmarks if needed.
  - acceptance: fake provider emits many chunks/tools while clients block,
    reconnect, or receive `429`; execution remains bounded and replayable.
  - verification: stress tests with timeouts and no goroutine leaks.
  - status: open.

- [ ] NH-04.3 Add sanitized recovery bundle command.
  - target areas: `cmd/fast-agent-harness`, `internal/diagnostics`,
    `internal/secrets`, `internal/eventlog`.
  - acceptance: command exports redacted config, selected session JSONL,
    recent service logs, context report, MCP status, and trace summary into a
    local tar/zip for debugging.
  - verification: generated bundle has no secrets, contains expected files,
    and can replay the selected session.
  - status: open.

- [ ] NH-04.4 Bound oversized history/index/replay paths.
  - target areas: session store, gateway replay, TUI transcript load,
    Telegram session resume.
  - acceptance: huge JSONL sessions load with windows, diagnostics, and safe
    mode instead of unbounded memory/time.
  - verification: large synthetic session fixture; replay window tests.
  - status: open.

## Milestone 5 - Solo Productivity Without Platform Bloat (P1)

Goal: add only the small local conveniences that make a one-user harness faster
than generic coding agents.

- [ ] NH-05.1 Add retrieval-first context admission for repo material.
  - acceptance: prefer bounded file maps, search refs, line-window reads, and
    output refs over stuffing repo text into the prompt.
  - verification: large repo prompt stays bounded while agent can inspect
    files on demand.
  - status: open.

- [ ] NH-05.2 Add user-facing compaction controls.
  - acceptance: `/compact`, `/compact preview`, `/context epochs`, and undo or
    recovery paths expose what changed without hiding raw history.
  - verification: TUI and Telegram command tests.
  - status: open.

- [ ] NH-05.3 Add official gateway follow/reconnect helpers.
  - acceptance: gatewayclient owns reconnect, seq cursor, replay-after-gap, and
    terminal-state helpers so TUI/Telegram do not duplicate transport logic.
  - verification: fake gateway tests; client surfaces consume the helper.
  - status: open.

## Verification Gates

Focused tasks should run the smallest relevant package tests first. Before
marking all P0 milestones complete, run:

```sh
/root/.local/go/bin/go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/session ./internal/telegrambot ./internal/tui ./internal/eventlog ./internal/tools ./internal/agent ./internal/clientux/projector ./internal/runstate ./internal/provider ./internal/webtools
/root/.local/go/bin/go test -run 'Test.*Replay.*|Test.*Seq.*|Test.*Interrupt.*|Test.*Admission.*|Test.*InputInbox.*|Test.*Telegram.*|Test.*Slow.*Client.*|Test.*Backpressure.*|Test.*TUI.*|Test.*Compaction.*|Test.*Cache.*|Test.*Web.*Summary.*|Test.*ToolSnapshot.*|Test.*TranscriptPairing.*|Test.*Golden.*Trace.*|Test.*Crash.*Repair.*|Test.*Stream.*Gap.*' -count=1 ./internal/...
/root/.local/go/bin/go test -count=1 ./...
/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict -repo /root/billyharness
```

If deployed services are touched, rebuild `/root/billyharness/bin/fast-agent-harness`,
restart `billyharness-gateway.service` and `billyharness-telegram.service`,
then record `/health`, `doctor`, and a safe TUI/Telegram smoke.

## Completion Criteria

This roadmap is complete when:

- all NH-01, NH-02, and NH-03 P0 items are implemented, verified, and marked
  completed or explicitly blocked with exact command/error and next action;
- all P1 items are either completed, split into a later roadmap, or blocked
  with a concrete reason;
- `docs/architecture.md` reflects any new package boundary or import rule;
- JSONL remains the durable source of truth;
- TUI and Telegram render from shared protocol/projector/toolrender state;
- every completed or blocked implementation task has scoped evidence and a
  pushed commit.
