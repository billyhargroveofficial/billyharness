# 010 — Durable provider-neutral multi-agent job engine

## Goal

Ship the complete single-daemon durable-job feature which composes the pure
domain and FileStore with bounded provider execution, isolated capabilities,
background recovery, gateway APIs, and CLI control. A job repeatedly traverses
one immutable built-in workflow with one to four workers, all barriers, a
singleton reducer, and a strict supervisor. Every external invocation is
bounded and durably accounted; process interruption, lost acknowledgements,
scheduled waits, deadlines, budgets, and ambiguous writer outcomes must be
handled honestly without claiming exactly-once provider or tool effects.

The scheduler treats the persisted provider/model route as opaque. Provider-
specific construction, credentials, payloads, finish semantics, and token-
limit quirks stay in the adapter layer. A requested duration is expressed
through a hard deadline, bounded cycle/call/token budgets, and optionally an
earliest-success gate plus durable cadence—not by keeping one provider request
alive.

## Source research summary

Twelve native Codex research tracks converged on these rules:

- a model's final/no-tool response ends one bounded inner run, not the durable
  objective;
- multi-hour work requires an outer orchestrator with checkpoints, budgets,
  recovery, and repeated evaluator decisions;
- the useful common topology is parallel workers, an all barrier, reducer,
  supervisor, and a bounded next cycle;
- supervisors may refine objectives only inside immutable goal, role,
  authority, route, deadline, and budget envelopes;
- wall-clock duration, useful work, and provider-call duration are different
  concepts and must not be conflated;
- read-only work may be retried at least once, but uncertain writer dispatch
  must fail closed;
- provider subscription terms are independent of technical compatibility.
  Qwen Token Plan Individual must not be presented as permission for unattended
  automation.

Primary references are the OpenAI Agents runner and multi-agent orchestration
guides, Anthropic's workflow and multi-agent research articles, Microsoft Agent
Framework orchestration patterns, LangGraph persistence documentation, and
Qwen Token Plan Individual terms. Stable links live in
`docs/architecture/durable-multi-agent-jobs.md`.

## Delivered architecture

```text
durable scheduler
      |
      v
1..4 isolated workers --all barrier--> reducer --> supervisor
      ^                                             |
      |          bounded continue objectives        |
      +--------- persisted cadence/checkpoint <-----+
```

The built-in immutable presets are `general`, `research`, `coding`, `debug`,
`review`, `planning`, `writing`, and `compare`. Coding, debugging, and writing
use sequential single-writer stages; the other work is parallel where the
preset declares it. Dynamic roles, recursive spawning, and model-owned route,
authority, budget, deadline, or IDs are rejected.

## Checklist

- [x] Persist and validate immutable execution route, workflow, stage cursor,
      reservations, completed batches, and scheduled wake state.
- [x] Traverse every preset stage with bounded parallel workers, deterministic
      barriers/reduction, strict supervisor proposals, and control repairs.
- [x] Implement two-phase attempt dispatch, factual/unknown/no-generation
      usage, retries, crash recovery, and fail-closed ambiguous writers.
- [x] Enforce deadlines, cycles, attempts, calls, tokens, output caps,
      cancellation, pause, wait, stagnation, and terminal precedence.
- [x] Implement `min_runtime` and durable `cadence`, persisted `NextWakeAt`,
      restart recovery, early-resume rejection, and deadline-capped waits.
- [x] Add the isolated `jobagent` adapter with exact route pinning, normalized
      finish/usage semantics, prompt-fit checks, and capability-scoped tools.
- [x] Add process-wide provider-call limiting, defaulting to one, while keeping
      per-job topologies at one to four workers.
- [x] Add daemon-owned background lifecycle, startup acknowledgement, recovery,
      shutdown, pause/resume/cancel, and retry backoff.
- [x] Add bounded gateway create/list/show/action APIs plus paged attempts and
      artifacts.
- [x] Make client-supplied job creation idempotent across concurrent requests,
      lost/truncated acknowledgements, relative schedules, and expired absolute
      deadlines; reject mismatched ID reuse.
- [x] Add CLI create/list/show/run/pause/resume/cancel, eight presets, explicit
      authority flags, route selection, duration/cadence flags, and JSON output.
- [x] Add prominent Qwen Token Plan automation warnings without contaminating
      JSON stdout.
- [x] Add deterministic Mock provider responses with factual usage and full
      loopback CLI→gateway→FileStore→runtime E2E coverage.
