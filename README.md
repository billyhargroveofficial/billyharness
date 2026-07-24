# billyharness

Fast Go agent harness with a gateway API, TUI chat, native tools, MCP server, and benchmark runner.

## Docs

- [Architecture map](docs/architecture.md) is the current package-boundary and
  import-rule source of truth.
- [Docs index](docs/README.md) routes the durable architecture canon and ADRs.

Work protocol for runtime changes:

1. Use `loop-develop/current-todo/NNN-todo.md` for active implementation plans.
2. Add or update focused tests.
3. Run the relevant package tests, then `/root/.local/go/bin/go test -count=1 ./...` for broad runtime changes.
4. Run `GO_BIN=/root/.local/go/bin/go ./scripts/verify-deps.sh` when `go.mod` or `go.sum` changes.
5. Rebuild locally with `go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness` when CLI, gateway, agent, provider, tool, TUI, or Telegram code changes.
6. For production deploys, use `scripts/deploy.sh` on hosts whose systemd units point at `bin/fast-agent-harness-current`; use `scripts/production-deploy.sh deploy --yes` for the older source-checkout deploy lane.
7. Restart `billyharness-gateway.service` and `billyharness-telegram.service` when deployed runtime behavior changes outside the deploy script.
8. After verification, move completed TODOs to `loop-develop/history` preserving
   their number.

Project health:

```bash
./bin/fast-agent-harness doctor
./bin/fast-agent-harness doctor -deep
./bin/fast-agent-harness doctor -json
```

`doctor` prints git status, a lightweight CLI build check, systemd service health,
gateway `/health` liveness and `/ready` readiness, current provider/model/reasoning
settings, provider capability validation, MCP allowlist availability, session
directory, and config paths. Use `-mode=production` on the production host to
include production-only unit and journal crash-signal checks.

For a non-failing local snapshot while editing, disable active checks:

```bash
./bin/fast-agent-harness doctor -build=false -services=false -gateway=false
```

Production runs on `root@82.23.163.16` under `/root/billyharness`. Competitor
research checkouts for clean-room comparison live under `/root/agent-research`.

## Quick start

```bash
/root/.local/go/bin/go test -count=1 ./...
/root/.local/go/bin/go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
setsid ./bin/fast-agent-harness > gateway.log 2>&1 < /dev/null &
./bin/fast-agent-harness tui
```

Windows local smoke path:

```powershell
go build -buildvcs=false -o .\bin\fast-agent-harness.exe .\cmd\fast-agent-harness
$env:BILLYHARNESS_HOME = "$HOME\billyharness"
.\bin\fast-agent-harness.exe gateway -mock -addr 127.0.0.1:8765 -dev-allow-unauthenticated-loopback-mutations
# or use the local PowerShell helper with the configured provider/settings:
.\dev.ps1
# or start a background real-provider gateway and open the TUI:
.\tui.ps1
# for a no-auth local smoke run:
.\dev.ps1 -Mock
```

Managed token persistence with UID, POSIX-mode, link, and process-lock checks
is currently enabled on macOS and Linux. On Windows, use an explicit
`BILLYHARNESS_GATEWAY_AUTH_TOKEN` in each gateway client process or the
documented loopback development bypass; the harness fails closed instead of
claiming POSIX-style file guarantees.

Running `bin/fast-agent-harness` with no subcommand starts the gateway. The gateway uses the model and
reasoning mode saved in `$BILLYHARNESS_HOME/settings.json` (`~/billyharness/settings.json` by default),
unless command-line flags or env vars override them. The TUI auto-discovers a local gateway from the same
config, so `-gateway` is only needed for a non-default remote gateway.

