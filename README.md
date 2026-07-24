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

Running `bin/fast-agent-harness` with no subcommand starts the gateway. The gateway uses the model and
reasoning mode saved in `$BILLYHARNESS_HOME/settings.json` (`~/billyharness/settings.json` by default),
unless command-line flags or env vars override them. The TUI auto-discovers a local gateway from the same
config, so `-gateway` is only needed for a non-default remote gateway.

By default the gateway listens on `127.0.0.1:8765`. Mutating `/v1` routes
require a bearer token even on loopback. Set one for normal use; the explicit
`-dev-allow-unauthenticated-loopback-mutations` flag is only for disposable
local development. A token is mandatory when binding a non-loopback address
such as `0.0.0.0:8765`:

```bash
export BILLYHARNESS_GATEWAY_AUTH_TOKEN='change-me'
./bin/fast-agent-harness gateway -addr 0.0.0.0:8765
curl -H "Authorization: Bearer $BILLYHARNESS_GATEWAY_AUTH_TOKEN" http://127.0.0.1:8765/v1/auth/status
```

`/health` remains unauthenticated for cheap liveness. `/ready` returns bounded
readiness details for effective config, tool/MCP status, and session-store startup
health. The `run`, `chat`, `telegram`, and `jobs` gateway clients read
`BILLYHARNESS_GATEWAY_AUTH_TOKEN` automatically when calling a protected gateway.

## Durable multi-agent jobs

The gateway can run persisted jobs with one to four isolated workers, a
barrier/reducer, and a supervisor which either completes, blocks, waits for a
real external dependency, or schedules another bounded cycle. Model-requested
`wait` is distinct from the operator `pause` command. Start the gateway with a
process-wide provider-call limit, then create a job against the provider/model
in resolved config:

Long unattended execution is allowed only on endpoints and plans whose terms
permit automation. In particular, the built-in `qwen` route currently targets
Qwen Token Plan Individual; its published terms permit interactive
programming/agent-tool use but prohibit automated scripts, application
backends, and non-interactive batch processing. Use a metered/custom endpoint
with suitable terms for unattended 6–24 hour jobs. The example below assumes
such an endpoint and an explicit gateway bearer token.

```bash
export BILLYHARNESS_GATEWAY_AUTH_TOKEN='change-me'
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
operating systems fail closed.

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
/new
/resume [session-id-prefix]
/fork [session-id-prefix]
/status
/exit
```

Runtime settings and saved chats are stored under `~/billyharness` by default.
Use `BILLYHARNESS_HOME=/path/to/dir` to move that state elsewhere.

## Credentials

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
curl -X POST http://127.0.0.1:8765/v1/auth/deepseek \
  -H 'Content-Type: application/json' \
  -d '{"api_key":"sk-..."}'

codex login
curl -X POST http://127.0.0.1:8765/v1/auth/codex/import \
  -H 'Content-Type: application/json' \
  -d '{}'

curl http://127.0.0.1:8765/v1/auth/status
```

Auth status responses show only metadata such as configured/missing, path, mode, account id, and expiry.
They do not return API keys, access tokens, or refresh tokens.

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
