# Billyharness Operations

Last verified: 2026-07-05. Commands in this index were checked against
`README.md`, `go run ./cmd/fast-agent-harness help`,
`go run ./cmd/fast-agent-harness doctor -h`, and the gateway/service/session
command source in this worktree. Production SSH facts must be checked live
against `root@82.23.163.16` before changing the VPS.

`ops/` is the runbook lane for production operation, health checks, and
diagnostics. Keep these procedures outside `docs/`, because `docs/` is the
architecture canon only.

## Runbooks

- [Doctor and diagnostics](doctor-and-diagnostics.md): local health snapshots,
  config inspection, gateway readiness, MCP status, session diagnostics, and
  redacted export commands.
- [Production services](production-services.md): production entrypoint, service
  names, readiness checks, deploy/rollback script, restarts, and
  deployment-time cautions.

## First Response

Use the doctor runbook for a fast state snapshot:

```sh
./bin/fast-agent-harness doctor
./bin/fast-agent-harness doctor -json
```

For an editing-time snapshot that skips active process checks:

```sh
./bin/fast-agent-harness doctor -build=false -services=false -gateway=false
```

Production is described by the project contract as `root@82.23.163.16` under
`/root/billyharness`. For current production facts, run
`./bin/fast-agent-harness doctor -json` and inspect the host live:

```sh
ssh root@82.23.163.16
cd /root/billyharness
```

## Secrets And Redaction

Do not paste raw contents of `$BILLYHARNESS_HOME/.env`, auth JSON files, MCP
configs with inline `env` values, shell histories, Telegram bot tokens, Codex
tokens, DeepSeek keys, GitHub tokens, or bearer tokens into tickets or chat.

Prefer status commands that already sanitize values, such as `doctor`,
`doctor -json`, `config inspect`, `sessions inspect`, `sessions context`,
`sessions export`, and `/v1/auth/status`.
When collecting logs, redact tokens, `Authorization` headers, API keys, and
credential-bearing URLs before sharing.

## Boundaries

These runbooks may name systemd services that are referenced by code or
`README.md`, but this repository does not currently contain `.service` unit
files. Do not invent unit definitions here; inspect the live host when unit
contents, environment files, restart policy, or working directories matter.
