# Billyharness Setup And Services

## Build

```sh
cd /root/billyharness
GO_BIN=/root/.local/go/bin/go ./scripts/verify-deps.sh
/root/.local/go/bin/go test -count=1 ./...
/root/.local/go/bin/go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
```

## Gateway

The gateway is the default binary mode:

```sh
cd /root/billyharness
./bin/fast-agent-harness gateway
```

The normal local address is `127.0.0.1:8765`. Check readiness with:

```sh
curl -fsS http://127.0.0.1:8765/health
./bin/fast-agent-harness doctor
./bin/fast-agent-harness doctor -json
```

Inspect saved gateway sessions directly from JSONL files without calling the API:

```sh
./bin/fast-agent-harness sessions list
./bin/fast-agent-harness sessions inspect SESSION_ID
./bin/fast-agent-harness sessions inspect -json SESSION_ID
```

Session status and list responses include `dropped_events` when a live `/events` subscriber falls behind its bounded buffer. The gateway keeps publishing without blocking the active run; clients should use the `after_seq` replay cursor to recover missed JSONL events when this counter is nonzero.

## TUI

The TUI auto-discovers the local gateway from config, so normal use does not need `-gateway`:

```sh
cd /root/billyharness
./bin/fast-agent-harness tui
```

For awkward SSH terminals:

```sh
stty -ixon
./bin/fast-agent-harness tui -plain
```

## Telegram

Telegram reads its token and allowlist from `/root/billyharness/.env` unless flags override them:

```sh
cd /root/billyharness
./bin/fast-agent-harness telegram
```

Useful `.env` keys:

```sh
TELEGRAM_BOT_TOKEN=...
BILLYHARNESS_TELEGRAM_ALLOWED_USER_IDS=342262559,8226987886
BILLYHARNESS_TELEGRAM_SEND_ENABLED=true
```

## Dangerous Local Tools

Billyharness is tuned for solo local use. The normal TUI command enables local write and shell tools:

```sh
./bin/fast-agent-harness tui
```

Equivalent explicit form:

```sh
./bin/fast-agent-harness tui -dangerous=true
```

To disable write/shell auto-approval for a safer session:

```sh
FAST_AGENT_AUTO_APPROVE_DANGEROUS=false ./bin/fast-agent-harness tui -dangerous=false
```

Run access mode can also be set explicitly:

```sh
BILLYHARNESS_ACCESS_MODE=plan ./bin/fast-agent-harness tui -access-mode plan
./bin/fast-agent-harness run -access-mode plan "inspect the project before editing"
./bin/fast-agent-harness telegram -access-mode plan
```

`access_mode=build` keeps the normal solo-owner behavior. `access_mode=guarded`
keeps tools visible but denies write and shell execution. `access_mode=plan`
advertises only read/search tools and hard-denies write, execute, and external
tool calls even when dangerous auto-approval is enabled.

Managed shell processes are available to the agent in build mode for dev
servers and watchers. A background `shell_exec` returns a Billy-owned
`process_id`; `shell_output` reads bounded output by cursor and records an
`output_ref`; `shell_kill` terminates only that Billy-owned process id.

```json
{"argv":["npm","run","dev"],"cwd":".","background":true}
{"process_id":"shell-1","cursor":0,"max_output_bytes":65536}
{"process_id":"shell-1"}
```

Audit status is visible through:

```sh
./bin/fast-agent-harness config inspect
./bin/fast-agent-harness doctor
```

Dangerous local operations still emit tool permission/audit events into the replayable JSONL event stream.

## Systemd

Installed units on this server:

```sh
/etc/systemd/system/billyharness-gateway.service
/etc/systemd/system/billyharness-telegram.service
```

Current unit definitions:

```sh
systemctl cat billyharness-gateway.service
systemctl cat billyharness-telegram.service
```

Start, stop, restart, and status:

```sh
systemctl start billyharness-gateway.service billyharness-telegram.service
systemctl stop billyharness-telegram.service billyharness-gateway.service
systemctl restart billyharness-gateway.service billyharness-telegram.service
systemctl --no-pager --full status billyharness-gateway.service billyharness-telegram.service
```

The gateway unit uses `KillSignal=SIGINT` and `TimeoutStopSec=20`. On shutdown the gateway cancels active sessions and records `run.failed` events so JSONL replay does not leave active runs looking permanently live.

## Logs

Use journalctl for service logs:

```sh
journalctl -u billyharness-gateway.service -n 200 --no-pager
journalctl -u billyharness-telegram.service -n 200 --no-pager
journalctl -u billyharness-gateway.service -f
journalctl -u billyharness-telegram.service -f
```

For boot-scoped logs:

```sh
journalctl -b -u billyharness-gateway.service --no-pager
journalctl -b -u billyharness-telegram.service --no-pager
```

## Quick Failure Checks

```sh
cd /root/billyharness
git status --short
./bin/fast-agent-harness hygiene -strict -repo /root/billyharness
./bin/fast-agent-harness doctor -strict
curl -fsS http://127.0.0.1:8765/health
./bin/fast-agent-harness sessions list
systemctl --no-pager --full status billyharness-gateway.service billyharness-telegram.service
journalctl -u billyharness-gateway.service -n 80 --no-pager
journalctl -u billyharness-telegram.service -n 80 --no-pager
```
