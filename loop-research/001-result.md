# Loop Research 001 Result

Loop ID: `loop-research-001-20260704-stability-debuggability-architecture`

## Hook Result

- Current iteration count: 15.
- Target: 15.
- Stop hook reached exactly 15 iterations: yes. `loop-research/.hook-state.json` reported `completed_iterations: 15` and `target: 15`.
- `loop-research/iterations`: 15 stars plus newline, 16 bytes total.
- `loop-research/001-raw.md`: 15 `## Iteration N` sections and 15 `ITERATION_DONE:` markers.
- Raw append ownership: no manual raw edits were made after arming; the raw file contains hook-added iteration envelopes with recorded metadata.
- Compact-context recovery: worked for this run. After a context compaction before iteration 13, the loop recovered by rereading the skill, `AGENTS.md`, the prompt, hook state, iteration counter, and raw tail, then continued with iteration 13 rather than restarting numbering.
- Session guard: not fully safe. Iteration 1 found that a copied activation block can rebind loop ownership from another session, so unrelated chats can potentially advance the same armed loop if they repeat the activation block and file paths. During this run, all processed turn IDs in `.hook-state.json` were the same turn ID, so no unrelated advancement was observed after binding.

## Sources Inspected

- Loop files: `loop-research/001-prompt.md`, `loop-research/001-raw.md`, `loop-research/iterations`, `loop-research/.hook-state.json`.
- Project/router docs: `AGENTS.md`, `.agents/skills/loop-research/SKILL.md`.
- Gateway/session/event store: `internal/gateway/gateway.go`, `internal/gateway/session_store.go`, `internal/gateway/session_index.go`, `internal/gateway/session_events.go`, `internal/gateway/session_inputs.go`, `internal/gateway/session_inspect.go`, `internal/gateway/memory_context.go`.
- Runtime/session/agent: `internal/session/session.go`, `internal/agent/runtime_loop.go`, `internal/agent/compaction.go`, `internal/agent/model_compaction.go`, `internal/agent/context_threshold.go`, `internal/agent/transcript_pairing.go`.
- Tools/MCP/checkpoint/provider/Telegram: `internal/tools/shell_process.go`, `internal/tools/tools.go`, `internal/tools/policy.go`, `internal/checkpoint/checkpoint.go`, `internal/mcpclient/*.go`, `internal/provider/provider.go`, `internal/provider/codex_provider.go`, `internal/telegrambot/state_runtime.go`, `internal/telegrambot/runner.go`.
- Docs: `docs/architecture/gateway-and-sessions.md`, `docs/architecture/runtime-event-system.md`, `docs/architecture/security-model.md`, `docs/architecture/tools-mcp-and-policy.md`.
- Focused tests run: `go test ./internal/eventlog`, `go test ./internal/tools`, `go test ./internal/gatewayclient`, `go test ./internal/provider -run 'Test(Parse|DeepSeek|Codex|Provider|Retry)'`, `go test ./internal/checkpoint`, `go test ./internal/gateway -run 'TestGatewaySession(Undo|Redo)'`, targeted MCP tests in `./internal/tools` and `./internal/mcpclient`, targeted compaction/context tests in `./internal/agent` and `./internal/clientux`.

## Top Findings

P0 - Loop hook session guard can be rebound.
The hook can bind ownership to another session whose transcript repeats the activation block and target file paths. This defeats the "same session only" guard for copied loop prompts. Harden by binding to the original transcript/session before accepting iteration markers, and reject session changes unless explicitly cleared.

P0 - Checkpoint restore has a symlink-ancestry TOCTOU.
`RestoreWithOptions` validates workspace roots and symlink ancestry before mutation, but restore/delete later operate by pathname. An ancestor swapped to a symlink between validation and restore can redirect undo/redo outside workspace roots. Add deterministic race-hook tests and switch restore/delete to ancestry-safe no-follow traversal or revalidate immediately at every mutation point.

P1 - Session-store writes can disappear after crash.
Manifest, index, and snapshot writes use rename/file fsync shapes without syncing parent directories. Acknowledged saves can vanish after power loss or crash. Introduce a shared durable write helper that fsyncs file and parent directory for manifests, indexes, snapshots, and corrupt-file renames.

P1 - Telegram update ack is not durable enough.
`ackOffset` advances the in-memory poll cursor even when offset persistence fails. The bot can skip updates until restart without a durable acknowledgement. Update offset only after successful persistence or retry/fail closed with an explicit operator-visible state.

P1 - Tool execution can exhaust process memory or slots.
Foreground `shell_exec` buffers complete stdout/stderr via `CombinedOutput` before truncation, so noisy commands can OOM despite `max_output_bytes`. Background `shell_exec` reports `timeout_sec` but never enforces it, so long-running processes can consume all managed slots.