- [x] Add scheduled restart E2E: persist `WAITING`/`NextWakeAt`, gracefully
      close the first stack, reopen the same FileStore, recover, and complete
      cycle two.
- [x] Run a short, actively monitored Qwen compatibility smoke; do not present
      it as unattended subscription authorization.
- [x] Correct README auth, provider-terms, workspace, platform, and architecture
      wording identified by hostile release review.
- [ ] Run final focused, race, stress, full, build, docsgen, and diff checks.
- [ ] Create scoped commits, push `codex/multi-agent-jobs`, append completion
      evidence, and move this TODO to `loop-develop/history`.

## Verification

```sh
go test -count=1 \
  ./internal/jobs ./internal/jobstore ./internal/jobruntime \
  ./internal/jobagent ./internal/jobservice \
  ./internal/gatewayapi ./internal/gateway ./internal/gatewayclient \
  ./internal/provider ./internal/tools ./cmd/fast-agent-harness

go test -race -count=1 \
  ./internal/jobs ./internal/jobstore ./internal/jobruntime \
  ./internal/jobagent ./internal/jobservice \
  ./internal/gatewayapi ./internal/gateway ./internal/gatewayclient \
  ./internal/provider ./internal/tools ./cmd/fast-agent-harness

go test -count=20 ./internal/jobruntime ./internal/jobservice

go test -race -count=3 ./cmd/fast-agent-harness \
  -run '^TestJobsCLILoopbackGateway(MockWorkflowCompletes|ScheduledWaitSurvivesStackRestart)$'

go test -count=1 ./...
go build ./cmd/fast-agent-harness
go test -count=1 ./internal/architecture ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go mod tidy -diff
git diff --check
```

Manual compatibility evidence must be short, foreground/actively monitored,
and run only when the configured plan permits interactive agent-tool use. It
must verify the persisted provider/model route, reducer/supervisor traversal,
non-zero factual usage, and terminal result. It is not an unattended-duration
test.

## Non-goals and current boundaries

- dynamic role creation, nested supervisors, recursive spawning, or topology
  mutation;
- a guarantee of continuous compute, useful work, or exact elapsed runtime;
- exactly-once provider billing or external tool side effects;
- replaying an uncertain non-idempotent writer after a crash;
- shell/process/build/test execution inside the current coding preset;
- multi-node scheduling or concurrent processes sharing one FileStore root;
- automatic support for providers that cannot expose enforceable output limits,
  required streaming/tool-call semantics, normalized finish, or factual usage;
- unattended use of subscription endpoints whose terms prohibit automation.

## Completion evidence

- Implemented the full durable job stack across `jobs`, `jobstore`,
  `jobruntime`, `jobagent`, `jobservice`, gateway API/client, CLI, isolated
  capabilities, and deterministic Mock support.
- `TestJobsCLILoopbackGatewayScheduledWaitSurvivesStackRestart` exercises an
  actual FileStore and loopback gateway: cycle one persists `WAITING`,
  `NextWakeAt`, revision, and factual usage; a new stack reopens the same root,
  recovers the timer, and cycle two finishes `COMPLETED/success` with a reducer
  final result.
- A short actively monitored Qwen compatibility smoke completed successfully:
  job `j-2611469df10c60c330c0073e72b031ea`, provider `qwen`, model
  `qwen3.8-max-preview`, thinking enabled, reasoning low, `review` preset, one
  worker, one cycle, revision 14, three attempts/model calls, 3,085 input
  tokens, and 1,931 output tokens. It traversed worker → reducer →
  supervisor and ended `COMPLETED/success`.
- That Qwen smoke proves adapter/control-flow compatibility only. It does not
  authorize unattended use of Token Plan Individual.
- Final verification and commit/push evidence will be appended after the clean
  release gate.

## Copy-ready Codex goal prompt

```text
/goal Implement loop-develop/current-todo/010-todo.md completely. Preserve and
do not absorb unrelated existing worktree changes. Keep the scheduler provider-
neutral, use an external durable loop of bounded agent runs, enforce immutable
authority/route/budgets/deadline, and fail closed for ambiguous writes. Deliver
eight presets, 1–4 workers, reducer/supervisor cycles, persistence/recovery,
cadence, gateway API/client, CLI, isolated tools, docs, and E2E tests. Run
focused/race/stress/full/docs verification, make scoped commits, push the
branch, and archive the TODO only after every release check is green.
```
