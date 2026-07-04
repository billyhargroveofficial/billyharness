# 003 TODO - Security, Integrity, And Debuggability Hardening

Status: current
Created: 2026-07-03
Owner loop: native Codex implementation loop

## Request

Run a fresh architecture and stability review with 12 native Codex subagents,
compare Billyharness against strong agent-project patterns from current public
agent systems, and turn the result into a deep implementation TODO plus a
copy-ready Codex `/goal` prompt.

This TODO is intentionally large. Work P0 first. Do P1 and P2 only after P0 is
green, unless a P1/P2 change is a cheap prerequisite for a P0 fix.

## Source Research Summary

### Native Codex Subagents Launched

All workers were read-only and clean-room. Competitor repositories were used
only for architecture, contracts, tests, UX, and security pattern comparison;
do not copy source code from them into Billyharness.

- Helmholtz: event log, replay, projector integrity.
- Lorentz: gateway, session lifecycle, resume, fork, persistence.
- Hubble: agent loop, tool lifecycle, cancellation, policy.
- Goodall: TUI runtime, transcript state, export/debug UX.
- Dirac: Telegram adapter, auth, interruption, safety.
- Zeno: MCP, web/search tools, schema and output settlement.
- Faraday: adversarial security pass.
- Bernoulli: tests, race coverage, CI, hygiene.
- Pasteur: config, providers, auth, model capability routing.
- Kant: macro architecture and package boundaries.
- Gauss: context, memory, compaction, long-run agent stability.
- Averroes: production operations, deploy, doctor, incident recovery.

### Local Verification Evidence

Commands run during research:

```sh
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./internal/eventlog ./internal/gateway ./internal/telegrambot ./internal/tools ./internal/tui ./internal/clientux/projector
go test -race -count=1 ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go test -coverprofile=/tmp/billyharness-cover.out ./...
go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict
```

Results:

- Full test suite passed.
- Focused race suite passed.
- Full race suite passed.
- `go vet` passed.
- `govulncheck` found no known vulnerabilities.
- Coverage from `go tool cover -func=/tmp/billyharness-cover.out`: 73.0%.
- Strict hygiene failed on file-size gates:
  - `internal/gateway/gateway.go`: 1530 LOC > 1500 source limit.
  - `internal/telegrambot/runner_test.go`: 1212 LOC > 1200 test limit.
- Current branch after previous implementation commit: `main` ahead of
  `origin/main` by 1 commit.

### Internet Research Signals

Use these as design constraints, not as copied implementation:

- OpenAI Agents SDK tracing:
  https://openai.github.io/openai-agents-python/tracing/
  - Agent systems should emit structured traces for agent runs, LLM generation,
    function/tool calls, guardrails, handoffs, and custom spans.
  - Sensitive trace payloads must be configurable and flushable for long-running
    workers.
- OpenAI Agents SDK guardrails:
  https://openai.github.io/openai-agents-python/guardrails/
  - Input and output guardrails are first-class validation points around agent
    work, before expensive or unsafe execution continues.
- OpenAI Codex approvals and security:
  https://developers.openai.com/codex/agent-approvals-security
  - Secure local agents should combine sandboxing, approval policy, and network
    controls. A local loopback service should not become an ambient authority
    boundary by accident.
- LangGraph persistence:
  https://docs.langchain.com/oss/python/langgraph/persistence
  - Checkpoints provide thread-scoped durability, human-in-loop continuity,
    time travel, replay, and fault tolerance. Production should not rely on
    in-memory persistence semantics.
- OWASP LLM Top 10:
  https://genai.owasp.org/llm-top-10/
  - Relevant risk buckets: prompt injection, sensitive information disclosure,
    supply chain, improper output handling, excessive agency, and unbounded
    consumption.
- OWASP Top 10 for Agentic Applications 2026:
  https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/
  - Agent-specific systems need explicit boundaries around planning, acting,
    delegation, tool use, and autonomous decision loops.

### Clean-Room Competitor Signals

Local research checkouts:

- `/Users/billy/agent-research/codex`
- `/Users/billy/agent-research/opencode-current`
- `/Users/billy/agent-research/claude-code`

High-level patterns worth matching:

- Keep the durable event stream as the source of truth; derive UI transcript,
  debug views, and incident reports from reducers.
- Use explicit sandbox and approval models around dangerous tools.
- Make tool calls observable as state machines, not loose text.
- Provide operator-grade diagnostics that can be collected without ad hoc grep.
- Keep session replay, fork, resume, and checkpoint behavior deterministic.
- Separate model/provider capability routing from UI and gateway transport code.
- Treat remote tool metadata and MCP instructions as untrusted input.
- Keep human-facing transcript views compact, but make raw debug evidence easy
  to inspect.

## Architecture Canon

These are the guiding rules for the implementation loop:

- Durable JSONL events are the source of truth.
- Live streams are wake/progress channels, not canonical history.
- Replayers and projectors must validate protocol envelopes and lifecycle rules.
- Corrupt persistence, corrupt replay, unsafe config, and ownership mismatch
  must fail closed.
- Gateway owns session identity, auth, and owner/scope checks centrally.
- TUI, Telegram, and exports are projections of a shared presentation policy.
- Raw evidence stays available for debugging; normal transcript stays readable.
- MCP/server-supplied instructions are untrusted metadata unless explicitly
  promoted by policy.
- Configuration resolution must be strict in runtime entrypoints.
- TODO work stays in `loop-develop/current-todo`; do not move this file to
  history until Billy asks for final verification and verification passes.

Non-goals for this TODO:

- Do not rewrite Billyharness into a database-backed product.
- Do not rewrite the provider or generation stack from scratch.
- Do not copy competitor source code.
- Do not move active TODO or goal prompts into `docs/`.

## P0 Milestone 1 - Close Security Boundaries

Goal: remove ambient authority paths where a browser, Telegram user, remote MCP
server, or permissive runtime config can cause unsafe action.

### P0.1 Gateway loopback/browser mutation boundary

Finding: a local browser page may be able to drive mutating gateway actions
against loopback when dangerous tools are enabled. Mutating routes currently
depend too much on loopback trust and request-body policy.

Target files to inspect:

- `internal/gateway/gateway.go`
- `internal/gateway/http*.go`
- `internal/gateway/auth*.go`
- `internal/config/*.go`
- route tests under `internal/gateway`

Implementation checklist:

- Require bearer-token auth for mutating HTTP endpoints even on loopback unless
  a clearly named explicit dev flag disables it.
- Add Origin, Host, and content-type checks for browser-reachable routes.
- Reject simple browser form content types for JSON mutation routes.
- Clamp request-provided privilege knobs server-side:
  - access mode;
  - dangerous-tool enablement;
  - max tool rounds;
  - provider/model override privilege.
- Add tests for cross-origin POST, missing bearer token, wrong token, allowed
  CLI client, and explicit dev-mode bypass.
- Ensure denial events are observable but redacted.

Verification:

```sh
go test -count=1 ./internal/gateway
go test -race -count=1 ./internal/gateway
go test -count=1 ./...
```

### P0.2 Telegram secret-bearing auth commands

Finding: `/auth deepseek` can contain secrets and must not rely on best-effort
message deletion before persisting key material.

Target files:

- `internal/telegrambot/*.go`
- `internal/gateway/auth*.go`
- tests under `internal/telegrambot`

Implementation checklist:

- Require private chat for secret-bearing auth commands by default.
- Or require successful deletion before persistence if group support remains.
- If deletion fails, do not persist the secret and show a redacted error.
- Store command safety metadata so future auth commands inherit the same guard.
- Ensure Telegram logs and error messages never echo key material.

Verification:

```sh
go test -count=1 ./internal/telegrambot
go test -race -count=1 ./internal/telegrambot
```

### P0.3 Central session owner and scope enforcement

Finding: Telegram adapter filtering is not enough. Gateway routes must enforce
session ownership/scope for list, get, events, run, cancel, undo, redo, fork,
and resume semantics.

Target files:

- `internal/gateway/gateway.go`
- `internal/gateway/session_store.go`
- `internal/gateway/client*.go`
- `internal/telegrambot/*.go`
- `internal/tui/*.go`

Implementation checklist:

- Add central owner/scope checks in gateway session access helpers.
- Make legacy unowned sessions explicit:
  - admin-only migration path, or
  - safe read-only legacy mode.
