# Post-Competitive Hardening And Decomposition TODO

Date: 2026-07-02
Status: new source-of-truth roadmap for the cleanup pass after the solo harness
competitive roadmap.

This roadmap is intentionally narrower than the previous feature roadmap. The
goal is to make the current harness boring, clean, testable, and easy to keep
extending after the large Codex/Claude/OpenCode-inspired implementation pass.

## Source Documents

- `/root/billyharness/docs/solo-harness-competitive-todo.md`
- `/root/billyharness/docs/architecture.md`
- `/root/billyharness/docs/harness-research-execution-todo.md`
- `/root/billyharness/docs/competitive-architecture-analysis.md`
- `/root/billyharness/docs/memory-systems-research.md`

## Initial Audit Snapshot

Captured on 2026-07-02.

- `git status -sb`:
  - `main...origin/main`
  - untracked: `docs/solo-harness-competitive-goal.md`
- `HEAD` equals `origin/main`:
  - `951cf21fc241c71053bcac4cc3ba616c878f516c`
  - `951cf21 Record Milestone 6 verification commit hash`
- `go test -count=1 ./internal/architecture` passed.
- `go run ./cmd/fast-agent-harness hygiene -strict` failed because of 6 large
  source/test files.
- Current tracked Go source size:
  - 279 tracked Go files
  - about 94k LOC under `internal` and `cmd`
- Largest packages by file count:
  - `internal/telegrambot`: 33 files
  - `internal/tools`: 31 files
  - `internal/config`: 20 files
  - `internal/tui`: 19 files
  - `internal/agent`: 19 files
  - `internal/gateway`: 18 files
- Large files reported by strict hygiene:
  - `internal/gateway/gateway.go`: 1767 LOC > 1500
  - `internal/tui/transcript_runtime.go`: 1559 LOC > 1500
  - `internal/tui/tui.go`: 1530 LOC > 1500
  - `internal/agent/tool_attempt_test.go`: 1416 LOC > 1200
  - `internal/tui/interaction_status_test.go`: 1391 LOC > 1200
  - `internal/gateway/session_events_test.go`: 1202 LOC > 1200

## Rules For This Pass

- Prefer pure decomposition, tests, docs, and smoke checks over new product
  features.
- Keep behavior compatible unless a test proves the current behavior is broken.
- Do not copy code from competitor repositories.
- Do not add new framework layers, databases, schedulers, background agents,
  marketplace mechanics, or enterprise policy.
- Do not rewrite package boundaries just because a package has many files.
  Split only when there is a clear ownership boundary and focused tests.
- Keep JSONL/event replay as the source of truth.
- Keep TUI and Telegram as clients of protocol/projector/toolrender state, not
  owners of runtime behavior.
- Keep commits scoped and pushed. Do not batch unrelated cleanup.

## Milestone 0 - Repo And Planning Hygiene (P0)

Goal: make the new active plan and git state clean before touching runtime code.

- [x] PH-00.1 Decide what to do with the untracked goal file.
  - target files: `docs/solo-harness-competitive-goal.md`,
    optional `docs/README.md`.
  - acceptance: either commit the file as historical artifact, fold it into the
    new goal docs, or intentionally remove it if obsolete. Record the choice.
  - verification: `git status --short` has no surprise untracked docs.
  - status: completed 2026-07-02.
  - evidence: kept `docs/solo-harness-competitive-goal.md` as a historical
    goal-prompt artifact instead of deleting or folding it, because it is the
    reproducible prompt for the completed solo competitive pass. Also committed
    the active `docs/post-competitive-hardening-todo.md` and
    `docs/post-competitive-hardening-goal.md` source-of-truth docs, and updated
    `docs/README.md` so the hardening TODO is the active cleanup roadmap while
    the solo roadmap is described as completed evidence. After push, `HEAD` and
    upstream both resolved to `456d4d46db912297dd50e700d6099be7c82a8c1a`, and
    `git status --short` was clean.
  - commit: `456d4d46db912297dd50e700d6099be7c82a8c1a`.