P1 - Stream/event recovery has durable replay gaps.
Store-backed `/events?after_seq&follow=true` can emit `seq=0` non-durable live events, and TUI/Telegram stream-gap recovery ignores `gateway.stream_gap.replay_after_seq`, replaying from the latest delivered seq instead. Clients can miss durable events and observe failures that cannot be replayed by seq.

P1 - Context epoch is run-local but presented like session state.
`context_epoch` resets on every `RunMessagesWithPromptOptions` call. Durable sessions can reuse epoch numbers across compacted runs, while `/context` reports latest epoch as current session epoch. Use a monotonic session compaction sequence or clearly scope epochs by run.

## Stability And Debuggability Risks

- Provider lifecycle: session run contexts are not canceled after normal completion, so goroutines waiting on `ctx.Done()` can outlive idle sessions.
- JSONL replay: appending records larger than the replay scanner's default max can create ledgers that later fail strict replay as corruption.
- DeepSeek SSE: parser treats each `data:` line as complete JSON, unlike the Codex parser, and rejects valid multi-line SSE frames.
- Lazy MCP: `mcp_call`, `mcp_list_tools`, and `tool_search` refresh every configured server before targeted work; a slow optional server can stall unrelated healthy MCP calls.
- Error paths need clearer operator stories for "persistence failed after mutation", "catalog stale", "gap replay cursor ignored", and "background timeout not enforced".

## Security And Authority Risks

- Checkpoint TOCTOU can cross workspace-root boundaries during undo/redo.
- `shell_exec` destructive git guardrail misses global-option forms such as `git -C . reset --hard`.
- No-store session runs skip attachment validation, allowing stale image refs through gateway preflight.
- MCP metadata is correctly labeled untrusted, but targeted side-effect allowance and refresh coupling still need tests around multi-server failure isolation.

## Missing Tests Or Gates

- Stop hook wrong-session/rebinding regression test.
- Parent-directory fsync/crash-safety tests for session manifests, indexes, snapshots, and corrupt-file renames.
- Telegram offset persistence failure test that proves updates are not skipped.
- `git -C . reset --hard` and other global-option destructive git guardrail tests.
- No-store `/v1/sessions/{id}/run` attachment-validation test.
- Store-backed follow test that separates durable events from `seq=0` live failures.
- JSONL append max-size parity test with replay.
- Background shell timeout enforcement test.
- Foreground shell bounded-writer memory test.
- DeepSeek multiline SSE parser test mirroring Codex coverage.
- TUI/Telegram stream-gap replay cursor tests.
- Multi-server lazy MCP test proving a slow optional server does not block targeted healthy calls.
- Multi-run context compaction test proving session-level epochs are monotonic or explicitly scoped.

## Feature Opportunities

- Add a small `billyharness debug doctor` or gateway diagnostics bundle with event-log integrity, session-store fsync mode, MCP status, shell-process state, and stream-gap replay hints.
- Add an operator-facing `mcp refresh --server <name>` or gateway route so targeted MCP status can be refreshed without touching all servers.
- Add a `shell_processes` health summary that flags expired background processes, missing timeout enforcement, and slot pressure.
- Add replay-audit commands for session event gaps, non-durable live events, and compaction epoch timelines.
- Add a checkpoint dry-run/audit mode that reports every path, resolved ancestry, hash expectation, and rollback plan before restore.

## External Clean-Room Patterns Worth Copying Conceptually

- Treat append-only event logs as the source of truth, but make every event/replay boundary explicitly typed: durable event, non-durable live hint, recovery cursor, and terminal state.
- Use session-scoped monotonic sequence numbers for compaction/state-machine epochs rather than run-local counters shown as global state.
- Prefer targeted lazy lifecycle operations over global refreshes: reconnect the server/tool being used, schedule unrelated repair in the background, and surface stale catalog state separately.
- Use shared durable-write primitives for all rename-based persistence, including parent-directory fsync.

## Recommended Next Loop-Development TODO

Create the next active TODO around a focused P0/P1 hardening batch:

1. Fix loop-research Stop hook session binding so copied activation blocks cannot advance an already armed loop from another session.
2. Harden checkpoint restore/delete against symlink-ancestry TOCTOU.
3. Add a shared durable-write helper with parent-directory fsync and migrate session manifest/index/snapshot writes.
4. Patch shell execution guardrails and resource bounds: destructive git parsing, foreground bounded capture, background timeout enforcement.
5. Add focused tests for each item plus `git diff --check`, targeted package tests, and the relevant broad Go tests for gateway/tools/checkpoint/session code.

## Things Not To Change Yet

- Do not move loop-development TODOs as part of this research result.
- Do not refactor the full gateway route layout while fixing these bugs.
- Do not copy competitor source code; only borrow conceptual lifecycle/replay patterns.
- Do not widen MCP tool visibility back into direct model-visible raw specs; keep lazy MCP but make refresh targeted.
- Do not replace deterministic compaction entirely; first fix epoch semantics and tests.
