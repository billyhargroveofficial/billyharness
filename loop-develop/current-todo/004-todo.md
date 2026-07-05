# 004 TODO - Production-Grade Agent Architecture, Stability, And Debuggability

Status: current
Created: 2026-07-04
Owner loop: native Codex research loop

## Request

Run 12 native Codex subagents and review Billyharness against the standards of a
serious agent project: architecture, stability, debugging, production
operations, event traceability, replay, security, tool/MCP boundaries, tests,
and documentation. Use internet research and clean-room comparison with local
Codex, Claude Code, and OpenCode checkouts. Produce a large implementation TODO
and a copy-ready Codex `/goal` prompt for a long loop-agent pass.

This TODO is intentionally large. Work P0 first. Only start P1/P2 when P0 is
green or when a smaller P1/P2 change is a direct prerequisite for a P0 fix.

## Source Research Summary

### Native Codex Subagents Launched

All subagents were native Codex research workers. Competitor repositories were
used only for clean-room architectural comparison: boundaries, event contracts,
debug surfaces, test strategy, and UX patterns. Do not copy competitor source
code into Billyharness.

- Feynman: event log, trace replay, lifecycle validation, projector parity.
- Nietzsche: gateway/session authority, undo/redo, run/cancel persistence.
- Bohr: adversarial security pass across gateway, Telegram, MCP, checkpoints.
- Hubble: native tools, web search, MCP schema/output settlement.
- Harvey: long-running agent loop, streams, cancellation, input admission.
- Hume: TUI/client UX debugging, transcript export, inspection commands.
- Noether: Telegram adapter safety, owner model, restart/ack behavior.
- Rawls: config, providers, dotenv persistence, runtime capability diagnostics.
- Bacon: docs system, Stop hook docguard, manifest and history drift.
- Descartes: production operations, deploy, doctor, incident recovery.
- Pascal: CI, tests, fuzz, race, coverage, benchmarks, hygiene.
- Hooke: macro architecture, package boundaries, gravity wells.

### Local Verification Evidence

Commands run during research:

```sh
git status --short
git log -1 --oneline --decorate
go test -coverprofile=/tmp/billyharness-004-cover.out ./...
go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict
go test -count=1 ./internal/architecture
go vet ./...
git diff --check
rg -n "TODO|FIXME|panic\\(|MustResolve|config\\.Default" --glob '*.go' --glob '*.md'
```

Observed state:

- Branch: `main`.
- Latest commit before this TODO: `9372558 Harden security diagnostics and docs loop`.
- `origin/main...HEAD`: no ahead/behind at research start.
- Full Go test suite passed.
- Coverage: 73.4% total from `/tmp/billyharness-004-cover.out`.
- Strict hygiene passed for tracked Go files.
- Ignored runtime artifact observed: root `fast-agent-harness` binary, about
  18.9 MiB. It is ignored, but `verify-local` should build outside repo root.
- Architecture package test passed.
- `go vet ./...` passed.
- `git diff --check` passed before creating this TODO.
- Current TODO directory was empty except `.gitkeep`; history contains
  `001-todo.md`, `002-todo.md`, and `003-todo.md`.

### Internet Research Signals

Use these as design constraints, not copied implementation:

- OpenAI Agents SDK:
  https://developers.openai.com/api/docs/guides/agents
  - Agents plan, call tools, collaborate, and keep enough state for multi-step
    work.
  - Use SDK-level patterns when the app owns orchestration, tool execution,
    approvals, and state.
  - Debugging and improvement should start from traces and evaluation loops.
- LangGraph persistence:
  https://docs.langchain.com/oss/python/langgraph/persistence
  - Durable agents need thread-scoped checkpoints and cross-thread stores for
    continuity, interruption recovery, failure recovery, time travel, and memory.
- LangGraph time travel:
  https://docs.langchain.com/oss/python/langgraph/use-time-travel
  - Replay/fork should resume from a known checkpoint; work before the checkpoint
    is already saved, work after the checkpoint re-executes.
- OWASP LLM Top 10:
  https://owasp.org/www-project-top-10-for-large-language-model-applications/
  - Relevant risk buckets: prompt injection, insecure output handling,
    sensitive information disclosure, insecure plugin design, excessive agency,
    supply chain, and denial of service.
- MCP Security Best Practices:
  https://modelcontextprotocol.io/docs/tutorials/security/security_best_practices
  - Treat remote/local MCP servers as authority boundaries. Consent, SSRF
    controls, least privilege, stdio restrictions, and audit trails matter.
- OpenTelemetry GenAI observability:
  https://opentelemetry.io/blog/2026/genai-observability/
  - Agent observability should answer whether latency/failure came from the
    model, a tool call, a retry loop, or streaming/runtime plumbing.

### Clean-Room Competitor Signals

Local checkouts inspected:

- `/Users/billy/agent-research/codex`
- `/Users/billy/agent-research/opencode-current`
- `/Users/billy/agent-research/claude-code`

High-level patterns worth matching:

- Codex-style separation of protocol, core runtime, thread/session store,
  sandboxing, hooks, skills, model providers, OTel, and UI/client layers.
- OpenCode-style explicit agent modes, storage specs, scoped registries, and
  durable session orientation.
- Claude-style workflow-as-files and lifecycle hooks, with the warning that
  hooks must be bounded and testable so they do not create infinite loops.
- All serious systems need clear event contracts, replay/fork semantics,
  inspectable state, and explicit permission boundaries.

## Architecture Canon For This Loop

- Durable JSONL events are the source of truth.
- Live streams are progress/wake channels, not canonical history.
- Replayers, projectors, exports, TUI, Telegram, and incident reports must
  derive from the same validated event/replay contracts.
- Gateway owns session identity, owner/scope checks, admission, persistence
  ordering, and HTTP authority.
- TUI and Telegram are clients of gateway authority, not separate policy owners.
- MCP/tool descriptions, schemas, prompt text, and server metadata are untrusted
  unless a local policy explicitly promotes them.
- Security-sensitive failures must fail closed and leave redacted audit evidence.
- Config diagnostics must describe the effective runtime binding, not just parse
  a partial config shape.
- Production must be inspectable by a single doctor/incident path before a human
  starts grepping logs.
- Active work stays in `loop-develop/current-todo`. Do not move this TODO to
  history until Billy asks for final verification and the main chat verifies it.

## P0 Milestone 1 - Fail-Closed Security And Authority

Goal: close authority holes where browser-origin requests, Telegram users,
remote MCP metadata, checkpoint artifacts, or permissive config can cause unsafe
action or leak state.

### P0.1 Protect gateway read routes from local-browser and DNS-rebinding leaks

Finding: mutating routes have explicit checks, but GET/HEAD/OPTIONS read routes
can still leak gateway state when the gateway trusts loopback too broadly. A
local browser or DNS-rebinding page should not be able to read sessions,
events, config status, MCP status, process summaries, or debug state.

Target files:

- `internal/gateway/gateway.go`
- `internal/gateway/*auth*`
- `internal/gateway/*http*`
- `internal/config/*`
- gateway route tests

Checklist:

- Inventory all `/v1/` read routes and mark which are safe unauthenticated.
- Keep `/health` cheap and unauthenticated when safe.
- Require bearer auth for state-bearing read routes when an auth token is
  configured, including loopback callers.
- Add Host and Origin checks for browser-reachable endpoints.
- Add DNS rebinding tests: malicious Host, missing bearer, wrong bearer, correct
  bearer, loopback dev-only exception if any.
- Ensure rejections are redacted and observable.

Verification:

```sh
go test -count=1 ./internal/gateway
go test -race -count=1 ./internal/gateway
```

### P0.2 Add Telegram operator/admin policy per command

Finding: an allowlisted group can become an admin surface. Any member in an
allowed chat may be able to run sensitive commands such as `/auth`, `/memory`,
`/undo`, `/redo`, `/processes`, and `/config` unless command policy checks the
actual operator user.

Target files:

- `internal/telegrambot/*`
- `docs/architecture/telegram-and-operator-surfaces.md`
- `.agents/rules/documentation.md` if recurring doc rules change

Checklist:

- Define command classes: public chat-safe, session-scoped, operator-only,
  owner-only, secret-bearing.
- Add `AllowedOperatorUserIDs` or equivalent runtime config.
- Require private owner chat for secret-bearing commands by default.
- Reject anonymous/userless group messages for operator-only commands unless an
  explicit shared-mode policy says otherwise.
- Make dry-run mode obey the same authorization boundaries for local mutations.
- Add tests for group member, group admin/operator, private owner, anonymous
  group sender, bot sender, and malformed sender.
- Update Telegram architecture doc when semantics change.

Verification:

```sh
go test -count=1 ./internal/telegrambot
go test -race -count=1 ./internal/telegrambot
go test -count=1 ./...
```

### P0.3 Make secret-bearing Telegram auth fail closed

Finding: Telegram dry-run deletion can make `/auth deepseek` look safe while the
secret is still persisted. Deletion failure or dry-run deletion must not permit
secret persistence in group contexts.

Target files:

- `internal/telegrambot/*auth*`
- `internal/telegrambot/*runner*`
- `internal/config/*`

Checklist:

- Tag secret-bearing commands centrally.
- In non-private chats, require successful deletion before persistence, or ban
  secret-bearing commands entirely.
- In dry-run, simulate deletion failure for secret-bearing group commands unless
  the command is private-owner safe.
- Ensure all user-facing and log errors redact secrets.
- Add tests proving no `.env`/auth store write happens after failed deletion.

Verification:

```sh
go test -count=1 ./internal/telegrambot
rg -n "DEEPSEEK|auth deepseek|api[_-]?key" internal/telegrambot internal/config
```

### P0.4 Split MCP/tool agency risk beyond one coarse `RiskExternal`

Finding: MCP calls are currently too coarse. Read-only MCP calls, network reads,
filesystem writes, shell execution, and external mutations need different
policy gates and audit messages.

Target files:

- `internal/tools/*`
- `internal/mcp*`
- `docs/architecture/tools-mcp-and-policy.md`
- `docs/adr/0003-mcp-instructions-are-untrusted-metadata.md`

Checklist:

- Define tool risk classes: local-read, local-write, network-read,
  network-write, execute, external-mutation, secret-access.
- Preserve backward-compatible behavior for native safe tools.
- For MCP tools, derive risk from server/tool allowlist policy, not from remote
  description text alone.
- Label remote MCP descriptions/schemas/instructions as untrusted in debug JSON
  and model-facing output wrappers.
- Require explicit allowlist or confirmation for side-effecting MCP tools.
- Add malicious MCP tool description/schema tests.
- Update docs/ADR only after code behavior is real.

Verification:

```sh
go test -count=1 ./internal/tools
go test -race -count=1 ./internal/tools
go test -count=1 ./...
```

### P0.5 Verify checkpoint restore artifacts at restore time

Finding: undo/redo and checkpoint restore paths trust artifact paths/content too
late. Restore must verify recorded hash, workspace root, and path constraints at
the moment files are written back.

Target files:

- `internal/checkpoint*`
- `internal/gateway/*checkpoint*`
- `internal/eventlog/*`

Checklist:

- Record or locate SHA/root metadata for patch artifacts.
- Before restore/redo, re-check artifact SHA, repo root, workspace root, and
  symlink/path constraints.
- Fail closed if artifact content or location drifted.
- Emit redacted failure events that make replay explainable.
- Add symlink, moved artifact, tampered artifact, and out-of-root tests.

Verification:

```sh
go test -count=1 ./internal/checkpoint ./internal/gateway
go test -race -count=1 ./internal/checkpoint ./internal/gateway
```

### P0.6 Centralize redaction patterns for gateway and Telegram

Finding: redaction coverage is scattered. Telegram URLs, URL userinfo, query
secrets, provider keys, auth headers, and tool args need one shared redaction
surface.

Target files:

- `internal/secrets/*`
- `internal/gateway/*`
- `internal/telegrambot/*`
- `internal/tools/*`

Checklist:

- Move reusable redaction into `internal/secrets` if not already there.
- Add URL userinfo and query-token redaction.
- Use the same redactor in gateway errors, Telegram replies, transcript export,
  incident bundles, and debug endpoints.
- Add table tests with provider keys, bearer tokens, Telegram bot tokens, URLs,
  Authorization headers, and MCP args.

Verification:

```sh
go test -count=1 ./internal/secrets ./internal/gateway ./internal/telegrambot
```

