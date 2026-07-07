# 019 - Agent-Club Auto-Run Policy

## Goal

Add a deliberately narrow policy-controlled path for selected agent-club
deliveries to start a gateway session run after the input is admitted.

Today all agent-club ingress is `admit_only`; that was the right foundation.
This TODO adds the next trust layer:

```text
trusted trigger delivery
  -> queued session input
  -> policy check
  -> existing session run route/path
  -> normal durable run events
```

Auto-run must be disabled by default, explicit per trigger or binding, rate
limited, owner-scoped, auditable, and unable to escalate runtime privileges.

## Current Local Finding

Existing behavior:

- trigger delivery admits input and returns `run_dispatched=false`;
- session run already admits/promotes/completes inputs through
  `POST /v1/sessions/{id}/run`;
- input ledger idempotency already exists;
- session run handles interrupt policy, context epochs, persistence failures,
  and completion status;
- there is no pending-input queue runner or auto-run policy.

The key design risk is accidentally adding a second runtime path. Auto-run
should reuse the existing session run machinery, not invent a parallel agent
executor.

Billy-facing failure fixed: some trusted triggers should be able to wake Billy
fully without a human clicking run every time, but only when Billy explicitly
allowed that source to do so.

## Source Research Summary

Local sources checked:

- `internal/gateway/gateway.go`: `handleSessionRun`, `admitSessionInput`,
  `promoteSessionInput`, and completion semantics.
- `internal/gateway/session_inputs.go`: idempotent input state machine.
- `internal/gateway/agentclub_triggers.go`: trigger admission boundary.
- `internal/agentclub/config.go`: persisted trigger/binding config.
- `internal/gateway/http_security.go`: mutation auth and privilege clamp
  behavior.
- `docs/architecture/security-model.md`: external ingress and proposal
  boundaries.

Design direction:

- auto-run is a gateway-owned policy decision after admission;
- auto-run must not take provider/model/access-mode/tool/MCP overrides from
  trigger payloads;
- configured policy can only lower or cap runtime behavior;
- audit must explain why a run was dispatched or skipped.

## Product Shape

Extend trigger or binding config with a disabled-by-default run policy:

```json
{
  "id": "daily.review",
  "kind": "schedule",
  "enabled": true,
  "run_policy": {
    "enabled": true,
    "mode": "start_if_idle",
    "interrupt_policy": "",
    "max_runs_per_hour": 4,
    "cooldown": "10m",
    "max_tool_rounds": 8,
    "access_mode": "guarded"
  }
}
```

Suggested first modes:

- `disabled` or omitted: preserve current behavior.
- `start_if_idle`: run only if target session is idle.
- `interrupt`: use existing interrupt behavior to replace active runs, only if
  explicitly configured.

Avoid a queue worker in this slice unless the implementation proves it is
simpler than dispatching immediately after successful admission.

## Implementation Checklist

1. Add run policy config and validation.
   - Add to trigger config first; binding-level inheritance can be future work
     unless implementation finds it trivial and tested.
   - Validate:
     - disabled default;
     - supported mode;
     - bounded `max_runs_per_hour`;
     - positive/bounded cooldown;
     - `access_mode` cannot exceed server configured access mode;
     - `max_tool_rounds` cannot exceed server configured runtime limit.
   - Reject provider/model/reasoning/tool/MCP override fields entirely.

2. Add registry exposure for run policy.
   - Runtime trigger binding should carry redacted policy fields needed by the
     gateway.
   - Discovery responses should either omit policy or expose only safe summary
     fields. Do not reveal secrets or session-sensitive data beyond existing
     policy.

3. Reuse the existing session run path.
   - After successful trigger admission, if policy is enabled:
     - check target session owner scope;
     - check active run state;
     - enforce cooldown/rate limit;
     - create a `RunRequest` using the admitted input id and the same trusted
       prompt;
     - call existing session-run internals rather than duplicating agent logic.
   - If reusing HTTP handler internals is messy, extract a small internal
     method that both `handleSessionRun` and auto-run call.
   - The extracted method must preserve input promotion/completion, context
     epoch, event persistence, and failure semantics.

4. Add auto-run audit evidence.
   - Extend trigger audit or add a small run-policy audit record.
   - Record:
     - binding id;
     - target session;
     - admitted input id;
     - decision: `skipped`, `dispatched`, `rate_limited`, `busy`, `failed`;
     - run seq/run id when known;
     - redacted reason.
   - Do not store raw prompts, payloads, metadata values, secrets, or delivery
     ids.

5. Add rate limit/cooldown state.
   - Prefer durable gateway-store state if auto-run decisions must survive
     restart.
   - It can start with per-binding recent dispatch timestamps in a private JSONL
     or existing trigger audit replay if that is reliable and simple.
   - Tests should cover restart/replay if durable state is implemented.

6. Add operator surfaces.
   - `agentclub triggers` local listing should show a redacted run-policy
     summary.
   - readiness/doctor should count enabled auto-run triggers and blocked
     policies.
   - TUI/Telegram `/agentclub` can remain proposal/capability focused unless
     adding safe summary text is straightforward.

