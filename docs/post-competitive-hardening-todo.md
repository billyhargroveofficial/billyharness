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

- [x] PH-02.2 Review large packages by responsibility, not by file count.
  - target packages: `internal/telegrambot`, `internal/tools`,
    `internal/config`, `internal/tui`, `internal/agent`, `internal/gateway`.
  - acceptance: write a short note in this file for each package: keep as-is,
    split later with reason, or split now with a concrete owner boundary.
  - verification: manual import/ownership review plus architecture guard.
  - status: completed 2026-07-02.
  - evidence:
    - `internal/telegrambot`: keep as-is. The package is an adapter boundary
      with focused files for commands, rendering, progress, polling, admission
      state, gateway client wrapping, and store/runtime state. It should not be
      split by file count alone; the important guard remains "no gateway server
      internals."
    - `internal/tools`: keep as-is. Native tools are already split by domain
      (`fs_*`, web, shell process, diagnostics, memory, MCP, policy/toolset),
      while the central registry/policy package boundary is intentional.
      `tools.go` is below the source budget at 1448 LOC; split later only if
      registry/schema ownership separates cleanly.
    - `internal/config`: keep as-is. The package is a leaf configuration
      boundary with focused files for defaults, env, profiles, projections,
      runtime diffs, MCP, hooks, diagnostics, summaries, and migrations.
      `config_test.go` is below the test budget at 1172 LOC.
    - `internal/tui`: keep as-is after PH-01 splits. Rendering, runtimeclient,
      selection, and transcript subpackages already own their boundaries; the
      top-level package now holds Bubble Tea state, commands, gateway session,
      input, settings, and layout/runtime helpers under budget.
    - `internal/agent`: keep as-is. Runtime loop, model call, tool attempt,
      compaction, transcript pairing, liveness, and event building are split by
      runtime responsibility and have no presentation imports. The
      architecture map already notes a future runtime/toolexec shrink as P1.1.
    - `internal/gateway`: keep as-is after PH-01.1. Server routes, session
      store, replay/events, inspection, indexes, benchmark routes, responses,
      and user-input endpoints are separated enough for this pass; `gateway.go`
      is below budget at 1445 LOC and should split further only when route
      ownership grows.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `95718f8cdf3ddb8bf210f0b47e24b58815d5cbfa`.