By default the gateway listens on `127.0.0.1:8765`. Mutating `/v1` routes
require a bearer token even on loopback. On the first normal macOS/Linux
launch, if no explicit token exists, the gateway generates a cryptographically
random token and stores it in the dedicated
`$BILLYHARNESS_HOME/auth/gateway.token` file (`auth` mode `0700`, file mode
`0600` on macOS/Linux). Billyharness gateway clients load that same file for
loopback requests, so separate local terminals do not need matching `export`
commands. `-auth-token` and `BILLYHARNESS_GATEWAY_AUTH_TOKEN` remain explicit
server/operator overrides; an override known only to the gateway process is not
automatically shared with another terminal. The
`-dev-allow-unauthenticated-loopback-mutations` flag is only for disposable
loopback development. First-time automatic provisioning is loopback-only. A
non-loopback deployment must already have the dedicated token or an explicit
environment override and must put bearer traffic behind HTTPS or an SSH tunnel.

```bash
./bin/fast-agent-harness gateway -addr 127.0.0.1:8765

# A second Billyharness process reads the generated token automatically.
./bin/fast-agent-harness tui -gateway http://127.0.0.1:8765
```

`/health` remains unauthenticated for cheap liveness. `/ready` returns bounded
readiness details for effective config, tool/MCP status, and session-store startup
health. The `run`, `chat`, `telegram`, and `jobs` gateway clients resolve the
shared token from the process environment or dedicated token file automatically
when calling a protected loopback gateway. The old home dotenv keys remain
migration fallbacks but project dotenv files cannot choose gateway transport
auth. Managed local-file credentials are never forwarded to a non-loopback URL;
remote clients must use an explicit process token. Non-Billyharness HTTP clients
must still attach the bearer explicitly.

## Durable multi-agent jobs

The gateway can run persisted jobs with one to four isolated workers, a
barrier/reducer, and a supervisor which either completes, blocks, waits for a
real external dependency, or schedules another bounded cycle. Model-requested
`wait` is distinct from the operator `pause` command. The gateway is the sole
durable owner: closing the TUI, losing SSH, or reopening `/jobs` does not cancel
a job. Stopping the gateway pauses execution until that same durable store is
started again; it does not turn TUI state into a second scheduler.

Jobs are gateway-wide operator resources, not chat- or profile-scoped records.
Every client holding the gateway bearer token can list and control every job on
that gateway; run separate gateways or credentials when operators need an
isolation boundary.

### Full-screen TUI control center

Start the gateway from the workspace whose files jobs may access, then connect
the TUI to it. With default configuration the gateway process's working
directory becomes its workspace root; an explicit config may narrow or replace
that value. The current jobs API does not expose configured workspace roots to
clients, so the creation wizard cannot discover or widen them. A requested
read/write root must still be inside the server authority.

```bash
BH=/absolute/path/to/bin/fast-agent-harness

# Terminal 1: with default config, this becomes the workspace boundary.
cd /absolute/path/to/workspace
"$BH" gateway -addr 127.0.0.1:8765 -job-concurrency 4

# Terminal 2:
"$BH" tui -gateway http://127.0.0.1:8765
```

Inside the TUI, type `/jobs` and press `Enter`. The keyboard-first path is:

1. The dashboard lists durable jobs; use `Up`/`Down` and `Enter` to open one.
2. The detail view shows canonical status, progress, budgets, the latest role
   attempts, artifacts, and the final result when available. Use the displayed
   state-valid controls to pause, resume, cancel, or refresh; `Esc` returns.
3. From the dashboard press `n` to open the creation wizard. It collects the
   goal, preset, provider/model, one to four workers, timing, minimum/maximum
   cycles, attempts/model-call/token budgets, and explicit tool, filesystem,
   network, and provider authority before creating the job.

The eight built-in presets are `general`, `research`, `coding`, `debug`,
`review`, `planning`, `writing`, and `compare`. “Four workers” means four
predeclared workflow roles may be dispatched in a parallel stage; it does not
override the gateway-wide provider semaphore. `gateway -job-concurrency`
defaults to `1` and caps simultaneous durable-job provider invocations across
all jobs, so set it deliberately when the provider plan and endpoint allow
more concurrency.

The wizard always offers the built-in DeepSeek, Qwen, Kimi, and Codex routes.
It also offers the one custom OpenAI-compatible binding resolved by this TUI
when that binding has an explicit base URL and model. The gateway still checks
the exact route against its own configuration. A TUI connected to a remote
gateway with a different custom binding cannot discover that binding through
the current jobs API; use matching configuration or a built-in route.

