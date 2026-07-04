# ADR 0006 - Telegram Is A Gateway Client

Status: accepted
Date: 2026-07-03
Owners: billy
Supersedes: none
Superseded by: none

## Context

Billyharness has multiple operator surfaces: the gateway, TUI, Telegram bot,
CLI commands, and future clients. Telegram needs to submit prompts, render
progress, ingest images, answer user-input prompts, and expose operator commands
without gaining direct access to runtime internals or gateway server state.

The package boundary map already requires `internal/telegrambot` not to import
`internal/gateway`. The current implementation also carries Telegram-specific
chat/thread/user identity that must be enforced consistently by the gateway, not
only by local bot filtering.

## Decision

Telegram is a scoped gateway client. It owns Telegram transport, admission
records, local chat state, rendering, and command dispatch. Gateway sessions,
run execution, durable event replay, input idempotency, owner-scoped
authorization, auth mutation, MCP/config/context/process status, cancellation,
and undo/redo stay behind typed gateway APIs.

Telegram stamps sessions and gateway requests with `client_type=telegram` plus
Telegram chat, thread, and user identifiers. The shared gateway client sends
that owner scope in create-session bodies and request headers. The gateway is
the authority that filters and denies cross-owner access.

## Consequences

- Telegram can evolve as a presentation adapter without coupling to gateway
  server internals, provider runtime, tools, or the agent loop.
- Gateway owner enforcement protects Telegram users from seeing or mutating
  other Telegram users' owned sessions.
- Legacy unowned sessions remain readable for scoped clients, but mutating them
  from Telegram is rejected by the gateway; fork creates a new owned session.
- Live Telegram progress can be lossy, but final rendering must recover from
  durable gateway events before delivery.
- New Telegram operator commands should use gateway APIs or presentation-neutral
  helper packages, not gateway server imports.

## Verification

Code paths:

- [internal/telegrambot/session_owner.go](../../internal/telegrambot/session_owner.go)
- [internal/telegrambot/gateway_client.go](../../internal/telegrambot/gateway_client.go)
- [internal/gatewayclient/client.go](../../internal/gatewayclient/client.go)
- [internal/gateway/session_authz.go](../../internal/gateway/session_authz.go)
- [internal/gateway/gateway.go](../../internal/gateway/gateway.go)
- [docs/architecture.md](../architecture.md)

Focused tests:

```sh
go test -count=1 ./internal/telegrambot ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestGatewaySessionOwner(MetadataPersistsAndLists|ScopeFiltersAndDeniesCrossOwner)$'
go test -count=1 ./internal/architecture
```