7. Tests for dangerous cases.
   - Default config still returns `run_dispatched=false`.
   - Enabled policy dispatches a run and durable events are present.
   - Busy session with `start_if_idle` skips without losing admitted input.
   - Busy session with `interrupt` uses existing interrupt path.
   - Rate limit/cooldown skips.
   - Cross-owner target denied before run.
   - Payload metadata cannot escalate provider/model/access mode/tool rounds.
   - Persistence failure marks input/run consistently.
   - Duplicate trigger delivery does not double-run the same input unless
     policy explicitly allows retry, which should not be added in this slice.

8. Docs and generated references.
   - Update ADR/security docs because this changes the long-standing
     `run_dispatched=false` invariant for configured policies.
   - Make clear that unconfigured/default agent-club remains admission-only.
   - Update generated gateway API docs if response fields or DTOs change.

## Target Files

Likely files:

- `internal/agentclub/config.go`
- `internal/agentclub/triggers.go`
- `internal/agentclub/config_test.go`
- `internal/gateway/agentclub_triggers.go`
- `internal/gateway/gateway.go`
- `internal/gateway/session_inputs.go`
- `internal/gateway/agentclub_triggers_test.go`
- `internal/gateway/session_store_replay_test.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`
- `cmd/fast-agent-harness/agentclub_cmd.go`
- `docs/adr/0009-external-ingress-is-gateway-admission.md`
- `docs/architecture/security-model.md`
- `docs/architecture/gateway-and-sessions.md`
- `docs/generated/*` through docsgen only

## Boundaries

- Auto-run must be opt-in and disabled by default.
- No project-local manifests.
- No executor/proposal apply.
- No arbitrary command/API/SQL/browser actions.
- No provider/model escalation from trigger payload/config.
- No direct tool/MCP execution outside the normal agent run.
- No new runtime path that bypasses existing session run persistence.
- No browser automation/debug tooling.

## Verification Commands

```sh
go test -count=1 ./internal/agentclub ./internal/config
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestGatewaySessionRun|TestGatewaySessionInput|TestGatewaySessionClientID'
go test -count=1 ./internal/gatewayclient ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

## Done Means

- Default agent-club behavior remains admission-only.
- Explicit run policy can dispatch a run through existing session run semantics.
- `run_dispatched` reflects reality.
- Busy/rate-limited/cross-owner/duplicate/persistence-failure cases are tested.
- Audit explains every auto-run decision without leaking secrets or payloads.
- Docs clearly mark the new trust boundary.
- The branch is committed and pushed.

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/019-todo.md end to end.

Add an opt-in agent-club auto-run policy. Configured trigger deliveries should
still admit a queued input first, then, only when explicit policy allows, start
a normal gateway session run through the existing session-run machinery. Default
behavior must remain admission-only.

Stay inside boundaries:
- no project-local manifests;
- no executor/proposal apply;
- no arbitrary command/API/SQL/browser actions;
- no provider/model/access-mode escalation from payloads;
- no bypass of session input/run persistence;
- no browser automation/debug tooling.

Verification required:
go test -count=1 ./internal/agentclub ./internal/config
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestGatewaySessionRun|TestGatewaySessionInput|TestGatewaySessionClientID'
go test -count=1 ./internal/gatewayclient ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short

If broad tests are blocked by unrelated pre-existing worktree changes, record
the exact blocker and still run every focused command that applies.

After verification passes, create a git commit and push the branch. Include the
commit hash, branch name, commands run, and residual blockers in the final
report.
```

## Final Status

Completed on 2026-07-07.

Implementation evidence:

- Added opt-in trigger `run_policy` config with disabled default, supported
  modes, rate/cooldown/tool-round/access-mode validation, and strict JSON
  rejection of forbidden override fields.
- Added safe readiness/local trigger summaries for enabled auto-run policy
  counts and redacted policy shape.
- Extracted the existing post-admission session-run machinery into a shared
  gateway helper used by both `POST /v1/sessions/{id}/run` and auto-run.
- Added gateway auto-run dispatch after successful trigger admission only,
  with duplicate, busy, owner-scope, cooldown/rate-limit, max-tool-round, and
  access-mode safeguards.
- Added durable redacted `agentclub-autorun-audit.jsonl` decision records for
  skipped/dispatched/rate-limited/busy/failed policy outcomes.
- Added tests for default admission-only behavior, dispatch through session-run
  semantics, busy skip, duplicate skip, rate limit, policy privilege bounds,
  strict forbidden-field rejection, and audit redaction.
- Updated architecture/security/ADR docs for the new explicit trust boundary.

Verification commands run:

```sh
go test -count=1 ./internal/agentclub ./internal/config
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestGatewaySessionRun|TestGatewaySessionInput|TestGatewaySessionClientID'
go test -count=1 ./internal/gatewayclient ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

Commit/push state:

- Pending final chain commit and push after TODO 020 completes.

Remaining blockers:

- None for 019.
- Unrelated pre-existing clipboard/TUI worktree changes remain unstaged and
  untouched.
