# ADR 0002 - Gateway Owns Session Authority

Status: accepted
Date: 2026-07-03
Owners: billy
Supersedes: none
Superseded by: none

## Context

Billyharness has multiple client surfaces over one runtime: TUI, Telegram,
CLI helpers, and future clients. Sessions are durable and replayable, and the
gateway is the only component that sees the complete HTTP request, session
store, event stream, and mutation path.

If each client surface decided independently which sessions it could read or
mutate, owner boundaries would drift and replay semantics would become hard to
trust. Session ownership also has to survive restart, so it must be stored with
the gateway session, not only in a client process.

## Decision

The gateway is the authority for session ownership and session access.

Clients may send owner metadata through `gatewayapi.SessionOwner` and the shared
`X-Billyharness-Session-*` headers. `internal/gatewayclient` is responsible for
attaching those headers from client context. `internal/gateway` is responsible
for normalizing owner metadata, storing it on session creation, filtering
session lists, and authorizing per-session reads and mutations.

Owner metadata is a scoping claim inside the gateway HTTP security boundary. It
is not a credential by itself. Bearer auth, loopback exposure, mutation checks,
and gateway configuration decide whether a request is trusted enough to mutate
runtime state.

Legacy unowned sessions remain readable to scoped clients, but scoped clients
may not mutate them. Future migrations may assign owners to legacy sessions, but
the current safe behavior is read-only for scoped actors.

## Consequences

Client surfaces stay thin: they identify themselves with shared DTOs/headers and
do not import gateway server internals.

Gateway changes that alter owner matching, scope headers, legacy-session
handling, bearer requirements, or mutation privilege must update the server
authorization code and this architecture documentation together.

The gateway can provide one durable audit and replay story: owner lives in the
session response/status/summary and durable manifest, while events remain the
source of replayable runtime history.

This does not make owner headers safe on an untrusted exposed network without
the HTTP security boundary. If the gateway is exposed beyond loopback, bearer
auth and deployment controls are required.

## Verification

Code paths:

- `internal/gateway/session_authz.go`
- `internal/gateway/http_security.go`
- `internal/gateway/gateway.go`
- `internal/gateway/session_store.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`

Focused tests:

- `TestGatewaySessionOwnerMetadataPersistsAndLists`
- `TestGatewaySessionOwnerScopeFiltersAndDeniesCrossOwner`
- `TestContextSessionOwnerSendsScopeHeaders`
- `TestGatewayMutationAuthProtectsLoopbackBrowserRoutes`
- `TestGatewayRunRequestPrivilegeClamps`
