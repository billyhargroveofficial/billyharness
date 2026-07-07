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

Ingress audit is stored in a separate redacted gateway-store ledger. It records
hashes, decision reasons, target session id, admitted input id, duplicate
state, client identity hash, and metadata keys, but not raw bodies, prompts,
external IDs, metadata values, or secrets.

## Consequences

Future adapters such as HH, cron, or webhook surfaces must enter through this
contract before any runtime work happens.

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
- `internal/gateway/ingress.go`
- `internal/gateway/session_inputs.go`
- `internal/gateway/session_authz.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`

Focused tests:

```sh
go test -count=1 ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestGatewayIngress|TestGatewaySessionClientID'
```
