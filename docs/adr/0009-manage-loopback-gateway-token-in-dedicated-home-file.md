# ADR 0009 - Manage Loopback Gateway Token In A Dedicated Home File

Status: accepted
Date: 2026-07-24
Owners: billy
Supersedes: none
Superseded by: none

## Context

ADRs 0007 and 0008 require bearer trust for protected gateway routes. Requiring
operators to export the same secret independently in the gateway, TUI, jobs,
Telegram, and CLI processes made normal local use brittle. Reusing generic
project dotenv discovery would also let repository-controlled files choose a
transport credential, while blindly loading a local token for any gateway URL
could disclose it to a remote host.

## Decision

Normal loopback startup on Darwin/Linux manages one transport token at
`$BILLYHARNESS_HOME/auth/gateway.token`. It creates the `auth` directory with
mode `0700`, writes the token with mode `0600`, checks ownership, regular-file
and single-link properties, rejects symlinks, serializes first startup across
processes, and publishes a generated 256-bit token atomically before binding
the listener.

Resolution precedence is:

1. explicit server `-auth-token`;
2. primary process `BILLYHARNESS_GATEWAY_AUTH_TOKEN`;
3. the dedicated token file;
4. the primary key in `$BILLYHARNESS_HOME/.env`;
5. legacy process `FAST_AGENT_GATEWAY_AUTH_TOKEN`;
6. the legacy key in `$BILLYHARNESS_HOME/.env`.

The primary process override is never persisted. Home-dotenv and legacy values
are bounded migration inputs and are copied into the dedicated file. Project
dotenv discovery and `FAST_AGENT_ENV_FILE` never select gateway transport auth.
Invalid configured values and unsafe managed files fail closed.

Automatic random generation is loopback-only. A first non-loopback or wildcard
bind without configured auth fails before listening and requires a
preprovisioned credential plus HTTPS or an SSH tunnel. The explicit
`-dev-allow-unauthenticated-loopback-mutations` flag skips generation only for
disposable loopback development.

Billyharness clients load managed file/home-dotenv sources only for loopback
URLs. A non-loopback URL accepts only an explicit process credential, and an
already-set `Authorization` header always wins. Public `/health` and `/ready`
probes do not receive the bearer header. Managed persistence fails closed
outside Darwin/Linux until equivalent ownership, link, permission, and
cross-process publication guarantees exist there.

Token rotation is not part of this decision; changing a live server credential
requires a separate synchronization and restart contract.

## Consequences

Normal local gateway and TUI/jobs processes sharing `BILLYHARNESS_HOME` no
longer need duplicated exports. The token is separate from provider
credentials, is never written to `config.toml`, and generated values have a
recognizable redaction prefix.

Raw HTTP clients still need to attach the bearer explicitly. Remote
Billyharness clients must receive an explicit process token rather than
silently reusing a local managed credential. Windows users can use an explicit
process token or the documented loopback development bypass.

## Verification

Code paths:

- `internal/gatewayauth/store.go`
- `internal/gatewayauth/open_unix.go`
- `internal/gatewayapi/net.go`
- `cmd/fast-agent-harness/gateway_auth.go`
- `cmd/fast-agent-harness/service_cmd.go`

Focused checks:

```sh
go test -race -count=1 ./internal/gatewayauth ./internal/gatewayapi
go test -count=1 ./cmd/fast-agent-harness ./internal/gatewayclient ./internal/telegrambot
go test -count=1 ./internal/architecture ./internal/docsgen
```
