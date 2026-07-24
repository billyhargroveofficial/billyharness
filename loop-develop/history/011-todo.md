# 011 — Full-screen multi-agent jobs control center (completed)

## Goal

Make durable multi-agent jobs a first-class BillyHarness TUI workflow. A user
must be able to open `/jobs`, launch any built-in scenario through a keyboard-
only wizard, watch the durable outer orchestrator and its workers, control the
job, and read or copy the final result without composing CLI flag walls.

The TUI remains a projection and control-plane client. The gateway owns durable
execution, recovery, provider construction, authority, and budgets; closing the
TUI must never stop a job. Local-only TUI mode must fail clearly instead of
creating an in-process scheduler with weaker durability.

## Research summary

- BillyHarness already uses Bubble Tea v2, Bubbles textarea/viewport, a shared
  action registry, and a gateway client. Jobs were missing entirely from this
  full-screen surface even though CLI/API support existed.
- Charmbracelet's official Bubbles patterns favor a focused list model with a
  custom delegate for selectable resource rows. Its Huh form design groups
  terminal forms into small pages with typed inputs, selects, multi-selects,
  validation, confirmation, and an accessible keyboard path.
- K9s continuously refreshes remote resource state and exposes contextual
  actions for the selected stable resource. Lazygit similarly uses focused
  panels, visible context keys, explicit destructive confirmations, and
  drill-down detail rather than requiring command flags.
- For BillyHarness this maps to a full-screen jobs dashboard, stable job-ID
  selection, a paged creation wizard, contextual state-gated actions, and
  generation-tagged polling which cannot overwrite newer UI state.
- `duration` must be labelled as a hard stop, not useful work. Minimum completed
  cycles is the visible useful-work floor. Public web access must be an explicit
  `*` authority grant, and subscription-route warnings must be acknowledged
  before create.

Primary clean-room references:

- https://github.com/charmbracelet/bubbles/tree/main/list
- https://github.com/charmbracelet/huh
- https://github.com/derailed/k9s
- https://github.com/jesseduffield/lazygit

No competitor source is copied into BillyHarness.

## UX contract

```text
chat --Ctrl+K /jobs--> jobs dashboard --n--> creation wizard
                                |
                                +--Enter--> selected job control center

workers (1..4) -> all barrier -> reducer -> supervisor
       ^                                      |
       +------- durable next cycle/cadence <--+
```

Dashboard and detail refresh from gateway-owned state. The detail view shows
status, active ownership, current cycle and stage, immutable roles, completed
batches, attempts, model calls, tokens, canonical elapsed wall time, deadline,
cadence/next wake, provider/model, authority, last error, and final result. New
specs persist the UTC gateway admission instant; legacy specs show elapsed as
unavailable instead of inventing a timestamp. Controls are visible even
without color: `n` new, arrows/j/k select, Enter detail, `p` pause, `r` resume,
`f` refresh, `x` cancel with default-No confirmation, `c` copy final result,
and Esc back/close. Invalid state transitions are rejected with an explanation
even when a compact footer cannot hide every unavailable key.

The wizard pages are goal, scenario, team/route, run envelope, authority, and
review. It supports all eight immutable presets and explicit advanced values,
while scenario defaults remove the need to know CLI flags.

Adversarial acceptance details:

- `duration` remains a hard stop. Optional `min_runtime` derives a durable
  cadence and is labelled as an earliest-success wall clock, while
  `min_cycles` is the useful review-depth floor.
- Create first persists a stable client ID and queued state, verifies that the
  gateway did not clamp requested authority, and only then starts when asked.
- Read, write, and public-web authority are selected as valid atomic bundles;
  write controls exist only for `coding`, `debug`, and `writing`.
- Every asynchronous response is fenced by screen instance, generation,
  request sequence, target job ID, and canonical job revision where relevant.
  Closing a screen cancels its requests; reopening cannot consume stale Tea
  messages from the old screen.
