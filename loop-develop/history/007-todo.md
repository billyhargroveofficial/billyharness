# 007 — Provider-neutral multi-agent job domain and preset compiler

## Goal

Create the pure, provider-independent domain kernel for durable multi-agent
jobs. This slice deliberately does not start goroutines, call models, touch the
gateway, or persist files. It defines the state machine and compiles popular
user-facing presets into one bounded adaptive batch/barrier workflow model.

## Source research summary

Twelve native Codex research tracks independently reviewed research, coding,
operations/data, topology frameworks, durability, provider semantics, security,
UX, tests, implementation boundaries, and adversarial scope. The repeated
conclusion was:

- keep the existing agent loop as one bounded attempt;
- model a long job as repeated flat batches of one to four isolated workers;
- join a batch at a barrier, sort results deterministically, reduce/review, and
  let a supervisor propose one next bounded batch;
- the supervisor may expand work only inside the immutable goal, authority,
  role catalog, deadline, and budgets;
- popular scenarios are declarative presets, not separate Go engines;
- parallel coding is read-only in this slice's safety model and a workspace may
  have at most one writer role;
- worker completion is never global job completion;
- JSONL persistence, provider termination normalization, runtime execution,
  gateway APIs, and live Qwen dogfood belong to later numbered slices.

Primary architecture references inspected:

- OpenAI Agents SDK orchestration and agent loop;
- Anthropic Building Effective Agents and multi-agent research architecture;
- Google ADK workflow/graph agents;
- Microsoft Agent Framework workflows and orchestrations;
- LangGraph workflow/checkpoint patterns;
- AutoGen teams and termination conditions.

## Scope

### New package

Create `internal/jobs` with:

- `doc.go`
- `types.go`
- `authority.go`
- `events.go`
- `reducer.go`
- `workflow.go`
- `presets.go`
- focused unit/property-style tests

### Domain contract

- `JobSpec`, `JobState`, `Budget`, `Usage`, `RoleSpec`, `StageSpec`,
  `WorkBatch`, `WorkItem`, `Attempt`, `ArtifactRef`, and `Decision`.
- Persistent job states: `queued`, `running`, `waiting`, `paused`,
  `completed`, `failed`, `cancelled`.
- Explicit terminal reasons such as success, deadline, budget, stagnation,
  blocked, unrecoverable, and operator cancellation.
- Pure `Reduce(JobState, Event) (JobState, error)` with monotonic revision and
  immutable terminal states.
- Deterministic priority: cancel, hard deadline, and hard budget override any
  model-proposed continuation or completion.
- Flat adaptive batches only: one to four work items, `all` barrier, stable
  result order, then reducer/reviewer decision.
- Supervisor decisions: `continue`, `complete`, `wait`, `blocked`; proposed
  tasks must use predeclared roles and remain within caps.
- Stagnation fingerprints and bounded cycles.

### Authority

- Explicit fail-closed authority; no legacy zero-value-means-unrestricted
  behavior.
- Effective authority is an intersection of server/job/role constraints.
- Child authority can only narrow parent authority.
- At most one writer role per batch/workspace.
- Worker output cannot add tools, roots, network access, providers, budget, or
  deadline.

### Built-in presets

Compile these names to validated workflow definitions:

- `general`
- `research`
- `coding`
- `debug`
- `review`
- `planning`
- `writing`
- `compare`

They may share stage/role definitions. No provider or model name is embedded in
a preset. `workers` means maximum concurrent worker attempts and is constrained
to `1..4`; supervisor/reducer roles do not count against that user-facing
number.

## Checklist

- [x] Add pure domain types with strict validation.
- [x] Add fail-closed authority intersection and monotonicity tests.
- [x] Add events and pure reducer with legal/illegal transition tests.
- [x] Make terminal states immutable and terminal emission idempotent.
- [x] Enforce deadline/budget/cancel precedence.
- [x] Add flat batch validation, deterministic work ordering, and writer
      exclusivity.
- [x] Add bounded supervisor decision validation and stagnation handling.
- [x] Compile all eight built-in presets deterministically.
- [x] Reject unknown roles, dangling stage references, unbounded cycles,
      provider-pinned presets, and concurrent writers.
- [x] Add table-driven preset conformance tests.
- [x] Run focused tests, race tests, full `go test ./...`, and `git diff --check`.
- [x] Check architecture docs; update only stable package/boundary maps affected
      by this slice.
- [x] Create a scoped git commit and push after verification passes, without
      including unrelated pre-existing worktree changes.

## Verification

```sh
go test -count=1 ./internal/jobs
go test -race -count=1 ./internal/jobs
go test -count=1 ./...
git diff --check
```

## Completion evidence

- Implemented the pure `internal/jobs` domain, fail-closed authority model,
  typed events, deterministic reducer, flat batch validation, and eight built-in
  provider-neutral presets.
- Added `internal/jobs` to the hand-written architecture boundary map and
  regenerated the package index.
- Verification passed on 2026-07-23:

```text
go test -count=1 ./internal/jobs
go test -race -count=1 ./internal/jobs
go test -count=1 ./internal/jobs ./internal/architecture ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
git diff --check
```

- Existing unrelated Qwen/Kimi/failover/capability worktree changes were left
  unstaged and unmodified except where the generated documentation reflects
  current package reality.

## Copy-ready Codex goal prompt

```text
/goal Implement loop-develop/current-todo/007-todo.md completely. Preserve all
unrelated existing worktree changes. Keep internal/jobs pure and provider-
neutral, use apply_patch for edits, run the focused/race/full verification, then
create a scoped commit containing only this slice and push the branch after all
verification passes. Do not implement persistence, provider calls, gateway APIs,
or live Qwen execution in this slice.
```
