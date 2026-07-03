# 002 TODO - Runtime Event Replay And Projection Hardening

Status: completed - verified 2026-07-02
Created: 2026-07-02
Completed: 2026-07-02

## Request

Run a clean-room 6-subagent research pass over Billyharness and local
Codex/OpenCode/Claude Code checkouts, then consolidate the high-confidence
findings into one implementation loop. This archived TODO is historical
evidence for the completed coding pass; do not implement competitor source.

## Source Research Summary

- Inputs inspected:
  - `AGENTS.md`
  - `loop-develop/README.md`
  - `docs/architecture.md`
  - `docs/codex-research-roadmap.md`
  - `docs/competitive-architecture-analysis.md`
  - `loop-develop/history/001-todo.md`
  - `git status --short`
  - active TODO scan across `loop-develop/current-todo` and `loop-develop/history`
  - 6 native Codex research subagents:
    - runtime/events/replay: `internal/agent`, `internal/session`, `internal/eventlog`, `internal/trace`
    - tools/web/MCP contracts: `internal/tools`, `internal/webtools`, `internal/mcpclient`, `internal/mcpserver`
    - TUI/client projector UX: `internal/tui`, `internal/clientux`, `internal/clientux/projector`, `internal/toolrender`
    - Telegram/gateway adapter UX: `internal/telegrambot`, `internal/gatewayclient`, `internal/gatewayapi`, gateway streaming/reconnect
    - config/provider/auth/context: `internal/config`, `internal/provider`, `internal/credentials`, `internal/codexauth`, `internal/modelinfo`, `internal/projectcontext`, `internal/memory`
    - architecture/hygiene/test strategy: `docs/architecture.md`, import boundaries, hygiene, `internal/testkit`, scripts
  - local clean-room source checkouts:
    - `/Users/billy/agent-research/codex`, remote `https://github.com/openai/codex.git`, branch `main`, HEAD `0ccb676dd090`, status clean
    - `/Users/billy/agent-research/opencode-current`, remote `https://github.com/anomalyco/opencode.git`, branch `dev`, HEAD `373cd08b9844`, status clean
    - `/Users/billy/agent-research/claude-code`, remote `https://github.com/anthropics/claude-code.git`, branch `main`, HEAD `75709eacf133`, status clean
- High-confidence findings:
  - Runtime tool attempts can currently emit a failed/aborted terminal event and then still emit `tool.call_finished`; lifecycle validation should reject duplicate terminal turn, step, and attempt events.
  - `turn.change_recorded` is not fully self-contained because the payload has a `RunID` field that can be left empty by current construction.
  - Gateway follow-mode event delivery still relies too much on lossy live fanout after the initial replay; follow should be store-backed and wake-driven, with gap recovery.
  - Telegram does not yet consume run stream-gap results or render/replay `gateway.stream_gap`, while TUI already has a recovery path.
  - Gateway `/cancel` is weaker than interrupt policy because it does not use the same cancel-and-wait plus terminal-event cleanup path.
  - TUI transcript projection can attach unmatched tool lifecycle/result/audit events to the last tool cell.
  - Client UX projection can let `tool.output_ref_created` downgrade a terminal tool status if output-ref arrives after finish/fail/abort.
  - Tool/MCP schema validation is a narrow subset and can reject stale/bad args more usefully with small local validation additions.
  - MCP structured result/meta preservation should use output refs instead of flattening everything into text when structured data matters.
  - Config/provider hygiene has useful P1 follow-ups: dotenv source provenance, sanitized diagnostics, and provider retry/auth-refresh observation.
- Open questions / assumptions:
  - Broad `go test -count=1 ./...` had known unrelated failures recorded in `001-todo.md`; refresh this during implementation and separate existing failures from new regressions.
  - Existing worktree changes are user-owned. Do not revert, format, move, or delete unrelated files.
  - Current checkout is the Mac checkout under `/Users/billy`; use Mac paths, not production `/root` paths.

## Architecture Boundaries

