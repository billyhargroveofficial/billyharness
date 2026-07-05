# Doctor And Diagnostics

Last verified: 2026-07-05. Command shapes here were checked against
`README.md`, `go run ./cmd/fast-agent-harness help`,
`go run ./cmd/fast-agent-harness doctor -h`,
`go run ./cmd/fast-agent-harness attachments gc -h`, and the current
`cmd/fast-agent-harness` and `internal/gateway` sources.

This runbook is operational. It records how to inspect a live or local
Billyharness checkout without making architecture claims.

## Redaction

`doctor`, `config inspect`, `sessions inspect`, `sessions context`,
`sessions export`, and `/v1/auth/status` are designed to report paths,
provider/model state, credential presence, transcripts, and session diagnostics
without returning obvious secret values. Still review output before sharing it.
Paths can reveal usernames and deployment layout, dirty git status can include
sensitive filenames, and redaction is a leak-reduction layer rather than proof
that user content is safe to disclose.

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
state, provider capability validation, credential presence, binary version,
build commit, UTC build time, gateway bind mode, native tool catalog state, MCP
allowlist availability, gateway session storage readability/writability,
tool-output storage, attachment storage, git status, a lightweight CLI build
check, systemd service activity, duplicate gateway/telegram processes, pid-file
staleness, gateway `/health` liveness, and gateway `/ready` readiness. It
records `mode` in text and JSON output. `auto` mode resolves `/root/billyharness`
as `production` and other checkouts as `local`; production mode also adds
selected systemd unit metadata and recent journal crash/error signal summaries.

Doctor JSON includes `runtime.attachments_store` beside
`runtime.tool_output_store`. The attachments usage check warns when the store is
larger than `1073741824` bytes (`1 GiB`), matching the default
`attachments gc -max-bytes` budget. This warning is maintenance visibility only:
even `doctor -strict` treats it as non-failing unless some other check fails.

## Attachment Store GC

Image attachments are stored under `$BILLYHARNESS_HOME/attachments`.
Inspect current usage without deleting files:

```sh
./bin/fast-agent-harness attachments gc -dry-run
```

Prune with the default budget:

```sh
./bin/fast-agent-harness attachments gc
```

The default policy deletes stored attachments older than `720h` and then evicts
the oldest remaining files until total regular-file usage is at or below
`1073741824` bytes (`1 GiB`). Override either bound when needed:

```sh
./bin/fast-agent-harness attachments gc -max-age=168h -max-bytes=536870912
```

Set `-max-age=0` to disable age-based pruning or `-max-bytes=0` to disable the
size budget. In-flight `.tmp-attachment-*` files created by atomic attachment
writes are ignored by pruning.

## Session Diagnostics

Inspect and export a specific persisted session directly:

```sh
./bin/fast-agent-harness sessions inspect SESSION_ID
./bin/fast-agent-harness sessions context SESSION_ID
./bin/fast-agent-harness sessions export -mode rich SESSION_ID
./bin/fast-agent-harness sessions export -mode raw -json SESSION_ID
```

Use `doctor -json` beside these commands when you need machine-readable runtime
state for the same investigation. For service logs, inspect the live host with
`journalctl` and redact secrets before sharing output.

## Config Inspection

Inspect resolved config and sanitized provenance:

```sh
./bin/fast-agent-harness config inspect
./bin/fast-agent-harness config inspect -json
```

The default Billyharness state root is `$BILLYHARNESS_HOME`, falling back to
`~/billyharness`. Important operational paths include `settings.json`, `.env`,
`mcp.config.toml`, `auth/credentials.json`, `auth/codex.json`,
`gateway-sessions`, `tool-output`, `attachments`, `gateway.pid`, and
`telegram.pid`.

## Gateway Liveness And Readiness

`/health` is the cheap liveness check and is intentionally unauthenticated:

```sh
curl http://127.0.0.1:8765/health
```

`/ready` is the bounded readiness check. It reports effective provider/model,
visible native tool count, MCP catalog summary, and startup session-store health
without returning raw MCP metadata, prompts, schemas, or store paths:

```sh
curl http://127.0.0.1:8765/ready
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
./bin/fast-agent-harness sessions debug -gateway http://127.0.0.1:8765 SESSION_ID
./bin/fast-agent-harness sessions debug -gateway http://127.0.0.1:8765 -json SESSION_ID
./bin/fast-agent-harness sessions context SESSION_ID
```

`sessions inspect` reads the local store directly. `sessions debug` asks the
live gateway for `GET /v1/sessions/{id}/inspect`, so it applies gateway auth and
session-owner read policy. Both report separate readiness states:
`message_snapshot_ready` means a message/history snapshot can be loaded;
`event_replay_ready` means the event JSONL is present, valid, and closed enough
for incident-grade replay. The legacy `offline_replay_ready` field follows
event replay readiness.

Export transcript text:

```sh
./bin/fast-agent-harness sessions export -mode rich SESSION_ID
./bin/fast-agent-harness sessions export -mode raw SESSION_ID
```

Both text and `-json` transcript exports pass through the shared local redactor
before printing. The persisted session JSONL remains the durable replay truth;
operator-facing exports are presentation artifacts.

Build or inspect the diagnostics index:

```sh
./bin/fast-agent-harness sessions index rebuild
./bin/fast-agent-harness sessions index show
```

`sessions index show` prints the session index build time and a derived
diagnostics status line with `present`, `missing`, `stale`, row counts, the
diagnostics build time, and the last read error when the diagnostics index is
missing or corrupt. `sessions index rebuild` rebuilds both the session list
index and the diagnostics rows from the durable session store.

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
- `provider capability` failures mean the effective runtime provider/model
  binding violates local model capability policy before credentials are probed.
- `mcp allowlist` failures mean an allowlisted MCP server is missing, disabled,
  or configured with an unsupported transport such as streamable HTTP.
- `service ...` failures come from `systemctl is-active` for the managed
  service names.
- Duplicate-process failures come from `pgrep -af fast-agent-harness` matching
  the `gateway` or `telegram` subcommand more than once.
- Stale pid warnings mean `$BILLYHARNESS_HOME/gateway.pid` or
  `$BILLYHARNESS_HOME/telegram.pid` points at a process that is no longer
  running.
- `gateway /health` failures mean no configured local gateway candidate
  returned a 2xx response.
- `gateway /ready` failures mean the gateway process answered but dependency
  readiness failed, or no configured local gateway candidate returned readiness.