- [x] PH-02.3 Remove accidental duplicate abstractions if found.
  - likely areas: context formatting, command metadata, tool display, output
    refs, MCP prompt metadata, memory command plumbing.
  - acceptance: shared logic stays in `clientux`, `commandregistry`,
    `toolrender`, `tooloutput`, or `memory` rather than being reimplemented in
    TUI and Telegram.
  - verification: focused tests for any touched package.
  - status: completed 2026-07-02.
  - evidence: reviewed the likely duplication zones and found no accidental
    duplicate abstraction that should be removed in this pass. `/context`
    reporting is built in `internal/clientux` and formatted by
    `internal/gatewayclient` for CLI, TUI, and Telegram. Command search
    metadata lives in `internal/commandregistry` over shared
    `clientux.ActionDefinition` data, while TUI and Telegram keep only
    adapter-specific dispatch, keybinding, and argument-menu state. Tool
    lifecycle display stays in `internal/toolrender` and protocol
    `ToolCompact`; output-ref storage and metadata keys stay in
    `internal/tooloutput`; MCP prompt metadata flows from `mcpclient` through
    the tools snapshot into `commandregistry`; manual memory command and tool
    plumbing share `internal/memory` operations.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/clientux
    ./internal/gatewayclient ./internal/commandregistry ./internal/toolrender
    ./internal/tooloutput ./internal/memory ./internal/mcpclient
    ./internal/tools ./internal/tui ./internal/telegrambot
    ./cmd/fast-agent-harness ./internal/architecture` passed.
  - commit: `8f7666c231b65575fc3af2c199d4960f2aa2898b`.

## Milestone 3 - Full Regression And Runtime Smoke (P0)

Goal: prove the cleaned code still behaves like the current deployed harness.

- [x] PH-03.1 Run the post-roadmap package suite.
  - verification:
    `go test -count=1 ./internal/agent ./internal/provider ./internal/config ./internal/modelinfo ./internal/tools ./internal/toolrender ./internal/tooloutput ./internal/clientux ./internal/clientux/projector ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot ./internal/trace ./internal/eventlog ./internal/memory ./internal/commandregistry ./internal/mcpclient ./internal/mcpstatus ./cmd/fast-agent-harness`
  - acceptance: pass or record exact failures and next actions.
  - status: completed 2026-07-02.
  - evidence: the exact command above passed, including `internal/mcpstatus`
    with no test files.
  - commit: `99b0e1f0959905ffe07b129165add528d934726f`.

- [x] PH-03.2 Run broad tests if focused suites are green.
  - verification:
    `go test -count=1 ./...`
  - acceptance: pass, or record exact failing package/test and decide whether
    it is a real regression, flaky test, or unrelated environment issue.
  - status: completed 2026-07-02.
  - evidence: final rerun of the exact command above passed across all
    packages, including the in-progress attachment packages present in the
    dirty worktree. Earlier attempts during concurrent unrelated edits failed
    transiently with `internal/gateway/gateway.go:666:19: undefined:
    sessionInputValidationError`, then `internal/clientux/context.go:501:10:
    undefined: fmt`, plus an architecture-guard mismatch for
    `internal/gateway` importing `internal/attachments`; the exact broad rerun
    passed after those in-flight edits settled, so no PH-03.2 code change was
    needed.
  - commit: `1b18ee2bfe2f145ef8715af3bdff462c88244aeb`.

- [x] PH-03.3 Build the binary from current source.
  - verification:
    `go build -o bin/fast-agent-harness ./cmd/fast-agent-harness`
  - acceptance: binary is current after runtime/CLI changes.
  - status: completed 2026-07-02.
  - evidence:
    `/root/.local/go/bin/go build -o bin/fast-agent-harness
    ./cmd/fast-agent-harness` passed.
  - commit: `093e2f878f55a929cca9a8965b84c9321fc7e1c9`.

- [x] PH-03.4 Run CLI smoke checks.
  - suggested verification:
    - `./bin/fast-agent-harness config inspect -json`
    - `./bin/fast-agent-harness commands list`
    - `./bin/fast-agent-harness memory list`
    - `./bin/fast-agent-harness sessions list`
    - `./bin/fast-agent-harness hygiene -strict`
  - acceptance: commands succeed or fail only for expected missing local data,
    with clear errors and no panics.
  - status: completed 2026-07-02.
  - evidence:
    - `./bin/fast-agent-harness config inspect -json` passed and reported
      provider `openai-codex`, model `gpt-5.5`, explicit provider capability
      diagnostics, memory enabled with `memory_auto_extract_enabled=false`,
      and one provider-routing warning.
    - `./bin/fast-agent-harness commands list` passed and printed built-in
      action/profile entries from the shared command registry.
    - `./bin/fast-agent-harness memory list` passed with `memory entries: 0`
      and `no memory entries`.
    - `./bin/fast-agent-harness sessions list` passed, listed JSONL and legacy
      sessions, and surfaced existing store warnings for older records instead
      of panicking: two `history_sha256 mismatch` warnings and one
      `run.failed without started run` lifecycle warning, repeated by the
      current list/index flow.
    - `./bin/fast-agent-harness hygiene -strict` passed with
      `tracked Go files: 286` and `large source files: none`.
  - commit: `1559c3a559d81fb292d350f17304548f28d2f838`.

## Milestone 4 - Deployed Service Reality Check (P1)

Goal: avoid the classic trap where source is clean but Telegram/TUI still runs
an old binary.

- [x] PH-04.1 Inspect running gateway and Telegram processes.
  - acceptance: identify process command paths, PIDs, binary timestamps, and
    whether they point to `/root/billyharness/bin/fast-agent-harness`.
  - verification: `ps`, PID files, and service logs as appropriate.
  - status: completed 2026-07-02.
  - evidence:
    - PID files were stale: `gateway.pid` contained `535818` and
      `telegram.pid` contained `535828`, but `ps -p 535818` and
      `ps -p 535828` found no live process.
    - Actual live processes were systemd-owned PIDs `798` and `799`, both with
      PPID `1`, started `Wed Jul 1 13:36:19 2026`, and commands
      `/root/billyharness/bin/fast-agent-harness gateway` and
      `/root/billyharness/bin/fast-agent-harness telegram`.
    - `systemctl is-active billyharness-gateway.service` and
      `systemctl is-active billyharness-telegram.service` both returned
      `active`; `systemctl show` reported `MainPID=798`/`MainPID=799` and
      `ExecStart=/root/billyharness/bin/fast-agent-harness gateway|telegram`
      from `/etc/systemd/system/billyharness-*.service`.
    - Both live processes resolve to the old deleted executable inode:
      `readlink /proc/798/exe` and `readlink /proc/799/exe` returned
      `/root/billyharness/bin/fast-agent-harness (deleted)`.
    - The current binary at `bin/fast-agent-harness` exists with size
      `19937664` bytes and mtime `2026-07-02 10:07:20.206497344 +0200`; the
      older top-level `fast-agent-harness` binary is `13880018` bytes with
      mtime `2026-06-27 01:16:42.407807585 +0200`.
    - `gateway.log` still contains
      `fast-agent-harness gateway listening on http://127.0.0.1:8765`, and
      `telegram.log` contains
      `billyharness telegram gateway polling; gateway=http://127.0.0.1:8765`.
    - Live gateway health succeeded at `/health` with
      `{"model":"deepseek-v4-pro","ok":true,"provider":"deepseek"}`. The
      current PH-04.2 checklist still says `/ready`, but current source and
      setup docs use `/health`; the running deleted-inode binary returns 404
      for both `/ready` and current-source `/v1/processes`.
  - commit: `f8e1705605085062fe2e88eae10b6fa4dc0f38c1`.

