# 018 - Agent-Club Schedule Runner

## Goal

Make configured `kind=schedule` agent-club triggers actually fire on a local
operator-controlled loop.

After this TODO, Billyharness should have a small scheduler process that:

- loads the trusted agent-club config;
- finds enabled schedule triggers;
- computes due ticks from an explicit schedule spec;
- calls the existing trigger delivery route;
- records local scheduler state/idempotency;
- reports readiness/doctor status;
- never runs sessions directly.

This TODO turns schedule triggers from a deterministic request shape into a
usable local runner. It still only admits queued inputs. Auto-run comes later.

## Current Local Finding

`internal/agentclub/triggers.go` already supports `TriggerKindSchedule` and
`scheduled_at_utc` delivery. `internal/gateway/agentclub_triggers.go` can admit
a schedule delivery and rejects future timestamps unless dry registration is
explicit. However:

- config has no schedule expression/spec beyond `kind=schedule`;
- there is no process that wakes up and posts due schedule deliveries;
- `serviceops` currently knows gateway and Telegram services only;
- readiness/doctor do not report a scheduler process;
- schedule delivery has no local state file for last fired ticks.

Billy-facing failure fixed: a configured schedule trigger currently means
"you can manually post schedule JSON", not "Billy can be woken on schedule".

## Source Research Summary

Local sources checked:

- `internal/agentclub/config.go`: persisted trigger config shape.
- `internal/agentclub/triggers.go`: schedule/manual delivery identity.
- `internal/gateway/agentclub_triggers.go`: route semantics and audit.
- `cmd/fast-agent-harness/service_cmd.go`: long-running command shape.
- `internal/serviceops/services.go`: managed service metadata.
- `cmd/fast-agent-harness/doctor.go`: service/process status reporting.
- `docs/architecture/gateway-and-sessions.md`: admission-only invariant.

Design direction:

- implement a tiny interval scheduler first; do not pull a cron dependency in
  this slice unless the implementation proves the dependency is worth it;
- persist scheduler state under `$BILLYHARNESS_HOME`, not in project dirs;
- delivery goes through the existing gateway route so all auth, owner scope,
  audit, and idempotency rules remain centralized.

## Product Shape

Extend schedule trigger config with an explicit schedule spec. Suggested JSON:

```json
{
  "id": "daily.review",
  "kind": "schedule",
  "source": "daily-review",
  "capability": "daily.review",
  "event_type": "tick",
  "owner": {"client_type": "ingress", "client_id": "ingress:daily-review"},
  "target_session_id": "session-id",
  "prompt": "Review the daily queue.",
  "auth_method": "none",
  "schedule": {
    "kind": "interval",
    "every": "30m",
    "jitter": "30s",
    "start_at_utc": "2026-07-07T00:00:00Z",
    "max_catchup": 1
  },
  "enabled": true
}
```

Keep `schedule.kind=interval` as the first supported schedule type. Cron can be
a future TODO.

Add a command such as:

```text
fast-agent-harness agentclub scheduler run [-gateway URL] [-path CONFIG] [-once] [-state PATH] [-tick SECONDS]
fast-agent-harness agentclub scheduler status [-state PATH] [-json]
```

Or use a top-level `scheduler` command if that fits the command registry better.
Prefer the smallest shape that works with service management and docsgen.

## Implementation Checklist

1. Extend config DTOs with schedule specs.
   - Add `Schedule *ScheduleConfig` to schedule trigger config only.
   - Validate:
     - schedule spec required for enabled `kind=schedule`;
     - unsupported schedule kind rejected;
     - positive `every`;
     - bounded `jitter`;
     - non-negative small `max_catchup`;
     - no schedule spec on webhook/manual triggers.
   - Disabled example schedules may exist without target session only if the
     current trigger validation already permits that; otherwise keep the
     existing fail-closed target session rule.

2. Add pure due-tick calculation.
   - New package can be `internal/agentclub/scheduler` or
     `internal/agentclub` if small.
   - Given now, schedule spec, last fired time, and max catchup, return due
     `scheduled_at_utc` values.
   - Deterministic, timezone-free, UTC-only.
   - Tests: first run, no catchup, one catchup, multiple missed ticks capped,
     jitter bounds, disabled trigger ignored.

3. Add scheduler state file.
   - Default:
     `$BILLYHARNESS_HOME/agentclub-scheduler-state.json`.
   - Store last successful scheduled timestamp per trigger id and summary
     counters.
   - Private dir/file permissions.
   - Atomic writes.
   - Corrupt state should fail closed or move aside with explicit warning; pick
     the pattern that matches existing session input quarantine behavior.

4. Add scheduler runner command.
   - Load runtime config and agent-club config.
   - Discover gateway URL or use `-gateway`.
   - For each due enabled schedule trigger, POST a
     `TriggerDeliveryRequest` to `/v1/agentclub/triggers/{id}/deliveries`.
   - Include stable payload/metadata:
     - trigger id;
     - scheduled_at_utc;
     - scheduler run id or host marker;
     - no secrets.
   - `-once` should evaluate once and exit for tests/systemd timers.
   - Long-running mode should tick periodically, handle SIGINT/SIGTERM, and not
     spin when gateway is down.

