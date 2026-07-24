# Production Services

Live-host facts last verified: 2026-07-05. Command shapes updated locally:
2026-07-24, checked against
`README.md`, `go run ./cmd/fast-agent-harness help`,
`go run ./cmd/fast-agent-harness doctor -h`, `internal/gatewayauth`,
`internal/gatewayapi`, and the current service and doctor command sources.
Re-check live production state before changing it.

This runbook records production operation steps. It does not define
architecture and it does not include systemd unit contents.

## Production Entrypoint

Production is described by the project contract as `root@82.23.163.16` under
`/root/billyharness`. Verify host identity live before changing production:

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
`StandardOutput=journal`. Re-run `doctor -json` and `systemctl cat` on the host
for current binary checksum, commit, doctor output, and route probe details.

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

The primary repo-owned deploy lane is a SHA-named binary plus a stable
`bin/fast-agent-harness-current` symlink. On the production host, the systemd
unit files must point at the symlink instead of the fixed binary path:

```sh
ExecStart=/root/billyharness/bin/fast-agent-harness-current gateway ...
ExecStart=/root/billyharness/bin/fast-agent-harness-current telegram ...
```

Unit file contents live on the host, not in this repo. Inspect and edit them on
the VPS with `systemctl cat` / `systemctl edit`, then run `systemctl daemon-reload`.
After that host-side switch, use:

```sh
scripts/deploy.sh
```

`scripts/deploy.sh` builds `bin/fast-agent-harness-$(git rev-parse --short
HEAD)`, records the previous `bin/fast-agent-harness-current` target in
`bin/.previous-release`, repoints the symlink, restarts
`billyharness-gateway.service` and `billyharness-telegram.service`, then gates
on:

```sh
./bin/fast-agent-harness-current doctor -mode=production
curl -sf http://127.0.0.1:8765/health
curl -sf http://127.0.0.1:8765/ready
```

If verification fails, the script restores the previous symlink target,
restarts services again, and exits non-zero. It appends verified releases to
`bin/.release-history` and keeps the last
`${BILLYHARNESS_DEPLOY_KEEP_RELEASES:-5}` SHA binaries.

Do not run this symlink lane against production until the production doctor and
`/ready` checks are confirmed stable there. The older source-checkout deploy
script remains available when you need commit checkout, test, build-provenance,
and manifest evidence in one command:

```sh
scripts/production-deploy.sh deploy --yes
```

For manual broad runtime changes, `README.md` lists this test and rebuild
shape. Prefer one of the guarded scripts above when changing production:

```sh
/root/.local/go/bin/go test -count=1 ./...
build_commit="$(git rev-parse HEAD)"
build_short="$(git rev-parse --short HEAD)"
build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
/root/.local/go/bin/go build -trimpath -buildvcs=false \
  -ldflags "-X main.version=0.1.0+$build_short -X main.buildCommit=$build_commit -X main.buildTime=$build_time" \
  -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
```

When `go.mod` or `go.sum` changes, verify dependencies:

```sh
GO_BIN=/root/.local/go/bin/go ./scripts/verify-deps.sh
```

After restart, run:

```sh
./bin/fast-agent-harness-current doctor -mode=production
curl http://127.0.0.1:8765/health
curl http://127.0.0.1:8765/ready
```

For machine-readable evidence:

```sh
./bin/fast-agent-harness-current doctor -mode=production -json
```

## Rollback Pattern

Use the symlink rollback script first:

```sh
scripts/rollback.sh
```

It reads `bin/.previous-release`, repoints
`bin/fast-agent-harness-current`, then runs the same restart, doctor, `/health`,
and `/ready` helper as deploy. If rollback verification fails, it restores the
symlink to the target that was active before the rollback attempt.

If the host has not switched its unit files to
`/root/billyharness/bin/fast-agent-harness-current`, use the older source
checkout script's manifest instead. It records the previous commit and a
copy-ready command:

```sh
scripts/production-deploy.sh rollback --yes --to PREVIOUS_GOOD_COMMIT
```

That rollback uses the source checkout rebuild model, embedded build
provenance, service restart, strict doctor gate, and `/health` plus `/ready`
probes as deploy.

Before changing production manually, record the current commit, binary path,
service status, and doctor output:

```sh
git rev-parse HEAD
ls -l ./bin/fast-agent-harness
systemctl is-active billyharness-gateway.service
systemctl is-active billyharness-telegram.service
./bin/fast-agent-harness doctor -json
```

For a manual source-level rollback on the production checkout, return to the
previous known-good commit, rebuild the binary with the same Go path used
during deploy, restart both managed services, and rerun doctor plus `/health`
and `/ready`:

```sh
git checkout PREVIOUS_GOOD_COMMIT
build_commit="$(git rev-parse HEAD)"
build_short="$(git rev-parse --short HEAD)"
build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
/root/.local/go/bin/go build -trimpath -buildvcs=false \
  -ldflags "-X main.version=0.1.0+$build_short -X main.buildCommit=$build_commit -X main.buildTime=$build_time" \
  -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
systemctl restart billyharness-gateway.service
systemctl restart billyharness-telegram.service
./bin/fast-agent-harness doctor -mode=production
curl http://127.0.0.1:8765/health
curl http://127.0.0.1:8765/ready
```

If the live host uses a binary archive, symlink, package manager, or separate
release directory, inspect that mechanism on the host and roll back through
that mechanism instead of forcing a source checkout pattern.

## Gateway Auth And Binding

The default gateway address is `127.0.0.1:8765`. If binding to a non-loopback
address, configure bearer auth first. An explicit process-managed deployment
has this shape:

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

Normal loopback startup provisions
`$BILLYHARNESS_HOME/auth/gateway.token` before opening the listener, so the
detached process and local Billyharness clients can share auth without an
environment export. For a non-loopback bind, preprovision the dedicated token
or configure an explicit process token before using this pattern.

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
2. If gateway liveness fails, run the gateway service status command above on
   the production host and check `/health` again after restart. If `/health`
   passes but readiness fails, keep the `/ready` body and `doctor
   -mode=production` output as the dependency snapshot.
3. If Telegram is failing while gateway is healthy, inspect
   `billyharness-telegram.service` live using the operator patterns above,
   then confirm the gateway URL and allowlist settings.
4. If MCP or tool discovery looks wrong, inspect
   `$BILLYHARNESS_HOME/mcp.config.toml` on the host without sharing secrets,
   then use the authenticated `/v1/mcp` pattern in
   [Doctor and diagnostics](doctor-and-diagnostics.md) for sanitized gateway
   status.
5. If session replay, errors, or usage are the issue, rebuild the session index
   with `./bin/fast-agent-harness sessions index rebuild`, then use the session
   diagnostics commands from [Doctor and diagnostics](doctor-and-diagnostics.md).
