# 011 - Verified Agent-Club Triggers V0

## Source Research Summary

After `010`, Billyharness should know which agent-club capabilities and bindings
are trusted. The next missing production layer is trigger delivery: webhook,
cron/manual replay, and scheduled ticks should be verified, normalized,
deduplicated, and admitted through the same session-input path.

Mature systems agree on the shape:

- GitHub webhooks validate a raw request body with an HMAC-SHA256 signature in
  `X-Hub-Signature-256`, use a delivery id header for dedupe, and compare
  signatures in constant time:
  <https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries>
- GitHub webhook best practice is to respond quickly, avoid long synchronous
  processing, and redeliver failed deliveries instead of doing work in the
  request path:
  <https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks>
- Trigger.dev models durable jobs around event triggering, idempotency,
  retries, and queues rather than immediate side effects:
  <https://trigger.dev/docs/triggering>
  <https://trigger.dev/docs/idempotency>
  <https://trigger.dev/docs/errors-retrying>
- Temporal schedules and workflow histories show the important split between a
  schedule/event and the workflow/session that eventually processes it:
  <https://docs.temporal.io/schedule>
  <https://docs.temporal.io/workflows>
- OpenHands Agent Canvas treats automations as cron/event/custom webhook
  sources that wake agent conversations:
  <https://docs.openhands.dev/openhands/usage/agent-canvas/overview>
  <https://docs.openhands.dev/openhands/usage/automations/event-automations>
- GitHub Agentic Workflows trigger/frontmatter docs are a useful reference for
  keeping trigger definitions declarative and inspectable:
  <https://github.github.com/gh-aw/reference/triggers/>
  <https://github.github.com/gh-aw/reference/frontmatter-triggers/>

Billyharness should not import Trigger.dev, Temporal, or a general workflow
engine here. V0 should be small: verify one trusted trigger delivery, map it to
an existing agent-club binding, admit a queued session input, and return a safe
delivery response.

## Product Direction

This slice turns Billyharness from "an adapter can POST a normalized event" into
"a trusted binding can receive a verified trigger delivery."

Target flow:

```text
external webhook / local cron tick / manual replay
  -> trusted trigger binding
  -> raw delivery verification and body cap
  -> event identity extraction
  -> agentclub.EventRequest
  -> existing gateway ingress admission
  -> queued session input, no run dispatch
```

Do not implement an always-on scheduler daemon yet. Cron v0 can be a manual
delivery shape or CLI/API helper that receives `scheduled_at_utc` and creates a
deterministic scheduled event id.

## Checklist

- [ ] Problem: trigger delivery currently has no typed model. Add agent-club
      trigger descriptor/binding types for `kind=webhook`, `kind=schedule`, and
      `kind=manual`, with binding id, source, capability, event type, owner,
      target session id or session selector, prompt template id/name, auth
      method, body cap, and enabled flag.
- [ ] Problem: webhook verification must happen over the raw body. Reuse or
      extend `internal/ingress/hmac.go` for HMAC-SHA256 verification,
      constant-time compare, header parsing, raw body caps, missing-signature
      errors, and redacted failure reasons.
- [ ] Problem: duplicate deliveries should be deterministic. Derive
      `external_event_id` from provider delivery id headers for webhooks and
      from `binding_id + scheduled_at_utc` for schedules; include payload hash
      and target session id in the existing ingress idempotency path.
- [ ] Problem: payloads cannot supply authority. The trigger binding, not the
      request JSON, must supply owner, source/capability/event type constraints,
      target session, prompt template, dispatch mode, and auth rules.
- [ ] Problem: the gateway needs a tiny delivery surface. Add a route such as
      `POST /v1/agentclub/triggers/{binding_id}/deliveries` that accepts raw
      webhook bodies or scheduled/manual JSON, verifies the binding, maps to
      `agentclub.EventRequest`, calls `Server.AdmitIngressEvent`, and returns a
      safe response with status, duplicate, input id, payload hash, event id
      hash, and `run_dispatched=false`.
- [ ] Problem: trigger failures need audit without leaking secrets. Extend
      ingress or agent-club audit only with redacted fields: binding id, source,
      capability, event type, decision, reason, body hash, external event id
      hash, and metadata keys. Do not store raw body, prompt, headers, signature,
      secret env name/value, delivery id, client id, or metadata values.
