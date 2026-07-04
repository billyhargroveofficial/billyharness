# Doctor And Diagnostics

Last verified: 2026-07-03. Command shapes here were checked against
`README.md`, `go run ./cmd/fast-agent-harness help`,
`go run ./cmd/fast-agent-harness doctor -h`, and the current
`cmd/fast-agent-harness` and `internal/gateway` sources.

This runbook is operational. It records how to inspect a live or local
Billyharness checkout without making architecture claims.

## Redaction

`doctor`, `config inspect`, `incident collect`, and `/v1/auth/status` are
designed to report paths, provider/model state, credential presence, and
session diagnostics without returning obvious secret values. Still review output
before sharing it. Paths can reveal usernames and deployment layout, and dirty
git status can include sensitive filenames.

Never share raw `$BILLYHARNESS_HOME/.env`,
`$BILLYHARNESS_HOME/auth/credentials.json`,
`$BILLYHARNESS_HOME/auth/codex.json`, MCP config inline `env` values,
Telegram bot tokens, bearer tokens, or API keys.

## Doctor

Run the full health snapshot:

```sh
./bin/fast-agent-harness doctor
./bin/fast-agent-harness doctor -deep
```

Use JSON for scripts or diffable evidence:

```sh
./bin/fast-agent-harness doctor -json
./bin/fast-agent-harness doctor -deep -json
```

Make doctor fail the command when any check fails:

```sh
./bin/fast-agent-harness doctor -strict
```

While editing locally, skip build, systemd, and gateway checks:

```sh
./bin/fast-agent-harness doctor -build=false -services=false -gateway=false
```

Point doctor at an explicit checkout and allow slower checks:

```sh
./bin/fast-agent-harness doctor -repo /root/billyharness -timeout-sec 20
```

The current doctor implementation reports config/runtime paths, provider/model
state, credential presence, gateway session storage, tool-output storage, git
status, a lightweight CLI build check, systemd service activity, duplicate
gateway/telegram processes, pid-file staleness, and gateway `/health`.

## Incident Bundles

Collect a local bundle for a specific persisted session:

```sh
./bin/fast-agent-harness incident collect -session SESSION_ID -out /tmp/billyharness-incident
```

Useful editing-time variant that skips live systemd, gateway, MCP, and journal
probes:

```sh
./bin/fast-agent-harness incident collect -session SESSION_ID -out /tmp/billyharness-incident -build=false -services=false -gateway=false -mcp=false -logs=false
```

The bundle includes redacted `doctor` output, config/auth summaries, optional
MCP status, session inspection/context, rich and raw transcript exports, a
redacted session event JSONL copy, optional `journalctl` tails for the managed
gateway and Telegram services, and `incident-manifest.json`.

The command uses strict runtime config resolution and refuses to continue when
the target session cannot be inspected. Optional MCP and journal failures are
recorded as error artifacts and warnings in the manifest.

## Config Inspection

Inspect resolved config and sanitized provenance:

```sh
./bin/fast-agent-harness config inspect
./bin/fast-agent-harness config inspect -json
```

Inspect external MCP config migration state:

```sh
./bin/fast-agent-harness config mcp-migrate -json
./bin/fast-agent-harness config mcp-migrate -file /path/to/config.toml -json
```

The default Billyharness state root is `$BILLYHARNESS_HOME`, falling back to
`~/billyharness`. Important operational paths include `settings.json`, `.env`,
`mcp.config.toml`, `auth/credentials.json`, `auth/codex.json`,
`gateway-sessions`, `tool-output`, `gateway.pid`, and `telegram.pid`.

## Gateway Readiness

`/health` is the readiness check and is intentionally unauthenticated:

```sh
curl http://127.0.0.1:8765/health
```

Read-only diagnostics that are useful after the gateway is reachable:

```sh
curl http://127.0.0.1:8765/v1/config
curl http://127.0.0.1:8765/v1/mcp
curl http://127.0.0.1:8765/v1/tools
curl 'http://127.0.0.1:8765/v1/processes?include_exited=true&limit=20'
```

When the gateway is protected by a bearer token, include the header for
non-loopback clients and for any protected route:

```sh
curl -H "Authorization: Bearer $BILLYHARNESS_GATEWAY_AUTH_TOKEN" http://127.0.0.1:8765/v1/auth/status
```

Do not echo the token. If a command transcript must be shared, replace the
header value with `Bearer REDACTED`.

## Session Diagnostics

List persisted sessions:

```sh
./bin/fast-agent-harness sessions list
./bin/fast-agent-harness sessions list -json
```

Inspect a specific session:

```sh
./bin/fast-agent-harness sessions inspect SESSION_ID
./bin/fast-agent-harness sessions inspect -json SESSION_ID
./bin/fast-agent-harness sessions context SESSION_ID
```

Export transcript text:

```sh
./bin/fast-agent-harness sessions export -mode rich SESSION_ID
./bin/fast-agent-harness sessions export -mode raw SESSION_ID
```

Build or inspect the diagnostics index:

```sh
./bin/fast-agent-harness sessions index rebuild
./bin/fast-agent-harness sessions index show
```

Query the diagnostics index:

```sh
./bin/fast-agent-harness sessions search -limit 20 QUERY
./bin/fast-agent-harness sessions tools -session SESSION_ID -status failed
./bin/fast-agent-harness sessions errors -session SESSION_ID -query exit
./bin/fast-agent-harness sessions runs -status failed
./bin/fast-agent-harness sessions usage -limit 20
```

If a query reports that the diagnostics index is missing, run:

```sh
./bin/fast-agent-harness sessions index rebuild
```

## Common Findings

- `git status` warnings mean the checkout is dirty; inspect before restart or
  deploy so unrelated work is not overwritten.
- `build check` failures come from a lightweight
  `go test -run '^$' ./cmd/fast-agent-harness` compile check.
- `service ...` failures come from `systemctl is-active` for the managed
  service names.
- Duplicate-process failures come from `pgrep -af fast-agent-harness` matching
  the `gateway` or `telegram` subcommand more than once.
- Stale pid warnings mean `$BILLYHARNESS_HOME/gateway.pid` or
  `$BILLYHARNESS_HOME/telegram.pid` points at a process that is no longer
  running.
- `gateway /health` failures mean no configured local gateway candidate
  returned a 2xx response.