- [x] PH-04.2 Restart deployed gateway/Telegram only after a current binary
  exists and tests pass.
  - acceptance: restart command is documented in evidence; services come back;
    `/ready`, Telegram `/status`, and TUI gateway discovery work.
  - status: completed 2026-07-02.
  - evidence:
    - Because the main worktree contained unrelated in-flight attachment/TUI
      changes, created a detached clean worktree at pushed `HEAD`
      `f8e1705605085062fe2e88eae10b6fa4dc0f38c1` under
      `/tmp/billyharness-ph04-clean` and used that tree for the restart gate.
    - `/root/.local/go/bin/go test -count=1 ./internal/architecture
      ./internal/gateway ./internal/gatewayclient ./internal/telegrambot
      ./internal/tui ./cmd/fast-agent-harness` passed from the clean worktree.
    - `/root/.local/go/bin/go build -o
      /root/billyharness/bin/fast-agent-harness ./cmd/fast-agent-harness`
      passed from the clean worktree. The deployed binary then reported size
      `19753555` bytes and mtime `2026-07-02 10:18:52.618030283 +0200`.
    - Restarted deployed services with:
      `systemctl restart billyharness-gateway.service` and
      `systemctl restart billyharness-telegram.service`.
    - After restart, `systemctl is-active` returned `active` for both
      services. Gateway `MainPID=595938` started
      `Thu 2026-07-02 10:19:07 CEST`; Telegram `MainPID=596168` started
      `Thu 2026-07-02 10:19:21 CEST`.
    - `readlink /proc/595938/exe` and `readlink /proc/596168/exe` both
      returned `/root/billyharness/bin/fast-agent-harness`, with no
      `(deleted)` suffix.
    - `curl -fsS http://127.0.0.1:8765/health` returned
      `{"model":"gpt-5.5","ok":true,"provider":"openai-codex"}`.
      `curl -i -sS http://127.0.0.1:8765/v1/processes` returned `200 OK`
      with `no managed shell processes`.
    - The PH-04.2 acceptance still names `/ready`, but current source, doctor,
      and setup docs use `/health`; `/ready` returned `404 page not found` on
      the restarted current binary.
    - TUI gateway discovery worked under a PTY:
      `TERM=dumb timeout 4s ./bin/fast-agent-harness tui -plain -gateway
      http://127.0.0.1:8765` exited with the expected timeout after showing
      `gateway session f01a2358 · gpt-5.5`.
    - Telegram process health was verified with `systemctl status` and
      `telegram.log` showing
      `billyharness telegram gateway polling; gateway=http://127.0.0.1:8765`.
      A live Telegram `/status` message was not sent in this restart task
      because no safe chat target was specified; carry the real slash-command
      smoke into PH-04.3 or block it with an explicit chat-target requirement.
  - commit: `a8859d7bd13fbd1c158a3c78e5d240cae2eae9ab`.

