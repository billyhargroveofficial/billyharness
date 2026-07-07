# 016 - Agent-Club E2E Smoke And Manual Delivery UX

## Goal

Turn the 015 foundation into a two-minute operator happy path.

After this TODO, Billy should be able to:

1. initialize or point at an agent-club config;
2. validate it;
3. create or reuse a gateway session;
4. deliver one trusted manual/schedule/webhook trigger from CLI;
5. see a queued session input and trigger audit evidence;
6. optionally run the session through the existing session run route.

This is still not an executor, scheduler daemon, or auto-run policy. It is the
operator smoke layer that proves the generic contract actually works outside
unit tests.

## Current Local Finding

015 added the important primitives:

- `agentclub.config.json` loader in `internal/agentclub/config.go`;
- local CLI config commands in `cmd/fast-agent-harness/agentclub_cmd.go`;
- gateway startup registry loading through `cmd/fast-agent-harness/service_cmd.go`;
- trigger delivery route:
  `POST /v1/agentclub/triggers/{binding_id}/deliveries`;
- typed gateway client helpers for sessions and agent-club discovery/proposals;
- readiness, config status, and doctor summaries.

The current missing usability layer:

- there is no CLI command to deliver a configured trigger;
- operators still need to hand-write JSON and HMAC headers to test webhook
  delivery;
- schedule/manual trigger delivery exists in the gateway, but no friendly CLI
  path constructs `TriggerDeliveryRequest`;
- the quick verification story is spread across config commands, curl-like
  requests, and session inspection.

Billy-facing failure fixed: after 015, the platform is correct but still feels
like plumbing. This TODO makes it feel like an operator tool.

## Source Research Summary

Local sources checked:

- `cmd/fast-agent-harness/agentclub_cmd.go`: current CLI shape and redacted
  output style.
- `cmd/fast-agent-harness/agentclub_config.go`: shared config loading and
  secret lookup.
- `internal/agentclub/triggers.go`: canonical webhook, schedule, and manual
  delivery request/response semantics.
- `internal/gateway/agentclub_triggers.go`: route behavior, HMAC verification,
  dry registration, and redacted trigger audit.
- `internal/gatewayclient/client.go`: typed session/input helpers and client
  transport patterns.
- `docs/architecture/gateway-and-sessions.md`: admission-only boundary.

External design already captured in 015 still applies: mature systems expose a
short local smoke path separate from integration authoring. This TODO should
not introduce a framework or new dependency for that.

## Implementation Checklist

1. Add a typed gateway client helper for trigger delivery.
   - Target: `internal/gatewayclient/client.go`.
   - Add `DeliverAgentClubTrigger(ctx, bindingID string, body []byte, headers map[string]string)` or a small request struct that can carry body and headers.
   - Preserve bearer auth and URL normalization behavior.
   - Add focused client tests for path escaping, body forwarding, header
     forwarding, and typed error status.

2. Add CLI command `agentclub trigger deliver`.
   - Keep existing `agentclub triggers` as local config listing.
   - Suggested usage:
     ```text
     fast-agent-harness agentclub trigger deliver TRIGGER_ID
       [-gateway URL]
       [-path CONFIG]
       [-payload JSON_OR_PATH_OR_-]
       [-delivery-id ID]
       [-scheduled-at RFC3339|now]
       [-dry-run-registration]
       [-json]
     ```
   - For `kind=manual` and `kind=schedule`, build
     `agentclub.TriggerDeliveryRequest`.
   - For `kind=webhook`, send the raw payload as the body.
   - For webhook HMAC triggers, load the local config, resolve
     `hmac_secret_env`, sign the raw body with the configured signature header,
     and set the configured delivery-id header.
   - Do not print raw secret values, signatures, raw payloads, or raw prompts.

3. Add payload input handling.
   - Accept inline JSON, `@path`, and `-` for stdin.
   - Bound CLI-read payload size to the trigger max body cap when config is
     available.
   - Default empty payload to `{}`.
   - Reject invalid JSON before calling the gateway for manual/schedule
     deliveries.

4. Add a compact smoke command or smoke runbook command.
   - Either add `agentclub smoke trigger TRIGGER_ID ...` or make
     `agentclub trigger deliver` print enough next-step lines.
   - The operator should see:
     - config status;
     - delivery admitted/rejected status;
     - input id;
     - duplicate marker;
     - target session id;
     - `run_dispatched=false`.
   - If the gateway is unavailable, reuse the existing unavailable hint.

5. Add optional session run follow-up without auto-run.
   - Add a printed hint:
     ```text
     queued input <id>; run it with: fast-agent-harness gateway run -session ...
     ```
     or, if there is already a sessions/gateway command for session runs, point
     to the real command.
   - Do not add `--run` unless the implementation also adds strict tests that
     prove it goes through the existing explicit session run route. The safer
     default is no run flag in this TODO.

