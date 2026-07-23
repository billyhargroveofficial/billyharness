# 009 — Provider-neutral model finish semantics and truthful agent completion

## Goal

Stop treating every provider stream `[DONE]` or response closure as a successful
final answer. Normalize provider-specific termination into one typed contract,
propagate it through model-call telemetry, and make the existing agent loop fail
closed when output was truncated, filtered, refused, paused, resource-limited,
or otherwise ambiguous. This closes the Qwen `finish_reason=length` bug before
the durable scheduler is allowed to decide that a worker or job completed.

## Source research summary

- An inner model turn ends because a provider reports a stop condition; that is
  not itself proof that the worker objective or outer Job is complete.
- OpenAI-compatible chat streams report `choices[].finish_reason` before the
  `[DONE]` sentinel. BillyHarness currently discards that field and emits a bare
  `EventDone`, so Qwen `stop`, `length`, `content_filter`, and tool termination
  are indistinguishable.
- A live Qwen `qwen3.8-max-preview` probe produced `finish_reason=length` with an
  empty answer under a tiny token cap, confirming the defect.
- OpenAI Responses-style streams use response status/incomplete details rather
  than the chat-completions field and need the same normalized boundary.
- Provider adapters may retain the raw reason for diagnostics, but control flow
  must switch only on a small provider-neutral enum.

## Scope

### Normalized finish contract

- Add typed finish kinds: natural, tool calls, output limit, context limit,
  pause, refusal, content filter, resource limit, and unknown.
- `provider.EventDone` carries normalized finish metadata plus the redacted raw
  provider reason.
- Keep a narrowly documented legacy-zero compatibility path for existing fake
  providers/tests; real adapters must emit an explicit finish.

### Provider parsing

- Parse OpenAI-compatible `choices[].finish_reason`, emit exactly one done
  event, and reject contradictory/multiple terminal reasons.
- `[DONE]` without a prior reason becomes explicit unknown, not natural.
- Normalize OpenAI Responses completed/incomplete/failed status and incomplete
  details.
- Make mock adapters explicit and preserve request/provider/model metadata.

### Agent semantics and telemetry

- Carry finish metadata through model-call results and protocol telemetry.
- With no tool calls, only natural/legacy-natural may become final answer.
- `tool_calls` requires parsed tool calls; contradictory combinations fail.
- Output/context/resource limits, refusal, content filter, pause, and unknown
  return typed errors and never emit successful run completion.
- Add helpers the later durable job runtime can classify without parsing error
  strings.

## Checklist

- [x] Add normalized finish types, validation, mapping, and typed finish error.
- [x] Parse chat-completions finish reasons and emit exactly one done event.
- [x] Parse Responses status/incomplete details into the same contract.
- [x] Make mock/provider test streams explicit where they model real adapters.
- [x] Propagate finish through model-call result and protocol telemetry.
- [x] Prevent truncated/filtered/refused/unknown output from becoming a final
      answer.
- [x] Reject tool-calls/no-tools and natural-with-tools contradictions.
- [x] Add table-driven parser, agent-loop, regression, and compatibility tests.
- [x] Run provider/agent/protocol focused tests, race tests, full relevant tests,
      docs generation check, and `git diff --check`.
- [x] Create a scoped commit and push without including unrelated pre-existing
      Qwen/Kimi/failover/capability worktree changes.

## Verification

```sh
go test -count=1 ./internal/provider ./internal/agent ./internal/protocol
go test -race -count=1 ./internal/provider ./internal/agent
go test -count=1 ./internal/architecture ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

## Completion evidence

- Normalized Chat Completions and Responses terminal states into an explicit
  provider-neutral finish contract and made unsuccessful or ambiguous model
  endings typed, observable failures.
- Fixed deterministic stream draining so a buffered terminal EventDone is
  processed before its terminal error; repeated and race tests cover the former
  select-order bug.
- Restricted legacy empty finishes to mock/package-local test doubles, added
  bounded control-free raw-reason normalization, and enforced natural finishes
  in model compaction and web-summary helpers.
- Adversarial review findings for completed-with-failure-details and cancelled
  Responses events were covered with regression tests.
- Verification passed on 2026-07-23 in the live combined worktree:

```text
go test -count=1 ./internal/provider ./internal/agent ./internal/protocol
go test -race -count=1 ./internal/provider ./internal/agent
go test -count=50 ./internal/provider -run '<finish/drain regressions>'
go test -count=20 ./internal/agent -run '<finish/helper regressions>'
go test -count=1 ./internal/architecture ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

## Copy-ready Codex goal prompt

```text
/goal Implement loop-develop/current-todo/009-todo.md completely. Preserve and
do not absorb unrelated existing Qwen/Kimi/failover/isolated-capability changes.
Normalize provider termination, require real adapters to emit explicit finish
metadata, and make truncated/filtered/refused/unknown output fail closed rather
than become a successful final answer. Use apply_patch, add provider and agent
regression tests, run focused/race/docs verification, then create a scoped
commit and push it. Do not implement the durable job scheduler/API/CLI yet.
```