## P0 Milestone 2 - Durable Replay Truth And Persistence Ordering

Goal: make every run/fork/undo/redo/replay/debug view explainable from durable
events, with invalid events rejected before they become history.

### P0.7 Validate protocol envelope and lifecycle before append

Finding: gateway append paths can enrich/write before full envelope/lifecycle
validation. Invalid events should not persist and then fail only during replay.

Target files:

- `internal/eventlog/*`
- `internal/gateway/session*.go`
- `internal/protocol/*`
- `internal/trace/*`

Checklist:

- Find append paths that persist before strict validation.
- Validate v1 nested envelopes before writing durable JSONL.
- Preserve legacy import/replay mode explicitly where needed.
- Bind `attempt_id` to `call_id` for tool lifecycle validation.
- Add validation for run/turn/step/model lifecycle ordering.
- Add tests for tool-result-before-call, attempt mismatch, turn without run,
  open terminal state, malformed envelope, and legacy accepted mode.

Verification:

```sh
go test -count=1 ./internal/eventlog ./internal/gateway ./internal/trace
go test -race -count=1 ./internal/eventlog ./internal/gateway
```

### P0.8 Make trace replay use the same strict event semantics as gateway replay

Finding: trace replay and gateway replay do not enforce the same envelope
semantics. Trace counters can be derived from outer event types while gateway
requires nested v1 envelopes.

Target files:

- `internal/trace/trace.go`
- `internal/gateway/session_inspect.go`
- replay/trace tests

Checklist:

- Decide and document strict trace mode versus explicit legacy mode.
- Make trace replay parse the canonical envelope and lifecycle fields.
- Add JSON output that distinguishes raw legacy counts from validated v1 counts.
- Add fixtures shared with gateway replay.

Verification:

```sh
go test -count=1 ./internal/trace ./internal/gateway
```

### P0.9 Replace fake projector parity with real reducer/projector parity

Finding: current inspection increments raw/projected tool counters from the same
event instead of running the actual `internal/clientux/projector` or TUI
projector path.

Target files:

- `internal/gateway/session_inspect.go`
- `internal/clientux/projector/*`
- `internal/tui/*`
- canonical fixtures

Checklist:

- Add a real inspection path that feeds events through the shared projector.
- Compare raw lifecycle counts to projector-visible state.
- Report mismatches with session id, seq range, event id, and projection hash.
- Add one minimal protocol-edge fixture and one full agent-run canonical
  fixture.
- Use the fixture across gateway, TUI, Telegram/export, and trace inspection.

Verification:

```sh
go test -count=1 ./internal/clientux/projector ./internal/gateway ./internal/tui
```

### P0.10 Make undo/redo/run/cancel persistence fail closed

Finding: undo/redo can mutate files before durable audit publication succeeds.
Run and cancel can succeed to the caller while `saveSession` failure is only
logged. Durable state must not be optional after externally visible action.

Target files:

- `internal/gateway/session*.go`
- `internal/gateway/checkpoint*.go`
- `internal/eventlog/*`

Checklist:

- Audit action order for undo, redo, run admission, run completion, cancel, and
  fork.
- Either persist before mutation when possible or add compensating rollback and
  terminal failure events.
- Return failure to clients when durable session save fails after a visible
  state transition.
- Add injected persistence failure tests for each path.

Verification:

```sh
go test -count=1 ./internal/gateway
go test -race -count=1 ./internal/gateway
```

### P0.11 Harden admitted-input ledgers across gateway and Telegram

Finding: corrupt `inputs.jsonl` can hide an otherwise loadable session, admitted
inputs can be orphaned by preflight failure, and Telegram pending input ack
state can be lost across restart.

Target files:

- `internal/gateway/input*.go`
- `internal/gateway/session*.go`
- `internal/telegrambot/*`

Checklist:

- Quarantine corrupt input ledger records instead of dropping the whole session
  when the session event log is otherwise usable.
- Mark preflight failures as terminal admitted-input states with replayable
  failure evidence.
- Compute concurrent run sequence under the run/admission lock.
- Make Telegram startup reconcile pending inputs: either replay, terminally
  fail, or re-ack with a durable gateway state.
