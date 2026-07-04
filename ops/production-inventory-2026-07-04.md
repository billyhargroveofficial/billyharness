# Production Inventory - 2026-07-04

Verified over SSH from the Mac checkout on `2026-07-04T16:16:50Z`.

Host:

- SSH target: `root@82.23.163.16`
- Hostname: `14420.example.com`
- Kernel: `Linux 5.15.0-185-generic #195-Ubuntu SMP Fri Jun 19 17:11:50 UTC 2026 x86_64`
- Checkout: `/root/billyharness`
- Production git commit at inspection time:
  `ce3f2c943ee2a08672a858a701cafa0a9e4f62fc`
- Git status at inspection time: clean

Binary and toolchain:

- Binary path: `/root/billyharness/bin/fast-agent-harness`
- Binary mode/owner/size/mtime:
  `-rwx------ root root 20342370 Jul 2 15:48`
- Binary SHA-256:
  `02c1e3cd06a928236c94bee80f7880a45147698e13fed4c2e1961d12cc72a654`
- Go binary: `/root/.local/go/bin/go`
- Go version: `go1.26.4 linux/amd64`

State roots and sensitive files:

- `$BILLYHARNESS_HOME`: `/root/billyharness`
- Settings path: `/root/billyharness/settings.json`
- Env file path: `/root/billyharness/.env`
- Env file metadata: mode `600`, owner `root`, group `root`, size `536`,
  mtime `2026-06-28 09:46:17 +0200`
- MCP config path: `/root/billyharness/mcp.config.toml`
- Codex auth path: `/root/billyharness/auth/codex.json`
- Gateway session store: `/root/billyharness/gateway-sessions`, exists,
  about `62978687` bytes at inspection time
- Tool output store: `/root/billyharness/tool-output`, exists, about
  `7135752` bytes at inspection time

Managed systemd services:

| Service | State | Unit | User | Workdir | ExecStart | Env file | Restart | Logs |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `billyharness-gateway.service` | `active/running`, enabled | `/etc/systemd/system/billyharness-gateway.service` | `root` | `/root/billyharness` | `/root/billyharness/bin/fast-agent-harness gateway` | `-/root/billyharness/.env` | `always`, `RestartSec=2` | `StandardOutput=journal`, `StandardError=inherit` |
| `billyharness-telegram.service` | `active/running`, enabled | `/etc/systemd/system/billyharness-telegram.service` | `root` | `/root/billyharness` | `/root/billyharness/bin/fast-agent-harness telegram` | `-/root/billyharness/.env` | `always`, `RestartSec=2` | `StandardOutput=journal`, `StandardError=inherit` |

Shared unit settings observed in `systemctl cat`:

- `After=network-online.target`
- `Wants=network-online.target`
- `Environment=FAST_AGENT_ENV_FILE=/root/billyharness/.env`
- `KillSignal=SIGINT`
- `TimeoutStopSec=20`
- `LimitNOFILE=1048576`
- Telegram additionally has
  `After=network-online.target billyharness-gateway.service` and
  `Requires=billyharness-gateway.service`.

Live process snapshot:

- Gateway: one process,
  `/root/billyharness/bin/fast-agent-harness gateway`
- Telegram: one process,
  `/root/billyharness/bin/fast-agent-harness telegram`

Gateway binding and route probe:

- Listener: `127.0.0.1:8765`
- `/health`: HTTP `200`, body
  `{"model":"gpt-5.5","ok":true,"provider":"openai-codex"}`
- Local unauthenticated route probes on this production commit returned:
  `/health 200`, `/v1/config 200`, `/v1/mcp 200`, `/v1/tools 200`,
  `/v1/processes 200`.
- Note: this inventory records production commit
  `ce3f2c943ee2a08672a858a701cafa0a9e4f62fc`. Local `main` has later gateway
  read-route hardening, so do not treat these route status codes as the desired
  post-deploy auth behavior.

Doctor summary:

- `./bin/fast-agent-harness doctor -json` completed successfully.
- Provider/model: `openai-codex` / `gpt-5.5`
- Profile: `billy`
- Access mode: `build`
- Reasoning effort: `xhigh`
- Gateway URL: `http://127.0.0.1:8765`
- Auth summary: DeepSeek API key configured via `/root/billyharness/.env`;
  Codex OAuth configured via `/root/billyharness/auth/codex.json`; credentials
  were reported as `redacted`.
- MCP enabled with allowed servers:
  `telegram,telegram-parilka,github,context7`
- Strict hygiene: `ok`, `317` tracked Go files, no large source files
- Checks: git root `ok`, git status `ok`, build check `ok`, both services
  `ok`, duplicate process checks `ok`, pid files `ok`, gateway `/health` `ok`

Redaction and non-inspected material:

- Raw `/root/billyharness/.env`, auth JSON files, MCP inline env values,
  bearer tokens, provider keys, Telegram tokens, and journal log contents were
  not copied into this inventory.
- `systemctl cat` and process lines were captured through token/key/password
  redaction before returning to the local machine.