- Update TUI and Telegram callers to pass identity/scope explicitly.
- Add tests for cross-owner access denial across every mutating route.
- Add tests for resume/fork/cancel on sessions owned by another identity.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/telegrambot ./internal/tui
go test -race -count=1 ./internal/gateway ./internal/telegrambot
```

### P0.4 Treat MCP initialize instructions as untrusted

Finding: remote MCP server instructions can be model-visible. Treat them as
untrusted metadata unless policy explicitly promotes them.

Target files:

- `internal/mcpclient/*.go`
- `internal/tools/*.go`
- `internal/gateway/*.go`
- provider instruction assembly code

Implementation checklist:

- Store MCP instructions as metadata, not authoritative system instructions.
- Add allowlist/config gate if an operator really wants MCP instructions in
  model context.
- Label promoted content with source and trust level.
- Add prompt-injection regression tests with malicious MCP instructions.

Verification:

```sh
go test -count=1 ./internal/mcpclient ./internal/tools ./internal/gateway
```

### P0.5 Strict runtime config resolution

Finding: runtime entrypoints can silently fall back through `config.Default()`
or `MustResolve()`. Runtime should fail closed on malformed config.

Target files:

- `internal/config/*.go`
- `cmd/fast-agent-harness/*.go`
- `internal/gateway/*.go`
- `internal/tui/*.go`
- `internal/bench/*.go`

Implementation checklist:

- Audit all runtime entrypoints for `config.Default()` and `MustResolve()`.
- Use strict `Resolve()` in production/runtime commands.
- Keep test helpers explicit if they intentionally use defaults.
- Emit redacted, actionable config errors.
- Add regression tests for malformed config, missing required provider config,
  and invalid auth settings.

Verification:

```sh
go test -count=1 ./internal/config ./cmd/fast-agent-harness ./...
```

### P0.6 Effective context window for per-session model override

Finding: session context/snapshot reporting can use stale provider/model limits
after a per-session override.

Target files:

- `internal/gateway/*.go`
- `internal/agent/*.go`
- `internal/provider*.go`
- context accounting code

Implementation checklist:

- Derive runtime token/context limits from the effective run provider and model.
- Include model/provider provenance in context snapshots.
- Add tests for default model, profile override, and per-session override.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/agent ./...
```

## P0 Milestone 2 - Event And Session Integrity

Goal: make durable history valid, replayable, and debuggable under corruption,
duplicates, partial writes, and tool lifecycle edge cases.

### P0.7 Validate envelopes during gateway replay

Finding: gateway replay paths can parse durable events without the same envelope
validation used by trace/replay code.

Target files:

- `internal/gateway/session_store.go`
- `internal/eventlog/*.go`
- `internal/clientux/projector/*.go`

Implementation checklist:

- Use the canonical event envelope validator for gateway replay.
- Fail closed on invalid envelope version/type/sequence/session mismatch.
- Preserve enough error context for operator diagnostics.
- Add tests for invalid JSON, wrong session ID, skipped seq, repeated seq,
  unknown event type, and unsupported envelope version.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/eventlog ./internal/clientux/projector
go test -race -count=1 ./internal/gateway ./internal/eventlog
```

### P0.8 Extend lifecycle validator beyond terminal ordering

Finding: lifecycle validation needs to catch duplicate requests and orphan
progress/output/permission events, not only late terminal cleanup.

Target files:

- `internal/eventlog/*.go`
- `internal/agent/*.go`
- `internal/tools/*.go`
- `internal/clientux/projector/*.go`

Implementation checklist:

- Reject duplicate `tool.call_requested` for the same call ID in a lifecycle.
- Bind progress, permission, output-ref, user-input, and hook events to known
  call/attempt IDs.
- Preserve existing canon for output-ref-before-terminal and late cleanup abort.
- Add tests for orphan progress, orphan output-ref, duplicate requested,
  permission without call, and progress after terminal.

Verification:

```sh
go test -count=1 ./internal/eventlog ./internal/agent ./internal/tools ./internal/clientux/projector
```

### P0.9 Reject duplicate current-turn tool call IDs before execution

Finding: duplicate tool call IDs in one assistant turn can cause ambiguous
lifecycle projection. Reject before any execution starts.

Target files:

- `internal/agent/*.go`
- provider accumulator code
- `internal/tools/*.go`

Implementation checklist:

- Validate current-turn provider output before appending/executing tool calls.
- On duplicate call ID, emit a run/tool validation failure, not
  `tool.call_started`.
- Add tests with duplicate IDs in serial and parallel batches.
- Ensure provider text before the invalid call is preserved if already emitted.

Verification:

```sh
go test -count=1 ./internal/agent ./internal/tools
go test -race -count=1 ./internal/agent ./internal/tools
```

### P0.10 Fail closed on persistence failure

Finding: gateway can publish in-memory/progress events even when durable
persistence fails. Ideal behavior is fail closed: no normal success path if
history cannot be recorded.

Target files:

- `internal/gateway/gateway.go`
- `internal/gateway/session_store.go`
- `internal/eventlog/*.go`

Implementation checklist:

- Make recorder APIs return `(event, error)` or equivalent.
- If persistence fails, mark store/session degraded.
- Emit a durable or clearly surfaced `persistence_failed` / `run.failed`
  terminal where possible.
- Do not continue normal seq=0 live-only publishing as if the run is healthy.
- Add tests for append failure before first event, mid-run append failure, and
  terminal persistence failure.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/eventlog
go test -race -count=1 ./internal/gateway
```

### P0.11 Fail closed on replay and catch-up errors

Finding: clients should distinguish typed missing-session from corruption or
server replay errors. TUI/Telegram fallback creation must not hide corruption.

Target files:

- `internal/gateway/*.go`
- `internal/tui/*.go`
- `internal/telegrambot/*.go`
- gateway client packages

Implementation checklist:

- Add typed errors for missing session, corrupt session, replay failure, and
  no-store history.
- TUI/Telegram may fallback-create only for typed missing-session.
- Surface corrupt session and replay failures to operator-visible diagnostics.
- Add regression tests for each error class.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/tui ./internal/telegrambot
```

### P0.12 Surface corrupt sessions on gateway startup

Finding: loading all sessions can drop per-session errors too quietly.

Target files:

- `internal/gateway/session_store.go`
- gateway startup code
- operator status/health code

Implementation checklist:

- Preserve per-session load errors in a startup diagnostic structure.
- Expose redacted corrupt-session counts and session IDs/hashes in health or
  doctor output.
- Ensure corrupt sessions do not disappear silently from operator visibility.

Verification:

```sh
go test -count=1 ./internal/gateway
```

## P0 Milestone 3 - Debug Baseline And Verification Harness

Goal: give the loop agent and future operators enough deterministic evidence to
debug long-running sessions without guessing.

### P0.13 Session inspector as debug reducer

Target files:

- `internal/eventlog/*.go`
- `internal/clientux/projector/*.go`
- `cmd/fast-agent-harness/*.go`

Implementation checklist:

- Add a session inspector command or package reducer that reports:
  - envelope validity;
  - sequence gaps and duplicates;
  - lifecycle violations;
  - unmatched progress/output/permission events;
  - terminal state;
  - projector parity between raw events and human transcript;
  - output-ref audit and missing blob references.
- Support JSON output and concise human output.
- Use redaction for secrets and provider payloads.

Verification:

```sh
go test -count=1 ./internal/eventlog ./internal/clientux/projector ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness inspect-session -h
```

### P0.14 Canonical trace fixtures

Target files:

- `internal/eventlog/testdata`
- `internal/clientux/projector/testdata`
- `internal/agent/testdata`
- `internal/telegrambot/testdata`

Implementation checklist:

- Add machine-readable golden fixtures for:
  - stream gap;
  - duplicate tool call ID;
  - parallel cancellation;
  - late output-ref;
  - provider error after partial stream;
  - invalid tool args;
  - MCP catalog change;
  - Telegram interruption;
  - corrupted replay envelope.
- Validate fixtures with eventlog validators and projectors.
- Make fixtures useful for regression reports and incident bundles.

Verification:

```sh
go test -count=1 ./internal/eventlog ./internal/clientux/projector ./internal/agent ./internal/telegrambot
```

### P0.15 Local verification script

Target files:

- `scripts/verify-local.sh`
- `cmd/fast-agent-harness`
- `.github` only if CI already exists and needs wiring

Implementation checklist:

- Add one local script that runs:
  - `git diff --check`;
  - `go vet ./...`;
  - `go test -count=1 ./...`;
  - focused race packages;
  - optional full race lane with flag;
  - `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`;
  - binary rebuild;
  - hygiene strict;
  - bench smoke if non-mutating.
- Ensure `verify-deps` is safe/read-only or clearly labeled as mutating.
- Print a compact summary at the end.

Verification:

```sh
scripts/verify-local.sh
scripts/verify-local.sh --full-race
```

### P0.16 Restore hygiene as a hard gate

Current failures:

- `internal/gateway/gateway.go`: 1530 LOC > 1500 source limit.
- `internal/telegrambot/runner_test.go`: 1212 LOC > 1200 test limit.

Implementation checklist:

- Split gateway code by cohesive responsibilities without changing public
  behavior.
- Split Telegram runner tests into focused files.
- Keep package boundaries stable unless a real abstraction is needed.

Verification:

```sh
go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict
go test -count=1 ./internal/gateway ./internal/telegrambot
```

## P1 Milestone 4 - Runtime Assembly And Contracts

Goal: make provider/tool/runtime assembly deterministic and shared across
gateway, TUI, commands, and tests.

### P1.1 Introduce a runtime assembly host

Finding: settings-to-provider/registry/agent assembly is spread across runtime
entrypoints.

Target files:

- new `internal/runtimehost` or similar small package
- `internal/gateway/*.go`
- `internal/tui/*.go`
- `internal/bench/*.go`
- `cmd/fast-agent-harness/*.go`

Implementation checklist:

- Centralize resolved settings, provider selection, model capability lookup,
  tool registry assembly, MCP registry attachment, and policy injection.
- Add parity tests proving gateway/TUI/bench/cmd assemble the same effective
  runtime for the same profile.
- Keep the package small and boring; do not create a framework.

Verification:

```sh
go test -count=1 ./internal/runtimehost ./internal/gateway ./internal/tui ./internal/bench ./...
```

### P1.2 Sanitize gateway config DTOs

Finding: gateway DTOs can expose config internals such as source path/source
key through `config.ResolvedValue`.

Target files:

- `internal/gatewayapi/*.go`
- `internal/gateway/*.go`
- config status handlers/tests

Implementation checklist:

- Add sanitized DTOs for public status/config output.
- Keep local debug-only details behind explicit debug/doctor mode.
- Add tests for absence of source paths, source keys, env var names containing
  secrets, and raw tokens.

Verification:

```sh
go test -count=1 ./internal/gatewayapi ./internal/gateway ./internal/config
```

### P1.3 Profile instructions and provider/model conflicts

Findings:

- One-shot `/v1/run` can ignore profile-specific instructions.
- Provider/model conflicts may silently reroute.

Target files:

- `internal/gateway/*.go`
- provider selection/config packages
- gateway API tests

Implementation checklist:

- Ensure `/v1/run` uses effective profile instructions.
- Reject incompatible provider/model pairs or emit explicit warnings in debug
  paths, never silent reroute.
- Preserve backwards compatibility only where tests prove safe behavior.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/config ./...
```

### P1.4 Central secret lookup and provenance

Finding: DeepSeek and other provider secret lookup order can drift from config
dotenv rules.

Target files:

- `internal/config/*.go`
- provider auth packages
- gateway auth/status code

Implementation checklist:

- Centralize secret lookup precedence.
- Record redacted provenance such as `env`, `keychain`, `config`, or `dotenv`
  without exposing secret values.
- Add tests for precedence and redaction.

Verification:

```sh
go test -count=1 ./internal/config ./internal/gateway ./...
```

### P1.5 Tool schema and lifecycle contract hardening

Target files:

- `internal/tools/*.go`
- `internal/agent/*.go`
- `internal/mcpclient/*.go`
- schema validation packages if any

Implementation checklist:

- Support a clear JSON Schema subset, preferably Draft 2020-12 where practical.
- For unsupported schema features, choose explicit server-validated-only or
  fail with a visible diagnostic.
- Unknown tools must not emit permission allow before failing.
- Permission denial reason should distinguish unknown tool, policy, user deny,
  and timeout.
- Parallel batches should aggregate child failed/completed counts and support
  `completed_with_errors` rather than unconditional success.
- Fix `SnapshotWithToolPolicy` style handlers so execution policy is actually
  bound to the snapshot or passed explicitly.

Verification:

```sh
go test -count=1 ./internal/tools ./internal/agent ./internal/mcpclient
go test -race -count=1 ./internal/tools ./internal/agent
```

## P1/P2 Milestone 5 - MCP, Web, Output, And Filesystem Hardening

### P1.6 Provider backend URL network boundaries

Target files:

- web/search provider backend code
- Tavily/Exa integration code
- HTTP transport helpers

Implementation checklist:

- Reuse public-host transport checks or allowlist known provider hosts.
- Block localhost/private/link-local metadata endpoints.
- Handle redirects safely.
- Add DNS rebinding-minded tests where feasible.

Verification:

```sh
go test -count=1 ./internal/tools ./...
```

### P2.1 Safe MCP status redaction

Implementation checklist:

- Redact URL credentials.
- Redact header-like values.
- Redact command args that carry tokens.
- Redact inherited secret env.
- Redact stderr/status snippets by policy.

Verification:

```sh
go test -count=1 ./internal/mcpclient ./internal/tools ./internal/gateway
```

### P2.2 Separate MCP transport state from catalog state

Implementation checklist:

- Represent states such as:
  - connected_no_tools;
  - tools_fetch_failed;
  - catalog_stale;
  - disconnected;
  - degraded.
- Emit structured diagnostics for status changes.
- Later, optionally make durable `mcp.status_changed` and
  `mcp.catalog_changed` events.

Verification:

```sh
go test -count=1 ./internal/mcpclient ./internal/tools ./internal/gateway
```

### P2.3 Central output-ref settlement

Implementation checklist:

- Route large tool outputs through one settlement path.
- Ensure output refs are emitted before terminal events.
- Add missing-blob diagnostics in session inspector.
- Keep transcript compact but export/debug views complete.

Verification:

```sh
go test -count=1 ./internal/tools ./internal/eventlog ./internal/clientux/projector
```

### P2.4 Filesystem write/checkpoint TOCTOU hardening

Implementation checklist:

- Audit `fs_write_file`, `fs_edit`, and checkpoint restore paths.
- Prevent symlink/path-swap escapes between validation and write/restore.
- Use safe open/rename patterns available on macOS/Linux.
- Add tests for symlink swap attempts.

Verification:

```sh
go test -count=1 ./internal/tools ./...
```

## P1/P2 Milestone 6 - TUI, Telegram, And Operator Debug UX

### P1.7 TUI exact call-ID collapse

Finding: `collapseToolBlockIfLarge` should not use positional fallback when
call ID matching fails.

Target files:

- `internal/tui/*.go`

Implementation checklist:

- Collapse only exact call-ID matches.
- Leave unmatched blocks visible as diagnostics.
- Add regression test for reordered/duplicated/missing call IDs.

Verification:

```sh
go test -count=1 ./internal/tui
```

### P1.8 Shared presentation policy for exports

Implementation checklist:

- Make export rendering use the same policy as normal transcript projection.
- Keep low-level lifecycle chatter out of normal export.
- Add raw/rich export modes where needed.

Verification:

```sh
go test -count=1 ./internal/tui ./internal/clientux/projector
```

### P1.9 TUI debug snapshot

Implementation checklist:

- Add `/status debug` or `/debug-tui`.
- Include:
  - current session ID;
  - last seen seq;
  - queue/backlog state;
  - stale markers;
  - transcript block counts;
  - selected block/call ID;
  - cache sizes;
  - reflow/viewport metrics;
  - usage/context snapshot.
- Redact content by default where appropriate.

Verification:

```sh
go test -count=1 ./internal/tui
```

### P2.5 TUI selection revision and durable export

Implementation checklist:

- Clear or revise selection on structural transcript changes.
- Prevent stale copy after hiding tools or switching sessions.
- Add `/export raw|rich [path]`.

Verification:

```sh
go test -count=1 ./internal/tui
```

### P1.10 Telegram pending-input outbox

Finding: admitted Telegram inputs can strand across process restart.

Implementation checklist:

- Persist pending/admitted input state before dispatch.
- Reconcile pending inputs on restart.
- Emit clear operator diagnostics for abandoned inputs.
- Add tests for restart between admission and gateway send.

Verification:

```sh
go test -count=1 ./internal/telegrambot ./internal/gateway
```

### P1.11 Telegram allow-user scope policy

Finding: `-allow-user` can authorize a user too broadly across chats.

Implementation checklist:

- Default user allowlist to private chat only.
- Require chat allowlist for group use unless an explicit broad flag is set.
- Add tests for private, allowed group, disallowed group, and unauthorized chat.

Verification:

```sh
go test -count=1 ./internal/telegrambot
```

### P2.6 Telegram gateway error redaction

Implementation checklist:

- Redact gateway/provider/tool error text before sending to Telegram.
- Keep full diagnostic only in local incident/debug bundle.
- Add tests with tokens, URLs with credentials, and header-looking strings.

Verification:

```sh
go test -count=1 ./internal/telegrambot ./internal/gateway
```

### P0/P1 Operator doctor and incident bundle

Target files:

- `cmd/fast-agent-harness/*.go`
- new operator/doctor package if useful
- TUI/Telegram admin command surfaces

Implementation checklist:

- Add `doctor --deep` with human and redacted JSON output.
- Probe:
  - config resolution;
  - provider auth presence without revealing values;
  - MCP connectivity and catalog;
  - tool registry and policy;
  - session store health;
  - corrupt session counts;
  - binary age/build info;
  - gateway auth-token handling;
  - production service assumptions where local context permits.
- Add `incident collect --session ID --out DIR`.
- Incident bundle should include redacted:
  - doctor output;
  - config summary;
  - auth/provider summary;
  - MCP status;
  - journal/log tail if available;
  - session event export;
  - projector/session-inspector summary.
- Surface admin-only `/doctor`, `/logs`, and `/incident` in TUI/Telegram if
  consistent with existing command style.

Verification:

```sh
go test -count=1 ./cmd/fast-agent-harness ./internal/gateway ./internal/tui ./internal/telegrambot
go run ./cmd/fast-agent-harness doctor --deep --json
```

### P1 Production deploy/service source of truth

Implementation checklist:

- Add production scripts or runbook lane outside `docs/` if missing.
- Capture:
  - build;
  - install;
  - restart;
  - status;
  - journal tail;
  - doctor;
  - rollback.
- Do not store secrets in repo.
- Keep `docs/` architecture-only. Prefer `ops/` or `loop-develop` support
  files for operator runbooks.

Verification:

```sh
go test -count=1 ./...
git diff --check
```

## P2/P3 Milestone 7 - Context, Memory, And Long-Run Stability

### P2.7 Auditable compaction checkpoints

Target files:

- context/memory packages
- event protocol definitions
- TUI/gateway context status paths

Implementation checklist:

- Add `context_epoch` to compaction-aware state.
- Emit `context.compacted` with audit metadata:
  - input span hash;
  - replacement hash;
  - pre/post history seq/hash;
  - summary hash;
  - strategy;
  - trigger;
  - cut indexes.
- Add tests proving replay can reconstruct epoch boundaries.

Verification:

```sh
go test -count=1 ./internal/agent ./internal/gateway ./internal/tui ./...
```

### P2.8 Epoch-aware threshold events

Implementation checklist:

- Reset threshold event logic per context epoch after compaction.
- Avoid repeated stale warnings for already-compacted context.
- Include epoch and threshold provenance in debug status.

Verification:

```sh
go test -count=1 ./internal/agent ./internal/gateway ./internal/tui
```

### P2.9 Memory drift policy

Implementation checklist:

- Decide between session-locked memory hashes and per-turn reconciliation.
- If memory is included in context, record current/stale hash marker.
- Expose drift in debug view without leaking memory contents.

Verification:

```sh
go test -count=1 ./internal/agent ./internal/gateway
```

### P2.10 Context diagnostics index

Implementation checklist:

- Add diagnostics for:
  - compactions;
  - thresholds;
  - epochs;
  - top context contributors;
  - helper/tool usage;
  - protected prefix/body token split;
  - compaction margin;
  - memory/project/AGENTS hashes.

Verification:

```sh
go test -count=1 ./internal/agent ./internal/gateway ./internal/tui
```

## Additional Reliability Backlog

Do these after the main milestones unless they fall out naturally.

- Add failure-injection testkit for provider, MCP, process, and filesystem
  failures.
- Add package-aware coverage policy. Keep current total coverage baseline
  visible; do not chase vanity percentages before P0 safety work.
- Add benchmark gates for hot transcript/projector paths.
- Add diagnostics presets:
  - quick;
  - broad;
  - diff-check;
  - build;
  - gateway-smoke;
  - session-index.
- Add deterministic sanitized MCP tool-name collision behavior.
- Add MCP prompt/command visibility across CLI/TUI/Telegram.
- Add server-side fork/resume semantics:
  - `parent_session_id`;
  - `fork_after_seq`;
  - no client-side message cloning as the source of truth.
- Add paginated/iterative event history and explicit 409/503 behavior when
  `after_seq` is requested with no durable store.
- Improve snapshot fsync/atomicity where the current platform allows.

## Required Final Verification Before This TODO Can Move To History

Run and record the results in this file before Billy verifies completion:

```sh
git diff --check
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict
go build ./cmd/fast-agent-harness
```

If `scripts/verify-local.sh` exists by then, it must pass too:

```sh
scripts/verify-local.sh --full-race
```

For runtime/security changes, also run targeted smoke tests or document why a
live smoke was not possible in the current environment.

## Evidence Log

Append implementation evidence here as work progresses.

- 2026-07-03 research pass:
  - 12 native Codex subagents completed.
  - Full tests, vet, focused race, full race, coverage, and govulncheck were
    run by the research loop.
  - No known Go vulnerabilities found by govulncheck.
  - Strict hygiene currently fails on two file-size gates listed above.
- 2026-07-03 P0.1 gateway mutation boundary implementation:
  - Added production `RequireMutationAuth` gateway mode with bearer auth required
    for mutating `/v1/*` routes even from loopback; `serve` now fails closed
    without an auth token unless
    `-dev-allow-unauthenticated-loopback-mutations` is explicitly passed.
  - Added loopback Host checks, same-origin Origin/Referer checks, JSON
    content-type enforcement for protected mutation routes, and redacted
    security-denial logging.
  - Added server-side clamps so run requests cannot increase `max_tool_rounds`,
    cannot escalate `access_mode`, and cannot change provider/model/reasoning
    through the unauthenticated dev loopback bypass.
  - Regression tests cover missing/wrong bearer token, cross-origin POST, bad
    Host, simple form content type rejection, allowed CLI JSON, explicit dev
    bypass, and request privilege clamps.
  - Verification passed:
    - `go test -count=1 ./internal/gateway`
    - `go test -race -count=1 ./internal/gateway`
    - `go test -count=1 ./cmd/fast-agent-harness`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.2 Telegram secret-bearing auth command safety:
  - `deleteMessage` is now a real success/failure boundary for secret-bearing
    Telegram auth commands instead of fire-and-forget logging.
  - Added auth-command safety metadata for DeepSeek/API key aliases:
    secret-bearing commands must have a deletable Telegram message and must
    delete it before any key persistence; group usage remains possible only
    after successful deletion.
  - If deletion fails, the key is not saved and Telegram receives a generic
    redacted failure. DeepSeek save/gateway errors are redacted against the
    submitted secret before being rendered back to Telegram.
  - Regression tests cover group success after deletion, deletion failure
    blocking persistence, and save-error redaction. A first broad test run
    caught a forbidden `telegrambot -> secrets` import; the redaction was moved
    local to preserve the architecture boundary.
  - Verification passed:
    - `go test -count=1 ./internal/telegrambot`
    - `go test -count=1 ./internal/telegrambot ./internal/architecture`
    - `go test -race -count=1 ./internal/telegrambot`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.3 central session owner/scope enforcement:
  - Added gateway API scope headers and `gatewayclient.WithSessionOwner`; TUI
    and Telegram now attach their session owner scope to session list/get,
    event replay/catch-up, run, cancel, undo/redo, context/status/toolview, and
    user-input answer paths.
  - Gateway route helpers now centrally enforce session scope for list, get,
    status, context, events, run, inputs, user-input answer/reject, cancel,
    undo, and redo. Scoped creates cannot spoof a different owner.
  - Scoped clients may read legacy unowned sessions, but legacy sessions are
    read-only for scoped clients. Unscoped local/admin calls keep the existing
    compatibility path.
  - Regression tests cover request-scope header emission, scoped list
    filtering, same-owner reads, cross-owner read denials, every mutating
    cross-owner route, legacy read-only mutation denial, and scoped create owner
    mismatch.
	- Verification passed:
	  - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/telegrambot ./internal/tui`
	  - `go test -race -count=1 ./internal/gateway ./internal/telegrambot`
	  - `git diff --check`
	  - `go test -count=1 ./...`
- 2026-07-03 P0.4 MCP initialize instructions untrusted by default:
  - Split MCP initialize instructions into raw server metadata and promoted
    model-visible instructions. `Registry.Instructions()` is now empty by
    default for MCP initialize text, while `MCPServerInstructions()` exposes the
    metadata for status/debug projections.
  - Added explicit `MCPPromoteServerInstructions` config plumbing through
    resolved config, projections, runtime diff, MCP manager settings, registry
    clones, gateway server cloning, and MCP snapshot hashes.
  - Promoted content is labeled with
    `trust=operator_promoted_mcp_initialize_instructions`; default-untrusted
    content remains visible in `/v1/mcp` and MCP status formatting without being
    inserted into agent transcripts.
  - Regression tests cover default no-injection, explicit promotion preserving
    the protected-prefix insertion path, gateway status metadata exposure,
    manager catalog metadata/promoted split, and runtime MCP trust-policy hash
    changes.
  - Verification passed:
    - `go test -count=1 ./internal/mcpclient ./internal/tools ./internal/gateway ./internal/agent ./internal/runstate`
    - `go test -race -count=1 ./internal/mcpclient ./internal/tools ./internal/agent`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.5 strict runtime config resolution:
  - Added `config.ResolveStrict()` and `ResolvedConfig.StrictError()` so typed
    config parse errors remain visible as redacted/actionable runtime failures
    instead of being swallowed by `MustResolve()` fallback defaults.
  - Swapped local runtime entrypoints to strict resolution: `run`, `chat`, TUI,
    Telegram gateway, `serve`, MCP stdio server, benchmark runners, `tools`,
    `memory`, `sessions context`, and `doctor`.
  - Kept CLI/runtime overrides explicit through resolver provenance for
    `-mock`, `-model`, `-profile`, and `-access-mode`; gateway-client-only run
    paths remain config-independent.
  - TUI `Run` now accepts a pre-resolved config and otherwise resolves strict
    internally. Local TUI MCP status also includes metadata-only MCP initialize
    instructions so it matches the gateway status projection.
  - Regression tests cover invalid typed config, malformed home TOML, `run
    -mock` rejecting invalid runtime config, real provider startup failing
    closed on missing DeepSeek auth, and `doctor` rejecting invalid runtime
    config.
  - Verification passed:
    - `go test -count=1 ./internal/config ./cmd/fast-agent-harness ./internal/tui ./internal/tui/runtimeclient`
    - `go test -count=1 ./internal/bench ./internal/gateway ./internal/telegrambot ./internal/tools`
    - `git diff --check`
    - `go test -count=1 ./...`
    - `go test -race -count=1 ./cmd/fast-agent-harness ./internal/config ./internal/tui/runtimeclient`
- 2026-07-03 P0.6 effective context window for per-session model override:
  - Session context responses now build from the session's effective runtime
    projection instead of the gateway's base runtime limits, so provider/model
    overrides recompute context window and compaction thresholds.
  - Session store config/model snapshots now use the same effective projection;
    `config.snapshot.json` records context window and compaction source labels.
  - Offline `sessions context` now prefers stored `config.snapshot.json`
    runtime limits over the current CLI config, with warnings if the snapshot is
    missing or invalid.
  - Regression tests cover authenticated run-setting model overrides deriving
    the `gpt-5.5` context window, live `/v1/sessions/{id}/context` reporting
    the effective session model/window, persisted config/model snapshots using
    the effective context budget, and offline context reading the stored window.
  - Verification passed:
    - `go test -count=1 ./internal/gateway ./internal/clientux ./cmd/fast-agent-harness`
    - `go test -race -count=1 ./internal/gateway`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.7 validate envelopes during gateway replay:
  - Gateway session event replay now uses the shared eventlog/protocol envelope
    validator for last-seq scans, status restore, and replay/catch-up reads,
    with required nested v1 event envelopes for durable session `events.jsonl`.
  - Protocol v1 envelopes now fail closed on unknown event types and sources;
    eventlog record validation can require nested envelopes and rejects
    record/event sequence mismatches.
  - Replay corruption is surfaced as structured `eventlog.CorruptionError`
    with path, line, record number, and lifecycle/envelope context. Regression
    coverage includes invalid JSON, wrong session ID, skipped seq, repeated
    seq, record/event seq mismatch, unknown event type, unsupported envelope
    version, and missing nested envelope.
  - Synthetic gateway test fixtures were updated to carry the same minimal
    envelope IDs real agent events provide, keeping strict replay semantics
    without weakening production validation.
  - Verification passed:
    - `go test -count=1 ./internal/protocol ./internal/eventlog ./internal/gateway ./internal/clientux/projector`
    - `go test -race -count=1 ./internal/gateway ./internal/eventlog`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.8 extended lifecycle validator:
  - Lifecycle validation now rejects duplicate `tool.call_requested` records
    for a call ID and binds permission/audit/progress/output-ref/user-input
    and tool-scoped hook events back to known call and attempt IDs.
  - Attempt-scoped progress preserves current agent ordering: the
    `attempt_started` progress phase may appear before `tool.call_started`,
    output refs may appear before terminal tool results, and cleanup progress
    phases (`attempt_finished`, `cancel_abort`, `retry_decision`, `finalize`)
    remain valid after a terminal result. Ordinary progress after terminal now
    fails closed.
  - User-input answer/reject events are checked against a prior
    `user_input.requested` when a request ID is present. Tool-scoped hooks are
    rejected if they cite unknown call/attempt IDs; global hooks remain valid.
  - Regression coverage includes orphan progress, orphan output-ref, duplicate
    requested, permission without call, user-input without attempt, hook with
    unknown call, progress after terminal, and a positive output-ref-before-
    terminal plus cleanup-progress trace.
  - The broad test run exposed synthetic output-ref fixtures in gateway/trace
    tests; those fixtures now include the minimal preceding call requested and
    started events required by the stricter lifecycle contract.
  - Verification passed:
    - `go test -count=1 ./internal/eventlog ./internal/agent ./internal/tools ./internal/clientux/projector`
    - `go test -count=1 ./internal/gateway ./internal/trace`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.9 reject duplicate current-turn tool call IDs:
  - Added agent-side validation immediately after provider tool-call
    accumulation finishes and before assistant tool-call messages are appended
    or tool execution/request events can start.
  - Duplicate call IDs now fail the model step and then the run/turn through
    the existing fail path. The emitted error names the duplicated call ID and
    both indexes.
  - Regression tests cover duplicate IDs in a serial-shaped response and a
    parallel-safe response. Both assert no `tool.call_requested` or
    `tool.call_started` events are emitted and that assistant delta text
    streamed before the invalid tool call remains in the event stream.
  - Verification passed:
    - `go test -count=1 ./internal/agent ./internal/tools`
    - `go test -race -count=1 ./internal/agent ./internal/tools`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.10 fail closed on persistence failure:
  - Session event recorders now return `(event, error)` and store-backed
    recorders surface append failures as typed session persistence errors
    instead of returning seq=0 live-only events.
  - Session status/run observation records events durably before publishing to
    the live hub. Terminal run events and their status snapshot are both
    persisted before either is published, so terminal status persistence failure
    no longer produces a healthy live `run.completed`.
  - Gateway session runs cancel the run context on the first persistence error,
    mark the in-memory session status as `persistence_failed`, and return that
    error to the stream so a surfaced `run.failed` is emitted when no durable
    terminal event could be published.
  - Undo/redo turn-change markers now return HTTP 500 if the durable event
    cannot be recorded instead of reporting success.
  - Regression tests cover real filesystem append failure before the first
    event, injected mid-run append failure, and injected terminal append
    failure. Tests assert no normal seq=0 live-only event is treated as healthy
    and session status carries the persistence failure.
  - Verification passed:
    - `go test -count=1 ./internal/gateway ./internal/eventlog`
    - `go test -race -count=1 ./internal/gateway`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.11 fail closed on replay and catch-up errors:
  - Gateway event replay now validates `after_seq` catch-up before writing
    streaming response headers. Missing durable history returns HTTP 409 and
    corrupt durable history returns HTTP 409 with a redacted corrupt-session
    error instead of starting an ambiguous stream.
  - Gateway client status errors now classify typed missing-session, corrupt
    session, replay failure, and no-store history cases. TUI replay and
    Telegram retry paths only fallback-create for the typed missing-session
    error, so corruption/replay failures stay operator-visible.
  - TUI replay non-2xx responses now preserve the gateway status/body as a
    typed client error. Telegram removed the legacy string-matching fallback
    that treated arbitrary `404 session not found` text as safe to recreate.
  - Regression tests cover no-store history, corrupt event history, typed
    gateway-client replay classes, TUI no-fallback on corruption, and Telegram
    missing-session fallback only for typed 404s.
  - Verification passed:
    - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.12 surface corrupt sessions on gateway startup:
  - Session store startup loading now returns a diagnostic sidecar with
    enabled, loaded, error, and corrupt counts plus redacted per-entry load
    errors. Clean sessions still load while corrupt directories stay visible.
  - Gateway startup retains those diagnostics and `/health` now includes a
    `session_store` block when a session store is configured. The block exposes
    session IDs/hashes and line/record metadata without leaking the store path.
  - Startup logs also record skipped entries with session IDs/hashes and
    sanitized error text, so operators have both live health JSON and process
    logs for incident triage.
  - Regression tests cover raw `LoadAllWithDiagnostics` corruption reporting
    and `/health` surfacing the same corrupt-session signal while the clean
    session remains listable.
  - Verification passed:
    - `go test -count=1 ./internal/gateway`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.13 session inspector as debug reducer:
  - Extended stored-session inspection with event validation, terminal-state,
    and projector/parity subreports under the existing JSON/human
    `sessions inspect` command. Added the top-level `inspect-session` alias for
    direct operator use.
  - The validation report surfaces envelope, sequence, lifecycle, unmatched
    progress/output-ref/permission counts, corruption kind, line, and record
    metadata. Corrupt event logs now produce structured inspection warnings
    without exposing raw provider payloads.
  - The terminal/parity reducer reports final run state, terminal event/run,
    transcript-event count, assistant/reasoning byte counts, tool-call parity,
    and sequence-gap metadata from the durable protocol stream. Output-ref
    audit and missing blob checks remain in the same inspection report.
  - Regression tests cover human/JSON inspection output, corrupt event replay
    reported as validation failure, redaction of raw provider payload in
    inspection JSON, and architecture-boundary compliance. A broad test run
    caught a forbidden `gateway -> clientux/projector` import; the reducer was
    adjusted to stay within the existing gateway package boundary.
  - Verification passed:
    - `go test -count=1 ./internal/eventlog ./internal/clientux/projector ./internal/gateway ./cmd/fast-agent-harness ./internal/architecture`
    - `go run ./cmd/fast-agent-harness inspect-session -h`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.14 canonical trace fixtures:
  - Added a shared machine-readable canonical edge-case catalog at
    `internal/testkit/testdata/traces/canonical_edge_cases.json` covering
    stream gaps, duplicate tool-call IDs, parallel cancellation, late
    output-ref audit, provider error after partial stream, invalid tool args,
    MCP catalog change, Telegram interruption, and corrupted replay envelope.
  - Extended testkit with a raw-event catalog reader so consumers decode the
    same fixture data without adding runtime package dependencies or bending
    architecture rules.
  - Eventlog tests validate each fixture against envelope, sequence, and
    lifecycle contracts. Client projector tests replay valid fixtures and check
    gap/cancellation/output-ref/provider-error/MCP/Telegram projection
    behavior. Agent tests pin duplicate executable tool-call rejection and
    invalid-argument metadata. Telegram tests render the interruption fixture
    through live progress and final failure output.
  - Verification passed:
    - `go test -count=1 ./internal/eventlog ./internal/clientux/projector ./internal/agent ./internal/telegrambot`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0.15 local verification script:
  - Added `scripts/verify-local.sh` as the local gate for `git diff --check`,
    read-only dependency verification, `go vet`, full tests, focused race
    packages, optional `--full-race`, govulncheck, binary rebuild, strict
    hygiene, and non-mutating benchmark smoke. The script prints a compact
    summary and exits non-zero on any failed step.
  - Changed `scripts/verify-deps.sh` from temporary `go mod tidy` mutation with
    restoration to read-only `go mod tidy -diff`, while keeping direct
    dependency checks.
  - Bench smoke initially failed because gateway benchmark fixtures predated
    strict event envelopes. Benchmark synthetic events now include required
    run/turn/step IDs, and output-ref benchmark logs include a valid
    requested/started prelude while preserving total record counts.
  - Verification passed:
    - `bash -n scripts/verify-local.sh scripts/verify-deps.sh scripts/bench-smoke.sh`
    - `scripts/verify-local.sh --help`
    - `scripts/verify-deps.sh`
    - `GO_BIN=go scripts/bench-smoke.sh`
    - `scripts/verify-local.sh`
    - `scripts/verify-local.sh --full-race`
- 2026-07-03 P0.16 restore hygiene as a hard gate:
  - Split gateway snapshot/context persistence helpers from
    `internal/gateway/gateway.go` into `internal/gateway/session_snapshot.go`.
    `gateway.go` is now 1380 LOC, below the 1500 source limit.
  - Split large gateway tests into focused files:
    `session_events_status_test.go` and `session_store_replay_test.go`. The
    original `session_events_test.go` and `session_store_test.go` are now below
    the 1200 test limit.
  - Split Telegram runner progress/replay tests into
    `internal/telegrambot/runner_progress_test.go`, bringing
    `runner_test.go` below the 1200 test limit.
  - Strict hygiene now reports `large source files: none`; the full local
    verification script passes with and without `--full-race`.
- 2026-07-03 P1.1 runtime assembly host:
  - Added `internal/runtimehost` as the shared, small runtime assembly package
    for resolved settings projection, provider construction, model capability
    attachment, tool registry/MCP assembly, prompt-submit options, and agent
    creation.
  - CLI `run`/`chat` now build a runtime host instead of separately wiring
    provider, registry, and agent. CLI tool registry helpers now delegate to
    `runtimehost`.
  - TUI local runtime mode now uses `runtimehost` through
    `internal/tui/runtimeclient`; the TUI package no longer needs that adapter
    to import provider/tools/agent directly.
  - Benchmark task execution now builds a runtime host while preserving its
    scripted-provider override path.
  - Gateway settings and per-run agent construction now bridge through
    `runtimehost` while preserving existing public gateway constructors and
    test injection points.
  - Updated the architecture package map for the new package and changed
    runtimeclient's allowed imports accordingly.
  - Added parity/regression tests for config-to-runtimehost projections,
    gateway settings parity, TUI settings alias parity, mock host construction,
    and runtime-diff instruction policy preservation.
  - Verification passed:
    - `go test -count=1 ./internal/runtimehost ./internal/tui/runtimeclient ./internal/bench ./internal/gateway ./cmd/fast-agent-harness ./internal/architecture`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.2 sanitized gateway config DTOs:
  - Replaced the public `/v1/config` response values with
    `gatewayapi.ConfigStatusValue`, which keeps key/value/source/warning/error
    but omits internal `source_key` and `source_path` fields.
  - Added a gateway-side config-status builder that redacts auth/env/path-like
    public config slots such as `api_key_env`, credential/auth files, web API
    key env names, MCP/diagnostic/hook config file lists, and Hermes env-file
    lists.
  - Sanitized the diagnostics projection returned by `/v1/config` so raw
    `config.DiagnosticSource` source keys/paths and auth/env/path-like fields
    stay out of the browser/gateway DTO. CLI/doctor diagnostics keep their
    existing local-debug provenance path.
  - Strengthened the gateway config-status regression test to reject raw
    secrets, `source_key`, `source_path`, temp home paths, exact env var names,
    and credential-file paths while preserving coarse provenance labels such
    as `gateway/tui runtime override`.
  - Verification passed:
    - `go test -count=1 ./internal/gatewayapi ./internal/gateway ./internal/config ./internal/tui ./internal/telegrambot`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.3 profile instructions and provider/model conflicts:
  - One-shot `/v1/run` now builds initial messages from the effective
    run-settings instructions, so profile overrides use their profile prompt
    instead of the gateway server's base profile.
  - Gateway run-settings resolution now rejects explicit provider/model
    conflicts before config defaults can silently reroute them. The check also
    catches explicit provider overrides that conflict with the inherited base
    model, while still allowing custom providers.
  - Added regression tests for effective profile instruction materialization
    and explicit provider/model conflict errors.
  - Verification passed:
    - `go test -count=1 ./internal/gateway ./internal/config ./cmd/fast-agent-harness`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.4 central secret lookup and provenance:
  - Added `config.LookupEnvOrDotenvSource` so runtime credential resolution can
    use the same dotenv discovery order and receive structured source
    metadata.
  - Routed DeepSeek API key lookup and Codex env-token/account lookup through
    one shared credentials helper with the precedence:
    environment -> config dotenv chain -> credential file.
  - Added redacted provenance labels (`env`, `dotenv`, `credential_file`) to
    secret values and provider auth status without exposing secret material.
    Auth status text now prints the provenance label.
  - Added regression tests for config dotenv-source reporting, shared dotenv
    precedence over credential files for both DeepSeek and Codex, credential
    file provenance, env provenance, and redaction-safe formatted status.
  - Verification passed:
    - `go test -count=1 ./internal/config ./internal/credentials ./internal/provider ./internal/gateway ./cmd/fast-agent-harness`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.5 unknown-tool permission lifecycle:
  - Changed registry policy so unknown tool names in a frozen tool snapshot
    fail closed at permission-decision time with `decision=deny`,
    `source=registry`, and `reason=unknown_tool` instead of emitting a
    misleading permission allow before failing during execution.
  - Routed denied-tool result construction through the shared tool policy
    denial message helper. Unknown-tool denials now preserve the existing
    user-visible `unknown tool <name>` content and `unknown_tool` error code;
    policy denials still use the existing `permission_denied` result path.
  - Strengthened the turn-snapshot regression test to assert the durable
    `tool.permission_decided` event is `deny/registry/unknown_tool` for a tool
    registered after the provider request snapshot was taken.
  - Verification passed:
    - `go test -count=1 ./internal/tools ./internal/agent ./internal/mcpclient`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.5 parallel batch aggregate status:
  - Added protocol status `completed_with_errors` for tool-batch steps whose
    parallel child tools all reached terminal states but at least one child
    produced an error.
  - Parallel batch completion events now include `completed_children`,
    `failed_children`, and `aborted_children` aggregate counts in step
    metadata instead of reporting every batch as a clean `completed`.
  - Updated TUI transcript/runtime projections so completed-with-errors
    batches render as explicit partial failures instead of a generic batch
    status.
  - Added regression coverage for mixed success/failure parallel batches and
    transcript batch status rendering.
  - Verification passed:
    - `go test -count=1 ./internal/agent ./internal/protocol ./internal/tui ./internal/tui/transcript ./internal/tui/runtimeclient`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.5 snapshot-bound tool execution policy:
  - Added private execution-context plumbing for `ToolPolicySettings` so
    `ToolSet.Call` injects the frozen per-turn snapshot policy before invoking
    handlers that may still be closures over the original registry.
  - Converted path/root-sensitive built-in handlers to consult the execution
    policy from context for filesystem path admission, relative base
    resolution, file search roots/display paths, shell/diagnostic cwd checks,
    skill discovery roots, and memory instruction settings.
  - Preserved existing stateful registry ownership for managed shell
    processes, MCP manager state, caches, and custom handlers while ensuring
    workspace-root policy is bound to the snapshot used for the provider turn.
  - Added a regression test proving a tool snapshot created from an
    `oldRoot` registry can read from the snapshot's `newRoot` and rejects the
    original root.
  - Verification passed:
    - `go test -count=1 ./internal/tools`
    - `go test -count=1 ./internal/tools ./internal/agent ./internal/mcpclient`
    - `git diff --check`
    - `go test -count=1 ./...`
    - `go test -race -count=1 ./internal/tools ./internal/agent`
- 2026-07-03 P1.5 explicit schema subset:
  - Extended the compact tool-schema validator with `anyOf` support, covering
    the built-in memory read/remove schemas that require either `topic` or
    `path`.
  - Added explicit validation errors for unsupported JSON Schema validation
    keywords and unsupported schema types instead of silently accepting them.
    Annotation-only keys such as `description`, `default`, `$schema`, `title`,
    and `examples` remain allowed.
  - Added regression tests for successful `anyOf` alternatives, failed
    `anyOf` matching, unsupported type diagnostics, and unsupported keyword
    diagnostics.
  - Verification passed:
    - `go test -count=1 ./internal/tools ./internal/mcpclient`
    - `go test -count=1 ./internal/tools ./internal/agent ./internal/mcpclient`
    - `git diff --check`
    - `go test -count=1 ./...`
    - `go test -race -count=1 ./internal/tools ./internal/agent`
- 2026-07-03 P1.5 permission-denial scope note:
  - Inspected the active permission path. The current runtime emits automatic
    permission decisions from registry/policy/access-mode checks; there is no
    user approval, user denial, or approval-timeout workflow yet.
  - Existing runtime reasons now distinguish `unknown_tool`, config policy
    denial (`dangerous_tools_disabled`), and access-mode denial
    (`plan_mode_read_only`, `guarded_mode_dangerous_tools_disabled`).
    Future approval implementation should add explicit `user_denied` and
    `approval_timeout` reasons at the point those states become real.
- 2026-07-03 P1.6 provider backend URL network boundaries:
  - Refactored `webtools.Client.Get` to use a shared public HTTP client
    factory with public-IP dial checks, no proxy, TLS minimums, response
    timeout, and redirect validation.
  - Routed default Tavily/Exa backend POST clients through the same public
    HTTP client, so production backend calls reject localhost, RFC1918,
    link-local, and redirected private targets by default.
  - Preserved explicit injected HTTP clients for tests and controlled
    in-process fixtures; runtime config does not expose such a bypass.
  - Added a backend regression test proving a default Tavily client with a
    private base URL fails with a non-public-IP error.
  - Verification passed:
    - `go test -count=1 ./internal/webtools ./internal/tools`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P2.1 safe MCP status redaction:
  - Expanded MCP server secret provenance to include URL userinfo credentials
    and token-like command argv forms such as `--token value` and
    `--api-key=value`.
  - Sanitized cloned MCP status snapshots so exported/status-listener copies
    redact URL credentials plus generic secret patterns in `last_error`,
    `error`, and `stderr_tail`.
  - Preserved existing server-env and inherited secret-env redaction while
    keeping transport/catalog status fields usable for diagnostics.
  - Added a regression test covering URL credentials, token-like argv values,
    status errors, and stderr snippets.
  - Verification passed:
    - `go test -count=1 ./internal/mcpclient ./internal/tools ./internal/gateway`
    - `git diff --check`
    - `go test -count=1 ./...`
  - Verification passed:
    - `go test -count=1 ./internal/gateway ./internal/telegrambot ./cmd/fast-agent-harness`
    - `go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict`
    - `scripts/verify-local.sh`
    - `scripts/verify-local.sh --full-race`
- 2026-07-03 P2.2 separate MCP transport/catalog state:
  - Added additive MCP status fields `transport_state`, `catalog_state`, and
    structured redacted diagnostics while preserving the legacy compact
    `state` field for compatibility.
  - Catalog states now distinguish `ready`, `connected_no_tools`,
    `tools_fetch_failed`, `catalog_stale`, `disconnected`, `degraded`, and
    `unsupported`. Transport states separately track connection, retry,
    crash, failure, disabled, and unsupported lifecycle.
  - Marked `tools/list_changed` notifications as `catalog_stale` before the
    refresh completes, and report `tools_fetch_failed` with a diagnostic when
    tools/list cannot be fetched.
  - Propagated the state split through `mcpstatus.Format`, `mcp_list_tools`,
    `tool_search`, tool metadata, and dynamic catalog projections.
  - Updated `docs/architecture/tools-mcp-and-policy.md` for the new MCP status
    and catalog-state contract.
  - Added regressions for connected-without-tools, tools/list failure,
    list-changed stale status, human status formatting, and tool-search/list
    metadata visibility.
  - Verification passed:
    - `go test -count=1 ./internal/mcpclient ./internal/mcpstatus ./internal/tools`
    - `go test -count=1 ./internal/gateway`
    - `go test -count=1 ./internal/architecture`
    - `go test -count=1 ./internal/mcpclient ./internal/tools ./internal/gateway`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P2.3 central output-ref settlement:
  - Verified the existing central large-output settlement path: agent tool
    execution routes oversized results through `Agent.compactToolResult`,
    which stores the full plaintext artifact under `tool-output`, annotates
    shared `tooloutput` metadata, and returns a bounded preview.
  - Hardened the runtime contract with a regression proving
    `tool.output_ref_created` is emitted before the terminal tool result and
    before finalize progress for a real large-output tool call.
  - The same regression now asserts attempt/call binding and compact
    `output_ref` metadata on the durable output-ref event.
  - Existing inspector/export coverage remains the source of truth for
    missing-blob diagnostics and raw/rich transcript completeness:
    `InspectStoredSession` reports missing/hash-mismatched output refs, rich
    exports omit lifecycle chatter, and raw exports retain output-ref
    diagnostics.
  - Verification passed:
    - `go test -count=1 ./internal/agent`
    - `go test -count=1 ./internal/tools ./internal/eventlog ./internal/clientux/projector ./internal/tui/transcript ./internal/gateway`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P2.4 filesystem write/checkpoint TOCTOU hardening:
  - Switched overwrite-style `fs_write_file` mutations to the shared
    temp-file-plus-rename writer, preserving existing file modes while
    avoiding direct truncation through a symlink target.
  - Tightened `fs_edit_file` and shared atomic write helpers to reject symlink
    file targets and symlink/non-directory parents before mutation.
  - Kept append mode in-place but added the same symlink target and real-parent
    checks before opening the file.
  - Replaced checkpoint restore's direct `os.WriteFile`/`Chmod` path with
    `atomicRestoreFile` and `restoreDirectory`, both of which fail closed on
    symlink file, directory, and parent-directory targets before writing or
    chmodding.
  - Added regressions proving `fs_write_file`, `fs_edit_file`, and checkpoint
    restore refuse symlink targets and leave pointed-to files unchanged.
  - Verification passed:
    - `go test -count=1 ./internal/tools ./internal/checkpoint`
    - `go test -count=1 ./internal/tools ./internal/checkpoint ./internal/agent`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.7 TUI exact call-ID collapse:
  - Removed the positional fallback from `collapseToolBlockIfLarge`; large
    tool-result blocks now collapse only when the finish event resolves to an
    exact call ID already present in the transcript.
  - Kept unmatched/missing-call-ID results visible as explicit diagnostic tool
    cells instead of auto-collapsing them as the latest tool block.
  - Added a regression covering a reordered exact match, duplicate neighboring
    call IDs, and a missing-call-ID orphan result with large output.
  - Verification passed:
    - `go test -count=1 ./internal/tui`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.8 shared presentation policy for exports:
  - Moved the event presentation policy source of truth into
    `internal/protocol` and kept `internal/clientux/projector` as a thin
    compatibility wrapper, preserving the package boundary guard.
  - Updated transcript export so rich event export uses the same
    `EventPresentationPolicy(...).Transcript` filter as normal TUI projection.
    Raw export remains unfiltered for debug/incident evidence.
  - Added session export regression coverage proving rich exports omit
    low-level progress/output-ref lifecycle chatter while raw exports retain
    those diagnostics.
  - Verification passed:
    - `go test -count=1 ./internal/tui/transcript ./internal/clientux/projector ./internal/protocol ./internal/architecture`
    - `go test -count=1 ./internal/tui ./internal/clientux/projector`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.9 TUI debug snapshot:
  - Added `/status debug` as a redacted TUI runtime snapshot under the existing
    `/status` command, with command-palette argument metadata.
  - Snapshot includes session/gateway identity, last seen seq, stream queue and
    pending-input state, stale markers, transcript block/cell counts, selected
    block IDs, cache/projector state, viewport/reflow metrics, usage counters,
    and context/cost summary.
  - Kept snapshot content-safe by reporting IDs/counts/metadata only; the
    regression seeds secret-looking block content, queued event payloads, and
    tool arguments and verifies they do not appear in `/status debug`.
  - Verification passed:
    - `go test -count=1 ./internal/tui`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P2.5 TUI selection revision and durable export:
  - Added model-level selection invalidation when transcript structure changes,
    when tool/thinking/transcript views hide or restyle rendered rows, and when
    a saved chat session is applied. Live assistant updates that keep block
    identity intact continue preserving highlight selection.
  - Added `/export raw|rich [path]` to the shared client action registry and
    TUI slash actions. With a path it writes the selected transcript mode to
    disk with `0600` permissions; without a path it shows the export in a
    status block.
  - Preserved raw slash arguments for `/export` so case-sensitive filesystem
    paths are not lowercased by the command parser.
  - Added regressions for clearing stale selection when tool rows are hidden
    and for writing an export to an exact path.
  - Verification passed:
    - `go test -count=1 ./internal/tui ./internal/clientux`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.10 Telegram pending-input outbox:
  - Made Telegram prompt admission require durable chat-state persistence of
    `PendingInputID`/`PendingUpdateID` before the poller can acknowledge the
    update or dispatch the run. If that save fails, the update remains
    retryable and no run starts.
  - Added startup reconciliation for pending inputs left by a previous process:
    the bot records an `abandoned` admission JSONL event, logs the affected
    key/session/input/update, clears the pending fields, and saves state before
    becoming runnable.
  - Kept the existing admission JSONL as the operator evidence trail by adding
    `RecordAbandoned` alongside admitted/ignored records.
  - Added regressions for restart reconciliation and for state-persistence
    failure between gateway admission and run dispatch.
  - Verification passed:
    - `go test -count=1 ./internal/telegrambot ./internal/gateway`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P1.11 Telegram allow-user scope policy:
  - Tightened `-allow-user` so allowed user IDs authorize private chats by
    default, while group/supergroup messages require the chat itself to be
    allowlisted.
  - Added explicit broad opt-in via `-allow-user-groups` /
    `BILLYHARNESS_TELEGRAM_ALLOW_USER_GROUPS` for the old cross-chat user
    behavior.
  - Exposed the effective allowed-user scope in Telegram `/status` output.
  - Added authorization regressions for private chat, allowed group,
    disallowed group, unknown user, and explicit broad user-in-groups scope.
  - Verification passed:
    - `go test -count=1 ./internal/telegrambot`
    - `go test -count=1 ./cmd/fast-agent-harness`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P2.6 Telegram gateway error redaction:
  - Added a Telegram outbound redaction helper that sanitizes credential URLs,
    secret query parameters, header-like credentials, and shared
    `internal/secrets` patterns before text leaves the adapter.
  - Applied the helper at delivery boundaries for sends, edits, progress edits,
    rich markdown send/edit payloads, and dry-run logs.
  - Redacted renderer-held run failures, tool failure summaries, progress
    messages, and final error chunks before Telegram delivery.
  - Added regressions for the redaction helper, rendered run failures, and a
    real `/mcp` command error containing tokens, credential URLs, and
    header-looking secrets.
  - Updated `docs/architecture/telegram-and-operator-surfaces.md` and
    `docs/architecture/security-model.md` because Telegram-facing redaction
    semantics changed.
  - Verification passed:
    - `go test -count=1 ./internal/telegrambot ./internal/gateway`
    - `go test -count=1 ./internal/architecture`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P0/P1 operator doctor and incident bundle slice:
  - Added `fast-agent-harness incident collect -session SESSION_ID -out DIR`
    as a local operator bundle command. It uses strict runtime config
    resolution, refuses to continue when the target persisted session cannot be
    inspected, and writes private `0600` artifacts under a private output dir.
  - Incident bundles include redacted doctor JSON/text, config/auth summaries,
    optional MCP status, session inspection/context, rich and raw transcript
    exports, a redacted session event JSONL copy, optional `journalctl` tails
    for managed gateway/Telegram services, and `incident-manifest.json`.
  - Added incident redaction for credential URLs, secret query params,
    header-shaped credentials, JSONL escaped newlines, and shared
    `internal/secrets` patterns before any bundle artifact is written.
  - Added `doctor -deep/--deep` as a compatibility/operator flag so the TODO
    verification shape works with existing human and JSON doctor output.
  - Updated `README.md`, `llms.txt`, `ops/README.md`,
    `ops/doctor-and-diagnostics.md`, and `agent-index/docs-manifest.json` for
    the new operator workflow.
  - Added a regression that creates a real stored gateway session with a mock
    run containing `sk-...`, credential URLs, query tokens, auth headers,
    API-key headers, and cookies, then proves collected transcript/event
    artifacts do not leak those values.
  - Remaining operator follow-up: admin-only TUI/Telegram `/doctor`, `/logs`,
    and `/incident` surfaces are still deferred; the local CLI bundle is the
    completed narrow safe slice.
  - Verification passed:
    - `go test -count=1 ./cmd/fast-agent-harness`
    - `go test -count=1 ./internal/architecture`
    - `go run ./cmd/fast-agent-harness doctor --deep --json`
    - `go run ./cmd/fast-agent-harness doctor --deep --json -build=false -services=false -gateway=false`
    - `go run ./cmd/fast-agent-harness incident collect -h`
    - `git diff --check`
    - `go test -count=1 ./...`
  - Non-blocking observed local doctor findings: the exact `doctor --deep
    --json` command exits 0 in non-strict mode but reports the current dirty
    worktree, missing local gateway/service binary/auth files, unavailable
    `systemctl` on this Mac, and strict hygiene large-file failures.
- 2026-07-03 P1 production deploy/service source of truth:
  - Confirmed the active operations lane is `ops/`, not `docs/`, and
    `ops/production-services.md` already covers production entrypoint, managed
    service names, build, install-time verification, restart, status, journal
    tails, doctor, gateway auth/binding, Telegram service flags, and restart
    triage.
  - Added an explicit rollback pattern that records current commit/binary/
    service/doctor state, rebuilds a previous known-good checkout when the live
    host uses source deploys, restarts both managed services, and cautions
    operators to use the live release mechanism when production is archive,
    symlink, package, or release-directory based.
  - Verification passed:
    - `git diff --check`
    - `go test -count=1 ./internal/architecture`
- 2026-07-03 P2.7 auditable compaction checkpoints:
  - Added `context_epoch` and `previous_context_epoch` to emitted
    `context.compacted` reports; the runtime increments the epoch after every
    compaction before the next model call.
  - Added audit hashes to compaction reports: compacted input span,
    replacement message, summary text, pre-history state, and post-history
    state, plus pre/post in-memory history sequence counts.
  - Refreshed replacement/post-history hashes after model-summary compaction so
    deterministic and helper-model strategies both report the final replacement
    state.
  - Exposed the latest compaction epoch and post-history hash through
    `GET /v1/sessions/{id}/context`, `gatewayclient.FormatSessionContext`, and
    TUI compaction status text.
  - Added replay coverage proving `StoredSessionContext` reconstructs the
    latest compaction epoch/hash from durable session JSONL events.
  - Updated `docs/architecture/runtime-event-system.md` and
    `docs/architecture/gateway-and-sessions.md` for the new event/context
    projection contract.
  - Verification passed:
    - `go test -count=1 ./internal/agent ./internal/clientux ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/tui/transcript`
    - `go test -count=1 ./internal/architecture`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P2.8 epoch-aware threshold events:
  - Added `context_epoch` and `threshold_key` to `context.threshold` events.
    The key is emitted as `epoch:<epoch>/<percent>` for operator correlation.
  - Reset the per-percent threshold emission map immediately after
    `context.compacted` advances the context epoch, so pre-compaction warnings
    no longer suppress threshold warnings for the new active context.
  - Propagated threshold epoch/key through the shared client projector and TUI
    threshold block rendering.
  - Updated `docs/architecture/runtime-event-system.md` to specify
    once-per-percent-per-context-epoch semantics.
  - Added regressions for once-per-epoch threshold emission and TUI rendering
    of epoch/key metadata.
  - Verification passed:
    - `go test -count=1 ./internal/agent ./internal/clientux/projector ./internal/tui ./internal/tui/transcript`
    - `go test -count=1 ./internal/architecture`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P2.9 memory drift policy:
  - Chose session-locked memory semantics for existing transcripts: active
    sessions keep the memory context they were created with, and new memory is
    not promoted into an existing session implicitly.
  - Added hash-only memory drift diagnostics to session context responses.
    Live gateway `/context` compares the locked session memory hash with the
    current rendered memory hash for the session profile and reports
    `current`, `changed`, `missing`, or `added` with policy, shortable hashes,
    counts, cap state, and generic error markers.
  - Offline `sessions context` reports the locked session hash only instead of
    pretending it can prove live memory state from the durable bundle.
  - Kept the gateway package boundary intact after `internal/architecture`
    caught a direct `gateway -> memory` import; the final implementation asks
    the agent transcript builder for a memory-only rendered context and hashes
    that block locally.
  - Updated `docs/architecture/gateway-and-sessions.md` and
    `docs/architecture/runtime-event-system.md` for the hash-only memory drift
    contract.
  - Added a regression that mutates the memory index after session creation and
    proves live context reports `changed`, formatted output shows only
    status/hash metadata, and offline context remains `locked`.
  - Verification passed:
    - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/clientux`
    - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/clientux ./internal/architecture`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 P2.10 context diagnostics index:
  - Added a compact derived `diagnostics` object to session context responses
    with current epoch, compaction/threshold/tool/helper counts,
    protected-prefix versus body token split, window and compaction margins,
    and hashes for memory, project, AGENTS, MCP instructions, prompt inventory,
    and latest compaction post-history state.
  - Taught context source classification to distinguish AGENTS and MCP
    instruction blocks from ordinary user messages so source/token accounting
    lines up with stable prompt sections.
  - Extended `gatewayclient.FormatSessionContext` with a single diagnostics
    line using short hashes and compact counts.
  - Updated `docs/architecture/gateway-and-sessions.md` and
    `docs/architecture/runtime-event-system.md` to describe the derived
    diagnostics index and its replay/debug role.
  - Added clientux regressions covering protected/body split, memory/project/
    AGENTS hashes, compaction and threshold event counts, helper/tool counts,
    margin fields, and formatted diagnostics output.
  - Verification passed:
    - `go test -count=1 ./internal/clientux ./internal/gatewayclient ./internal/gateway`
    - `go test -count=1 ./internal/architecture`
    - `go test -count=1 ./internal/agent ./internal/gateway ./internal/tui ./internal/clientux ./internal/gatewayclient`
    - `git diff --check`
    - `go test -count=1 ./...`
- 2026-07-03 final verification and status:
  - Fixed final-verification fallout before claiming the loop complete:
    `go vet` caught `telegramAdmissionStore` being copied by value despite its
    mutex, so the admission store now uses pointer ownership; strict hygiene
    still caught large-file gates, so helper/test splits restored the file-size
    budget without weakening the gate.
  - Final verification passed:
    - `git diff --check`
    - `go vet ./...`
    - `go test -count=1 ./...`
    - `go test -race -count=1 ./...`
    - `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
    - `go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict`
    - `go build ./cmd/fast-agent-harness`
    - `scripts/verify-local.sh --full-race`
  - `govulncheck` reported `No vulnerabilities found.`
  - `scripts/verify-local.sh --full-race` passed its full sequence:
    diff-check, dependency metadata, vet, normal tests, focused race tests,
    full race tests, govulncheck, binary rebuild, strict hygiene, and bench
    smoke.
  - Current completion boundary: all named P0/P1/P2 milestones in this TODO
    have implementation evidence and final verification is green. Deferred
    follow-ups remain the admin-only TUI/Telegram `/doctor`, `/logs`, and
    `/incident` surfaces recorded in the operator diagnostics slice, plus the
    optional Additional Reliability Backlog. This file stays in
    `loop-develop/current-todo/003-todo.md` for Billy's main-chat verification.
- 2026-07-04 main-chat verification:
  - Billy reported the second implementation loop finished and asked this chat
    to verify it.
  - Re-read `AGENTS.md`, inspected the active TODO/worktree, and confirmed
    `003-todo.md` was the only active TODO in `loop-develop/current-todo`.
  - Verification command passed:
    - `scripts/verify-local.sh --full-race`
  - The script passed its full sequence: `git diff --check`, dependency
    metadata check, `go vet ./...`, `go test -count=1 ./...`, focused race
    packages, `go test -race -count=1 ./...`, `govulncheck`, binary rebuild,
    strict hygiene, and bench smoke.
  - `govulncheck` reported `No vulnerabilities found.`
  - Branch state at verification: `main` was ahead of `origin/main` by 1
    commit before staging/committing this TODO closeout; no push was performed
    in this verification step.
  - Remaining follow-ups: admin-only TUI/Telegram `/doctor`, `/logs`, and
    `/incident` surfaces remain deferred as already recorded; no blocker for
    closing this loop.
  - Final status: completed and verified; moved from
    `loop-develop/current-todo` to `loop-develop/history` by the main
    verification chat.

## Copy-Ready Codex Goal Prompt

```text
/goal You are in /Users/billy/repos/billyharness. Read AGENTS.md first, then read loop-develop/current-todo/003-todo.md completely. Work the TODO as the implementation agent.

Mission: harden Billyharness into a safer, more debuggable, production-grade agent harness. Prioritize P0 only until it is green. Do P1/P2 only when required by a P0 fix or clearly cheap. Keep active work in loop-develop/current-todo/003-todo.md; do not move it to history.

Non-negotiables:
- Native repo patterns first; no competitor source copying.
- Durable JSONL events are the source of truth.
- Runtime security must fail closed on auth, ownership, persistence, replay, and config corruption.
- Gateway owns session identity/scope checks centrally.
- MCP/server instructions are untrusted metadata unless explicitly promoted by policy.
- TUI/Telegram/export views are projections of shared event/presentation contracts.
- Keep docs/ architecture-only. Put implementation evidence in this TODO.
- Do not revert unrelated user changes.

Implementation order:
1. P0 security boundaries:
   - Gateway loopback/browser mutation auth, Origin/Host/content-type checks, server-side privilege clamps.
   - Telegram secret-bearing auth command safety.
   - Central session owner/scope enforcement.
   - MCP initialize instructions untrusted by default.
   - Strict runtime config resolution.
   - Effective context window for per-session provider/model override.
2. P0 event/session integrity:
   - Validate envelopes during gateway replay.
   - Extend lifecycle validator to duplicate/orphan progress/permission/output-ref/user-input/hook cases.
   - Reject duplicate current-turn tool call IDs before execution.
   - Fail closed on persistence failure.
   - Fail closed on replay/catch-up corruption.
   - Surface corrupt sessions on gateway startup.
3. P0 debug and verification baseline:
   - Session inspector/debug reducer.
   - Canonical trace fixtures.
   - scripts/verify-local.sh.
   - Restore strict hygiene file-size gate.
4. Then continue into P1/P2 runtime assembly, sanitized DTOs, provider/model conflicts, secret provenance, schema validation, MCP/web hardening, TUI/Telegram/operator diagnostics, and context compaction audit.

As you work:
- Update the Evidence Log in loop-develop/current-todo/003-todo.md after each meaningful completed slice.
- Keep edits scoped and well-tested.
- Prefer small cohesive commits only if Billy explicitly asks for commits.
- If a P0 item reveals a bigger architecture issue, write the narrow safe fix first, then add follow-up detail to the TODO.

Minimum verification after each slice:
- git diff --check
- targeted go test packages for touched code
- broader go test -count=1 ./... when runtime/gateway/TUI/Telegram/provider/tool/event code changes

Final verification before claiming done:
- git diff --check
- go vet ./...
- go test -count=1 ./...
- go test -race -count=1 ./...
- go run golang.org/x/vuln/cmd/govulncheck@latest ./...
- go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict
- go build ./cmd/fast-agent-harness
- scripts/verify-local.sh --full-race if the script exists

Do not move 003-todo.md to history. Leave final status, commands, evidence, and remaining blockers in the TODO for Billy's main chat verification.
```