- [x] PH-00.2 Resolve active TODO `commit: pending` leftovers.
  - target files: `docs/solo-harness-competitive-todo.md`.
  - acceptance: final verification blocks for Milestone 4 and Milestone 5 no
    longer say `commit: pending` unless a real missing commit is identified.
  - verification: `rg -n "commit: pending" docs/solo-harness-competitive-todo.md`
    returns no active-roadmap pending entries.
  - status: completed 2026-07-02.
  - evidence: replaced the Milestone 4 final verification placeholder with the
    pushed verification `HEAD`
    `e346c6b8d21aa1202e0afe3207cb119711e20bf0`, and replaced the Milestone 5
    placeholder with pushed verification `HEAD`
    `3218d79563b361b6f0eba014640de2d195b009c4`. No real missing commit was
    identified; both hashes were already recorded in the corresponding
    verification evidence.
  - verification evidence: `rg -n "commit: pending"
    docs/solo-harness-competitive-todo.md` returned no matches.
  - commit: `8835131b187953c066bada622bd53a9c4dc6025d`.

- [x] PH-00.3 Mark old harness-research roadmap as historical if it is no
  longer active.
  - target files: `docs/harness-research-execution-todo.md`,
    optional `docs/README.md`.
  - acceptance: avoid rewriting dozens of historical `commit: pending` lines;
    instead add a short status note saying the active follow-up roadmap is this
    file, unless those old entries are still intended to be resolved.
  - verification: manual diff review.
  - status: completed 2026-07-02.
  - evidence: updated the header note in
    `docs/harness-research-execution-todo.md` to point current follow-up work
    at `docs/post-competitive-hardening-todo.md`, while preserving the old
    checklist and its historical `commit: pending` entries unchanged.
  - verification evidence: manual diff review confirmed only the historical
    status note and this PH-00.3 TODO entry changed.
  - commit: `1401e38198e2508f6b7835dab0a1b5808b6ff382`.

## Milestone 1 - Strict Hygiene And Decomposition (P0)

Goal: make `fast-agent-harness hygiene -strict` pass without changing behavior.

- [x] PH-01.1 Split `internal/gateway/gateway.go`.
  - current issue: 1767 LOC, above the 1500 LOC source budget.
  - likely seams: server construction/config, session CRUD routes, run routes,
    context/config/auth routes, benchmark/trace routes, small response helpers.
  - acceptance: no route behavior changes; exported API surface does not grow
    unless necessary; `internal/gateway` tests still pass.
  - verification:
    `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/eventlog`
  - status: completed 2026-07-02.
  - evidence: split benchmark route/artifact helpers into
    `internal/gateway/benchmark_routes.go` and stream/JSON response helpers
    into `internal/gateway/response.go`. The split is package-local and does
    not add exported API surface or import edges. `internal/gateway/gateway.go`
    is now 1445 LOC, below the 1500 LOC source budget.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/gateway
    ./internal/gatewayclient ./internal/eventlog` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `7f919a3b8d224ceb040b3e35fb33ef9b666d4f91`.

- [x] PH-01.2 Split `internal/tui/transcript_runtime.go`.
  - current issue: 1559 LOC, above the 1500 LOC source budget.
  - likely seams: event-to-cell projection, context/status cells, tool cells,
    streaming/live-tail cells, selection/no-select metadata.
  - acceptance: raw/rich transcript mode, no-select copy, collapsed tools, and
    context cells render exactly as before.
  - verification:
    `go test -count=1 ./internal/tui ./internal/tui/transcript ./internal/tui/render ./internal/tui/selection`
  - status: completed 2026-07-02.
  - evidence: split semantic copy and viewport selection helpers into
    `internal/tui/selection_runtime.go`, leaving transcript projection,
    context/status cells, tool cells, streaming cells, and render caching in
    `internal/tui/transcript_runtime.go`. `transcript_runtime.go` is now 1403
    LOC, below the 1500 LOC source budget.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tui
    ./internal/tui/transcript ./internal/tui/render ./internal/tui/selection`
    passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `b6b344c5e322d4756ef8bd2ca62584e859f55061`.