`duration` is a hard wall-clock cutoff, not a request to keep a model busy.
`min_cycles` forces complete worker/reducer/supervisor review cycles and is the
usual quality-depth control. `min_runtime` only delays successful completion by
wall clock; queued, paused, offline, and cadence time count, so it is not a
useful-compute guarantee. Attempts are loaded as a bounded recent tail in the
detail view; full canonical history remains in the gateway store. New jobs
persist their UTC admission time, so list/detail elapsed time survives TUI and
gateway restarts; legacy jobs created before that field show elapsed as
unavailable.

Ordinary chat may run in local TUI mode, but `/jobs` requires a reachable
gateway because a TUI process cannot durably own background execution. If the
selected route is the built-in Qwen Token Plan route, the wizard requires an
explicit unattended-use warning confirmation. Confirmation records operator
intent; it does not change the provider terms. Use an endpoint/plan which
actually permits automation.

The CLI remains available for scripting. Start the gateway with a process-wide
provider-call limit, then create a job against the provider/model in resolved
config:

Long unattended execution is allowed only on endpoints and plans whose terms
permit automation. In particular, the built-in `qwen` route currently targets
Qwen Token Plan Individual; its published terms permit interactive
programming/agent-tool use but prohibit automated scripts, application
backends, and non-interactive batch processing. Use a metered/custom endpoint
with suitable terms for unattended 6–24 hour jobs. The example below assumes
such an endpoint; the gateway and jobs client share their generated token file
automatically.

```bash
./bin/fast-agent-harness gateway -job-concurrency 2

./bin/fast-agent-harness jobs create \
  -preset research \
  -workers 4 \
  -duration 6h \
  -min-runtime 5h \
  -max-cycles 8 \
  -max-model-calls 128 \
  -max-tokens 1000000 \
  -tool fs_list \
  -tool fs_grep \
  -tool fs_read_file \
  -read-root /absolute/path/to/notes \
  'Repeatedly audit the supplied notes, update the forecast, seek disconfirming evidence, and report calibrated uncertainty.'

./bin/fast-agent-harness jobs list
./bin/fast-agent-harness jobs show JOB_ID
./bin/fast-agent-harness jobs pause JOB_ID
./bin/fast-agent-harness jobs resume JOB_ID
./bin/fast-agent-harness jobs cancel JOB_ID
```

Every `-read-root` and `-write-root` must also be contained by the gateway's
configured `workspace_roots`; job flags can narrow server authority but cannot
widen it. The process-wide `-job-concurrency` cap applies across all jobs.
Current durable FileStore execution is supported on Darwin and Linux; other
operating systems fail closed. The current wizard validates roots using the TUI
host's path grammar because the jobs API does not expose the gateway OS or its
canonical roots. Cross-OS remote control (for example, a Windows TUI targeting
a Linux gateway path) must use the CLI/API on a compatible host until a route
and authority capabilities endpoint exists.

The scheduler is provider-neutral, but route construction is deliberately
explicit: one daemon can select the built-in DeepSeek, Qwen, Kimi, Codex, and
Mock routes plus the daemon's one configured custom OpenAI-compatible binding.
An arbitrary provider name is not an endpoint registry entry, and no route
inherits another provider's URL or credential. Additional independent custom
endpoints require explicit provider-profile registry support.

`-duration` is a hard maximum, not a promise to consume that time.
`-min-runtime` is an admission-relative wall-clock earliest-success gate, not
a guarantee of useful compute: queued, paused, and gateway-offline time count.
When `-cadence` is omitted, the CLI derives the smallest interval that lets the
`-max-cycles` schedule span that gate. Durable timers and checkpoints survive
gateway restarts. Available presets are `general`, `research`, `coding`,
`debug`, `review`, `planning`,
`writing`, and `compare`. Authority is fail-closed: a job gets only the tools,
roots, network hosts, and provider explicitly granted at creation.
See the [durable jobs architecture and guarantees](docs/architecture/durable-multi-agent-jobs.md).

For SSH terminals with broken alt-screen or key handling:

