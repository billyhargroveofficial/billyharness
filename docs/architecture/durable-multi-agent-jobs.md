# Durable Multi-Agent Jobs

> **Provider-terms warning:** unattended jobs are permitted only when the
> configured endpoint and plan explicitly allow automation. BillyHarness's
> built-in `qwen` provider defaults to Qwen Token Plan Individual. Its current
> terms limit use to interactive programming and agent tools and prohibit
> automated scripts, application backends, and non-interactive batch
> processing. The published concurrent-agent allowances are Lite 1–2,
> Standard 3–4, and Pro 6–8. Staying under that concurrency limit does not make
> unattended execution permissible. This is not a blanket restriction on Qwen
> models: a metered or custom endpoint may have different terms, which the
> operator must verify. See the official
> [Token Plan Individual overview](https://docs.qwencloud.com/token-plan/personal/token-plan-personal-overview#terms-of-use).

## Ownership

`internal/jobs` is the pure provider-neutral domain. It owns immutable job
specifications, authority envelopes, built-in workflow presets, event types,
and the deterministic reducer. It has no persistence, provider, tool, gateway,
or UI imports.

`internal/jobstore` owns the canonical per-job JSONL event stream, hash chain,
compare-and-append revisions, replay, disposable snapshots, artifact storage,
and exclusive store-root ownership. Exact replay of an already committed event
is idempotent even when later events exist. Version 1 records are rejected:
they did not persist the exact provider route or pre-dispatch reservations
needed for honest recovery.

`internal/jobruntime` owns deterministic workflow-step execution and treats the
persisted provider/model route as opaque data. It depends only on `jobs`,
`jobstore`, and secret redaction; it contains no provider-specific branching,
credentials, transports, or model payloads. A provider adapter implements the
narrow `Invoker` contract.

`internal/jobagent` pins one persisted route to a fresh isolated agent and
normalizes provider termination and usage. `internal/jobservice` owns daemon-
lifetime background step loops and restart admission. Gateway and CLI code are
adapters over that service; they do not implement another scheduler.

## Why the outer scheduler exists

A model prompt cannot make one provider request last for a requested number of
hours. One inner agent run is bounded: the provider/agent runtime returns a
final answer when the model emits no further tool call, or stops on a turn
limit, deadline, cancellation, or error. Long work therefore needs an outer
state machine which starts many bounded runs, persists their results, reviews
progress, and decides whether another cycle is useful.

BillyHarness uses a manager-style topology with evaluator feedback:

```text
durable job state
      |
      v
1..4 isolated workers --all barrier--> reducer --> supervisor
      ^                                             |
      |                 continue + next objectives |
      +---------- durable cadence/checkpoint <-----+
```

This combines the manager/agents-as-tools, orchestrator-workers, parallel,
sequential, and evaluator-optimizer patterns without keeping a provider call
open between cycles. It follows the same separation described by the official
[OpenAI Agents runner loop](https://openai.github.io/openai-agents-python/running_agents/)
and [multi-agent orchestration guide](https://openai.github.io/openai-agents-python/multi_agent/),
Anthropic's [workflow and agent patterns](https://www.anthropic.com/engineering/building-effective-agents)
and [multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system),
Microsoft's [workflow orchestration patterns](https://learn.microsoft.com/en-us/agent-framework/workflows/orchestrations/),
and LangGraph's [checkpoint/persistence model](https://docs.langchain.com/oss/python/langgraph/persistence).

The supervisor may invent better *next objectives* inside the immutable goal;
it may not silently expand the goal, permissions, role set, provider, budget,
or deadline. That boundary turns open-ended iteration into an auditable job
rather than granting a model recursive authority over the host.

## Built-in use-case presets

All presets use the same durable contracts, but their roles and sequential
stages differ:

| Preset | Stable workflow shape | Typical use |
| --- | --- | --- |
| `general` | parallel explore, reduce, supervise | arbitrary bounded problem solving |
| `research` | independent research/falsification/provenance, reduce, supervise | evidence synthesis and forecast revision |
| `coding` | parallel analysis, one isolated writer, parallel static review, reduce, supervise | scoped repository changes |
| `debug` | parallel diagnosis, one isolated fixer, parallel static review, reduce, supervise | root-cause investigation and repair |
| `review` | correctness/tests/security/maintainability, reduce, supervise | adversarial review |
| `planning` | plan/dependencies/risks, reduce, supervise | implementation or project planning |
| `writing` | content development, one isolated author, parallel review, reduce, supervise | durable document production |
| `compare` | criteria/independent comparison/risk/evidence, reduce, supervise | option selection |

## Workflow and cycle semantics

A built-in preset compiles to an immutable ordered list of stages. Every stage
is executed: ordinary stages contain one to four isolated workers; reducer and
supervisor stages are singleton agent invocations. Each stage uses an `all`
barrier. One job cycle is one complete traversal of the stage order, not one
model turn or one worker batch.

The supervisor may return only `continue`, `complete`, `wait`, or `blocked`.
For `continue`, it supplies one bounded objective for every predeclared role in
the first stage. Runtime code creates all IDs, authority, route, deadline,
budget, stage selection, and stagnation fingerprint. Model output cannot add a
role, spawn a nested scheduler, change provider, or widen permissions.
`min_cycles` is a quality floor before successful completion, not a requested
wall-clock duration. `duration`/`deadline` are hard upper cutoffs rather than
targets. `min_runtime` is a fixed wall-clock earliest-success gate, measured
from create admission. Queued time, operator pauses, and daemon
downtime count toward it, so it is not a guarantee of active model compute or
useful work. With immediate start and an uninterrupted daemon it approximates
the requested iteration window; factual work remains bounded by cycles, calls,
tokens, and stagnation checks. `cadence` is the durable delay after a supervisor
chooses `continue`. The CLI derives the smallest safe cadence from
`min_runtime` and `max_cycles` when the
operator omits it; the API keeps it explicit for reproducible admission.
Between cycles no model request, goroutine busy-loop, or fake tool call is kept
alive. The next UTC wake is persisted, recovered after restart, and capped by
the hard deadline. An operator pause preserves that wake, and an early resume
cannot bypass it. `wait` is instead a manual durable pause for a real operator
or external dependency. It has no automatic wake and lasts until explicit
resume or the hard deadline. Autonomous rechecking, research, critique, coding,
and testing must use `continue`.

New immutable specs also persist `admitted_at`, captured from the same UTC
clock sample used to resolve the deadline and earliest-success schedule. It is
the canonical origin for elapsed wall time in operator clients and survives
gateway/TUI restarts. A zero value remains valid only for legacy specs and is
rendered as unavailable rather than inferred from a local observation.

For example, this prevents successful completion during the first five
wall-clock hours and applies a six-hour hard cap. Omitting `-cadence` lets the
CLI derive and send the explicit provider-neutral cadence. The configured
endpoint must permit unattended automation:

```sh
fast-agent-harness jobs create \
  -preset research -workers 4 \
  -duration 6h -min-runtime 5h -max-cycles 8 \
  "Recheck the evidence, challenge prior conclusions, and produce the best bounded result"
```

Reducer and supervisor failures receive at most three durable repair attempts;
duplicate JSON keys, unknown fields, empty output, and runtime-owned fields are
rejected before a supervisor decision can cross the barrier.

## Durable dispatch boundary

Every external invocation has a persisted `attempt_started` followed by one
`attempt_finished`. The start reserves attempts, model calls, and tokens before
parallel dispatch so siblings cannot spend the same remaining budget.
`Attempt.Dispatched` records whether the live runtime admitted the external
call:

- a known pre-dispatch interruption is undispatched, has zero provider usage,
  and may be retried, including for an isolated writer;
- a provider-confirmed HTTP rejection before generation records the rejected
  model call with zero tokens and may safely retry read or writer work when the
  provider classifies the rejection as transient;
- an admitted call with unknown usage burns its whole reservation;
- a provider-classified transient DNS/reset/stream failure still burns that
  reservation, then retries only read-only work with outer backoff;
- after process loss, dispatch is unknowable, so recovery treats the attempt as
  dispatched and burns the reservation;
- an interrupted read-only attempt becomes `abandoned` and is retryable;
- an interrupted writer becomes `ambiguous` and the job fails unrecoverable.

This is at-least-once execution for read-only work. Exactly-once external
mutation is not claimed; it requires an idempotency key, transactional sink, or
sink-specific reconciliation API.

## Cancellation, shutdown, and concurrency

Operator cancellation is its own durable event. Caller/process context
cancellation is not operator intent. A process-wide coordinator, keyed by the
store's opaque stable coordination key and job ID, gives all `Runner` instances
over that durable namespace the same job ownership, dispatch gate, and active
cancellation handle. Cancellation and dispatch admission are linearly ordered,
including when they arrive through different `Runner` objects.

The gateway also owns one process-wide durable-invocation semaphore shared by
every job. `gateway -job-concurrency` configures it and defaults to `1`; job
topologies may still declare one to four workers, but independent jobs and
parallel stages cannot collectively cross this provider-call cap. Cancellation
while queued returns an undispatched/unknown-usage outcome and never reaches
the provider adapter.

If an `Invoker` ignores context, the scheduler stops waiting, persists a
conservative outcome, and ignores any late result. The still-live call keeps
the job coordinator quarantined, so a retry cannot exceed the configured
parallel cap until that call actually returns.

The store uses CAS for every transition. A finish may rebase only across the
exact reducer state produced by the one deterministic
`cancellation_requested` event. Any other intervening revision is a foreign
conflict and fails closed.

Creation has a separate lost-ack boundary. First-party clients generate a
portable `job_id` before POSTing it, and the durable spec binds a SHA-256 hash
of the exact typed create request. An identical retry returns the one durable
winner, including after its absolute deadline has elapsed; reuse of the ID for
a different request conflicts. The client retries one ambiguous transport or
decode failure, but never blindly retries a typed HTTP error. This makes job
creation idempotent without pretending that later provider calls or external
tool effects are exactly-once.

## Limits and authority

Deadline, cycles, attempts, model-call reservations, token reservations,
per-attempt calls/tokens, and per-call output caps are runtime-owned admission
limits. Where a transport supports an exact output field, the adapter sets it
on every call; Qwen's documented possible ten-token deviation is reserved
inside the durable cap. Provider-reported input or billing usage can only be
known after it is spent: factual excess is persisted in full and immediately
terminates the job for budget, while unknown post-dispatch usage burns the
whole reservation. Inline goals, results, errors, proposals, prior attempts,
artifacts, and authority entries are bounded before an adapter sees them.
Public admission also rejects a budget smaller than the arithmetic minimum of
one attempt, model call, and token for every stage-role invocation needed by
the configured cycle/runtime floor. This is only an impossibility guard; it
does not promise that a real prompt, tool loop, or repair attempt will fit that
minimum.

Effective authority is `server ∩ job ∩ role ∩ work item`, narrowed to the
persisted route provider. Job-store roots and their ancestors/descendants are
always excluded from worker writes. Zero or unrepresentable authority must fail
closed in the provider/tool adapter; it must never silently widen to ambient
process permissions.

Tool-enabled attempts use the `durable-job-v1` isolated capability scope. A
fresh registry snapshot exposes only explicitly authorized structured native
tools: bounded filesystem reads/searches, filesystem write/edit/mkdir for the
single declared writer, public-web reads, and time. Local reads receive only
`read_roots`; local writes receive only `write_roots` at the handler call
boundary, including when cloned handlers still close over the shared registry.
Concrete `network_hosts` become HTTPS origin/path-prefix policies enforced on
the initial request and every redirect. `web_search` requires an explicit `*`
network grant because a query cannot be constrained to one destination host.

The durable adapter rejects shell/process execution, diagnostics, secrets,
network writes, external/MCP tools, memory, skills, cache controls, user-input,
and ambient discovery. Non-writer roles never receive write schemas even when
the enclosing job granted them. Isolated snapshots also disable MCP catalogs
and instructions, hooks, profiles/project context, model helpers, model web
summaries, and shared web-cache reuse.

## Current coding and recovery boundaries

The `coding` preset can inspect files and apply structured filesystem writes or
edits inside explicitly granted roots. It does not currently receive a shell,
process runner, or build/test sandbox, so it cannot compile a project or run a
test suite by itself. A coding result must state which verification remains for
an operator or a separately authorized execution service.

Writer recovery is deliberately fail-closed. If the process crashes after a
writer invocation may have crossed the dispatch boundary, BillyHarness records
the attempt as ambiguous and fails the job instead of repeating a potentially
non-idempotent edit. For any invocation whose post-crash usage is unknowable,
recovery charges the entire persisted call/token reservation. This can
underuse a budget, but prevents a restart from silently exceeding it.

Provider-neutrality is a scheduler property, not a promise that every API is
automatically compatible. Admission and the adapter require a pinned route,
streaming and tool-call semantics where tools are granted, an enforceable
output bound, normalized finish metadata, and factual usage. Unsupported
routes fail before durable execution. The current daemon admits the built-in
DeepSeek, Qwen, Kimi, Codex, and Mock routes and, when configured, exactly one
named custom OpenAI-compatible binding. Built-in bindings are reconstructed
from provider-owned defaults so an endpoint or credential override for the
primary provider cannot leak into another route. An arbitrary custom provider
name is not a registry entry; supporting several independent custom endpoints
requires a future explicit provider-profile registry. The current exclusive
FileStore runtime is supported on Darwin and Linux; other operating systems
fail closed.