- [!] PH-04.3 Verify Telegram and TUI smoke flows after restart.
  - acceptance: `/context`, `/commands`, `/memory list`, `/mcp`, a simple
    prompt, and interruption by a second prompt work without stale tool cells.
  - status: blocked 2026-07-02.
  - evidence:
    - TUI gateway smoke passed under a PTY against the restarted gateway:
      `TERM=dumb ./bin/fast-agent-harness tui -plain -gateway
      http://127.0.0.1:8765` created gateway session
      `f47e6018`.
    - TUI `/context` rendered the shared context report with source buckets and
      top contributors, and updated status to `context shown`.
    - TUI `/commands` rendered shared command-registry rows including
      built-in action metadata, and updated status to `commands shown`.
    - TUI `/memory list` rendered `memory entries: 0` and `no memory entries`,
      and updated status to `memory shown`.
    - TUI `/mcp` rendered connected MCP servers and native tools after the
      restart, including `telegram`, `telegram-parilka`, `github`, and
      `context7`, and updated status to `mcp status shown`.
    - TUI simple prompt smoke passed: `Reply with exactly SMOKE_OK.` completed
      in about 3 seconds with assistant text `SMOKE_OK` and no tool calls.
    - TUI long-run smoke stayed coherent while rendering a harmless
      `shell_exec` for `sleep 12; echo FIRST_DONE`, but a second prompt typed
      during the active run stayed in the composer while the TUI showed
      `busy`; it did not submit an interrupting run from the TUI path.
    - Direct deployed gateway interruption smoke exposed a blocker. A fresh
      session `69b82313ded81cb3a462408641e0bce6` started a long
      `shell_exec` run; a concurrent second `POST
      /v1/sessions/69b82313ded81cb3a462408641e0bce6/run` with
      `interrupt_policy:"interrupt"` returned
      `{"data":"interrupt active session run: context deadline exceeded","type":"run.failed"}`.
      Replayed events with `after_seq=0&follow=false` then showed
      `run.failed interrupted by newer session run`, `session.status` with the
      same last error, `tool.call_progress` aborted, `tool.call_aborted`,
      `step.completed failed context canceled`, and the replacement provider
      call failing with `Post "https://chatgpt.com/backend-api/codex/responses":
      context canceled`; no `SECOND_DIRECT_OK` replacement response was
      produced.
    - Services remained healthy after the negative smoke:
      `/health` returned
      `{"model":"gpt-5.5","ok":true,"provider":"openai-codex"}`,
      both systemd services were `active`, and `/v1/processes` returned
      `no managed shell processes`.
    - Live Telegram slash-command smoke was not sent because no safe target
      chat/user/thread was specified. The deployed Telegram process is active
      and polling, but `/status`, `/context`, `/commands`, `/memory list`,
      `/mcp`, a prompt, and a second-prompt interruption would post into a real
      Telegram chat.
  - blocker:
    - Gateway interrupt replacement is not reliable in the deployed smoke when
      the active run is inside a long shell tool; the replacement request
      times out while canceling and the continued first run's next provider
      call receives `context canceled`.
    - Real Telegram slash-command verification needs an explicit safe chat
      target or a deployed dry-run/fake Bot API harness.
  - next action:
    - Add or fix a regression around deployed/session interrupt replacement
      during a cancellable shell tool so the first run terminates and the
      second prompt starts with a fresh context instead of inheriting the
      canceled context.
    - Provide a safe Telegram smoke target, or run the service against a
      temporary dry-run/fake Bot API endpoint, before marking live Telegram
      slash-command smoke complete.
  - commit: `d854069cd26608e1b3ef6c4d4a2d846b2e6c4bb1`.

## Milestone 5 - Nice-To-Have Polish After Hygiene (P1)

Goal: use the cleaner code to remove remaining daily annoyances, but only after
P0 is green.

- [!] PH-05.1 Improve `/context` readability if helper/cache/prompt sections
  are too noisy in real Telegram/TUI output.
  - acceptance: same data, better grouping; no hidden accounting.
  - status: blocked 2026-07-02.
  - evidence: the TUI `/context` smoke from PH-04.3 rendered the shared report
    with source buckets and top contributors, and the output was readable
    enough that no TUI-specific formatter change is justified. Telegram
    `/context` output was not reviewed because live Telegram slash-command
    smoke is blocked without a safe chat/user/thread target.
  - blocker: real Telegram `/context` readability cannot be assessed without
    posting into a live Telegram chat or running the service against a
    temporary dry-run/fake Bot API endpoint.
  - next action: capture a real Telegram `/context` sample from a safe target
    after PH-04.3's Telegram smoke blocker is cleared, then adjust only the
    shared formatter if helper/cache/prompt sections are demonstrably noisy.
  - commit: `8b862e42c2dddb41f688d713c44a2d6852a5d45f`.