```bash
stty -ixon
./bin/fast-agent-harness tui -plain
```

## TUI commands

Slash commands autocomplete in the composer with `Tab`, `Up`, and `Down`.

```text
/auth deepseek|codex
/auth status
/theme light|dark
/model flash|pro|qwen|kimi|gpt|spark|<model-id>
/reasoning low|medium|high|xhigh|max|off
/toolview auto|expanded|collapsed|hidden
/thinkview expanded|collapsed|hidden
/context
/jobs
/new
/resume [session-id-prefix]
/fork [session-id-prefix]
/status
/exit
```

Runtime settings and saved chats are stored under `~/billyharness` by default.
Use `BILLYHARNESS_HOME=/path/to/dir` to move that state elsewhere.

## Credentials

The gateway bearer is a local transport credential, separate from model
provider credentials. Normal macOS/Linux loopback startup without an explicit
override or development bypass creates
`$BILLYHARNESS_HOME/auth/gateway.token` automatically; Billyharness clients
using the same home read it for loopback requests without shell exports.
Billyharness deliberately does not forward that managed local token to a remote
gateway; set `BILLYHARNESS_GATEWAY_AUTH_TOKEN` in the remote client process
instead. Do not put the raw gateway token in `config.toml` or pass it in argv
unless an external deployment system explicitly manages that override.

The TUI credential menu is available through `/auth`. It has two setup actions:

- `/auth deepseek` prompts for a DeepSeek API key and stores it in the effective
  dotenv file: `FAST_AGENT_ENV_FILE` when set and allowed, otherwise
  `$BILLYHARNESS_HOME/.env`.
- `/auth codex` imports an existing Codex CLI ChatGPT/OAuth login into
  `$BILLYHARNESS_HOME/auth/codex.json`.
  On Windows, the import source is `%CODEX_HOME%\auth.json` when `CODEX_HOME`
  is set, otherwise `%USERPROFILE%\.codex\auth.json`; the default destination
  is `%USERPROFILE%\billyharness\auth\codex.json`.

Qwen Cloud Token Plan and Kimi Code credentials use their own environment
variables (process environment or the effective Billyharness dotenv file):

```bash
export QWEN_TOKEN_PLAN_API_KEY='sk-sp-...'
export KIMI_API_KEY='sk-kimi-...'
```

The model selects the official endpoint automatically:

```bash
FAST_AGENT_MODEL=qwen3.8-max-preview ./bin/fast-agent-harness
FAST_AGENT_MODEL=k3 ./bin/fast-agent-harness
```

Aliases `/model qwen` (also `/model qn`) and `/model kimi` select
`qwen3.8-max-preview` and `k3`.
Kimi also exposes `kimi-for-coding` and `kimi-for-coding-highspeed`. The Qwen
Token Plan key is distinct from Qwen pay-as-you-go and Coding Plan keys.
K3 uses its advertised maximum 1,048,576-token context in the model catalog;
Moderato plans are limited to 262,144, so set
`FAST_AGENT_CONTEXT_WINDOW_TOKENS=262144` on that tier.