- Cancellation snapshots the target job and defaults to No. A slow mutation
  timeout is reconciled with canonical gateway state rather than presented as
  a definite rejection.

## Checklist

- [x] Add typed asynchronous TUI job-client commands with idempotent create IDs.
- [x] Register `/jobs` and `/job` in shared command metadata and generated docs.
- [x] Add a full-screen dashboard with stable-ID selection and bounded polling.
- [x] Add a job detail/orchestrator view with roles, stages, clocks, budgets,
      authority, result, and state-gated controls.
- [x] Add a paged keyboard-only creation wizard for all eight presets and route,
      workers, run envelope, roots, tools, network, and start mode.
- [x] Preserve draft state on validation/server errors, gate duplicate submits,
      and require explicit Qwen/unattended and cancel confirmations.
- [x] Keep background jobs independent from chat `busy`; local mode must explain
      that a durable gateway is required.
- [x] Add responsive rendering for narrow/wide/plain terminals and ensure
      untrusted job text cannot inject terminal control sequences.
- [x] Update TUI architecture/help/README and regenerate command inventories.
- [x] Run focused, race, docsgen, architecture, full clean-worktree tests, build,
      diff checks, then commit, push, and archive this TODO.

## Target boundaries

- `internal/tui`: modal state, wizard, dashboard/detail rendering and actions.
- `internal/tui/jobclient`: typed async gateway-client adapter only; no UI.
- `internal/clientux`: frontend-neutral `/jobs` action metadata.
- `internal/gatewayapi`, `internal/gatewayclient`, `internal/jobs`: existing DTO,
  client, and pure-domain contracts remain authoritative.
- `cmd/fast-agent-harness`: no second scheduler; only existing TUI composition.
- `docs/architecture/tui-and-clientux.md`, README, generated command docs:
  stable public behavior only.

The TUI must not import `internal/gateway`, `internal/jobruntime`, provider, agent,
or tools. It must not infer permissions that are absent from the reviewed
create request.

## Verification

```sh
go test -count=1 ./internal/tui ./internal/tui/jobclient \
  ./internal/clientux ./internal/gatewayclient ./internal/gatewayapi
go test -race -count=1 ./internal/tui ./internal/tui/jobclient
go test -count=1 ./internal/architecture ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build ./cmd/fast-agent-harness
go vet ./...
go mod tidy -diff
git diff --check
```

Release evidence (2026-07-24):

- `go test -count=1 ./...`, `go vet ./...`, `go mod tidy -diff`, docsgen check,
  clean build, and `git diff --check` passed in a detached clean worktree.
- `go test -race -count=1 ./internal/tui ./internal/tui/jobclient
  ./internal/gatewayclient` passed.
- A real authenticated PTY smoke opened `/jobs`, completed the 18-step wizard,
  created and safely started a `research` job with four workers, the authorized
  read root `/Users/billy/Documents/billynotes/mobilization`, and unrestricted
  public-web tools. The durable gateway completed all six stage-role attempts,
  persisted the canonical result, and rejected an unauthenticated list with
  HTTP 401.
- The provider/preset/worker request matrix covers Qwen
  `qwen3.8-max-preview`. An unattended request against the default Qwen Token
  Plan was deliberately not sent; the wizard exposes and requires acknowledgement
  of the route's current automation restriction.

## Copy-ready Codex goal prompt

```text
/goal Implement loop-develop/current-todo/011-todo.md completely. Build a
full-screen provider-neutral multi-agent jobs control center inside the existing
BillyHarness TUI, not a slash-command wrapper around CLI flags. Preserve the
gateway as sole durable execution owner, keep chat usable while jobs run, use
stable-ID generation-tagged polling, require explicit authority and destructive
confirmations, support all eight presets and all current job controls, update
stable docs/generated inventories, run focused/race/full clean verification,
create scoped commits, push the branch, and archive the TODO only when every
release check is green.
```
