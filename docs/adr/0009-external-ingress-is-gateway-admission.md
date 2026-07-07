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
the resulting input before any runtime work. By default it does not dispatch a
run. A later opt-in trigger `run_policy` layer may start the existing session
run machinery only after admission, owner-scope checks, duplicate checks,
busy/interrupt policy, cooldown/rate limits, and runtime privilege caps pass.
Schedule/manual delivery is a deterministic request shape; local schedule
runner ownership remains separate from the gateway.

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
project-local manifest loader, generic safe-output executor, generic action
approval loop, generic command runner, raw API caller, raw SQL caller, browser
auth bridge, or concrete project adapter.

The registry can be persisted in operator-owned JSON under
`$BILLYHARNESS_HOME/agentclub.config.json` or explicit env-configured files.
Those files define descriptors, stable trusted binding IDs, trigger bindings,
and HMAC secret env references; startup resolves secrets into runtime memory
only and fails closed for invalid enabled config.

The trigger delivery endpoint is not payload-driven execution. It writes
redacted trigger audit evidence and, on success, a queued session input. Default
triggers still require a separate operator/client action to run the session
later. Explicit `run_policy` triggers can dispatch only through the existing
gateway session run path and record a separate redacted auto-run decision.

Safe-output proposals are also admission state until an operator explicitly
applies them. A proposal is a durable session-scoped review artifact with
action kind, risk, preview or output ref, payload hash, target scope, policy
version, proposal hash, owner, timestamps, optional expiry, and metadata keys.
Decisions are separate hash-bound records: `approve`, `reject`, or `edit` as a
new proposal. The JSONL ledger records proposal creation, decision, expiration,
supersede, failure, apply, and apply-failure states so replay can reconstruct
the queue.

Approving a proposal still does not apply it. Apply is a second route:
`POST /v1/sessions/{id}/agentclub/proposals/{proposal_id}/apply` requires the
expected proposal hash and an idempotency key, verifies the current approved
artifact and owner scope, and returns duplicate apply results without invoking
the executor again. The v0 executor registry is intentionally narrow:
`record_note` writes only a redacted `proposal_applied` record and synthetic
`agentclub:apply:<apply_id>` output ref. Unsupported action kinds become
`proposal_apply_failed`. This route does not call external APIs, send HH
replies, apply to jobs, modify GitHub, run shell commands, call MCP tools,
perform browser work, dispatch a run, or execute arbitrary payloads.

Retries become idempotent because input IDs include rule id, source, external
event id, payload hash, and target session id. If local mapping changes while
those identity fields stay the same, existing session input conflict behavior
surfaces the mismatch.

Protocol `ingress.*` events are not added yet. Auto-run, when enabled, uses the
normal session run event stream after input admission; trigger and auto-run
decisions stay in redacted gateway-store audit ledgers. If a future adapter
persists ingress lifecycle in session event streams, it must add protocol
constants, generated docs, and lifecycle/projection tests in that same change.

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