5. Add managed service metadata.
   - Add `billyharness-agentclub-scheduler.service` only if the project wants it
     managed like gateway/telegram.
   - Update `internal/serviceops`, doctor, and ops docs if added.
   - If not adding a service yet, document the command as manually runnable and
     leave service integration for later.

6. Preserve admission-only behavior.
   - Scheduler must not call `/v1/sessions/{id}/run`.
   - Scheduler must not execute tools/MCP/shell.
   - Scheduler must not load project manifests.
   - Scheduler must not mutate proposal state.
   - Its only side effect should be trigger delivery plus its own state file.

7. Add observability.
   - `agentclub scheduler status` should show configured schedules, enabled
     count, due count, last success, last error, and state path.
   - Doctor/readiness can show scheduler config/state if the service exists.
   - Redact session IDs/client IDs consistently with existing agent-club output.

8. Docs and generated references.
   - Update security/gateway docs to say schedule runner is a separate local
     process that only submits trigger deliveries.
   - Update generated CLI docs.
   - Add ops note if service metadata is added.

## Target Files

Likely files:

- `internal/agentclub/config.go`
- `internal/agentclub/config_test.go`
- `internal/agentclub/schedule.go`
- `internal/agentclub/schedule_test.go`
- `cmd/fast-agent-harness/agentclub_cmd.go`
- `cmd/fast-agent-harness/agentclub_scheduler.go`
- `cmd/fast-agent-harness/agentclub_cmd_test.go`
- `internal/gatewayclient/client.go`
- `internal/serviceops/services.go` if adding service metadata
- `cmd/fast-agent-harness/doctor.go`
- `ops/production-services.md`
- `docs/architecture/security-model.md`
- `docs/architecture/gateway-and-sessions.md`
- `docs/generated/*` through docsgen only

## Boundaries

- No cron parser unless deliberately justified.
- No auto-run.
- No executor/proposal apply.
- No project-local manifests.
- No browser automation/debug tooling.
- No raw secrets in state/status/logs.
- No direct gateway server imports from scheduler command code beyond normal
  command adapter boundaries.

## Verification Commands

```sh
go test -count=1 ./internal/agentclub ./internal/config ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness ./internal/serviceops
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestGatewayReadiness|TestConfigStatus'
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

## Done Means

- Enabled interval schedule triggers can be fired by a local scheduler runner.
- Scheduler posts through the existing trigger delivery route.
- State is durable, private, and deterministic.
- `-once` mode gives a testable/systemd-timer-friendly path.
- No session run is dispatched.
- Status/doctor/docs explain the scheduler boundary.
- The branch is committed and pushed.

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/018-todo.md end to end.

Build the agent-club schedule runner. Extend agentclub config with a small
UTC-only interval schedule spec for kind=schedule triggers, add deterministic
due-tick calculation and private scheduler state, and add a CLI runner that
loads trusted config and submits due schedule deliveries through the existing
gateway trigger delivery route. It must not run sessions directly.

Stay inside boundaries:
- no cron dependency unless explicitly justified in the implementation report;
- no auto-run;
- no executor/apply;
- no project-local manifests;
- no browser automation/debug tooling;
- no secret leakage in state/status/logs.

Verification required:
go test -count=1 ./internal/agentclub ./internal/config ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness ./internal/serviceops
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestGatewayReadiness|TestConfigStatus'
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

- Extended agent-club trigger config with an interval `schedule` spec for
  `kind=schedule` triggers, including validation for required enabled
  schedules, unsupported kinds, positive `every`, bounded deterministic jitter,
  bounded catchup, RFC3339 UTC start time, and no schedules on webhook/manual
  triggers.
- Added deterministic UTC due-tick calculation, enabled schedule filtering,
  private scheduler state load/save, success/error recording, and focused
  schedule/state tests.
- Added `agentclub scheduler run` and `agentclub scheduler status`; `run -once`
  posts due ticks only through `/v1/agentclub/triggers/{id}/deliveries`, writes
  state, reports redacted summaries, and never calls a session run route.
- Added SIGINT/SIGTERM handling for long-running mode.
- Documented the separate local runner boundary in gateway/session and
  security architecture docs. No service-manager unit was added; the docs state
  the command is manually runnable or timer-owned by the operator.

Verification commands run:

```sh
go test -count=1 ./internal/agentclub ./internal/config ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness ./internal/serviceops
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestGatewayReadiness|TestConfigStatus'
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

Commit/push state:

- Pending final chain commit and push after TODOs 019-020 complete.

Remaining blockers:

- None for 018.
- Unrelated pre-existing clipboard/TUI worktree changes remain unstaged and
  untouched.
