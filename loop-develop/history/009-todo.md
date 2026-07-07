# 009 - Generic Agent-Club Contract V0

## Source Research Summary

Billy corrected the direction: Billyharness must not grow a hardcoded
`hh-applicant-tool` adapter in its core. The correct layer is a generic
agent-club contract that any later project, including HH Application Tool, can
target from the outside.

Six native Codex research subagents and local inspection converged on the same
shape:

- Keep the existing gateway ingress/session-input foundation. It already gives
  durable idempotent input admission, owner scoping, and redacted
  `ingress-audit.jsonl`.
- Remove the HH-specific route and adapter because they put a concrete project
  into Billyharness core:
  `internal/agentclub/hhapplicant`,
  `internal/gateway/agentclub_hh.go`,
  `POST /v1/sessions/{id}/agentclub/hh/review-queue`,
  `HHReviewQueueRequest`, `HHReviewQueueResponse`, and the typed client method.
- Model external work as a neutral event/capability admission contract:
  a local adapter verifies or creates an event, then Billyharness admits it as a
  session input. Running the agent remains a separate gateway-owned decision.
- Do not introduce a generic command runner. Manifests and events may describe
  capabilities, but project-local data must not silently grant shell, provider,
  model, access-mode, tool, secret, raw SQL, or remote mutation authority.

External references used for the contract:

- MCP tools/resources/prompts split and JSON Schema tool descriptors:
  <https://modelcontextprotocol.io/specification/draft/server/tools>
- OpenAI Apps SDK tool-first planning, read-only/destructive/open-world hints:
  <https://developers.openai.com/apps-sdk/plan/tools>
  <https://developers.openai.com/apps-sdk/reference>
- OpenHands Agent Canvas: conversations plus cron/event/custom webhook
  automations:
  <https://docs.openhands.dev/openhands/usage/agent-canvas/overview>
  <https://docs.openhands.dev/openhands/usage/automations/event-automations>
- Trigger.dev durable tasks: retries, queues, idempotency, waitpoints, HITL:
  <https://github.com/triggerdotdev/trigger.dev>
  <https://trigger.dev/product>
- Temporal workflow/event-history model for durable execution and schedules:
  <https://docs.temporal.io/workflows>
- LangGraph/LangChain HITL: interrupt, checkpoint, approval policy:
  <https://docs.langchain.com/oss/python/langchain/human-in-the-loop>
  <https://docs.langchain.com/oss/python/langgraph/interrupts>
- Agent Client Protocol and Agent Protocol as examples of neutral agent/session
  interfaces:
  <https://github.com/agentclientprotocol/agent-client-protocol>
  <https://github.com/agi-inc/agent-protocol>

## Contract Direction

Agent-club v0 is not a scheduler, not a webhook service, not a shell runner, and
not an HH adapter. It is a neutral contract for external project adapters to
admit events/capabilities into a Billyharness session.

Minimum model:

```json
{
  "schema_version": 1,
  "source": "github",
  "capability": "pull_request.review",
  "event_type": "pull_request.opened",
  "external_event_id": "provider-delivery-id",
  "prompt": "Summarize this PR event and recommend next actions.",
  "payload": { "redacted_or_structured_event": true },
  "metadata": {
    "project": "owner/repo",
    "actor": "login"
  }
}
```

Authority must come from local gateway auth and session-owner headers, not from
the payload. For external project adapters this normally means:

```text
X-Billyharness-Session-Client-Type: ingress
X-Billyharness-Session-Client-ID: ingress:<adapter-id>:<profile-or-env>
```

The gateway should derive deterministic identity from:

```text
sha256(rule_id/capability, source, external_event_id, payload_sha256, target_session_id)
```

## Checklist

- [ ] Problem: Billyharness core currently imports a concrete HH project.
      Remove the HH-specific adapter package, gateway handler, route, DTOs,
      client helper, and tests. Keep `loop-develop/history/008-todo.md` as
      historical evidence; do not rewrite history.
- [ ] Problem: generic ingress tests currently use HH-shaped fixtures.
      Rename those fixtures to neutral examples such as
      `ingress:fixture:prod`, `source=fixture`, and `project=fixture-project`
      so the remaining ingress foundation is project-agnostic.
- [ ] Problem: future adapters need a small, typed contract instead of raw
      session inputs. Add a neutral `internal/agentclub` contract package with
      v0 request/response structs, identifier validation, risk/dispatch
      constants, and a pure mapper to `ingress.IngressEvent` plus
      `ingress.IngressRule`.
- [ ] Problem: external adapters need idempotent admission without becoming
      gateway internals. Add a generic gateway route such as
      `POST /v1/sessions/{id}/agentclub/events` that admits one normalized
      agent-club event into the existing session input queue and never starts a
      run.
- [ ] Problem: payload authority is unsafe. The generic route must require a
      non-empty owner actor from session-owner headers, require
      `client_type=ingress`, authorize that actor against the target session,
      and use that actor as the ingress rule owner. The JSON body must not be
      able to set owner, provider, model, thinking, reasoning effort,
      access mode, max tool rounds, MCP, tool, shell, command, or env authority.