- [x] PH-01.3 Split `internal/tui/tui.go`.
  - current issue: 1530 LOC, above the 1500 LOC source budget.
  - likely seams: layout sizing, input/key handling, mouse/selection handling,
    update loop helpers, status/footer rendering.
  - acceptance: no regression in SSH keyboard input, mouse scroll/selection,
    slash popup, theme, statusline, or gateway/local mode.
  - verification:
    `go test -count=1 ./internal/tui ./internal/tui/selection ./internal/tui/render`
  - status: completed 2026-07-02.
  - evidence: split runtime config projection and model/profile/theme/access
    mode/view selection helpers into `internal/tui/runtime_config.go`. The
    split is package-local, keeps Bubble Tea state and gateway/local runtime
    behavior in `internal/tui`, and does not add import edges or exported API.
    `internal/tui/tui.go` is now 1150 LOC, below the 1500 LOC source budget.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/tui
    ./internal/tui/selection ./internal/tui/render` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `1c101defee44404d409e3467bf87df31b35e32a4`.

- [x] PH-01.4 Split oversized focused test files.
  - current issues:
    - `internal/agent/tool_attempt_test.go`: 1416 LOC
    - `internal/tui/interaction_status_test.go`: 1391 LOC
    - `internal/gateway/session_events_test.go`: 1202 LOC
  - acceptance: move tests by behavior area without weakening assertions or
    introducing shared mutable test state.
  - verification:
    `go test -count=1 ./internal/agent ./internal/tui ./internal/gateway`
  - status: completed 2026-07-02.
  - evidence: split agent parallel/cancel/output-ref/MCP cases into
    `internal/agent/tool_attempt_parallel_test.go`, TUI transcript-selection
    cases into `internal/tui/interaction_selection_test.go`, and gateway
    stream writer/stall cases into
    `internal/gateway/session_stream_events_test.go`. Assertions and helper
    usage were preserved; no shared mutable fixtures were added. New line
    counts are 909/523 LOC for the agent tests, 1100/301 LOC for the TUI
    tests, and 1043/172 LOC for the gateway tests, all below the 1200 LOC
    `_test.go` budget.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/agent ./internal/tui
    ./internal/gateway` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `32e4ea7a6472a0d59b0bb9e51f90f831a33a6280`.

- [x] PH-01.5 Make strict hygiene pass.
  - acceptance: no handwritten `.go` file exceeds 1500 LOC and no `_test.go`
    file exceeds 1200 LOC unless `docs/architecture.md` records a temporary
    exception with owner and removal plan.
  - verification:
    `go run ./cmd/fast-agent-harness hygiene -strict`
  - status: completed 2026-07-02.
  - evidence: strict hygiene reported `tracked Go files: 286` and
    `large source files: none`, so no file-size exception is needed in
    `docs/architecture.md`.
  - verification evidence:
    `/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict`
    passed.
  - commit: `229a03b79ab9c0fe3cc857ec3cfb190784c2c122`.

## Milestone 2 - Boundary And Complexity Audit (P0)

Goal: verify decomposition helped architecture instead of just moving lines.

- [x] PH-02.1 Re-run and review architecture guard after each split.
  - target files: `docs/architecture.md`, `internal/architecture/*`.
  - acceptance: new files follow existing package responsibilities; no runtime
    package imports presentation, and clients do not import gateway server or
    provider internals.
  - verification:
    `go test -count=1 ./internal/architecture`
  - status: completed 2026-07-02.
  - evidence: all decomposition files added during PH-01 were package-local
    (`internal/gateway`, `internal/tui`, and focused package tests) and added
    no new internal package import edges. The architecture map already covers
    the affected package responsibilities, so no `docs/architecture.md`
    boundary change or file-size exception is needed.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `168eabc02d0a59de1323825a06f8495cdd0d7b0f`.

- [ ] PH-02.2 Review large packages by responsibility, not by file count.
  - target packages: `internal/telegrambot`, `internal/tools`,
    `internal/config`, `internal/tui`, `internal/agent`, `internal/gateway`.
  - acceptance: write a short note in this file for each package: keep as-is,
    split later with reason, or split now with a concrete owner boundary.
  - verification: manual import/ownership review plus architecture guard.
  - status: open.

- [ ] PH-02.3 Remove accidental duplicate abstractions if found.
  - likely areas: context formatting, command metadata, tool display, output
    refs, MCP prompt metadata, memory command plumbing.
  - acceptance: shared logic stays in `clientux`, `commandregistry`,
    `toolrender`, `tooloutput`, or `memory` rather than being reimplemented in
    TUI and Telegram.
  - verification: focused tests for any touched package.
  - status: open.