- [ ] Problem: schedules need a minimal deterministic path without a daemon.
      Add a schedule/manual delivery request that requires `scheduled_at_utc`
      and refuses future timestamps by default unless explicitly marked as dry
      registration. Do not start background timers in this slice.
- [ ] Problem: tests must prove the dangerous boundaries. Cover valid HMAC,
      invalid/missing HMAC, duplicate delivery, body cap, unknown/disabled
      binding, cross-owner/session denial, unsafe metadata rejection, and no run
      dispatch/no session event stream writes.
- [ ] Problem: docs should explain how external systems integrate. Update
      architecture/security docs with the trigger path, idempotency key,
      redaction behavior, and the fact that actual agent execution is still a
      separate session run.

## Target Files

Likely edit:

- `internal/agentclub/registry.go`
- `internal/agentclub/contract.go`
- `internal/ingress/hmac.go`
- `internal/gateway/agentclub_events.go`
- `internal/gateway/routes.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`
- `docs/architecture/gateway-and-sessions.md`
- `docs/architecture/security-model.md`
- `docs/adr/0009-external-ingress-is-gateway-admission.md`
- generated docs via `go run ./cmd/fast-agent-harness docsgen`

Likely add:

- `internal/agentclub/triggers.go`
- `internal/agentclub/triggers_test.go`
- `internal/gateway/agentclub_triggers.go`
- `internal/gateway/agentclub_triggers_test.go`

## Architecture Boundaries

- Trigger delivery is admission-only.
- Do not start runs, execute tools, call MCP, call provider APIs, or run
  project commands.
- Do not add a scheduler daemon, Windows Task Scheduler integration, systemd
  timers, or cron installer yet.
- Do not load project-local manifests.
- Do not add HH-specific, GitHub-specific, Nareshka-specific, or other concrete
  project logic in Billyharness core.
- Keep secrets out of response/audit/session inputs.
- No Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots,
  network capture, or browser debug.

## Verification Commands

```sh
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewayIngress|TestGatewaySessionClientID'
go test -count=1 ./internal/gateway ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/011-todo.md end to end. Add verified agent-club trigger delivery v0 on top of the trusted registry/bindings layer. Keep it admission-only: verified webhooks, schedule/manual deliveries, idempotent event identity, redacted audit/response, and queued session inputs with run_dispatched=false. The binding, not the payload, must supply authority: owner, source, capability, event_type, target session, prompt template, auth method, body cap, and enabled state. Reuse or extend ingress HMAC verification for raw-body HMAC-SHA256, constant-time compare, body caps, duplicate delivery handling, and safe errors. Do not add a scheduler daemon, auto-run, safe-output executor, generic command runner, project-local manifest loading, raw API caller, raw SQL caller, browser auth/debug, or any concrete HH/GitHub/Nareshka adapter. Do not use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, browser network capture, or browser debug. Update architecture/security docs and generated docs if API/package docs change. Verify with the TODO commands, then create a git commit and push the branch after verification passes.
```

## Final Status

Completed on 2026-07-07.

Implemented verified agent-club trigger delivery v0 as generic Billyharness
admission policy. Trusted trigger bindings now live in the in-memory
agent-club registry, with `webhook`, `schedule`, and `manual` kinds,
binding-owned source/capability/event type/owner/target session/prompt/auth/body
cap fields, HMAC-SHA256 raw-body verification, deterministic external event
identity, body caps, redacted trigger audit, duplicate-safe input admission,
and `run_dispatched=false`.

Added `POST /v1/agentclub/triggers/{binding_id}/deliveries`. Webhook
deliveries use binding id plus delivery-id header for event identity.
Schedule/manual deliveries require `scheduled_at_utc`; future timestamps are
rejected unless explicitly marked as dry registration, and dry registration
does not admit an input. The route never starts a run.

Documentation and generated docs were updated for the new route, trigger path,
audit behavior, and no-scheduler/no-auto-run boundary.

Verification passed:

```sh
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewayIngress|TestGatewaySessionClientID'
go test -count=1 ./internal/gateway ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
```

`git diff --check` emitted only Windows CRLF normalization warnings and exited
successfully. No scheduler daemon, auto-run, safe-output executor, command
runner, project-local manifest loader, raw API/SQL caller, browser auth/debug,
or concrete HH/GitHub/Nareshka adapter was added.

Commit/push state: included in the final 011 task commit and pushed after
verification. Remaining unrelated dirty worktree files were left untouched.