- Target packages/files:
  - `internal/agent/tool_attempt.go`
  - `internal/eventlog/eventlog.go`
  - `internal/eventlog/*_test.go`
  - `internal/trace/*`
  - `internal/session/*`
  - `internal/gateway/*`
  - `internal/gatewayapi/*`
  - `internal/gatewayclient/*`
  - `internal/telegrambot/*`
  - `internal/clientux/projector/*`
  - `internal/tui/transcript/*`
  - `internal/tui/*_test.go`
  - `internal/tools/schema.go`
  - `internal/tools/*_test.go`
  - `internal/mcpclient/*`
  - `internal/tooloutput/*`
  - `internal/testkit/testdata/traces/*` only when a shared fixture genuinely reduces duplicated edge traces
- Contracts to preserve:
  - JSONL session events remain the durable source of truth.
  - Live `/run` and `/events?follow=true` streams are progress channels; dropped live events must be recoverable through replay after durable `seq`.
  - TUI and Telegram remain projectors/adapters, not runtime owners.
  - Raw MCP schemas stay hidden/lazy by default; `mcp_list_tools` and `mcp_call` stay compact gateway tools.
  - Large or structured tool data stays out-of-band through output refs with bounded model-visible previews.
  - `internal/tui` must not import gateway server internals, agent, provider, or tools directly.
  - `internal/telegrambot` must not import gateway server internals.
  - `internal/tools` must not import `provider`.
  - `internal/eventlog` owns lifecycle validation and must stay independent of runtime/presentation packages.
- Out of scope:
  - No implementation fixes before this TODO is used in a coding loop.
  - No competitor source copying, source porting, or long source quotes.
  - No production SSH, systemd, provider calls, package installs, commits, pushes, or live benchmarks unless Billy separately asks.
  - No app-server/ACP compatibility layer, generated SDKs, cloud workspaces, SQLite migration, marketplace/plugin platform, team/RBAC policy, analytics, compliance, Slack/mobile surfaces, hidden git state, shell-history scraping, vector DB, or background auto-memory extraction.

## Checklist

### P0 - Must Ship

- [x] Make runtime tool attempt terminal events exclusive.
  - target files:
    - `internal/agent/tool_attempt.go`
    - `internal/eventlog/eventlog.go`
    - `internal/eventlog/eventlog_test.go`
    - `internal/trace/trace_test.go`
    - focused `internal/agent` tests
  - acceptance:
    - failed and aborted tool attempts emit exactly one terminal attempt event, not `tool.call_failed` or `tool.call_aborted` followed by `tool.call_finished`.
    - lifecycle validation rejects duplicate terminal turn, step, and tool attempt events for the same IDs.
    - trace replay counters cannot double-count duplicate terminal tool/step/turn completions.
  - verification:
    - `go test -count=1 ./internal/eventlog ./internal/trace ./internal/agent`

- [x] Make `turn.change_recorded` replay payloads self-contained.
  - target files:
    - `internal/agent/tool_attempt.go`
    - `internal/checkpoint/*` only if the run identity belongs in checkpoint records
    - `internal/agent/tool_attempt_test.go`
  - acceptance:
    - emitted `turn.change_recorded` payload `run_id` is populated and matches the enclosing event/run identity.
    - focused tests cover decoded payload identity, not only envelope identity.
    - if current file-count tests fail because directory entries are counted, make the intended checkpoint semantics explicit in tests instead of weakening coverage.
  - verification:
    - `go test -count=1 ./internal/agent -run 'TurnChange|ShellExec|PatchRecord'`
    - `go test -count=1 ./internal/agent`

- [x] Make gateway follow-mode event streaming store-backed and gap-safe.
  - target files:
    - `internal/gateway/gateway.go`
    - `internal/gateway/session_events.go`
    - `internal/gateway/session_events_test.go`
    - `internal/gatewayclient/client.go`
    - `internal/gatewayclient/client_test.go`
  - acceptance:
    - `/v1/sessions/{id}/events?follow=true` replays durable JSONL events after the caller cursor until caught up, then uses the live hub only as a wake signal.
    - dropped live hub messages cannot permanently hide durable events from a follower.
    - stream gap semantics are explicit and recoverable from the last durable `seq`.
    - `/events` sets the same anti-buffering headers expected for streaming paths, including `X-Accel-Buffering: no`.
    - client-side gap errors or recovery paths are covered without introducing a generated protocol layer.
  - verification:
    - `go test -count=1 ./internal/gateway ./internal/gatewayclient`
    - `go test -count=1 ./internal/gateway -run 'SessionEvents|Stream|Gap'`