## Milestone 3 - Full Regression And Runtime Smoke (P0)

Goal: prove the cleaned code still behaves like the current deployed harness.

- [ ] PH-03.1 Run the post-roadmap package suite.
  - verification:
    `go test -count=1 ./internal/agent ./internal/provider ./internal/config ./internal/modelinfo ./internal/tools ./internal/toolrender ./internal/tooloutput ./internal/clientux ./internal/clientux/projector ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot ./internal/trace ./internal/eventlog ./internal/memory ./internal/commandregistry ./internal/mcpclient ./internal/mcpstatus ./cmd/fast-agent-harness`
  - acceptance: pass or record exact failures and next actions.
  - status: open.

- [ ] PH-03.2 Run broad tests if focused suites are green.
  - verification:
    `go test -count=1 ./...`
  - acceptance: pass, or record exact failing package/test and decide whether
    it is a real regression, flaky test, or unrelated environment issue.
  - status: open.

- [ ] PH-03.3 Build the binary from current source.
  - verification:
    `go build -o bin/fast-agent-harness ./cmd/fast-agent-harness`
  - acceptance: binary is current after runtime/CLI changes.
  - status: open.

- [ ] PH-03.4 Run CLI smoke checks.
  - suggested verification:
    - `./bin/fast-agent-harness config inspect -json`
    - `./bin/fast-agent-harness commands list`
    - `./bin/fast-agent-harness memory list`
    - `./bin/fast-agent-harness sessions list`
    - `./bin/fast-agent-harness hygiene -strict`
  - acceptance: commands succeed or fail only for expected missing local data,
    with clear errors and no panics.
  - status: open.

## Milestone 4 - Deployed Service Reality Check (P1)

Goal: avoid the classic trap where source is clean but Telegram/TUI still runs
an old binary.

- [ ] PH-04.1 Inspect running gateway and Telegram processes.
  - acceptance: identify process command paths, PIDs, binary timestamps, and
    whether they point to `/root/billyharness/bin/fast-agent-harness`.
  - verification: `ps`, PID files, and service logs as appropriate.
  - status: open.

- [ ] PH-04.2 Restart deployed gateway/Telegram only after a current binary
  exists and tests pass.
  - acceptance: restart command is documented in evidence; services come back;
    `/ready`, Telegram `/status`, and TUI gateway discovery work.
  - status: open.

- [ ] PH-04.3 Verify Telegram and TUI smoke flows after restart.
  - acceptance: `/context`, `/commands`, `/memory list`, `/mcp`, a simple
    prompt, and interruption by a second prompt work without stale tool cells.
  - status: open.

## Milestone 5 - Nice-To-Have Polish After Hygiene (P1)

Goal: use the cleaner code to remove remaining daily annoyances, but only after
P0 is green.

- [ ] PH-05.1 Improve `/context` readability if helper/cache/prompt sections
  are too noisy in real Telegram/TUI output.
  - acceptance: same data, better grouping; no hidden accounting.
  - status: open.

- [ ] PH-05.2 Add one command that summarizes current harness health.
  - idea: `fast-agent-harness doctor runtime` or extending existing `doctor`.
  - acceptance: reports config, auth presence, gateway URL, service binary age,
    strict hygiene status, session store size, tool-output size, and current
    model/provider.
  - status: open.

- [ ] PH-05.3 Add benchmarks only for measured hot spots.
  - likely targets: web summary/context bloat, toolrender rendering, gateway
    replay, TUI reflow.
  - acceptance: benchmark has a decision attached; no vanity benchmark pile.
  - status: open.

## Completion Criteria

This cleanup pass is done when:

- active roadmap docs are clean and committed;
- `go run ./cmd/fast-agent-harness hygiene -strict` passes;
- architecture guard passes;
- focused package suites pass;
- broad `go test -count=1 ./...` either passes or has documented non-code
  blockers;
- current binary is rebuilt after code changes;
- gateway/TUI/Telegram smoke checks are documented if deployed services are
  touched;
- every completed or blocked task has evidence and a scoped pushed commit.