- [ ] Problem: adapters need deterministic retries. Reuse the existing ingress
      deterministic input behavior and return a safe response with admitted
      state, duplicate state, input id, target session id, source, capability,
      event type, payload hash, external event id hash, metadata keys, and
      `run_dispatched=false`. Do not include raw prompt, raw payload, external
      event id, client id, or metadata values in audit.
- [ ] Problem: the contract must be discoverable but not overbuilt. Document v0
      capability descriptors as metadata only: `id`, `title`, `description`,
      `kind`, `risk`, `input_schema`, `output_schema`, `dispatch=admit_only`,
      and approval semantics. Do not execute capabilities directly in this
      slice.
- [ ] Problem: docs currently teach the wrong HH-specific shape. Update
      architecture/security docs to say project adapters target the generic
      agent-club event contract, then run docs generation instead of manually
      editing generated docs.
- [ ] Problem: implementation must not smuggle in a scheduler/webhook system.
      Explicitly defer cron, webhook HMAC endpoints, auto-run, generic command
      execution, manifests from project files, raw API callers, raw SQL callers,
      browser auth/debug, and HH-specific behavior to later TODOs.

## Target Files

Likely remove:

- `internal/agentclub/hhapplicant/`
- `internal/gateway/agentclub_hh.go`
- `internal/gateway/agentclub_hh_test.go`

Likely edit:

- `internal/gateway/gateway.go`
- `internal/gateway/routes.go`
- `internal/gateway/ingress_test.go`
- `internal/gateway/session_events_status_test.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`
- `internal/gatewayclient/client_test.go`
- `docs/architecture/gateway-and-sessions.md`
- `docs/architecture/security-model.md`
- `docs/adr/0009-external-ingress-is-gateway-admission.md`
- generated docs via `go run ./cmd/fast-agent-harness docsgen`

Likely add:

- `internal/agentclub/contract.go`
- `internal/agentclub/contract_test.go`
- `internal/gateway/agentclub_events.go`
- `internal/gateway/agentclub_events_test.go`

## Architecture Boundaries

- Keep `internal/ingress` generic and reusable. Do not move HH logic into it.
- Keep `internal/agentclub` pure and small. It may import `gatewayapi` and
  `ingress`; it must not import `gateway`, `tools`, `agent`, provider packages,
  shell runners, browser/CDP code, or HH project code.
- The gateway route may call `Server.AdmitIngressEvent`, but must not promote
  the input, dispatch a run, execute a command, call MCP/tools, or change
  provider/model/access settings.
- Agent-club `skills` are instruction/capability metadata, not executable
  authority. Execution remains under normal Billyharness tool policy in a later
  session run.
- No Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots,
  network capture, or browser debug.

## Verification Commands

```sh
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestGatewayIngress|TestAgentClub|TestGatewaySessionClientID|TestSessionEvents'
go test -count=1 ./internal/gateway ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

If generated docs change, include them in the commit with the source changes.

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/009-todo.md end to end. Correct the architecture by removing the hardcoded HH Application Tool agent-club integration from Billyharness core and replacing it with a neutral agent-club contract v0. Keep the existing generic gateway ingress/session-input/audit foundation, but delete the HH-specific adapter package, gateway route, DTOs, client helper, tests, and generated docs references. Add a small generic internal/agentclub contract package and a generic POST /v1/sessions/{id}/agentclub/events admission route that accepts one normalized external adapter event, requires a non-empty session-owner actor from headers with client_type=ingress, authorizes that actor against the target session, maps to ingress.IngressEvent/IngressRule, admits an idempotent queued session input, returns only safe hashes/ids/metadata keys, and never dispatches a run. Do not add a scheduler, webhook HMAC endpoint, auto-run, generic command runner, manifests loaded from project files, raw API caller, raw SQL caller, browser auth/debug, or any HH-specific behavior in this slice. Do not use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, browser network capture, or browser debug. Update architecture/security docs and regenerated docs for the changed API/package surface. Verify with the TODO commands, then create a git commit and push the branch after verification passes.
```

## Final Status

Completed on 2026-07-07.

- Removed the HH-specific Billyharness core integration:
  `internal/agentclub/hhapplicant`,
  `internal/gateway/agentclub_hh.go`,
  `POST /v1/sessions/{id}/agentclub/hh/review-queue`,
  `HHReviewQueueRequest`, `HHReviewQueueResponse`, and the typed HH client
  helper/tests.
- Added neutral `internal/agentclub` contract v0 and generic
  `POST /v1/sessions/{id}/agentclub/events` gateway admission route.
- The route requires non-empty session-owner headers with
  `client_type=ingress`, authorizes that actor against the target session,
  admits through `Server.AdmitIngressEvent`, returns safe ids/hashes/metadata
  keys, and never dispatches a run.
- Updated architecture/security docs, ADR 0009, and generated gateway/package
  references.
- No scheduler, webhook HMAC endpoint, auto-run, generic command runner,
  project manifest loading, raw API/SQL caller, browser auth/debug, or
  HH-specific behavior was added.

Verification commands run:

```sh
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestGatewayIngress|TestAgentClub|TestGatewaySessionClientID|TestSessionEvents'
go test -count=1 ./internal/gateway ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
```

Commit/push state:

- Implementation commit and push are pending immediately after this history
  record is staged.

Remaining blockers:

- None.