- [x] Align Telegram/gateway run recovery and cancellation with durable replay.
  - target files:
    - `internal/gateway/gateway.go`
    - `internal/session/session.go`
    - `internal/gatewayapi/types.go`
    - `internal/gatewayclient/client.go`
    - `internal/telegrambot/gateway_client.go`
    - `internal/telegrambot/runner.go`
    - `internal/telegrambot/render.go`
    - `internal/telegrambot/*_test.go`
  - acceptance:
    - Telegram can observe run stream gaps, render a compact recovery status, and replay from `lastEventSeq` before final delivery.
    - `gateway.stream_gap` is handled as a projector/recovery hint, not raw confusing output.
    - `/cancel` uses the same cancel-and-wait plus terminal cleanup semantics as interrupt policy, and persists replay-valid terminal state.
    - one active run per session remains enforced; no stale tool rows survive an interrupted or cancelled run.
  - verification:
    - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/telegrambot`
    - `go test -count=1 ./internal/gateway ./internal/telegrambot -run 'Cancel|Interrupt|Gap|Replay'`

- [x] Fix client projection so stale or out-of-order tool events cannot corrupt the visible transcript.
  - target files:
    - `internal/tui/transcript/projector.go`
    - `internal/tui/transcript/projector_test.go`
    - `internal/clientux/projector/projector.go`
    - `internal/clientux/projector/projector_test.go`
    - `internal/tui/cross_surface_consistency_test.go`
    - `internal/toolrender/*_test.go` when compact output parity changes
  - acceptance:
    - unmatched or missing `call_id` tool lifecycle/result/audit events never mutate the previous or last tool cell.
    - unknown tool IDs become explicit unmatched audit/status output or are ignored only where safe; they are never silently attached to another call.
    - `tool.output_ref_created` is additive metadata and cannot downgrade terminal `finished`, `failed`, or `aborted` status.
    - tests cover two parallel tools, unknown/missing `call_id`, output-ref before finish, output-ref after finish, and a seq-gap projection path.
  - verification:
    - `go test -count=1 ./internal/clientux/projector ./internal/tui/transcript ./internal/tui ./internal/toolrender`

### P1 - Should Ship

- [ ] Extend the local tool/MCP schema validator for common constraints.
  - target files:
    - `internal/tools/schema.go`
    - `internal/tools/tools_test.go`
    - `internal/tools/mcp_test.go`
  - acceptance:
    - validator supports useful local subsets for string length, patterns, numeric ranges, `const`, simple local `$ref`, and simple combiners where current MCP/tool schemas need them.
    - errors include stable paths and do not require a full JSON Schema engine or remote schema loading.
  - verification:
    - `go test -count=1 ./internal/tools ./internal/mcpclient`

- [ ] Preserve structured MCP call results safely.
  - target files:
    - `internal/mcpclient/jsonrpc.go`
    - `internal/mcpclient/content.go`
    - `internal/tools/tools.go`
    - `internal/tooloutput/*`
    - focused `internal/agent` tests if result metadata reaches events
  - acceptance:
    - MCP `structuredContent` and `_meta` are preserved behind an output ref when structured/meta/truncation is present.
    - model-visible content remains a compact text preview with metadata flags.
    - raw MCP result preservation does not clone app-server resource/elicitation behavior.
  - verification:
    - `go test -count=1 ./internal/mcpclient ./internal/tools ./internal/agent`

- [ ] Add minimal provider/auth/config observability where it helps local debugging.
  - target files:
    - `internal/config/*`
    - `internal/credentials/*`
    - `internal/provider/*`
    - `cmd/fast-agent-harness/*`
  - acceptance:
    - dotenv credential source kind/path is available as sanitized status so project-cwd `.env` usage is visible without blocking solo convenience.
    - diagnostics exposed to gateway/Telegram/default CLI output do not leak raw secrets or unnecessary local auth file paths unless explicitly using a local-debug path.
    - provider retry/auth-refresh observations are captured with fake HTTP tests if they are wired in this loop.
  - verification:
    - `go test -count=1 ./internal/config ./internal/credentials ./internal/provider ./cmd/fast-agent-harness`

- [ ] Strengthen hygiene and shared fixture checks only where touched code creates risk.
  - target files:
    - `cmd/fast-agent-harness/hygiene.go`
    - `cmd/fast-agent-harness/hygiene_test.go`
    - `internal/testkit/testdata/traces/*`
    - `internal/tui/cross_surface_consistency_test.go`
  - acceptance:
    - near-budget file warnings are visible before hard failure, without forcing automatic file splits.
    - large-file exceptions require owner, removal phase, and split plan before strict hygiene accepts them.
    - duplicated cross-surface edge traces are folded into canonical fixtures only if doing so simplifies real tests.
  - verification:
    - `go test -count=1 ./cmd/fast-agent-harness ./internal/architecture`
    - `go test -count=1 ./internal/trace ./internal/clientux/projector ./internal/tui ./internal/telegrambot ./internal/toolrender`

### P2 - Nice Later

- [ ] Add durable `mcp.status_changed` and `mcp.catalog_changed` events only after P0 replay semantics are green.
  - target files:
    - `internal/protocol`
    - `internal/mcpclient`
    - `internal/tools`
    - `internal/agent`
    - `internal/gateway`
    - `internal/clientux`
  - acceptance:
    - events are bounded, redacted, replayable, and carry only useful status, tool_count, catalog version, collisions, retry/backoff data.
    - no UI panel rewrite or managed policy surface appears.
  - verification:
    - `go test -count=1 ./internal/protocol ./internal/mcpclient ./internal/tools ./internal/agent ./internal/gateway ./internal/clientux`

- [ ] Add a failing fake-stdio test before changing idle `notifications/tools/list_changed` handling.
  - target files:
    - `internal/mcpclient/jsonrpc.go`
    - `internal/mcpclient/stdio.go`
    - `internal/mcpclient/server.go`
    - `internal/mcpclient/client_test.go`
  - acceptance:
    - first prove whether idle notifications leave `mcp_list_tools` stale.
    - only then add a small read pump/demux with caps and redaction.
  - verification:
    - `go test -count=1 ./internal/mcpclient -run 'TestMCP.*ListChanged|TestMCP.*Idle'`

- [ ] Add context/memory bucket accounting only after a real context-window failure or workflow proof.
  - target files:
    - `internal/agent`
    - `internal/memory`
    - `internal/projectcontext`
    - `internal/config`
  - acceptance:
    - memory remains manual, local-file based, summary-only, and lower priority than current user/project instructions.
    - no vector DB, background memory agent, or auto-extraction default.
  - verification:
    - `go test -count=1 ./internal/agent ./internal/memory ./internal/projectcontext ./internal/config`

- [ ] Convert known broad-test failures into a clean stabilization gate after the P0/P1 focused tests pass.
  - target files:
    - `internal/agent`
    - `internal/attachments`
    - `internal/tui`
    - `internal/telegrambot`
  - acceptance:
    - refresh failures recorded in `001-todo.md`.
    - fix or isolate each failure with focused tests before requiring broad green.
    - do not hide failures with unconditional skips.
  - verification:
    - `go test -count=1 ./internal/agent ./internal/attachments ./internal/tui ./internal/telegrambot`
    - `go test -count=1 ./...`

## Acceptance

- P0 runtime and replay invariants are fixed with focused regression tests:
  - one terminal event per tool attempt;
  - lifecycle validator rejects duplicate terminal turn, step, and attempt events;
  - `turn.change_recorded` payload identity is self-contained;
  - gateway follow-mode recovers from dropped live events by replaying durable JSONL;
  - Telegram and TUI/client projections do not corrupt visible tool state after gaps, unknown IDs, or out-of-order output refs.
- P1 work is completed only when it is clearly cheap or directly touched by P0 changes.
- Any P2 work is left unchecked unless a failing test or workflow proof appears during implementation.
- Existing user-owned worktree changes are preserved.
- No active TODO, goal prompt, temporary research log, setup note, or feature note is written into `docs/`.
- This file is updated with implementation evidence/status as work completes.

## Implementation Evidence - 2026-07-02

Status:

- P0 complete and verified in the local Mac checkout.
- P1 not implemented; `./internal/tools ./internal/mcpclient` was run as a focused guard only.
- P2 not implemented; broad-test fallout fixes were limited to keeping the verification gate honest.
- TODO moved to `loop-develop/history` after the verifier pass.
- No production SSH, provider calls, systemd validation, or live runtime validation was performed.

Implemented:

- Tool attempts now emit exactly one terminal attempt event: `tool.call_finished`, `tool.call_failed`, or `tool.call_aborted`.
- Eventlog lifecycle validation rejects duplicate terminal turn, step, and tool-attempt events; trace replay cannot double-count duplicate terminal outcomes.
- `turn.change_recorded` payloads now carry `run_id`, and focused tests assert decoded payload identity.
- Gateway `/events?follow=true&after_seq=N` is store-backed after the cursor and uses the live hub only as a wake signal; streaming headers include `X-Accel-Buffering: no`.
- Gateway `/cancel` uses the interrupt cancel-and-wait path before persisting terminal state.
- Telegram renders `gateway.stream_gap` compactly and replays durable events from `lastEventSeq` before final delivery after stream gaps.
- TUI/client projectors no longer attach unmatched/missing-`call_id` tool result/audit events to the previous tool; unmatched events become explicit cells.
- `tool.output_ref_created` remains additive metadata and no longer downgrades terminal tool status.
- Bench replay/result accounting now treats failed/aborted tool terminals as terminal outcomes and tool errors without requiring a synthetic `tool.call_finished`.
- macOS temp-path symlink test helpers were hardened in attachment tests where the attachment importer intentionally rejects symlink path components.

Verification run:

- `go test -count=1 ./internal/eventlog ./internal/trace ./internal/agent` - pass.
- `go test -count=1 ./internal/agent -run 'TurnChange|ShellExec|PatchRecord'` - pass.
- `go test -count=1 ./internal/agent` - pass.
- `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/telegrambot` - pass.
- `go test -count=1 ./internal/gateway -run 'SessionEvents|Stream|Gap'` - pass.
- `go test -count=1 ./internal/gateway ./internal/telegrambot -run 'Cancel|Interrupt|Gap|Replay'` - pass.
- `go test -count=1 ./internal/clientux/projector ./internal/tui/transcript ./internal/tui ./internal/toolrender` - pass.
- `go test -count=1 ./internal/tools ./internal/mcpclient` - pass.
- `go test -count=1 ./internal/attachments ./internal/bench` - pass after focused broad-gate fallout fixes.
- `go test -count=1 ./...` - pass.
- `go build -o /tmp/fast-agent-harness ./cmd/fast-agent-harness` - pass.
- `git diff --check` - pass.

Known failures:

- No known existing broad-test failures remained on the final verification run.
- Earlier broad failures during this pass were fixed rather than classified as existing: `internal/attachments` macOS symlinked temp source paths, and `internal/bench` assumptions that failed tool terminals still also emitted `tool.call_finished`.

## Verifier Follow-Up - 2026-07-02

The verification pass after the implementation chat found and fixed two final
issues before this TODO was moved to history:

- `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/telegrambot`
  initially exposed a Telegram test flake in
  `TestTelegramConcurrentPhotoChatsRemainIsolated`: `handlePolledUpdate`
  returns after admission and starts the run in a background goroutine, so the
  test could clean its `BILLYHARNESS_HOME` temp directory while final run edits
  were still completing. The test now waits for the relevant active run keys to
  clear before cleanup, and `go test -count=3 ./internal/telegrambot` passes.
- `internal/clientux/projector` now also preserves terminal compact status when
  a late `tool.output_ref_created` event arrives after a terminal tool result
  that did not include an existing compact payload.

Verifier commands:

- `git diff --check` - pass.
- `go test -count=1 ./internal/eventlog ./internal/trace ./internal/agent` - pass.
- `go test -count=1 ./internal/agent -run 'TurnChange|ShellExec|PatchRecord'` - pass.
- `go test -count=1 ./internal/gateway -run 'SessionEvents|Stream|Gap'` - pass.
- `go test -count=1 ./internal/gateway ./internal/telegrambot -run 'Cancel|Interrupt|Gap|Replay'` - pass.
- `go test -count=1 ./internal/attachments ./internal/bench` - pass.
- `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/telegrambot` - pass after the Telegram test synchronization fix.
- `go test -count=1 ./internal/clientux/projector ./internal/tui/transcript ./internal/tui ./internal/toolrender` - pass.
- `go test -count=1 ./internal/tools ./internal/mcpclient` - pass.
- `go test -count=3 ./internal/telegrambot` - pass.
- `go test -count=1 ./...` - pass.
- `go build -o /tmp/fast-agent-harness ./cmd/fast-agent-harness` - pass.

## Verification

- `git diff --check`
- focused package tests:
  - `go test -count=1 ./internal/eventlog ./internal/trace ./internal/agent`
  - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/telegrambot`
  - `go test -count=1 ./internal/clientux/projector ./internal/tui/transcript ./internal/tui ./internal/toolrender`
  - `go test -count=1 ./internal/tools ./internal/mcpclient`
  - `go test -count=1 ./internal/config ./internal/credentials ./internal/provider ./cmd/fast-agent-harness` when P1 config/provider work is touched
- broader tests/rebuild required if touching CLI/gateway/TUI/Telegram/provider/tool/agent code:
  - `go test -count=1 ./...`
  - `go build -o /tmp/fast-agent-harness ./cmd/fast-agent-harness`
- known existing broad-test failures must be separated from new regressions; do not blindly require all `go test ./...` green if unrelated existing failures remain.

## Rejected Bloat

- Do not copy, translate, or port competitor source code.
- Do not add Codex app-server/ACP compatibility, generated SDKs, remote workspace orchestration, cloud task queues, remote environments, reviewer/guardian stacks, managed policies, enterprise RBAC, analytics, compliance layers, or broad plugin marketplaces.
- Do not replace Bubble Tea/TUI architecture with React/Ink/web timeline machinery.
- Do not migrate durable events to mandatory SQLite, FTS, vector DB, or an event-sourcing platform before JSONL replay fails a measured test.
- Do not add Slack/mobile/CCR-style surfaces.
- Do not add background auto-memory extraction, vector search, shell-history scraping, hidden git commits/stashes/resets, or `.env` value injection into prompts.
- Do not expand remote HTTP/OAuth MCP in this loop; keep stdio/lazy MCP unless Billy brings a concrete failing workflow.

## Goal Prompt

```text
/goal Objective: Implement loop-develop/current-todo/002-todo.md for Runtime Event Replay And Projection Hardening.

Workspace: /Users/billy/repos/billyharness

Source of truth:
- Read AGENTS.md.
- Read loop-develop/current-todo/002-todo.md.
- Work P0 first; do P1/P2 only when needed or clearly cheap.

Acceptance:
- Satisfy the TODO's Acceptance section.
- Preserve listed architecture boundaries.
- Update the TODO with evidence/status as work completes.

Verification:
- Run the TODO's focused verification commands.
- Run git diff --check.
- Run broader Go tests and rebuild when required by AGENTS.md.
- Separate known existing broad-test failures from new regressions.

Completion:
- Do not move the TODO to history unless Billy explicitly asks after verification.

Final answer:
- report changed files and P0/P1/P2 status;
- summarize focused tests, broad tests, rebuild, and git diff --check;
- call out any known existing failures separately from new regressions;
- do not claim production/runtime validation unless it was actually performed.
```