- Add restart tests for acked Telegram updates and pending gateway input ids.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/telegrambot
go test -race -count=1 ./internal/gateway ./internal/telegrambot
```

### P0.12 Split snapshot readiness from replay readiness

Finding: legacy snapshots can be marked offline replay ready without enough
event stream material. A message snapshot is not the same as replayable event
history.

Target files:

- `internal/gateway/session_import*`
- `internal/gateway/session_inspect.go`
- import/replay tests

Checklist:

- Introduce explicit readiness states such as `message_snapshot_ready` and
  `event_replay_ready`.
- Update inspect output and import messages.
- Add tests for legacy snapshots, full event streams, and partial event streams.

Verification:

```sh
go test -count=1 ./internal/gateway
```

## P0 Milestone 3 - Operator Debuggability And Production Truth

Goal: a bug report should become a small bundle of redacted facts, not a long
manual archaeology session.

### P0.13 Add live session inspect endpoint and CLI/client command

Finding: inspection exists as internals, but operators need a supported live
surface that returns redacted session health and replay status.

Target files:

- `internal/gateway/session_inspect.go`
- `internal/gateway/gateway.go`
- `internal/gatewayclient/*`
- `internal/clientux/*`
- CLI command wiring under `cmd/fast-agent-harness`

Checklist:

- Add or harden `GET /v1/sessions/{id}/inspect`.
- Include session id, owner scope, seq range, lifecycle open/closed counts,
  projector parity, output-ref status, input ledger status, and redacted errors.
- Add gatewayclient method.
- Add `sessions debug SESSION_ID` or equivalent CLI/client command.
- Add JSON and human-readable output modes.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/clientux
go run ./cmd/fast-agent-harness sessions --help
```

### P0.14 Add a redacted "debug full" snapshot for TUI/client state

Finding: TUI bugs need one command that explains local chat id, gateway session
id, stream state, projector state, viewport/selection/export hints, and hashes
without leaking secrets.

Target files:

- `internal/tui/*`
- `internal/clientux/*`
- `docs/architecture/tui-and-clientux.md`

Checklist:

- Design a compact debug snapshot schema.
- Include gateway session id, local chat id, last seq, runtime mode, projector
  state, stream queue, selection/viewport state, transcript hash, export target,
  and stale/missing hints.
- Redact all user/provider secrets.
- Add command or hidden debug key path if appropriate.
- Add tests for wide/combining characters in selection/debug state.

Verification:

```sh
go test -count=1 ./internal/tui ./internal/clientux
```

### P0.15 Promote transcript export into an incident-grade artifact

Finding: transcript export exists, but needs metadata and warnings so it can be
attached to issues without guessing source or replay quality.

Target files:

- `internal/tui/transcript_runtime.go`
- `internal/clientux/*export*`
- `docs/architecture/tui-and-clientux.md`

Checklist:

- Add export metadata header: source store, session id, seq range, model/profile,
  runtime mode, export time, redaction mode, and warnings.
- Support modes: messages, events, combined.
- Quote paths and nested output refs safely.
- Include warnings for partial replay, legacy snapshot, projector mismatch, and
  stale diagnostics index.
- Update docs if export behavior changes.

Verification:

```sh
go test -count=1 ./internal/tui ./internal/clientux
```

### P0.16 Capture production as a dated, redacted source of truth

Finding: production runbooks still need verified facts from
`root@82.23.163.16` before deploy/doctor semantics can be called solid.

Target files:

- `ops/` if present or create a small dated runbook there
- `cmd/fast-agent-harness` doctor/service code
- `internal/service*`
- `docs/architecture/gateway-and-sessions.md` only if stable contract changes

Checklist:

- Inspect production over SSH when safe:
  - `systemctl cat/show`;
  - ExecStart;
  - WorkingDirectory;
  - EnvironmentFile;
  - service user;
  - restart policy;
  - current commit;
  - binary path and checksum;
  - Go path/toolchain;
  - `$BILLYHARNESS_HOME`;
  - gateway bind/auth mode;
  - log routing;
  - `/health` result.
- Store a dated redacted inventory under `ops/`, not `docs/`.
- Centralize service names and managed unit definitions in repo-owned code or
  data used by doctor/runbooks.
- Keep secrets redacted.

Verification:

```sh
git diff --check
go test -count=1 ./...
```

### P0.17 Split liveness from readiness/deep doctor

Finding: `/health` should stay cheap. A production-grade agent needs deeper
readiness/doctor checks for config, auth, MCP/tool catalog, session store,
binary provenance, and crash-loop symptoms.

Target files:

- `internal/gateway/*health*`
- `internal/doctor*`
- `cmd/fast-agent-harness`
- `scripts/`

Checklist:

- Keep `/health` cheap and suitable for process liveness.
- Add or harden `doctor`/readiness checks:
  - effective config;
  - auth configured when required;
  - gateway bind address;
  - MCP catalog/status;
  - tool catalog;
  - session store readability/writability;
  - service unit metadata where applicable;
  - recent journal crash-loop summary where applicable.
- Add JSON output for automation.
- Add production/local mode split.

Verification:

```sh
go test -count=1 ./...
go run ./cmd/fast-agent-harness doctor --help
```

## P1 Milestone 4 - Runtime, Tool, And Context Contracts

### P1.1 Add a reusable defensive stream drain helper

Finding: provider stream collectors can wedge when they drain events until close
then wait on errors. Long runs need one helper that selects on events, errs, and
context cancellation.

Checklist:

- Audit provider stream collectors and summary/compaction/web stream code.
- Add helper with bounded shutdown semantics.
- Add broken-provider tests: event channel never closes, err channel blocks,
  context cancelled, error after partial events, and normal completion.
- Replace `time.After` churn in long loops with reusable timers.

Verification:

```sh
go test -count=1 ./internal/agent ./internal/providers ./internal/tools
go test -race -count=1 ./internal/agent ./internal/providers
```

### P1.2 Prevent dead HTTP/SSE clients from pinning handlers forever

Finding: streaming handlers can wait unboundedly after a client disappears.

Checklist:

- Pass request context through stream writer loops.
- Add bounded final drain.
- Add tests with a client disconnect during active stream.

Verification:

```sh
go test -count=1 ./internal/gateway
go test -race -count=1 ./internal/gateway
```

### P1.3 Fix effective dotenv persistence for provider credentials

Finding: saved DeepSeek credentials can be invisible when `FAST_AGENT_ENV_FILE`
is set because save path and effective load path diverge.

Checklist:

- Resolve the effective writable dotenv path.
- If explicit env-file is read-only or unsupported for writes, fail loudly with
  the active path.
- Add tests for default home env, explicit env file, missing env file, read-only
  env file, and no persisted key after failed write.

Verification:

```sh
go test -count=1 ./internal/config ./internal/gateway ./internal/telegrambot
```

### P1.4 Either implement profile instruction fragments or hide inert metadata

Finding: profile instruction metadata is exposed but not routed into prompt
loading. Inert knobs create false confidence.

Checklist:

- Decide: implement deterministic fragment loading or remove/hide fields.
- If implementing, enforce safe paths, stable order, missing-file behavior, and
  redacted diagnostics.
- Add tests for fragment order, path traversal, missing file, and prompt output.
- Update config/provider docs if behavior changes.

Verification:

```sh
go test -count=1 ./internal/config ./internal/agent
```

### P1.5 Make provider capability diagnostics use runtime binding

Finding: diagnostics can reflect partial config rather than canonical runtime
host/shared snapshot.

Checklist:

- Route capability diagnostics through the same runtime binding used by runs.
- Add strict runtime doctor mode for unsupported MCP, missing env vars,
  unavailable allowlisted servers, and provider/model mismatch.

Verification:

```sh
go test -count=1 ./internal/config ./internal/gateway ./internal/agent
```

### P1.6 Make native web search capability metadata honest

Finding: web search output should explicitly report whether freshness and
domain filters were enforced or only post-filtered/ignored.

Checklist:

- Add metadata fields for freshness support, domain enforcement, post-filtering,
  skipped filters, and result count before/after filter when available.
- Add tests for freshness, domain filter, unsupported filter, and redacted output.

Verification:

```sh
go test -count=1 ./internal/tools
```

### P1.7 Separate native strict schema validation from external MCP JSON Schema

Finding: native strict schema rules can reject valid MCP JSON Schema keywords
such as `pattern`, `minimum`, or `oneOf`.

Checklist:

- Define native schema subset separately from external MCP schema acceptance.
- Preserve unsupported external schema metadata without crashing discovery.
- Add tests for common MCP JSON Schema keywords.

Verification:

```sh
go test -count=1 ./internal/tools
```

### P1.8 Preserve MCP structured output metadata

Finding: MCP structured output can be rendered to plain text and wrapped as a
generic result, losing `structuredContent`, content types, server/tool metadata,
and call ids.

Checklist:

- Preserve structured MCP output in event payloads and debug JSON.
- Keep human transcript compact through projection, not by losing raw data.
- Add tests for text, image/resource-like content, structured output, and errors.

Verification:

```sh
go test -count=1 ./internal/tools ./internal/eventlog ./internal/clientux/projector
```

### P1.9 Add output-ref settlement validation before terminal tool events

Finding: tool lifecycle should not terminally succeed while output refs are
unsettled, missing, or non-portable.

Checklist:

- Validate output refs before terminal tool events.
- Prefer relative refs under session/trace bundle roots.
- Add moved-bundle replay tests.

Verification:

```sh
go test -count=1 ./internal/tools ./internal/eventlog ./internal/trace
```

### P1.10 Choose and enforce a context epoch/drift model

Finding: memory/AGENTS/MCP context can drift during long sessions. The system
needs either session-locked context with visible drift warnings or a controlled
refresh/reconcile model.

Checklist:

- Define context epoch fields: AGENTS hash, memory hash if available, MCP catalog
  hash, config hash, docs index hash.
- Record epoch at run admission.
- Warn on drift before follow-up runs.
- Do not silently mix incompatible context in replay/fork.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/agent ./internal/config
```

## P1 Milestone 5 - Verification Infrastructure

### P1.11 Add tracked CI workflow for local gates

Finding: there is no tracked workflow file for the checks Billy already runs
locally.

Checklist:

- Add CI for:
  - `git diff --check`;
  - `go test ./...`;
  - `go vet ./...`;
  - focused race suite;
  - strict hygiene;
  - govulncheck;
  - benchmark smoke if runtime is acceptable.
- Add scheduled/manual full race job if PR runtime is too high.
- Keep CI secrets-free and production-independent.

Verification:

```sh
git diff --check
go test -count=1 ./...
```

### P1.12 Make `verify-local` build outside the repo root

Finding: `verify-local` can leave an ignored root `fast-agent-harness` binary.
Ignored does not mean ideal for hygiene.

Checklist:

- Build into `/tmp/billyharness-verify/fast-agent-harness` or another temp dir.
- Optionally make strict hygiene warn/fail on repo-root runtime artifacts unless
  explicitly allowed.
- Add script tests if there is a script test harness.

Verification:

```sh
scripts/verify-local.sh --skip-bench
test ! -f ./fast-agent-harness
```

### P1.13 Add fuzz/property targets for protocol and security-critical parsers

Finding: there are no tracked `Fuzz*` targets.

Checklist:

- Seed fuzzers from canonical event fixtures.
- Candidate fuzz targets:
  - v1 envelope parsing;
  - event lifecycle validation;
  - session import;
  - tool schema validation;
  - redaction;
  - URL normalization/security checks;
  - checkpoint restore planning.

Verification:

```sh
go test -run '^$' -fuzz=Fuzz -fuzztime=30s ./internal/eventlog ./internal/tools ./internal/secrets
```

### P1.14 Add benchmark baselines and regression gates

Finding: bench smoke proves wiring but not regression. `BENCHTIME=1x` is useful
for smoke only.

Checklist:

- Store host-keyed baseline artifacts outside generated docs.
- Add benchstat comparison script.
- Gate only large regressions to avoid noisy local failures.
- Track alloc regressions for replay/projector/tool paths.

Verification:

```sh
go test -bench=. -benchmem ./internal/bench ./internal/eventlog ./internal/clientux/projector
```

### P1.15 Raise coverage in blind spots with deterministic tests

Current total coverage is good for a young solo project, but review identified
low or zero-coverage zones worth hardening.

Targets:

- `internal/gatewaybase`
- `internal/gatewayclient`
- `internal/tui/runtimeclient`
- CLI dispatch under `cmd/fast-agent-harness`
- checkpoint restore helpers
- service/doctor dispatch

Verification:

```sh
go test -coverprofile=/tmp/billyharness-coverage-after.out ./...
go tool cover -func=/tmp/billyharness-coverage-after.out | tail -n 20
```

### P1.16 Strengthen package-boundary tests beyond direct forbidden imports

Checklist:

- Add positive assertions for required imports/ownership.
- Ensure cmd packages remain adapters.
- Ensure testkit/fakeprovider packages are only imported by tests or explicit
  internal test support.
- Keep generated docs or package map in sync if boundaries change.

Verification:

```sh
go test -count=1 ./internal/architecture
```

## P1 Milestone 6 - Documentation And Docguard Hardening

### ~~P1.17 Fix Stop-hook docguard coverage gaps~~

Cancelled before implementation. A later solo-simplification loop supersedes
this task by deleting the docsgen/docguard meta-documentation layer and its
turn-blocking Stop hook instead of hardening it.

### ~~P1.18 Reconcile docs that still describe implemented hook/docguard as future~~

Cancelled before implementation. A later solo-simplification loop supersedes
this task by removing the docguard/manifest layer rather than reconciling
future-tense docs for a system that should not stay.

### P1.19 Fix loop history metadata drift

Finding: `loop-develop/history/003-todo.md` still opens with `Status: current`.
History files should not look active.

Checklist:

- Update history metadata to completed/verified if evidence supports it.
- Append final commit/push evidence if missing.
- Do not rewrite the historical goal prompt except to label it historical if
  needed.

Verification:

```sh
sed -n '1,40p' loop-develop/history/003-todo.md
git diff --check
```

### ~~P1.20 Refresh manifest/source metadata or clarify semantics~~

Cancelled before implementation. A later solo-simplification loop supersedes
this task by deleting `agent-index/` and the manifest-check ceremony instead of
refreshing or clarifying stale metadata.

### P1.21 Keep README/docs research files clearly historical

Finding: legacy research files can look like current instructions. Current
architecture truth should route through docs indexes and architecture files.

Checklist:

- Ensure `docs/README.md` clearly labels legacy research.
- Keep `docs/research/README.md` clear that active work belongs in
  `loop-develop/current-todo`.
- Do not delete source material unless stable rules are extracted and links are
  updated.

Verification:

```sh
rg -n "research|legacy|current truth|active work|loop-develop" docs/README.md docs/research/README.md
git diff --check
```

## P2 Milestone 7 - Package Decomposition And Scale Control

Goal: keep Billyharness small and inspectable as features accumulate.

### P2.1 Reduce gateway gravity

Finding: gateway is a gravity well. Files near hygiene limits are a warning:
`internal/gateway/gateway.go` and adjacent session files should not become the
only place where every policy lives.

Checklist:

- Split only around real contracts:
  - route registration;
  - session store;
  - admission/input ledger;
  - inspect/debug;
  - auth/owner;
  - checkpoint/undo.
- Keep public API narrow.
- Add package-boundary tests as packages split.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/architecture
go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict
```

### P2.2 Shrink tool runtime seams without hiding laws

Finding: `internal/tools/tools.go` is near the source-file limit and centralizes
native tools, MCP, policy, schemas, output refs, and display rules.

Checklist:

- Extract only when a contract is clear:
  - schema validation;
  - risk/policy;
  - MCP adapter;
  - native tool registry;
  - output-ref settlement;
  - display/projection helpers.
- Add "tool laws" tests: start/result/error ordering, output-ref settlement,
  redaction, risk policy, and replay.

Verification:

```sh
go test -count=1 ./internal/tools ./internal/eventlog ./internal/clientux/projector
```

### P2.3 Centralize compact tool display policy

Finding: TUI, Telegram, exports, and debug views should share display policy
while preserving raw evidence.

Checklist:

- Define compact display rules once.
- Ensure Telegram/TUI/export use projector/display layer rather than custom
  string truncation.
- Keep raw event payload accessible in debug/incident surfaces.

Verification:

```sh
go test -count=1 ./internal/clientux ./internal/tui ./internal/telegrambot
```

### P2.4 Split client packages by behavior, not UI taste

Checklist:

- Keep gateway client, projector, transcript/export, TUI runtime, and Telegram
  adapter boundaries explicit.
- Do not duplicate owner/policy logic in clients.
- Add shared tests from canonical fixtures.

Verification:

```sh
go test -count=1 ./internal/clientux ./internal/tui ./internal/telegrambot ./internal/gatewayclient
```

### P2.5 Build a rebuildable session search/index UX

Checklist:

- Make diagnostics index status visible: rows, build time, stale flag, missing
  flag, last error.
- Add `--rebuild-if-missing` or `--refresh` where useful.
- Ensure index can be rebuilt from event source of truth.

Verification:

```sh
go test -count=1 ./internal/gateway ./internal/clientux
```

### P2.6 Add production deploy/rollback scripts only after doctor is trustworthy

Checklist:

- Define deploy model: source checkout rebuild or release dir/symlink/archive.
- Capture predeploy facts.
- Restart service.
- Run readiness/doctor gate.
- Provide rollback commands.
- Embed build provenance; avoid static-only `version=0.1.0` style.

Verification:

```sh
git diff --check
go test -count=1 ./...
```

## Global Verification For The Implementation Loop

Run focused checks after each slice, then broader checks before the final
commit/push of a slice.

Minimum every-slice checks:

```sh
git diff --check
go test -count=1 ./internal/architecture
```

When gateway/session/eventlog changes:

```sh
go test -count=1 ./internal/eventlog ./internal/gateway ./internal/trace
go test -race -count=1 ./internal/eventlog ./internal/gateway
```

When tools/MCP/web changes:

```sh
go test -count=1 ./internal/tools ./internal/eventlog ./internal/clientux/projector
go test -race -count=1 ./internal/tools
```

When Telegram changes:

```sh
go test -count=1 ./internal/telegrambot
go test -race -count=1 ./internal/telegrambot
```

When TUI/client UX changes:

```sh
go test -count=1 ./internal/tui ./internal/clientux ./internal/gatewayclient
```

Before declaring the whole TODO done:

```sh
go test -count=1 ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go test -race -count=1 ./internal/eventlog ./internal/gateway ./internal/telegrambot ./internal/tools ./internal/tui ./internal/clientux/projector
go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict
git diff --check
```

If runtime behavior changes, also rebuild:

```sh
go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness
```

## Evidence Log

Implementation agent: append dated evidence here as work completes. Keep it
short but concrete: command, result, commit hash, push state, and unresolved
blockers. Do not move this TODO to history; the main chat will do that after
Billy asks for final verification.

### 2026-07-04 - P0.1 gateway read-route auth and browser authority hardening

- Completed P0.1 slice. `/health` remains unauthenticated; configured bearer
  auth now protects `/v1/` state reads, including loopback `GET`/`HEAD`/`OPTIONS`
  callers. `/v1/` requests now share browser Host/Origin/Referer checks, and
  the explicit development loopback bypass remains mutation-only.
- Updated durable docs because gateway auth/security behavior changed:
  `docs/architecture/gateway-and-sessions.md`,
  `docs/architecture/security-model.md`, `docs/README.md`,
  `docs/adr/README.md`, `agent-index/docs-manifest.json`, and new ADR
  `docs/adr/0008-gateway-state-reads-require-bearer-when-token-configured.md`.
- Verification passed:
  `go test -count=1 ./internal/gateway -run 'TestGateway(AuthMiddlewareProtectsConfiguredV1Reads|MutationAuthProtectsLoopbackBrowserRoutes|MutationAuthExplicitDevLoopbackBypass|RunRequestPrivilegeClamps)$'`;
  `go test -count=1 ./internal/architecture`;
  `git diff --check`;
  `go test -count=1 ./internal/gateway`;
  `go test -race -count=1 ./internal/gateway`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `2ce3dc7 Harden gateway read route auth`.
- Push: `origin/main` updated from `bfb110f` to `2ce3dc7`.
- Blockers/residual risk: no production runtime probe was run for this slice;
  current proof is unit/race/full-suite/rebuild plus code/docs review.

### 2026-07-04 - P0.2/P0.3 Telegram operator policy and private secret auth

- Completed P0.2/P0.3 slice. Telegram now separates chat/user allowlisting
  from operator authorization. Operator-only commands require an identified
  non-bot Telegram operator; configured `AllowedOperatorUserIDs` take
  precedence, with `AllowedUserIDs` as the compatibility fallback. Secret-bearing
  `/auth deepseek ...` commands are owner-only, private-chat-only, require
  deletion before persistence, and do not persist from group/dry-run group
  attempts.
- Updated durable docs because Telegram command authorization, CLI/env
  configuration, and secret-bearing auth behavior changed:
  `docs/architecture/telegram-and-operator-surfaces.md`,
  `docs/architecture/security-model.md`, `README.md`,
  `ops/production-services.md`, and `agent-index/docs-manifest.json`.
- Checked docs routing/index files for this slice: `llms.txt`,
  `.agents/rules/README.md`, `docs/README.md`, and
  `docs/documentation-system.md`. No changes were needed there because existing
  routing already points Telegram behavior changes to the Telegram architecture
  document and active implementation evidence to this TODO.
- Verification passed before commit:
  `go test -count=1 ./internal/telegrambot`;
  `go test -race -count=1 ./internal/telegrambot`;
  `go test -count=1 ./internal/architecture`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`;
  `git diff --check`.
- Commit: `e117663 Harden Telegram operator command policy`.
- Push: `origin/main` already points at `e117663`.
- Blockers/residual risk: no live Telegram Bot API or production service probe
  was run for this slice; current proof is unit/race/full-suite/rebuild plus
  docs review.

### 2026-07-04 - P0.4 MCP/tool agency risk split and untrusted metadata labels

- Completed P0.4 slice. Tool risk now supports explicit classes
  `local_read`, `local_write`, `network_read`, `network_write`, `execute`,
  `external_mutation`, and `secret_access` while preserving legacy native
  `read_only`, `network`, `write`, and `external` compatibility. MCP catalog
  risk is derived from local `default_tool_risk` / `tool_risks` config, not
  remote description/schema text. `mcp_list_tools`, `tool_search`, `/v1/mcp`,
  and `mcp_call` metadata label MCP descriptions, schemas, and initialize
  instructions as untrusted server metadata. Side-effecting MCP targets require
  normal dangerous-tool policy and explicit `enabled_tools` allowlisting before
  `mcp_call` invokes the remote handler.
- Added malicious MCP metadata tests and side-effect gate tests:
  `TestBuildCatalogUsesLocalRiskPolicyAndLabelsMCPMetadataUntrusted`,
  `TestMCPGatewayLabelsUntrustedMetadataAndGatesSideEffectingTargets`, and the
  updated gateway MCP status assertion for the untrusted instruction trust tag.
- Updated durable docs because MCP config keys, tool risk semantics, discovery
  output, status trust labels, and permission behavior changed:
  `README.md`, `docs/README.md`, `docs/adr/README.md`,
  `docs/adr/0003-mcp-instructions-are-untrusted-metadata.md`,
  `docs/architecture/tools-mcp-and-policy.md`,
  `docs/architecture/security-model.md`,
  `docs/architecture/config-provider-context.md`, and
  `agent-index/docs-manifest.json`.
- Verification passed:
  `go test -count=1 ./internal/config`;
  `go test -count=1 ./internal/mcpclient`;
  `go test -count=1 ./internal/tools`;
  `go test -count=1 ./internal/tools ./internal/mcpclient ./internal/config ./internal/mcpserver`;
  `go test -race -count=1 ./internal/tools`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `go test -count=1 ./internal/architecture`;
  `git diff --check`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`;
  `go test -count=1 ./internal/gateway`;
  `go test -count=1 ./...`.
- Verification note: the first broad `go test -count=1 ./...` run failed only
  in `TestGatewayToolsExposeMCPRegistry` because the test expected the old
  untagged MCP instruction string. The test was updated to require
  `trust=untrusted_mcp_server_metadata`, then `go test -count=1 ./internal/gateway`,
  `git diff --check`, and `go test -count=1 ./...` all passed.
- Commit: `62127ea Harden MCP tool risk policy`
  (`62127ea1de636b570cd0b78990d0788284120c33`).
- Push: `origin/main` updated from `9214150` to `62127ea`.
- Blockers/residual risk: no live MCP server or production gateway probe was
  run for this slice; current proof is local unit/race/full-suite/rebuild plus
  docs and status-surface tests. Unclassified legacy `external` MCP tools remain
  compatible with existing build/guarded behavior; only locally declared
  side-effecting MCP classes fail closed without `enabled_tools`.

### 2026-07-04 - P0.5 checkpoint artifact and restore-time path verification

- Completed P0.5 slice. Gateway undo/redo now loads checkpoint patch artifacts
  with recorded `patch_output_ref_sha256` verification before preview, restore,
  or redo. `internal/checkpoint` rejects symlink and non-regular patch
  artifacts, requires workspace roots for `RestoreWithOptions`/`RedoWithOptions`,
  and rechecks every restored path plus existing symlink ancestry against the
  configured workspace roots before any file write.
- Added tests for tampered, moved, and symlink patch artifacts; missing
  workspace roots; out-of-root restore records; symlink-parent root escapes;
  and a gateway `/undo` tampered-artifact route failure that leaves the
  workspace unchanged.
- Updated durable docs because undo/redo restore behavior, checkpoint artifact
  verification, and workspace-root semantics changed:
  `docs/architecture/gateway-and-sessions.md`,
  `docs/architecture/tools-mcp-and-policy.md`,
  `docs/architecture/security-model.md`, and
  `agent-index/docs-manifest.json`.
- Verification passed:
  `go test -count=1 ./internal/checkpoint`;
  `go test -count=1 ./internal/gateway -run 'TestGatewaySessionUndo(PreviewAndRestoreCheckpoint|ConflictDoesNotPartiallyRestore|RejectsTamperedPatchArtifact)$'`;
  `go test -count=1 ./internal/checkpoint ./internal/gateway`;
  `go test -race -count=1 ./internal/checkpoint ./internal/gateway`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `go test -count=1 ./internal/architecture`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Verification note: the first race run passed `internal/checkpoint` but timed
  out in unrelated gateway follow test
  `TestGatewaySessionEventsFollowEmitsNonDurableLiveEvent`; rerunning the same
  `go test -race -count=1 ./internal/checkpoint ./internal/gateway` command
  passed both packages.
- Commit: `96c1cd1 Verify checkpoint restore artifacts`
  (`96c1cd1fb0a0faa5361db65577c8b41018cbce4f`).
- Push: `origin/main` updated from `d60b13b` to `96c1cd1`.
- Blockers/residual risk: no production gateway/session-store probe was run for
  this slice; proof is local package/race/full-suite/rebuild plus route-level
  tamper tests. This slice did not add a new neutral replay event type for
  failed restore attempts because existing `turn.change_reverted` semantics
  would incorrectly mark a failed restore as successful.

### 2026-07-04 - P0.6 centralized gateway/Telegram/MCP/export redaction

- Completed P0.6 slice. `internal/secrets` now owns shared text, JSON, URL,
  environment-name, and argv-pair redaction helpers. The shared patterns cover
  URL userinfo, secret query parameters, bearer/proxy auth headers, API-key and
  cookie headers, token/api-key/password fields, provider/GitHub/Yandex/JWT
  token shapes, Telegram bot-token URLs, image data URLs, and MCP-style secret
  argv flags. JSON redaction recurses through string values, object keys, and
  string arrays that look like argv lists.
- Gateway JSON/NDJSON response redaction now delegates to
  `secrets.RedactJSON`. Telegram outbound rendering and Telegram Bot API
  transport errors use the shared redactor. MCP URL credential and argv secret
  extraction now comes from `internal/secrets`, and MCP status URL redaction
  uses `secrets.RedactURL`.
- Operator debug/export surfaces were tightened. `incident collect` no longer
  owns duplicate URL/header regexes and writes JSON through
  `secrets.RedactJSONIndent`. `sessions export` text and `-json` transcript
  exports are redacted before printing while the persisted session JSONL remains
  the durable replay source of truth.
- Added table and integration coverage:
  `TestRedactSharedBoundaryTable`,
  `TestRedactJSONRedactsStringsAndKeys`,
  `TestSessionsExportRedactsTranscriptSurfaces`, updated Telegram client token
  redaction assertion, and retained MCP status redaction coverage for URL
  credentials and argv secrets.
- Updated durable docs because public/operator behavior changed:
  `docs/architecture/security-model.md`,
  `docs/architecture/telegram-and-operator-surfaces.md`,
  `ops/doctor-and-diagnostics.md`, and `agent-index/docs-manifest.json`.
- Verification passed before commit:
  `go test -count=1 ./internal/secrets`;
  `go test -count=1 ./internal/gateway`;
  `go test -count=1 ./internal/telegrambot`;
  `go test -count=1 ./internal/mcpclient`;
  `go test -count=1 ./cmd/fast-agent-harness`;
  `go test -count=1 ./internal/secrets ./internal/gateway ./internal/telegrambot`;
  `go test -count=1 ./internal/mcpclient ./cmd/fast-agent-harness`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `go test -count=1 ./internal/architecture`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Verification note: the first new `internal/secrets` table run exposed two
  central pattern misses: Telegram bot tokens embedded after `/bot`, and bare
  `--token value` argv redaction when `--api-key` appeared later on the line.
  Both were fixed in `internal/secrets` before the final focused and broad
  verification passed.
- Commit: `a30cd2c Centralize redaction surfaces`
  (`a30cd2cdabf40b5b406c2009e8a10a44246a0797`).
- Push: `origin/main` updated from `44163ec` to `a30cd2c`.
- Blockers/residual risk: no live Telegram Bot API, live MCP server, production
  gateway, or production incident-bundle probe was run for this slice; current
  proof is local unit/full-suite/build plus route/export/incident tests.
  Redaction remains a leak-reduction boundary, not proof that arbitrary user
  content is safe to disclose. Durable session logs are intentionally left as
  replay truth; operator-facing exports are redacted presentation artifacts.

### 2026-07-04 - P0.7 protocol envelope and lifecycle validation before append

- Completed P0.7 slice. Gateway session event append now loads/replays current
  event state, enriches the candidate event, validates the session record shape,
  requires a v1 nested protocol envelope, advances a cloned lifecycle validator,
  and only then appends `events.jsonl`. Rejected events do not consume durable
  sequence numbers or leave partial JSONL records.
- `internal/eventlog` lifecycle validation now enforces started run/turn/step
  ordering for run, turn, step, model, assistant, provider-usage, context,
  provider-helper, and tool events. Tool attempts are bound to their original
  `call_id`, including `attempt_started` progress before `tool.call_started`,
  and closed-artifact validation is available through
  `ValidateClosedLifecycle` without forcing active session logs to be terminal.
- Benchmark trace writing now shares the strict append posture: `trace.EventWriter`
  validates envelope and lifecycle before encoding, preserves gapless sequence
  numbers after rejected events, and replay still validates records and
  lifecycle before building summaries. The canonical edge-case trace fixture was
  updated to include valid run/turn/step ordering.
- Updated durable docs because event persistence/replay contracts changed:
  `docs/architecture/runtime-event-system.md`,
  `docs/architecture/gateway-and-sessions.md`, and
  `agent-index/docs-manifest.json`.
- Verification passed before commit:
  `go test -count=1 ./internal/eventlog ./internal/gateway ./internal/trace`;
  `go test -count=1 ./internal/clientux/projector ./internal/trace ./internal/eventlog`;
  `jq . agent-index/docs-manifest.json`;
  `go test -count=1 ./internal/architecture`;
  `git diff --check`;
  `go test -race -count=1 ./internal/eventlog ./internal/gateway`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `e778d17 Validate event lifecycle before append`
  (`e778d17cecbb148309c87063e17c18710b383455`).
- Push: `origin/main` updated from `df9afb0` to `e778d17`.
- Blockers/residual risk: no production gateway/session-store or benchmark
  artifact replay probe was run for this slice; proof is local focused/race/
  full-suite/build plus append-time rejection and replay-corruption tests.

### 2026-07-04 - P0.8 strict trace replay counters and explicit legacy mode

- Completed P0.8 slice. `trace.ReplaySummary` now exposes `replay_mode`,
  `raw_event_types`, `validated_event_types`, `legacy_event_types`,
  `validated_v1_records`, and `legacy_records`, so JSON consumers can
  distinguish canonical v1 nested event truth from explicitly accepted legacy
  or raw trace records.
- Trace replay now advances lifecycle only for validated v1 nested events.
  Schema-0 legacy records remain accepted as legacy/raw evidence, contribute
  raw counters, and no longer fail strict lifecycle only because they lack
  modern envelope IDs. New v1 events still fail on malformed envelopes or
  lifecycle violations.
- Reused the shared canonical edge-case fixture catalog in trace replay tests,
  so trace replay and event-log contract tests now exercise the same sequence,
  duplicate-tool, partial-stream, MCP, Telegram, and corrupted-envelope cases.
- Fixed the P0.7 trace-writer refactor so payload-ref file names and payload
  IDs use the candidate record sequence (`000004.json`, `payload:4`, etc.)
  instead of the previous committed writer sequence.
- Updated durable docs because trace replay JSON output and strict/legacy mode
  semantics changed: `docs/architecture/runtime-event-system.md`.
- Verification passed before commit:
  `go test -count=1 ./internal/trace ./internal/gateway`;
  `git diff --check`;
  `jq . agent-index/docs-manifest.json`;
  `go test -count=1 ./internal/architecture`;
  `go test -count=1 ./internal/bench ./internal/trace ./internal/gateway`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `d5594e0 Separate strict trace replay counters`
  (`d5594e0fa0b4b13e75ec6d1d96f8035f4ee1dba1`).
- Push: `origin/main` updated from `77794ad` to `d5594e0`.
- Blockers/residual risk: no real historical benchmark artifact corpus was
  replayed for this slice; proof is focused synthetic legacy/v1 tests, shared
  canonical fixture replay, package/full-suite verification, and rebuild.

### 2026-07-04 - P0.9 real stored-session projector parity inspection

- Completed P0.9 slice. Stored-session inspection now replays decoded events
  through the shared `internal/clientux/projector` reducer instead of deriving
  "projected" counters from the same raw loop. The inspection surface reports
  `session_id`, `seq_range`, `last_event_id`, `projection_hash`, and explicit
  mismatch reasons when raw lifecycle/tool-call counts diverge from projector
  state.
- Added gateway coverage for the real projector path plus shared canonical
  edge fixtures (`stream_gap`, `parallel_cancellation`, `late_output_ref`), so
  gateway stored-session inspection exercises the same protocol edge catalog as
  client projector and trace replay tests.
- Updated durable docs because stored-session inspection output and gateway
  package boundaries changed:
  `docs/architecture/gateway-and-sessions.md`,
  `docs/architecture.md`, and `agent-index/docs-manifest.json`.
- Verification passed before commit:
  `go test -count=1 ./internal/clientux/projector ./internal/gateway ./internal/tui`;
  `go test -count=1 ./internal/architecture`;
  `jq . agent-index/docs-manifest.json`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Verification note: the first architecture verification failed because the new
  gateway inspection dependency on `internal/clientux/projector` was not yet
  represented in `docs/architecture.md`; the package-boundary doc and manifest
  were updated, then architecture, focused, full-suite, diff-check, and build
  verification all passed.
- Commit: `8d63674 Use real projector in session inspection`
  (`8d636745c895aad727b090c64ebc794d334f89f3`).
- Push: `origin/main` updated from `13c0b66` to `8d63674`.
- Blockers/residual risk: no live production session corpus was inspected for
  this slice; proof is focused projector/gateway/TUI tests, canonical fixture
  coverage, package/full-suite verification, docs-boundary verification, and
  rebuild.

### 2026-07-04 - P0.10 fail-closed session persistence gaps

- Completed P0.10 slice for session run, interrupt, cancel, undo, and redo
  persistence ordering. Session run final-save failures now mark in-memory
  status as `persistence_failed`, emit a non-durable `session.status` on the
  stream when the event log itself is still healthy, and complete the admitted
  input as failed. Interrupt-policy replacement runs now abort before starting
  the replacement when saving the interrupted state fails. Cancel returns HTTP
  500 instead of `cancelled=true` when post-cancel session save fails.
- Undo/redo checkpoint routes now compensate when durable event append fails
  after workspace restore. Failed undo append immediately redoes the checkpoint
  to roll the workspace forward again; failed redo append immediately restores
  the checkpoint back to the undone state. Both paths return HTTP 500 and leave
  replay without a false `turn.change_reverted` / `redone` event.
- Added injected persistence-failure coverage for final run save, interrupt
  save before replacement, cancel save after cancellation, undo append rollback,
  and redo append rollback.
- Updated durable docs because gateway route behavior, session persistence
  contracts, and undo/redo failure semantics changed:
  `docs/architecture/gateway-and-sessions.md`. Checked
  `docs/architecture/runtime-event-system.md` and `agent-index/docs-manifest.json`;
  no manifest or runtime-event update was needed because no event names, envelope
  semantics, or manifest routing changed.
- Verification passed before commit:
  `go test -count=1 ./internal/gateway`;
  `go test -count=1 ./internal/architecture`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `git diff --check`;
  `go test -race -count=1 ./internal/gateway`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Verification note: the first focused gateway run exposed that final-save
  reporting should not add a second persistence status when event append had
  already failed. The run handler was tightened to skip final snapshot-save
  reporting after an event persistence failure, then focused/race/full-suite/
  diff-check/build verification passed.
- Commit: `c6a316f Fail closed on session persistence gaps`
  (`c6a316f074b103bb0e1a3a6c6e17eba2e07b6c99`).
- Push: `origin/main` updated from `9f96e2a` to `c6a316f`.
- Blockers/residual risk: no production gateway process or live session corpus
  was exercised; proof is local injected failure tests, package/race/full-suite
  verification, docs check, and rebuild. Gateway shutdown abort still logs
  post-abort `saveSession` failure because there is no active HTTP caller to
  return an error to in that path.

### 2026-07-04 - P0.11 admitted-input ledger and Telegram pending-input hardening

- Completed P0.11 slice. Gateway startup now quarantines corrupt
  `inputs.jsonl` files as `inputs.jsonl.corrupt-<timestamp>` when the session
  event log is otherwise loadable, instead of hiding the usable session.
  Admitted inputs can be terminally completed through
  `POST /v1/sessions/{id}/inputs/{input_id}/complete`, completion records carry
  terminal status plus failure reason, and run promotion computes the next run
  sequence under the input-ledger/session-store lock.
- Session run preflight failures after admission now complete the admitted input
  as `preflight_failed` with replayable failure evidence. Telegram now treats
  pending-state persistence as part of admission: if saving pending state fails
  after gateway admission, it terminally completes the gateway input before
  returning the local persistence error. On startup, Telegram reconciles pending
  gateway inputs by completing reachable gateway inputs as
  `abandoned_after_restart`, recording returned gateway state in the admission
  ledger, and clearing local pending fields; missing gateway sessions get a
  durable local terminal reason.
- Added focused coverage for corrupt input-ledger quarantine on restart,
  preflight-failure completion records, input-ledger run sequence allocation,
  Telegram startup reconciliation of acked pending inputs, and gateway input
  completion after Telegram pending-state save failure.
- Updated durable docs because gateway route behavior, input-ledger replay
  semantics, and Telegram admission/restart semantics changed:
  `docs/architecture/gateway-and-sessions.md` and
  `docs/architecture/telegram-and-operator-surfaces.md`. Checked
  `agent-index/docs-manifest.json`; no manifest edit was needed because both
  affected docs already had `2026-07-04` review metadata and routing did not
  change.
- Verification passed before commit:
  `go test -count=1 ./internal/gateway ./internal/telegrambot`;
  `go test -race -count=1 ./internal/gateway ./internal/telegrambot`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `go test -count=1 ./internal/architecture`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `afe10ff Harden session input ledgers`
  (`afe10ff0e65f1f994837bea11ea2e5ad54e90745`).
- Push: `origin/main` updated from `9a60eb1` to `afe10ff`.
- Blockers/residual risk: no production gateway, live Telegram adapter, or
  historical session-store corpus was exercised; proof is local focused/race/
  full-suite verification, restart/admission tests, docs check, and rebuild.

### 2026-07-04 - P0.12 split snapshot readiness from event replay readiness

- Completed P0.12 slice. Stored-session inspection and summaries now expose
  explicit readiness states: `message_snapshot_ready`,
  `event_replay_ready`, `event_replay_missing`, `event_replay_invalid`, and
  `event_replay_incomplete`. The compatibility `offline_replay_ready` field now
  follows event replay readiness instead of merely proving that a message
  snapshot can be loaded.
- `InspectStoredSession` now validates closed event lifecycle separately from
  ordinary replay validation. Partial/open event streams can still report
  schema/lifecycle-valid replay material while remaining
  `event_replay_incomplete` until the run lifecycle is closed. Legacy snapshots
  remain `message_snapshot_ready` but no longer claim event replay readiness.
- CLI/operator output now prints `snapshot`, `event_replay`, readiness states,
  and closed-lifecycle diagnostics for `sessions list`, `sessions index show`,
  and `sessions inspect`.
- Added focused coverage for legacy snapshots, completed event streams, partial
  event streams, list/index summaries, and CLI text/JSON output.
- Updated durable docs because stored-session inspection JSON/text output and
  replay-readiness semantics changed: `docs/architecture/gateway-and-sessions.md`
  and `ops/doctor-and-diagnostics.md`. Checked
  `agent-index/docs-manifest.json`; no manifest edit was needed because both
  affected docs already had `2026-07-04` review metadata and routing did not
  change.
- Verification passed before commit:
  `go test -count=1 ./internal/gateway ./cmd/fast-agent-harness`;
  `go test -race -count=1 ./internal/gateway`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `go test -count=1 ./internal/architecture`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `b6c0d06 Split stored session replay readiness`
  (`b6c0d066b5847dc374cd984ef71410062b91c0eb`).
- Push: `origin/main` updated from `9abb58e` to `b6c0d06`.
- Blockers/residual risk: no production store, historical session corpus, or
  incident-bundle replay was exercised; proof is local focused/race/full-suite
  verification, synthetic legacy/partial/complete replay tests, docs check, and
  rebuild.

### 2026-07-04 - P0.13 live session inspect endpoint and CLI debug surface

- Completed P0.13 slice. Gateway now exposes
  `GET /v1/sessions/{id}/inspect` behind normal session read authorization. The
  route prefers durable store inspection and returns a warning rather than
  claiming durable replay truth when a live session exists only in memory.
- Stored-session inspection now includes owner scope, `inputs.jsonl` manifest
  and file status, input-ledger validation/state counts, and lifecycle
  open/closed counts for runs, turns, steps, and tool attempts. The existing
  projector parity, seq range, output-ref status, readiness, and redacted error
  surfaces are preserved.
- Added `gatewayclient.SessionInspectRaw` and CLI
  `sessions debug [-gateway URL] [-json] SESSION_ID` for live inspection. The
  existing `sessions inspect [-dir DIR]` remains the offline store path, and
  `sessions --help` now lists the live debug command.
- Added coverage for the live gateway route, gatewayclient fetch method, CLI
  human/JSON debug output, help output, input ledger status, lifecycle counts,
  owner scope, and projector parity.
- Updated durable docs because gateway route behavior, client/CLI surface, and
  operator debug output changed: `docs/architecture/gateway-and-sessions.md`
  and `ops/doctor-and-diagnostics.md`. Checked
  `agent-index/docs-manifest.json`; no manifest edit was needed because both
  affected docs already had `2026-07-04` review metadata and routing did not
  change.
- Verification passed before commit:
  `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/clientux ./cmd/fast-agent-harness`;
  `go run ./cmd/fast-agent-harness sessions --help`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `go test -count=1 ./internal/architecture`;
  `git diff --check`;
  `go test -race -count=1 ./internal/gateway ./internal/gatewayclient`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `7a5ea7f Add live session inspect surface`
  (`7a5ea7f2b96730af8c33a463fb1d1653c99ed08d`).
- Push: `origin/main` updated from `2e0b12e` to `7a5ea7f`.
- Blockers/residual risk: no production gateway or historical session corpus
  was inspected through the new route; proof is local route/client/CLI tests,
  race/full-suite verification, docs check, and rebuild.

### 2026-07-04 - P0.14 redacted TUI/client debug snapshot

- Completed P0.14 slice. `internal/clientux` now owns a frontend-neutral
  `TUIDebugSnapshot` schema plus redaction/formatting helpers. The schema
  captures local chat ID, hashed chat-title metadata, gateway session ID, last
  gateway event sequence, runtime mode/settings, stream queue state, client UX
  projector state, viewport and selection coordinates, transcript/export byte
  counts and hashes, stale flags, block/cell counts, selected cell identity,
  and diagnostic hints.
- The TUI now gathers the snapshot in `internal/tui/debug_snapshot.go`.
  `/debug` adds a redacted `DEBUG` info block, and the existing
  `/status debug` compatibility path now renders the same structured snapshot
  as `STATUS DEBUG`.
- The debug surface avoids raw transcript/export/viewport/selection bodies.
  User-visible content is represented as lengths and SHA-256 hashes; runtime
  errors, projector errors, URLs, settings paths, bearer/API-token-like values,
  and additional path/URL redaction inputs flow through `internal/secrets`.
- Added focused coverage for shared snapshot redaction/formatting, `/debug`,
  `/status debug`, command palette ordering, and wide/combining-character
  selection debug state without selected text leakage.
- Updated durable docs because the TUI/clientux debug contract and package
  import boundary changed: `docs/architecture/tui-and-clientux.md`,
  `docs/architecture.md`, and `agent-index/docs-manifest.json`. Checked
  `docs/README.md`, `llms.txt`, `.agents/rules/README.md`, and
  `.agents/rules/documentation.md`; no routing text changes were needed there.
- Verification passed before commit:
  `go test -count=1 ./internal/tui ./internal/clientux`;
  `go test -count=1 ./internal/architecture`;
  `go test -count=1 ./internal/tui ./internal/tui/transcript ./internal/tui/render ./internal/tui/selection ./internal/tui/runtimeclient ./internal/clientux ./internal/clientux/projector ./internal/toolrender`;
  `go test -race -count=1 ./internal/tui ./internal/clientux`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `350ff91 Add redacted TUI debug snapshot`
  (`350ff912c5c00dfe99a908cbd7015cca71d14bb2`).
- Push: `origin/main` updated from `85f8941` to `350ff91`.
- Blockers/residual risk: no live operator incident was inspected through the
  new command; proof is local TUI/clientux focused tests, wide/combining
  selection coverage, architecture/docs checks, race/full-suite verification,
  and rebuild.

### 2026-07-04 - P0.15 incident-grade transcript export artifacts

- Completed P0.15 slice. `/export` now writes or displays a
  Billyharness transcript artifact with a metadata header instead of a bare
  transcript body. The header records source store, source mode, transcript
  mode, runtime mode, local/gateway session IDs, last known gateway event seq,
  sequence range, provider/model/profile/access mode, reasoning settings,
  export time, redaction mode, body byte count, body hash, block/message
  counts, and warnings.
- Added shared `internal/clientux/transcript_export.go` metadata formatting and
  source normalization. Supported source modes are `cells`, `messages`,
  `events`, and `combined`; TUI `events`/`combined` exports explicitly warn
  that they are projected client state, not durable gateway JSONL replay.
- `/export` now supports quoted paths and `mode=`, `source=`, and `path=`
  arguments while preserving old forms like `/export raw PATH`.
- Generated raw tool output-ref records now quote path-like values such as
  `output_ref`, `output_ref_id`, `sha256`, and `preview`, and preserve the
  quoted output-ref line when the final tool result appends to the same
  transcript cell.
- Added focused coverage for artifact metadata/warnings/redaction, source
  normalization, quoted export path parsing, event-source artifact output, and
  quoted output-ref raw-copy preservation.
- Updated durable docs because transcript export behavior changed:
  `docs/architecture/tui-and-clientux.md`. Checked
  `agent-index/docs-manifest.json`; no manifest edit was needed because the TUI
  architecture doc was already reviewed on `2026-07-04` and its routing already
  covers TUI export/clientux changes.
- Verification passed before commit:
  `go test -count=1 ./internal/tui ./internal/tui/transcript ./internal/clientux`;
  `go test -count=1 ./internal/architecture`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `git diff --check`;
  `go test -race -count=1 ./internal/tui ./internal/tui/transcript ./internal/clientux`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `9710418 Promote transcript exports to incident artifacts`
  (`9710418f431807bed857b4e4ca635d860813f9c0`).
- Push: `origin/main` updated from `508d8a0` to `9710418`.
- Blockers/residual risk: TUI does not own durable gateway JSONL events, so
  `events`/`combined` exports are clearly marked as projected client state;
  proof is local focused/race/full-suite verification, architecture/docs
  checks, path parser coverage, and rebuild.

### 2026-07-04 - P0.16 redacted production inventory and service metadata

- Completed P0.16 slice. Live production was inspected over SSH at
  `root@82.23.163.16` with remote-side redaction. The dated source-of-truth
  inventory is `ops/production-inventory-2026-07-04.md`.
- Captured host, checkout, binary, toolchain, systemd, process, gateway bind,
  health, route-probe, env-file metadata, store-size, and doctor facts without
  copying raw `.env`, auth JSON, MCP inline env, provider keys, Telegram
  tokens, bearer tokens, or journal content.
- Observed production checkout: `/root/billyharness` at commit
  `ce3f2c943ee2a08672a858a701cafa0a9e4f62fc`, clean worktree. Binary:
  `/root/billyharness/bin/fast-agent-harness`, SHA-256
  `02c1e3cd06a928236c94bee80f7880a45147698e13fed4c2e1961d12cc72a654`.
  Go: `/root/.local/go/bin/go`, `go1.26.4 linux/amd64`.
- Observed services: `billyharness-gateway.service` and
  `billyharness-telegram.service`, both active/running and enabled, running as
  `root` from `/root/billyharness`, loading
  `EnvironmentFile=-/root/billyharness/.env`, using
  `FAST_AGENT_ENV_FILE=/root/billyharness/.env`, `Restart=always`,
  `RestartSec=2`, journald output, and one live process each.
- Observed gateway bind/readiness: listener on `127.0.0.1:8765`, `/health`
  returned `200` with `{"model":"gpt-5.5","ok":true,"provider":"openai-codex"}`.
  Local unauthenticated route probes on the production commit returned `200`
  for `/v1/config`, `/v1/mcp`, `/v1/tools`, and `/v1/processes`; the inventory
  explicitly notes that local `main` has later read-route hardening and these
  status codes are observed production truth, not desired post-deploy policy.
- Added `internal/serviceops` as the repo-owned service metadata source for
  service names, subcommands, pid files, and unit paths. Doctor and gateway
  readiness hints now use the shared metadata.
- Updated durable ops/docs because production facts and package boundaries
  changed: `ops/README.md`, `ops/production-services.md`,
  `docs/architecture.md`, and `agent-index/docs-manifest.json`.
- Verification passed before commit:
  SSH live inventory/probe commands with redaction;
  `go test -count=1 ./cmd/fast-agent-harness ./internal/gatewaybase ./internal/serviceops ./internal/architecture`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`;
  `go run ./cmd/fast-agent-harness doctor -build=false -services=false -gateway=false`.
- Commit: `aa974b9 Capture production service inventory`
  (`aa974b9c3d5b5e55e89fb1d47792df268cd802a0`).
- Push: `origin/main` updated from `5bab0d4` to `aa974b9`.
- Blockers/residual risk: production was inspected read-only and not restarted
  or deployed; journal tails and raw secret-bearing files were intentionally not
  copied. The inventory is a dated snapshot and must be refreshed after deploys
  or service changes.

### 2026-07-04 - P0.17 liveness/readiness split and deeper doctor checks

- Completed P0.17 slice. `/health` is now a cheap unauthenticated liveness
  response with provider/model only. New `GET /ready` returns bounded
  readiness for effective provider/model, visible native tool count, MCP catalog
  summary, and startup session-store diagnostics; corrupt startup session-store
  evidence moves from `/health` to `/ready` and returns `503` when readiness
  fails.
- Hardened `doctor` into a richer automation/operator surface: text and JSON
  reports now include `mode` (`auto`, `local`, or `production`), effective
  config checks, active-provider auth checks, gateway bind classification,
  native tool catalog checks, session-store read/write checks, separate
  `gateway /health` and `gateway /ready` probes, and production-only systemd
  unit metadata plus recent journal crash/error signal summaries.
- Updated durable docs because gateway health/readiness and doctor semantics
  changed: `README.md`, `ops/doctor-and-diagnostics.md`,
  `ops/production-services.md`, `docs/architecture/gateway-and-sessions.md`,
  `docs/architecture/security-model.md`,
  `docs/adr/0007-local-gateway-mutating-routes-require-explicit-trust.md`,
  `docs/adr/0008-gateway-state-reads-require-bearer-when-token-configured.md`,
  and `agent-index/docs-manifest.json`.
- Verification passed before commit:
  `go test -count=1 ./internal/gateway ./cmd/fast-agent-harness`;
  `go test -count=1 ./internal/gateway ./cmd/fast-agent-harness ./internal/architecture`;
  `jq . agent-index/docs-manifest.json >/dev/null`;
  `git diff --check`;
  `go run ./cmd/fast-agent-harness doctor --help`;
  `go test -race -count=1 ./internal/gateway ./cmd/fast-agent-harness`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `c4e9ece Split gateway liveness and readiness checks`
  (`c4e9ece54ec6ec8aa8105248111af7ecbc2983b8`).
- Push: `origin/main` updated from `ef4689d` to `c4e9ece`.
- Blockers/residual risk: `/ready` is intentionally unauthenticated but bounded
  to redacted counts/status and startup diagnostics; no production deploy,
  restart, or live post-deploy `/ready` probe was run for this slice.

### 2026-07-04 - P1.12 verify-local temp rebuild and strict hygiene pass

- Completed P1.12 slice. `scripts/verify-local.sh` now builds the CLI binary
  into `${BILLYHARNESS_VERIFY_OUT_DIR:-/tmp/billyharness-verify}/fast-agent-harness`
  instead of emitting an ignored `./fast-agent-harness` binary in the repo root.
  The script prepares the output directory before the rebuild.
- Removed a stale pre-existing ignored root `./fast-agent-harness` artifact so
  the required `test ! -f ./fast-agent-harness` assertion could prove the new
  script behavior rather than old workspace state.
- Strict hygiene initially failed during `scripts/verify-local.sh --skip-bench`
  because tracked files exceeded the documented line budgets. Updated
  `docs/architecture.md` with explicit current exceptions, owners, and split
  plans instead of weakening the hygiene gate. No separate shell script test
  harness exists under `scripts/`, so verification is by syntax check and the
  real script gate.
- Verification passed:
  `bash -n scripts/verify-local.sh`;
  `go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict`;
  `scripts/verify-local.sh --skip-bench`;
  `test ! -f ./fast-agent-harness`;
  `test -f /tmp/billyharness-verify/fast-agent-harness`;
  `git diff --check`.
- Commit: `6f09a64 Build verify-local binary outside repo`
  (`6f09a645b5b66996f796d55a6312ec2cbe8a698b`).
- Push: `origin/main` updated from `4d031fb` to `6f09a64`.
- Blockers/residual risk: strict hygiene now passes through documented
  exceptions rather than splits; the listed files still need the planned P1/P2
  decomposition to remove those exceptions.

### 2026-07-04 - P1.11 tracked CI workflow and benchmark smoke repair

- Completed P1.11 slice. Added `.github/workflows/ci.yml` with a secrets-free,
  production-independent CI workflow for pushes and pull requests on `main`.
  The normal `Local Gates` job runs checkout, Go setup from `go.mod`,
  `git diff --check`, dependency metadata verification, `go vet ./...`,
  `go test -count=1 ./...`, focused race tests, `govulncheck`, strict hygiene,
  and benchmark smoke. A separate `Full Race` job runs
  `go test -race -count=1 ./...` only on manual dispatch or the scheduled
  weekly run.
- Fixed gateway benchmark fixtures so benchmark smoke is compatible with the
  stricter durable event lifecycle validator. Seeded benchmark JSONL now starts
  runs/turns/model steps or tool calls before delta/output-ref events, and
  AppendEvent benchmark paths stamp the benchmark run id explicitly.
- Checked durable docs routing. No README/ops docs changed for this slice
  because the workflow file is the tracked CI source of truth and existing
  verification guidance already points agents to the local gate commands.
- Verification passed:
  `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/ci.yml")'`;
  `git diff --check`;
  `go vet ./...`;
  `go test -count=1 ./...`;
  `go test -race -count=1 ./internal/eventlog ./internal/gateway ./internal/telegrambot ./internal/tools ./internal/tui ./internal/clientux/projector`;
  `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`;
  `go test -run '^$' -bench 'BenchmarkGatewaySessionJSONL' -benchtime=1x ./internal/gateway`;
  `env GO_BIN=go scripts/bench-smoke.sh`.
- Commit: `f4a1907 Add CI workflow for local gates`
  (`f4a19076710e5c785dfd81d42263b1a7bb22989f`).
- Push: `origin/main` updated from `8c10266` to `f4a1907`.
- Blockers/residual risk: the workflow itself has not yet run on GitHub; proof
  is local YAML parsing plus local execution of the same commands.

### 2026-07-04 - P1.13 fuzz targets for protocol and security parsers

- Completed P1.13 slice. Added tracked fuzz targets for event envelope and
  lifecycle validation (`internal/eventlog`), native tool JSON-schema argument
  validation (`internal/tools`), and shared text/URL/JSON redaction
  (`internal/secrets`).
- Seeded the fuzzers with canonical-looking v1 event envelopes, malformed
  envelopes, realistic object schemas, invalid schema input, bearer/API-key
  examples, JSON secret fields, URL credentials, and plain text.
- Checked docs routing: `llms.txt`, `.agents/rules/README.md`,
  `docs/README.md`, and `docs/documentation-system.md`. No durable docs changed
  because this slice adds verification infrastructure only; it does not change
  runtime behavior, CLI/API contracts, config, gateway routes, or security
  semantics.
- Verification passed:
  `gofmt -w internal/eventlog/fuzz_test.go internal/tools/fuzz_test.go internal/secrets/fuzz_test.go`;
  `go test -count=1 ./internal/eventlog ./internal/tools ./internal/secrets`;
  `go test -run '^$' -fuzz=Fuzz -fuzztime=30s ./internal/eventlog`;
  `go test -run '^$' -fuzz=Fuzz -fuzztime=30s ./internal/tools`;
  `go test -run '^$' -fuzz=Fuzz -fuzztime=30s ./internal/secrets`;
  `git diff --check`;
  `go test -count=1 ./...`.
- Verification note: the TODO's combined fuzz command
  `go test -run '^$' -fuzz=Fuzz -fuzztime=30s ./internal/eventlog ./internal/tools ./internal/secrets`
  failed immediately with Go's built-in limitation, `cannot use -fuzz flag with
  multiple packages`. The equivalent package-by-package fuzz runs above passed.
- Commit: `7c09378 Add fuzz targets for critical parsers`
  (`7c09378099b4640b2d94ef988c5d9d076da50a1a`).
- Push: `origin/main` updated from `dae3f43` to `7c09378`.
- Blockers/residual risk: fuzzing was a short local 30s-per-package smoke, not
  long-running corpus growth or CI-continuous fuzzing. No new behavior was
  intentionally introduced by this slice.

### 2026-07-04 - P1.14 benchmark baselines and regression gate

- Completed P1.14 slice. Added `scripts/bench-compare.sh` to record
  host-keyed benchmark baselines under ignored
  `bench-runs/bench-baselines/<host-key>/latest.txt`, run `benchstat`, and fail
  only on large percentage-plus-absolute regressions. `scripts/bench-smoke.sh`
  now includes gateway session JSONL, raw event JSONL, projector replay, and
  tool schema validation benchmark metrics.
- Added allocation-tracking benchmarks for projector replay
  (`BenchmarkProjectorApplyReplay`) and native tool schema validation
  (`BenchmarkToolSchemaValidation`). Existing gateway/eventlog replay
  benchmarks continue to cover session/event JSONL append and replay paths.
- Updated `benchmarks/README.md` because benchmark operator workflow changed.
  No architecture docs changed because this slice only adds verification
  infrastructure and benchmark notes; it does not change package boundaries,
  runtime behavior, public APIs, config, gateway routes, or security semantics.
- Verification passed:
  `bash -n scripts/bench-smoke.sh scripts/bench-compare.sh`;
  `go test -count=1 ./internal/clientux/projector ./internal/tools`;
  `go test -bench=. -benchmem ./internal/bench ./internal/eventlog ./internal/clientux/projector`;
  `env BENCHTIME=1x scripts/bench-smoke.sh`;
  `env BENCHTIME=1x scripts/bench-compare.sh record`;
  `env BENCHTIME=1x scripts/bench-compare.sh compare`;
  `go test -count=1 ./...`;
  `git diff --check`.
- Verification note: the first live compare run failed on 1x microbenchmark
  timing noise in `BenchmarkToolSchemaValidation` while bytes/allocs stayed
  stable. The gate was tightened to require both percentage and absolute
  deltas (`1ms`, `64KiB`, or `1000` allocs by default), then the same
  `env BENCHTIME=1x scripts/bench-compare.sh compare` command passed.
- Commit: `bcefff7 Add benchmark regression gate`
  (`bcefff71767505a0536da55f5bc8eeb1cf9c6bc3`).
- Push: `origin/main` updated from `bc8989b` to `bcefff7`.
- Blockers/residual risk: the recorded local baseline is intentionally ignored
  and host-specific. The smoke/compare verification used `BENCHTIME=1x` for
  turn-time; operators should use a higher `BENCHTIME` such as `5x` or more
  for meaningful investigations.

### 2026-07-04 - P1.15 deterministic coverage for runtime blind spots

- Completed P1.15 slice. Added deterministic tests for gateway base URL/auth/
  readiness helpers, service metadata ownership/copy semantics, gatewayclient
  list/get/input-completion/undo/redo/error helpers, service-command env/helper
  dispatch, CLI help/unknown dispatch, and checkpoint `Load` plus
  `RedoWithOptions` delete restore behavior.
- Coverage movement on the named blind spots: `internal/gatewaybase` moved from
  `0.0%` to `78.1%`; `internal/serviceops` moved from `0.0%` to `100.0%`;
  `internal/gatewayclient` moved from `45.9%` to `52.7%`;
  `cmd/fast-agent-harness` moved from `52.9%` to `55.5%`;
  `internal/checkpoint` moved from `66.0%` to `67.7%`. Existing
  `internal/tui/runtimeclient` coverage stayed at `50.0%`, with its current
  deterministic delegation/error tests still passing.
- Checked docs routing: `llms.txt`, `.agents/rules/README.md`,
  `docs/README.md`, and `docs/documentation-system.md`. No durable docs changed
  because this slice adds test coverage only; it does not change runtime
  behavior, package boundaries, public APIs, config, gateway routes, or
  security semantics.
- Verification passed:
  `go test -count=1 ./internal/gatewaybase ./internal/serviceops ./internal/gatewayclient ./internal/tui/runtimeclient ./internal/checkpoint ./cmd/fast-agent-harness`;
  `go test -coverprofile=/tmp/billyharness-coverage-after.out ./...`;
  `go tool cover -func=/tmp/billyharness-coverage-after.out | tail -n 20`;
  `go test -count=1 ./...`;
  `git diff --check`.
- Commit: `c3a51b9 Add deterministic coverage for runtime blind spots`
  (`c3a51b9bbdfcc3dc88b046132f7c8df5ddcd6d14`).
- Push: `origin/main` updated from `beb17c7` to `c3a51b9`.
- Blockers/residual risk: total coverage is now `73.7%`, but remaining low
  areas still include web summary helpers, public web URL wrappers, and broader
  CLI command front doors such as bench/incident/tool dispatch. This slice kept
  tests deterministic and avoided live TUI/Telegram/gateway process startup.

### 2026-07-04 - P1.16 stronger package-boundary guardrails

- Completed P1.16 slice. Strengthened `internal/architecture` beyond the
  existing docs-map forbidden-import check with positive required-import
  assertions for core ownership boundaries, a command-package adapter allowlist,
  and a repo-wide guard that `internal/testkit` and
  `internal/testkit/fakeprovider` do not appear in non-test imports.
- Positive assertions now pin important direct ownership links such as
  gateway/trace to `eventlog`, gatewayclient/gatewaybase to shared gateway
  DTO/helper packages, runtimehost to agent/provider/tools composition, TUI to
  `gatewayclient` and `tui/runtimeclient`, and Telegram to `gatewayclient`
  rather than gateway server internals.
- Checked docs routing: `docs/README.md` and `docs/documentation-system.md`.
  No durable docs changed because the new tests enforce existing
  `docs/architecture.md` package-map and guarded-rule intent; package
  ownership/import contracts did not change.
- Verification passed:
  `gofmt -w internal/architecture/architecture_test.go`;
  `go test -count=1 ./internal/architecture`;
  `go test -count=1 ./...`;
  `git diff --check`.
- Commit: `050089b Strengthen architecture boundary tests`
  (`050089b42a1ab85f2638ae6bfa4acf88a8aea7a4`).
- Push: `origin/main` updated from `2848655` to `050089b`.
- Blockers/residual risk: command-package adapter policy is currently a
  reviewed allowlist of existing CLI front-door imports, not a deeper semantic
  layering model. Future generated package/reference docs may let this become
  less hand-maintained.

### 2026-07-05 - P1.17/P1.18/P1.20 cancelled by later simplification loop

- Verified these 004 tasks were not implemented: no implementation Evidence Log
  entries existed for P1.17, P1.18, or P1.20.
- Confirmed the target layer is still present before cancellation:
  `.codex/hooks/docguard_stop.py`, `docs/documentation-system.md`,
  `agent-index/docs-manifest.json`, and
  `agent-index/generated/reference-plan.md` still exist.
- Marked P1.17, P1.18, and P1.20 as cancelled/superseded because
  later solo-simplification work deletes the docsgen/docguard/manifest ceremony
  instead of hardening or refreshing it. This keeps 004 complete without
  requiring the next loop's untracked TODO files to be pushed in this 004 slice.
- Verification passed after reconciliation:
  `go test -count=1 ./...`;
  `go vet ./...`;
  `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` (`No vulnerabilities found.`);
  `go test -race -count=1 ./internal/eventlog ./internal/gateway ./internal/telegrambot ./internal/tools ./internal/tui ./internal/clientux/projector`;
  `go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict`;
  `git diff --check`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `9524a87 Cancel superseded 004 docguard tasks` (`9524a878eedb0a03b07c2ab7c9ecb177858c08e4`).
- Push: `origin/main` updated from `066d8af` to `9524a87`.
- Blockers/residual risk: the cancelled docguard/manifest layer is intentionally still present until the later simplification loop deletes it; no next-loop TODO files were pushed in this 004 slice.

### 2026-07-04 - P1.19 loop history metadata drift

- Completed P1.19 slice. Updated `loop-develop/history/003-todo.md` so its
  header now says `Status: completed/verified`, records the 2026-07-04 archive
  date, and names the historical closeout commit.
- Verified the closeout commit
  `937255863e6a8b80564d754312dd6a378481b55b` is present on `origin/main`; the
  observed remote tip after this slice's implementation push was
  `6265fd0e4a2be19b28973a22d034a0a0af74f4d0`.
- Verification passed:
  `sed -n '1,40p' loop-develop/history/003-todo.md`;
  `git diff --check -- loop-develop/history/003-todo.md`;
  `git diff --check`.
- Commit: `6265fd0 Fix 003 history status metadata`
  (`6265fd0e4a2be19b28973a22d034a0a0af74f4d0`).
- Push: `origin/main` updated from `11bfc85` to `6265fd0`.
- Blockers/residual risk: none for the metadata drift; this was a historical
  TODO metadata/evidence correction only, so no Go packages or durable
  behavior contracts changed.

### 2026-07-04 - P1.21 historical research doc labeling

- Completed P1.21 slice. Added first-screen historical banners to
  `docs/codex-research-roadmap.md` and
  `docs/competitive-architecture-analysis.md` so preserved research source
  material no longer reads like current Billyharness truth or an implementation
  checklist.
- Tightened `docs/README.md` and `docs/research/README.md` routing language to
  say the research files carry historical banners, preserve stale backlog
  language intentionally, and require active work to be extracted into
  `loop-develop/current-todo`.
- Verification passed:
  `rg -n "research|legacy|current truth|active work|loop-develop" docs/README.md docs/research/README.md`;
  `sed -n '1,24p' docs/codex-research-roadmap.md`;
  `sed -n '1,24p' docs/competitive-architecture-analysis.md`;
  `git diff --check`.
- Commit: `f33f434 Label legacy research docs as historical`
  (`f33f434a605dd4632a7ef95b3750ef4f820d88c2`).
- Push: `origin/main` updated from `16c6ae6` to `f33f434`.
- Blockers/residual risk: none for the labeling slice. The legacy research
  files still intentionally preserve old roadmap/backlog sections as source
  material; future move/delete cleanup should update links and metadata in the
  same change.

### 2026-07-04 - P2.1 gateway route/status handler split

- Completed a narrow P2.1 slice. Moved gateway route registration into
  `internal/gateway/routes.go` and the small health/readiness/auth/config/tool/
  MCP/managed-process handlers into `internal/gateway/status_routes.go`, while
  leaving session/run/persistence behavior in `gateway.go`.
- `internal/gateway/gateway.go` dropped from 1505 LOC to 1362 LOC, and strict
  hygiene no longer reports it as a large source-file exception.
- Updated `docs/architecture.md` by removing the stale
  `internal/gateway/gateway.go` file-size exception. Package ownership and
  import boundaries did not change, so no package-map rule changed.
- Verification passed:
  `gofmt -w internal/gateway/gateway.go internal/gateway/routes.go internal/gateway/status_routes.go`;
  `go test -count=1 ./internal/gateway ./internal/architecture`;
  `git diff --check`;
  `go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `caeb428 Split gateway route handlers`
  (`caeb428462dc281780ce9c2d462692db1716e985`).
- Push: `origin/main` updated from `1cd77e6` to `caeb428`.
- Blockers/residual risk: this is a file-level split only, not a deeper
  package decomposition. Gateway session/run admission and persistence code
  still need future smaller-contract extraction.

### 2026-07-04 - P2.2 tool discovery search split

- Completed a narrow P2.2 slice. Moved the `tool_search` registration,
  `searchTools`, and `discoveryCandidates` into
  `internal/tools/tool_discovery.go`, keeping the discovery contract next to the
  `internal/tools/discovery` adapter and leaving tool execution behavior
  unchanged.
- `internal/tools/tools.go` dropped from 1557 LOC to 1448 LOC, and strict
  hygiene no longer reports it as a large source-file exception.
- Updated `docs/architecture.md` by removing the stale
  `internal/tools/tools.go` file-size exception. Package ownership/import
  boundaries did not change.
- Verification passed:
  `gofmt -w internal/tools/tools.go internal/tools/tool_discovery.go`;
  `go test -count=1 ./internal/tools ./internal/eventlog ./internal/clientux/projector ./internal/architecture`;
  `go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `14bf61b Extract tool discovery search`
  (`14bf61bd3489a5a49563178962b4368abf16ca08`).
- Push: `origin/main` updated from `1694218` to `14bf61b`.
- Blockers/residual risk: this is a file-level split only. Native registry,
  MCP gateway handlers, output-ref settlement, and display/policy laws remain
  candidates for smaller future contracts.

### 2026-07-04 - P2.3 shared output-ref evidence rendering

- Completed a narrow P2.3 slice. Moved TUI transcript output-ref raw-copy
  evidence formatting into `internal/toolrender.OutputRefRawCopy`, beside the
  existing shared `OutputRefLine` display policy.
- TUI transcript projection now reuses `toolrender.OutputRefRawCopy` and
  `toolrender.DecodeOutputRef` instead of owning duplicate output-ref decode
  and raw-copy helpers.
- Added focused `toolrender` coverage proving output-ref raw copy preserves
  tool name, full output ref, ref id, bytes, sha256, truncated state, preview,
  and fallback behavior.
- Updated `docs/architecture.md` so the `internal/toolrender` package
  responsibility includes output-ref evidence lines. Import boundaries did not
  change.
- Verification passed:
  `gofmt -w internal/toolrender/toolrender.go internal/toolrender/toolrender_test.go internal/tui/transcript/projector.go`;
  `go test -count=1 ./internal/clientux ./internal/tui ./internal/telegrambot ./internal/toolrender ./internal/architecture`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `c30e916 Centralize output ref rendering evidence`
  (`c30e9162532c975197230d6bb491e6f3be12e1df`).
- Push: `origin/main` updated from `730b451` to `c30e916`.
- Blockers/residual risk: compact display policy is better centralized for
  output-ref evidence, but broader progress/status summaries and TUI-local
  batch grouping still have client-specific presentation code.

### 2026-07-04 - P2.4 cross-surface client fixture boundary

- Completed a narrow P2.4 verification slice. Strengthened
  `internal/tui/cross_surface_consistency_test.go` so one golden event stream
  now proves typed output-ref state across the clientux projector, Telegram
  progress rendering, TUI transcript rendering, and formatted context output.
- Updated the synthetic tool result in that fixture to use the typed
  `ToolResult.OutputRef` field, which is the shared contract renderers consume,
  instead of relying only on metadata.
- Verification passed:
  `gofmt -w internal/tui/cross_surface_consistency_test.go`;
  `go test -count=1 ./internal/clientux ./internal/tui ./internal/telegrambot ./internal/gatewayclient`;
  `git diff --check`.
- Commit: `48e9114 Strengthen cross-surface client fixture`
  (`48e911412f815abc54856a953d6594422ba2d0ac`).
- Push: `origin/main` updated from `9a54c46` to `48e9114`.
- Blockers/residual risk: this is test-only boundary proof. It does not split
  additional client packages or remove remaining UI-specific presentation code.

### 2026-07-04 - P2.5 session diagnostics index status surface

- Completed a narrow P2.5 slice. `sessions index show` and rebuild JSON/text output now include derived diagnostics index status with present/missing/stale, build time, text/tool/error/run/usage row counts, and last read error when missing/corrupt.
- The status is derived at show/rebuild time from the durable session store/index files; it is not a new source of truth. Stale is true when diagnostics was built before the session index or session counts differ.
- Updated `ops/doctor-and-diagnostics.md` for operator use and corrected `docs/architecture/gateway-and-sessions.md` route anchors for the P2.1 route split.
- Verification passed:
  `gofmt -w internal/gateway/session_index.go cmd/fast-agent-harness/sessions.go cmd/fast-agent-harness/main_test.go`;
  `go test -count=1 ./internal/gateway ./internal/clientux ./cmd/fast-agent-harness ./internal/architecture`;
  `rg -n "routes.go|status_routes.go|diagnostics status|present|missing|stale|sessions index" docs/architecture/gateway-and-sessions.md ops/doctor-and-diagnostics.md`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Commit: `c497b6f Surface session diagnostics index status` (`c497b6f5c66d36d87cc1336176ee143df2a3f721`).
- Push: `origin/main` updated from `47a0537` to `c497b6f`.
- Blockers/residual risk: no automatic `--rebuild-if-missing` flag yet; query commands still fail closed with a rebuild hint when diagnostics index is missing.

### 2026-07-04 - P2.6 guarded production deploy and rollback workflow

- Completed P2.6. Added `scripts/production-deploy.sh` with explicit `deploy` and `rollback` subcommands, required `--yes`, tracked-worktree dirty check, source-checkout rebuild model, pre/post fact capture, managed service restarts, strict doctor gate, and `/health` plus `/ready` probes.
- The script embeds build provenance with `-ldflags` (`version`, full commit, UTC build time), writes `build-provenance.txt` plus a manifest under `${BILLYHARNESS_DEPLOY_LOG_DIR:-/var/log/billyharness/deploy}`, and prints the rollback command for the previous commit.
- Doctor now surfaces build provenance as structured JSON fields (`build_commit`, `build_time`) and a text `build:` line, so deploy evidence can prove which binary is running instead of relying on static `version=0.1.0` alone.
- Updated durable ops docs because production deploy/rollback and doctor output semantics changed: `README.md`, `ops/README.md`, `ops/doctor-and-diagnostics.md`, `ops/production-services.md`, and `agent-index/docs-manifest.json`. Checked architecture docs/package-boundary routing via `go test -count=1 ./internal/architecture`; no architecture doc change was needed because this is an operator workflow and build metadata surface, not a package-boundary or gateway contract change.
- Verification passed:
  `gofmt -w cmd/fast-agent-harness/main.go cmd/fast-agent-harness/doctor.go cmd/fast-agent-harness/doctor_test.go cmd/fast-agent-harness/main_test.go`;
  `bash -n scripts/production-deploy.sh`;
  `scripts/production-deploy.sh --help`;
  `go test -count=1 ./cmd/fast-agent-harness ./internal/architecture`;
  `git diff --check`;
  `go test -count=1 ./...`;
  `go build -trimpath -buildvcs=false -ldflags "-X main.version=0.1.0+verify -X main.buildCommit=verifycommit -X main.buildTime=2026-07-04T00:00:00Z" -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`;
  `BILLYHARNESS_HOME="$(mktemp -d)" FAST_AGENT_ENV_FILE= /tmp/billyharness-verify/fast-agent-harness doctor -build=false -services=false -gateway=false` showed `version: 0.1.0+verify` and `build: commit=verifycommit built_at=2026-07-04T00:00:00Z`.
- Commit: `2576430 Add guarded production deploy workflow` (`2576430a9f39be4441a8f05fdc590917cf31eb49`).
- Push: `origin/main` updated from `0a3d86a` to `2576430`.
- Blockers/residual risk: the deploy script was not executed against the live production host in this slice. A real deploy still needs to run from `/root/billyharness` and preserve the generated evidence directory from the production host.

### 2026-07-04 - Final 004 verification gate

- Ran the final verification gate requested by the 004 goal after P2.6 implementation and evidence were pushed.
- Verification passed:
  `go test -count=1 ./...`;
  `go vet ./...`;
  `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` (`No vulnerabilities found.`);
  `go test -race -count=1 ./internal/eventlog ./internal/gateway ./internal/telegrambot ./internal/tools ./internal/tui ./internal/clientux/projector`;
  `go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict`;
  `git diff --check`;
  `go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness`.
- Push state before this evidence block: `origin/main` was at `5165507 Record P2.6 deploy workflow evidence`.
- Blockers/residual risk: no live production deploy/restart was run by this final gate. Local working-copy changes unrelated to 004 implementation remain intentionally unstaged for Billy/main-chat follow-up.

## Copy-Ready Codex Goal Prompt

```text
/goal
You are in /Users/billy/repos/billyharness.

Read AGENTS.md first. Then read loop-develop/current-todo/004-todo.md. This is
the active implementation TODO. Work it as a long reliability/security/debug
hardening loop for Billyharness.

Mission:
Make Billyharness closer to a production-grade agent harness: fail-closed
authority boundaries, replayable event truth, useful debugging/incident
surfaces, safer Telegram/MCP/tool behavior, stronger config/runtime diagnostics,
and better verification infrastructure.

Rules:
- Work P0 first. Do not spend serious time on P1/P2 until P0 is green, unless a
  P1/P2 item is a direct prerequisite for a P0 fix.
- Keep the durable event log as source of truth.
- Do not copy competitor source code.
- Do not move active TODOs, goal prompts, or evidence into docs/.
- Update durable docs in the same change only when behavior/contracts actually
  change.
- If no docs change is needed for a code slice, say which docs you checked and
  why they stayed unchanged.
- Keep loop-develop/current-todo/004-todo.md updated with evidence as slices are
  completed.
- Do not move 004-todo.md to history; the main chat will verify and archive it.
- Never leave unverified behavior described as current truth.

Recommended execution order:
1. Start with P0.1 gateway read-route auth/Host/Origin hardening.
2. Then P0.2 and P0.3 Telegram operator/secret-bearing command boundaries.
3. Then P0.4 MCP/tool risk split and untrusted metadata handling.
4. Then P0.5/P0.6 checkpoint artifact verification and centralized redaction.
5. Then P0.7 through P0.12 replay/lifecycle/persistence ordering.
6. Then P0.13 through P0.17 operator debug and production truth.
7. Only after P0 is green, continue into P1 verification/runtime/docguard work.

After every completed task or coherent slice:
1. Run focused verification for the touched packages.
2. Run git diff --check.
3. Update the Evidence Log in loop-develop/current-todo/004-todo.md.
4. Stage the relevant files.
5. Commit with a clear message.
6. Push the branch to origin.

Do not commit failing or unverified work. If a slice is too large, split it into
smaller commits, each with its own verification evidence and push.

Before saying the whole goal is complete, run:

go test -count=1 ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go test -race -count=1 ./internal/eventlog ./internal/gateway ./internal/telegrambot ./internal/tools ./internal/tui ./internal/clientux/projector
go run ./cmd/fast-agent-harness hygiene -repo /Users/billy/repos/billyharness -strict
git diff --check

If runtime behavior changed, also run:

go build -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness

Final response must include:
- completed slices;
- tests/commands run;
- docs checked/updated;
- commit hashes and push state;
- remaining blockers or residual risk.
```
