# ADR 0009 - External Ingress Is Gateway Admission

Date: 2026-07-07
Owners: billy

## Context

Billyharness needs future project/webhook/schedule triggers to wake existing
sessions without giving those triggers direct runtime authority. External
events can be retried, spoofed, duplicated, or malformed, and rejected events
need replayable evidence.

## Decision

External ingress is a gateway admission path, not a direct execution path.

A local rule verifies and sanitizes an external event, derives a deterministic
session `input_id`, and asks the gateway to append the normal session input
ledger. The gateway owner-scope check still decides whether that rule may
mutate the target session. Ingress does not promote the input, start a run,
execute tools, call MCP, shell out, or accept provider/model/access-mode
overrides from payload metadata.

Agent-club v0 is the neutral HTTP contract for project adapters in this
decision. A request to `POST /v1/sessions/{id}/agentclub/events` carries
`schema_version=1`, `source`, `capability`, `event_type`,
`external_event_id`, `prompt`, a JSON payload, and safe metadata. Authority
comes from gateway auth plus session-owner headers, not from the body. The
route requires `client_type=ingress` and a non-empty client id, uses that actor
as the ingress rule owner, admits the event as a queued session input, and
returns only redacted ids, hashes, metadata keys, and `run_dispatched=false`.

Agent-club descriptors and trusted bindings are the first registry layer over
that route. A descriptor is metadata only (`id`, title, description, kind, risk,
schemas, `dispatch=admit_only`, approval, version). A binding is trusted local
gateway policy that links a descriptor to `client_type=ingress`, a concrete
client id, optional source/event-type restrictions, optional safe metadata
keys, and an enabled flag. When a registry is configured, the event route
rejects unknown, disabled, or mismatched capability submissions before ingress
audit or input admission. `GET /v1/agentclub/capabilities` exposes only enabled
descriptors and safe binding metadata visible to the current actor.

Verified trigger delivery is the first route that receives raw external
deliveries. A trusted trigger binding describes webhook, schedule, or manual
delivery for one known source/capability/event type, ingress owner, target
session, prompt, auth method, body cap, and enabled state. The delivery route
`POST /v1/agentclub/triggers/{binding_id}/deliveries` verifies the raw body
when HMAC-SHA256 is configured, derives deterministic external event identity,
maps the delivery to the same normalized agent-club event contract, and admits
the resulting input without dispatching a run. Schedule/manual delivery is a
deterministic request shape only; this decision still does not start a
scheduler daemon.

Ingress audit is stored in a separate redacted gateway-store ledger. It records
hashes, decision reasons, target session id, admitted input id, duplicate
state, client identity hash, and metadata keys, but not raw bodies, prompts,
external IDs, metadata values, or secrets.

## Consequences

Future adapters such as project CLIs, cron, or webhook surfaces must enter
through this contract before any runtime work happens. Billyharness core should
not import concrete project adapters.

The registry is an admission policy surface, not an execution surface. This
decision adds verified webhook delivery as admission, but still does not add a
scheduler, project-local manifest loader, safe-output executor, action approval
loop, generic command runner, raw API caller, raw SQL caller, browser auth
bridge, or concrete project adapter.

The trigger delivery endpoint is not an auto-run endpoint. It writes redacted
trigger audit evidence and, on success, a queued session input. A separate
operator/client action must still run the session later.

Safe-output proposals are also admission state, not execution. A proposal is a
durable session-scoped review artifact with action kind, risk, preview or
output ref, payload hash, target scope, policy version, proposal hash, owner,
timestamps, optional expiry, and metadata keys. Decisions are separate
hash-bound records: `approve`, `reject`, or `edit` as a new proposal. The
JSONL ledger records proposal creation, decision, expiration, supersede, and
failure states so replay can reconstruct the queue.

Approving a proposal does not apply it. This decision intentionally stops at
Proposal -> Decision -> Future Apply; any future executor must prove that it
applies the exact approved artifact and must add its own tests, docs, and
security review.

Retries become idempotent because input IDs include rule id, source, external
event id, payload hash, and target session id. If local mapping changes while
those identity fields stay the same, existing session input conflict behavior
surfaces the mismatch.

Protocol `ingress.*` events are not added yet because this slice does not
dispatch runtime work. If a future adapter persists ingress lifecycle in
session event streams, it must add protocol constants, generated docs, and
lifecycle/projection tests in that same change.

## Verification

Code paths:

- `internal/ingress`
- `internal/agentclub`
- `internal/gateway/agentclub_events.go`
- `internal/gateway/agentclub_triggers.go`
- `internal/gateway/agentclub_proposals.go`
- `internal/gateway/ingress.go`
- `internal/gateway/session_inputs.go`
- `internal/gateway/session_authz.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`

Focused tests:

```sh
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestGatewayIngress|TestAgentClub|TestGatewaySessionClientID'
```
