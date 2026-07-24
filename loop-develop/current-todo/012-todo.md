# 012 — Secure automatic gateway bearer token

## Source and user-facing failure

Recovered from Codex thread `019f904b-54a3-7f21-8249-9786a7cb2946` and its
unfinished worktree `codex/gateway-token-autoconfig`. Durable multi-agent jobs
and `/jobs` are already complete at `65feb36`; the remaining daily-use defect is
that gateway and TUI require the same bearer secret to be exported separately
in two terminals.

The secure design review converged on one provider-neutral transport credential:

- explicit `-auth-token` remains the highest-precedence server override;
- primary process env remains an explicit non-persisted override;
- normal loopback startup may create a random 256-bit token at
  `$BILLYHARNESS_HOME/auth/gateway.token`;
- Billyharness clients read the same dedicated token automatically for
  loopback URLs, while remote URLs require an explicit process token;
- old home-dotenv and legacy env values are migration inputs only;
- project dotenv discovery and `FAST_AGENT_ENV_FILE` must never choose gateway
  transport auth;
- first-time non-loopback startup fails closed and requires preprovisioned auth
  plus HTTPS or an SSH tunnel;
- token rotation is out of scope until live-server/client synchronization is
  designed.

## Checklist

- [x] Finish `internal/gatewayauth` secure resolve/ensure contract.
- [x] Add bounded validation, no-follow/single-link checks, atomic publication,
      private modes, concurrent-first-start behavior, and fail-closed errors.
- [x] Integrate gateway startup and every built-in HTTP client with the shared
      resolver while preserving explicit overrides and dev bypass semantics.
- [x] Ensure loopback auto-provisions but non-loopback never invents a new
      credential silently.
- [x] Prevent managed local tokens from being forwarded to remote URLs and keep
      public health/readiness probes free of bearer headers.
- [x] Replace obsolete `.env`/manual-export TUI and CLI guidance.
- [x] Add focused unit, race, integration, and real no-export smoke coverage.
- [x] Update architecture/security/ADR/operator documentation and regenerate
      code-owned docs.
- [ ] Run full verification, commit only this slice, and push
      `codex/gateway-token-autoconfig`.
- [ ] Reconcile the commit into the main dirty checkout without changing or
      losing its provider/failover WIP, then rebuild local binaries.

## Target boundaries

- `internal/gatewayauth/`: sole owner of dedicated token resolution/persistence.
- `internal/gatewayapi/`: shared request-header adapter, no persistence logic.
- `cmd/fast-agent-harness/service_cmd.go`: loopback/non-loopback provisioning
  policy and server override handling.
- `internal/gatewayclient/`, TUI, Telegram, CLI helpers, doctor: shared client
  resolution with propagated errors.
- `README.md`, gateway/security architecture docs, ADR 0007/0008, generated
  package/CLI docs: public contract.

## Verification

```sh
go test -count=1 ./internal/gatewayauth ./internal/gatewayapi ./internal/gatewayclient ./internal/gateway ./cmd/fast-agent-harness ./internal/tui ./internal/telegrambot
go test -race -count=1 ./internal/gatewayauth ./internal/gatewayapi ./internal/gatewayclient ./internal/gateway
go test -count=1 ./...
go vet ./...
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
```

## Copy-ready `/goal` prompt

```text
/goal Finish loop-develop/current-todo/012-todo.md end to end. Preserve the
existing provider/failover WIP in the main checkout. Complete the dedicated
gateway bearer token store, loopback provisioning policy, all built-in client
integration, tests, docs, generated references, and a real no-export smoke.
After verification passes, create a scoped git commit and push the branch.
Reconcile that commit into the main dirty checkout without losing unrelated
changes, rebuild the local binaries, archive 012 with exact evidence, and do
not stop at an intermediate milestone.
```