Official references: [Qwen Token Plan quick start](https://docs.qwencloud.com/token-plan/personal/token-plan-personal-quickstart),
[Qwen OpenAI Chat API](https://docs.qwencloud.com/api-reference/chat/openai-chat),
and [Kimi Code models](https://www.kimi.com/code/docs/en/kimi-code/models.html).

When exposed through Telegram, `/auth` is owner-only and secret-bearing
`/auth deepseek ...` is accepted only in a private owner chat.

The same actions are exposed through the gateway API:

```bash
gateway_token="${BILLYHARNESS_GATEWAY_AUTH_TOKEN:-}"
if [ -z "$gateway_token" ]; then
  gateway_token="$(tr -d '\r\n' < "${BILLYHARNESS_HOME:-$HOME/billyharness}/auth/gateway.token")"
fi

curl -X POST http://127.0.0.1:8765/v1/auth/deepseek \
  -H "Authorization: Bearer $gateway_token" \
  -H 'Content-Type: application/json' \
  -d '{"api_key":"sk-..."}'

codex login
curl -X POST http://127.0.0.1:8765/v1/auth/codex/import \
  -H "Authorization: Bearer $gateway_token" \
  -H 'Content-Type: application/json' \
  -d '{}'

curl -H "Authorization: Bearer $gateway_token" \
  http://127.0.0.1:8765/v1/auth/status
unset gateway_token
```

Auth status responses show only metadata such as configured/missing, path, mode, account id, and expiry.
They do not return API keys, access tokens, or refresh tokens.
Prefer Billyharness clients for normal use; raw `curl` necessarily receives an
explicit header and may expose it briefly in local process inspection.

## MCP

Billyharness uses its own MCP config at `$BILLYHARNESS_HOME/mcp.config.toml`.
Default allowed servers are `telegram`, `telegram-parilka`, `github`, and `context7`.

The model-visible tool specs, including `/v1/tools`, are the stable gateway
tools: native tools plus `tool_search`, `mcp_list_tools`, and `mcp_call`.
Dynamic MCP tools are exposed lazily through `tool_search`/`mcp_list_tools`
and called through `mcp_call`, so large external inventories do not inflate
every model request.
Use `tool_search` with `query`, `server`, `namespace`, `risk`, and capped
`include_schema` when the model needs a specific native or MCP tool. Discovery
responses include `model_visible_tools.kind=static_gateway_tools` and
`mcp_catalog.kind=dynamic_mcp_catalog` to make the boundary explicit. MCP
descriptions, schemas, and initialize instructions are labeled as untrusted
server metadata.

MCP tool risk is local policy, not server self-attestation. Use
`default_tool_risk` or `tool_risks = { tool = "network_read" }` in
`$BILLYHARNESS_HOME/mcp.config.toml` to classify MCP tools as `local_read`,
`local_write`, `network_read`, `network_write`, `execute`,
`external_mutation`, or `secret_access`. Side-effecting MCP tools also require
their original MCP tool name in `enabled_tools` before `mcp_call` will run
them.

## Hooks

Local command hooks can be configured in `$BILLYHARNESS_HOME/hooks.config.toml`.
They are no-op by default and emit replayable `hook.started`, `hook.finished`, and `hook.failed` events.

## Skills

Skills live under `$BILLYHARNESS_HOME/skills/<name>/SKILL.md` or project `.billyharness/skills/<name>/SKILL.md`.
They are loaded on demand with `skill_list` and `skill_read`; `.claude/skills` compatibility input requires `include_compat=true`.

## Web tools and cache

`web_fetch`, `web_extract`, and `web_crawl` return compact summaries by default and store full extracted text in output refs.
Compact web outputs are cached under `$BILLYHARNESS_HOME/web-cache`; inspect or clear them with `web_cache_status` and `web_cache_clear`.

## Codex / GPT subscription mode

`/model gpt`, `/model gpt-5.5`, `/model gpt-5.4`, `/model gpt-5.4-mini`, and `/model spark`
route through the Codex-compatible ChatGPT backend provider.
The default `billy` profile sets `disable_spark = true`; set `disable_spark = false` in config/profile if you intentionally want Spark.

Use one of:

```bash
codex login
# then run `/auth codex` in the TUI or call POST /v1/auth/codex/import
```

or:

```bash
export CODEX_ACCESS_TOKEN=...
export CODEX_CHATGPT_ACCOUNT_ID=...
```

## AGENTS.md

Billyharness reads Codex-style instructions as a contextual user message:

- global: `$BILLYHARNESS_HOME/AGENTS.override.md`, then `$BILLYHARNESS_HOME/AGENTS.md`
- fallback global: `$CODEX_HOME/AGENTS.override.md`, then `$CODEX_HOME/AGENTS.md`
- project: `AGENTS.override.md`, then `AGENTS.md`, from project root to workspace directory

Project docs are capped by `FAST_AGENT_PROJECT_DOC_MAX_BYTES` and fallback filenames can be set with
`FAST_AGENT_PROJECT_DOC_FALLBACK_FILENAMES=CLAUDE.md,README.agent.md`.