6. Add tests around realistic operator mistakes.
   - Missing config file.
   - Unknown trigger id.
   - Disabled trigger.
   - Missing HMAC env.
   - Bad payload JSON.
   - Webhook delivery missing delivery id.
   - Gateway returns 401/403/404/413 and CLI redacts output.
   - Duplicate delivery prints duplicate state.

7. Update docs and generated references.
   - Update `docs/architecture/gateway-and-sessions.md` only if behavior
     changes.
   - Update `docs/architecture/security-model.md` if CLI signing/secret
     behavior needs durable documentation.
   - Run docsgen if CLI docs change.

## Target Files

Likely files:

- `cmd/fast-agent-harness/agentclub_cmd.go`
- `cmd/fast-agent-harness/agentclub_cmd_test.go`
- `cmd/fast-agent-harness/agentclub_config.go`
- `internal/gatewayclient/client.go`
- `internal/gatewayclient/client_test.go`
- `internal/agentclub/triggers.go` only if a small helper belongs there
- `internal/ingress` only if HMAC signing helpers need to be shared
- `docs/architecture/gateway-and-sessions.md`
- `docs/architecture/security-model.md`
- `docs/generated/*` through docsgen only

## Boundaries

- Do not add scheduler loops.
- Do not add auto-run.
- Do not add proposal apply.
- Do not load project-local manifests.
- Do not hardcode HH behavior beyond using the disabled default example config.
- Do not expose raw HMAC secrets, signatures, prompts, payloads, delivery IDs,
  metadata values, or bearer tokens in CLI output.
- Do not use browser automation/debug tooling.

## Verification Commands

```sh
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestAgentClubEvent|TestGatewaySessionClientID'
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

## Done Means

- A configured manual/schedule/webhook trigger can be delivered from CLI.
- Webhook HMAC signing works from `hmac_secret_env` without leaking secrets.
- The gateway admits the trigger as a queued input and still reports
  `run_dispatched=false`.
- Operator output is compact, redacted, and actionable.
- Tests cover bad config, disabled/unknown triggers, missing secrets, bad JSON,
  duplicate delivery, and gateway errors.
- Generated docs are current.
- The branch is committed and pushed.

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/016-todo.md end to end.

Build the agent-club E2E smoke/manual delivery UX. Add a typed gateway client
helper for trigger delivery and a CLI path that can deliver configured manual,
schedule, and webhook triggers from the local agentclub config. Webhook delivery
must sign raw bodies from hmac_secret_env when configured and must never print
raw secrets, signatures, payloads, prompts, delivery IDs, metadata values, or
bearer tokens.

Stay inside the TODO boundaries:
- no scheduler loop;
- no auto-run;
- no proposal apply;
- no project-local manifest loader;
- no HH hardcode beyond disabled example config;
- no browser automation/debug tooling.

Verification required:
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestAgentClubEvent|TestGatewaySessionClientID'
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short

If broad tests are blocked by unrelated pre-existing worktree changes, record
the exact blocker and still run every focused command that applies.

After verification passes, create a git commit and push the branch. Include the
commit hash, branch name, commands run, and any residual blockers in the final
report.
```

## Final Status

Completed on 2026-07-07.

Implementation evidence:

- Added typed gatewayclient trigger delivery transport with body/header
  forwarding, path escaping, owner headers, and typed status errors.
- Added `agentclub trigger deliver TRIGGER_ID` with interspersed flags, payload
  sources, local config validation, webhook HMAC signing through trigger-domain
  code, safe gateway status errors, and compact redacted smoke output.
- Added focused tests for manual delivery, webhook signing/redaction, missing
  config, unknown/disabled trigger ids, missing HMAC env, bad JSON, missing
  delivery id, gateway status redaction, duplicate delivery output, and client
  transport behavior.
- Regenerated generated package docs.

Verification commands run:

```sh
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness
go test -count=1 ./internal/gateway -run 'TestAgentClubTrigger|TestAgentClubEvent|TestGatewaySessionClientID'
go run ./cmd/fast-agent-harness docsgen
go test -count=1 ./internal/architecture
go test -count=1 ./internal/agentclub ./internal/config ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewaySessionRun|TestGatewaySessionInput|TestGatewaySessionClientID|TestGatewayReadiness|TestConfigStatus'
go test -count=1 ./cmd/fast-agent-harness ./internal/runtimehost ./internal/serviceops
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

Commit/push state:

- Pending final chain commit and push after TODOs 017-020 complete.

Remaining blockers:

- None for 016.
- Unrelated pre-existing clipboard/TUI worktree changes remain unstaged and
  untouched.
