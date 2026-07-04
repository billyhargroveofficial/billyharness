# Production Services

Last verified: 2026-07-04. Command shapes here were checked against
`README.md`, `go run ./cmd/fast-agent-harness help`,
`go run ./cmd/fast-agent-harness doctor -h`, `internal/gatewaybase`, and the
current service and doctor command sources. Live production service facts were
checked over SSH in `ops/production-inventory-2026-07-04.md`.

This runbook records production operation steps. It does not define
architecture and it does not include systemd unit contents.

## Production Entrypoint

Production is described by the project contract as `root@82.23.163.16` under
`/root/billyharness`. The current dated inventory is
`ops/production-inventory-2026-07-04.md`; verify host identity again before
changing production:

```sh
ssh root@82.23.163.16
cd /root/billyharness
```

## Service Names

The managed service names checked by doctor are:

- `billyharness-gateway.service`
- `billyharness-telegram.service`

`README.md` says to restart both after deployed runtime behavior changes:

```sh
systemctl restart billyharness-gateway.service
systemctl restart billyharness-telegram.service
```

Doctor checks service activity with:

```sh
systemctl is-active billyharness-gateway.service
systemctl is-active billyharness-telegram.service
```

The gateway readiness helper includes this recovery/status command for the
gateway service:

```sh
systemctl --no-pager --full status billyharness-gateway.service
```

There are no `.service` unit files in this repository as of this verification
pass. The live host currently has unit files at:

- `/etc/systemd/system/billyharness-gateway.service`
- `/etc/systemd/system/billyharness-telegram.service`

Both run as `root`, use `WorkingDirectory=/root/billyharness`, load
`EnvironmentFile=-/root/billyharness/.env`, set
`Environment=FAST_AGENT_ENV_FILE=/root/billyharness/.env`, restart with
`Restart=always` and `RestartSec=2`, and log to journald with
`StandardOutput=journal`. See the dated inventory for binary checksum, commit,
doctor output, and route probe details.

For fresh unit contents, environment files, `WorkingDirectory`, restart policy,
and log routing, inspect the live host:

```sh
systemctl cat billyharness-gateway.service
systemctl cat billyharness-telegram.service
journalctl -u billyharness-gateway.service -n 200 --no-pager
journalctl -u billyharness-telegram.service -n 200 --no-pager
```

Redact secrets from unit environment files and logs before sharing output. Do
not copy raw `/root/billyharness/.env`, auth JSON, MCP inline env, Telegram
tokens, provider keys, or bearer tokens into tickets.

## Deploy-Time Checks

For broad runtime changes, `README.md` lists this test and rebuild shape:

```sh
/root/.local/go/bin/go test -count=1 ./...
/root/.local/go/bin/go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
```

When `go.mod` or `go.sum` changes, verify dependencies:

```sh
GO_BIN=/root/.local/go/bin/go ./scripts/verify-deps.sh
```

After restart, run:

```sh
./bin/fast-agent-harness doctor
curl http://127.0.0.1:8765/health
```

For machine-readable evidence:

```sh
./bin/fast-agent-harness doctor -json
```

## Rollback Pattern

This repository does not define the production deploy mechanism or a release
archive layout. Before changing production, record the current commit, binary
path, service status, and doctor output:

```sh
git rev-parse HEAD
ls -l ./bin/fast-agent-harness
systemctl is-active billyharness-gateway.service
systemctl is-active billyharness-telegram.service
./bin/fast-agent-harness doctor -json
```

For a source-level rollback on the production checkout, return to the previous
known-good commit, rebuild the binary with the same Go path used during deploy,
restart both managed services, and rerun doctor plus `/health`:

```sh
git checkout PREVIOUS_GOOD_COMMIT
/root/.local/go/bin/go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
systemctl restart billyharness-gateway.service
systemctl restart billyharness-telegram.service
./bin/fast-agent-harness doctor
curl http://127.0.0.1:8765/health
```

If the live host uses a binary archive, symlink, package manager, or separate
release directory, inspect that mechanism on the host and roll back through
that mechanism instead of forcing a source checkout pattern.

## Gateway Auth And Binding

The default gateway address is `127.0.0.1:8765`. If binding to a non-loopback
address, configure bearer auth first. The README shows the protected-gateway
shape:

```sh
export BILLYHARNESS_GATEWAY_AUTH_TOKEN='change-me'
./bin/fast-agent-harness gateway -addr 0.0.0.0:8765
curl -H "Authorization: Bearer $BILLYHARNESS_GATEWAY_AUTH_TOKEN" http://127.0.0.1:8765/v1/auth/status
```

Do not use the literal `change-me` value in production. Store the real token in
the production environment or service environment file, and redact it from
logs, shell history, command transcripts, and incident notes.

The current gateway startup code also requires a bearer token for mutating
routes by default. For loopback-only development there is a
`-dev-allow-unauthenticated-loopback-mutations` flag, but that flag is not a
production setting.

## Manual Gateway Pattern

`README.md` contains this detached local gateway pattern:

```sh
setsid ./bin/fast-agent-harness > gateway.log 2>&1 < /dev/null &
```

Because current gateway startup requires auth unless the development bypass is
explicitly enabled, verify that `BILLYHARNESS_GATEWAY_AUTH_TOKEN` or
`-auth-token` is configured before using that pattern on production.

## Telegram Service

The `telegram` command supports a gateway URL override:

```sh
./bin/fast-agent-harness telegram -gateway http://127.0.0.1:8765
```

Live Telegram sending requires a bot token and either an allowed chat, an
allowed user, or explicit `-allow-all-chats`. Treat `-allow-all-chats` as unsafe
unless the operator intentionally wants the bot to respond everywhere the token
has access.

Sensitive commands such as `/config`, `/processes`, `/memory`, `/undo`, `/redo`,
and `/auth` also require an identified operator user. Configure that with
`-operator-user` or `BILLYHARNESS_TELEGRAM_OPERATOR_USER_IDS`; do not rely on
group allowlisting as operator authority.

Relevant flags checked from the command source include `-token`, `-bot-api-base`,
`-allow-chat`, `-allow-user`, `-operator-user`, `-require-allowlist`,
`-allow-all-chats`, `-send-enabled`, `-dry-run`, `-poll-timeout-sec`,
`-edit-interval-ms`, `-model`, `-profile`, `-reasoning`, `-access-mode`, and
`-max-rounds`.

## Restart Triage

1. Run `./bin/fast-agent-harness doctor` and keep the sanitized output.
2. If gateway readiness fails, run the gateway service status command above on
   the production host and check `/health` again after restart.
3. If Telegram is failing while gateway is healthy, inspect
   `billyharness-telegram.service` live using the operator patterns above,
   then confirm the gateway URL and allowlist settings.
4. If MCP or tool discovery looks wrong, inspect
   `$BILLYHARNESS_HOME/mcp.config.toml` on the host without sharing secrets,
   then use `curl http://127.0.0.1:8765/v1/mcp` for sanitized gateway status.
5. If session replay, errors, or usage are the issue, rebuild the session index
   with `./bin/fast-agent-harness sessions index rebuild`, then use the session
   diagnostics commands from [Doctor and diagnostics](doctor-and-diagnostics.md).
