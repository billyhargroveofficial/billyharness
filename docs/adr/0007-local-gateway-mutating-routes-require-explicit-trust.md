# ADR 0007 - Local Gateway Mutating Routes Require Explicit Trust

Status: accepted
Date: 2026-07-03
Owners: billy
Supersedes: none
Superseded by: none

## Context

Billyharness exposes a local HTTP gateway for TUI, Telegram, CLI helpers,
browser-side local clients, and future clients. Mutating gateway routes can
create sessions, run providers and tools, persist credentials, cancel active
runs, answer user-input prompts, and undo/redo workspace changes.

Earlier gateway auth treated loopback mutation as trusted whenever a bearer
token was configured: `/health` and loopback remote addresses bypassed bearer
auth, while non-loopback clients needed the token. That was convenient for local
development, but a browser can also reach loopback services, so local mutating
routes need a more explicit trust boundary.

## Decision

Mutating `/v1/` gateway routes require explicit trust.

When mutation auth is enabled, the gateway admits a mutating request only after
browser-oriented request checks and either a valid bearer token or an explicit
development loopback bypass. The request checks validate loopback host,
same-host `Origin` or `Referer` when present, and `application/json`
content-type when a body is present.

The `serve` command must not silently launch a mutation-capable gateway without
that trust boundary. It requires a gateway auth token for mutating routes unless
the operator explicitly enables unauthenticated loopback mutations for
development.

`/health` remains unauthenticated for cheap liveness probes. `/ready` remains
unauthenticated for bounded readiness probes that expose redacted counts and
health state rather than raw gateway state. Session owner headers remain scoping
claims inside the HTTP security boundary, not credentials.
Per-run provider/model/reasoning overrides require bearer-authenticated
mutation when mutation auth is enabled; requests may still lower privilege
through stricter access mode or lower tool-round caps.

## Consequences

Local browser and local CLI clients that mutate gateway state need a bearer
token unless the operator deliberately opts into loopback development bypass.

Loopback remains useful for development, but it is no longer treated as enough
trust for mutation by default in the hardened gateway path.

Gateway auth, browser request validation, session owner scope, and runtime
override privilege are one boundary. Future changes to any of those pieces
must update the security architecture doc and the gateway tests together.

Release and deployment docs must describe the auth behavior of the specific
build being shipped; this ADR records the intended architecture boundary, not a
substitute for binary or commit verification.

## Verification

Code paths:

- `internal/gateway/http_security.go`
- `internal/gateway/session_authz.go`
- `internal/gateway/gateway.go`
- `internal/gateway/url.go`
- `internal/gatewaybase/gatewaybase.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`
- `cmd/fast-agent-harness/service_cmd.go`

Focused tests:

```sh
go test -count=1 ./internal/gateway -run 'TestGateway(AuthMiddlewareProtectsNonLoopbackClients|MutationAuthProtectsLoopbackBrowserRoutes|MutationAuthExplicitDevLoopbackBypass|RunRequestPrivilegeClamps)$'
go test -count=1 ./internal/gateway -run 'TestGatewaySessionOwner(MetadataPersistsAndLists|ScopeFiltersAndDeniesCrossOwner)$'
go test -count=1 ./internal/architecture
```