- [x] PH-05.2 Add one command that summarizes current harness health.
  - idea: `fast-agent-harness doctor runtime` or extending existing `doctor`.
  - acceptance: reports config, auth presence, gateway URL, service binary age,
    strict hygiene status, session store size, tool-output size, and current
    model/provider.
  - status: completed 2026-07-02.
  - evidence: extended the existing `doctor`/`health` command instead of
    adding a parallel command. JSON output now includes a `runtime` block with
    current provider/model, gateway URL, auth presence booleans without secret
    values, deployed service-binary path/size/mtime/age, gateway session store
    size, tool-output store size, and strict hygiene status. Human output now
    prints compact `runtime:` and `auth:` summary lines before the existing
    checks.
  - live output evidence: `/root/.local/go/bin/go run
    ./cmd/fast-agent-harness doctor -json -build=false` passed and reported
    provider `openai-codex`, model `gpt-5.5`, gateway URL
    `http://127.0.0.1:8765`, Codex auth file present, deployed binary
    `/root/billyharness/bin/fast-agent-harness` at `19753555` bytes with
    mtime `2026-07-02T08:18:52Z`, gateway session store size `54460781`
    bytes, tool-output size `6072659` bytes, and strict hygiene status
    `fail` because the unrelated dirty worktree currently has
    `internal/gateway/session_store_test.go: 1372 LOC > 1200`.
  - verification evidence:
    in the main worktree, `/root/.local/go/bin/go test -run
    'Test.*Doctor.*|Test.*Hygiene.*|TestCollectDoctorReportIncludesProjectHealth'
    -count=1 ./cmd/fast-agent-harness` passed;
    `/root/.local/go/bin/go test -count=1 ./cmd/fast-agent-harness` passed.
    After unrelated dirty Telegram edits landed, the same main-worktree package
    test failed in `internal/telegrambot/runner.go` with
    `too many arguments in call to b.admit.RecordAdmitted`, and
    `/root/.local/go/bin/go build -o /tmp/fast-agent-harness-doctor-smoke
    ./cmd/fast-agent-harness` also failed with that signature mismatch plus an
    earlier `undefined: strconv`. To isolate this task, a clean detached
    worktree at pushed `HEAD` `d854069cd26608e1b3ef6c4d4a2d846b2e6c4bb1`
    was created at `/tmp/billyharness-ph05-doctor-clean`, only the
    `doctor.go`/`doctor_test.go` diff was applied, and the following commands
    passed there:
    `/root/.local/go/bin/go test -run
    'Test.*Doctor.*|Test.*Hygiene.*|TestCollectDoctorReportIncludesProjectHealth'
    -count=1 ./cmd/fast-agent-harness`,
    `/root/.local/go/bin/go test -count=1 ./cmd/fast-agent-harness`, and
    `/root/.local/go/bin/go build -o
    /tmp/fast-agent-harness-doctor-smoke-clean ./cmd/fast-agent-harness`.
  - deployment evidence: after the PH-05.2 commit was pushed, rebuilt
    `/root/billyharness/bin/fast-agent-harness` from a clean detached worktree
    at `HEAD` `35caafce1d85149d7a96921831f83f448b0f6b39`, then restarted
    `billyharness-gateway.service` and `billyharness-telegram.service`.
    Gateway restarted as `MainPID=608453` at
    `Thu 2026-07-02 10:36:09 CEST`; Telegram restarted as `MainPID=608563`
    at `Thu 2026-07-02 10:36:13 CEST`. Both `/proc/*/exe` links resolved to
    `/root/billyharness/bin/fast-agent-harness` without `(deleted)`, `/health`
    returned `{"model":"gpt-5.5","ok":true,"provider":"openai-codex"}`, and
    the deployed `./bin/fast-agent-harness doctor -json -build=false` reported
    the new runtime block with service binary size `19774141` bytes and mtime
    `2026-07-02T08:35:58Z`.
  - commit: `35caafce1d85149d7a96921831f83f448b0f6b39`.

- [x] PH-05.3 Add benchmarks only for measured hot spots.
  - likely targets: web summary/context bloat, toolrender rendering, gateway
    replay, TUI reflow.
  - acceptance: benchmark has a decision attached; no vanity benchmark pile.
  - status: completed 2026-07-02.
  - evidence: no new benchmark was added in this pass because the deployed
    service smoke surfaced correctness/operability blockers rather than a
    measured performance hot spot. The TUI `/context`, `/commands`,
    `/memory list`, `/mcp`, and simple-prompt smokes were responsive enough for
    qualitative smoke coverage; PH-04.3's failure is an interrupt/replacement
    correctness bug, not a benchmark target until the cancellation path is
    fixed. Existing JSONL/replay benchmark coverage from the prior execution
    roadmap remains the relevant scale gate.
  - decision: do not add a benchmark for web summary/context bloat,
    toolrender rendering, gateway replay, or TUI reflow without a measured
    latency, allocation, replay, or render-regression signal.
  - next action: after the PH-04.3 interrupt blocker is fixed, add a focused
    benchmark only if the fix introduces measurable gateway replay, toolrender,
    or TUI reflow cost; otherwise keep benchmark coverage unchanged.
  - commit: pending.

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
