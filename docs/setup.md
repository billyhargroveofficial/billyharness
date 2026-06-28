# Billyharness Setup And Services

## Build

```sh
cd /root/billyharness
/root/.local/go/bin/go test -count=1 ./...
/root/.local/go/bin/go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
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
./bin/fast-agent-harness doctor -strict
curl -fsS http://127.0.0.1:8765/health
./bin/fast-agent-harness sessions list
systemctl --no-pager --full status billyharness-gateway.service billyharness-telegram.service
journalctl -u billyharness-gateway.service -n 80 --no-pager
journalctl -u billyharness-telegram.service -n 80 --no-pager
```
