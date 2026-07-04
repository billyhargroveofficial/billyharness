# ADR 0008 - Gateway State Reads Require Bearer When Token Configured

Status: accepted
Date: 2026-07-04
Owners: billy
Supersedes: none
Superseded by: none

## Context

Billyharness exposes state-bearing `/v1/` read routes for sessions, events,
configuration status, auth status, MCP status, tool catalogs, process
summaries, and debugging surfaces. Earlier hardening required explicit trust
for mutating local routes, but loopback reads could still bypass bearer auth
when a token was configured.

Local browsers can reach loopback services, and DNS rebinding can make a remote
page appear to share an origin with a local gateway. State-bearing reads
therefore need the same fail-closed transport boundary as mutations whenever
the operator configured a gateway bearer token.

## Decision

`/health` remains unauthenticated for cheap liveness probes. `/ready` remains
unauthenticated for bounded readiness probes that summarize effective config,
tool/MCP catalog health, and startup session-store diagnostics without raw MCP
metadata, schemas, prompts, or store paths.

All `/v1/` gateway routes are browser-reachable and must pass host and
same-origin request checks before handlers run. Loopback requests must use a
loopback gateway host, and any `Origin` or `Referer` header must match the
gateway host.

When `ServerOptions.AuthToken` is configured, `/v1/` requests require a
matching bearer token even from loopback remote addresses. This includes
`GET`, `HEAD`, and `OPTIONS` requests.

`DevAllowUnauthenticatedLoopbackMutations` remains a mutation-only development
bypass. It does not make configured-token read routes public.

## Consequences

Local CLI, TUI, Telegram, and future client surfaces must attach the shared
gateway bearer token for protected `/v1/` reads when the gateway is token
protected. The shared gateway client already reads
`BILLYHARNESS_GATEWAY_AUTH_TOKEN` and the legacy
`FAST_AGENT_GATEWAY_AUTH_TOKEN`.

Operators can still run an unauthenticated loopback development gateway when
they deliberately omit a token, but a configured token now means configured
protection for both state reads and mutations.

Future `/v1/` read routes should assume they are protected state unless a new
ADR explicitly marks them safe for unauthenticated access.

## Verification

Code paths:

- `internal/gateway/http_security.go`
- `internal/gateway/gateway.go`
- `internal/gateway/url.go`
- `internal/gatewaybase/gatewaybase.go`
- `internal/gatewayclient/client.go`
- `cmd/fast-agent-harness/service_cmd.go`

Focused tests:

```sh
go test -count=1 ./internal/gateway -run 'TestGateway(AuthMiddlewareProtectsConfiguredV1Reads|MutationAuthProtectsLoopbackBrowserRoutes|MutationAuthExplicitDevLoopbackBypass)$'
go test -count=1 ./internal/architecture
```
