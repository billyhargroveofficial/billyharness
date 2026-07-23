# billyharness

Fast Go agent harness with a gateway API, TUI chat, native tools, MCP server,
and DeepSeek, Qwen Cloud Token Plan, Codex, and mock provider paths.

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

By default the gateway listens on `127.0.0.1:8765`. If you bind it to a non-loopback address such as
`0.0.0.0:8765`, set a bearer token first:

```bash
export BILLYHARNESS_GATEWAY_AUTH_TOKEN='change-me'
./bin/fast-agent-harness gateway -addr 0.0.0.0:8765
curl -H "Authorization: Bearer $BILLYHARNESS_GATEWAY_AUTH_TOKEN" http://127.0.0.1:8765/v1/auth/status
```

`/health` remains unauthenticated for cheap liveness. `/ready` returns bounded
readiness details for effective config, tool/MCP status, and session-store startup
health. The `run`, `chat`, and `telegram` gateway clients read
`BILLYHARNESS_GATEWAY_AUTH_TOKEN` automatically when calling a protected gateway.

### Bounded gateway runs

`POST /v1/run` supports two rollout-safe, versioned execution contracts:

```json
{"prompt":"draft a bounded activity","access_mode":"bounded-automation-v1","max_tool_calls":12}
```

```json
{
  "prompt":"research one source",
  "access_mode":"bounded-isolated-plan-v1",
  "context_mode":"isolated",
  "allowed_tools":["web_fetch"],
  "allowed_url_prefixes":["https://example.com/news"],
  "max_tool_calls":4
}
```

Both contracts force provider retries to zero, disable provider failover, use
deterministic/extractive helpers, and reject missing or mismatched caps before
the provider is called. The cumulative tool-call cap is checked before a whole
parallel batch, so an overflowing batch executes no partial subset. The first
event is `run.started`; its data contains exactly `submission_id`, `run_id`,
`status`, `execution_contract`, `provider_max_retries`,
`provider_failover_enabled`, and `max_tool_calls`.

The isolated contract is one-shot only and cannot be used on an existing
session. It rejects ambient profile, attachment, provider, model, thinking,
memory, MCP, hook, cache, and helper-model authority. Its tool allowlist is
limited to `time_now`, `web_fetch`, `web_extract`, and `web_crawl` and must
include at least one web tool. Despite the historical
`allowed_url_prefixes` field name, entries are exact canonical HTTPS
origin/path allowlists; request query strings do not affect the match.

`isolated-plan-v1` remains accepted for rollout compatibility but does not
carry the new fixed cap attestation. Unknown versioned access modes fail
closed. An ordinary run may supply a positive `max_tool_calls` only to reduce
its otherwise unbounded cumulative tool-call allowance.

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
/model flash|pro|qwen|gpt|spark|<model-id>
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

### Qwen Cloud Token Plan

`qwen`, `qwen max`, and `qn max` normalize to the exact supported model
`qwen3.8-max-preview`. Selecting it derives provider `qwen`, the fixed official
Token Plan endpoint
`https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1`, and
the `QWEN_TOKEN_PLAN_API_KEY` credential name:

```bash
export FAST_AGENT_PROVIDER=qwen
export FAST_AGENT_MODEL=qwen3.8-max-preview
export QWEN_TOKEN_PLAN_API_KEY='sk-sp-...'
```

The key can also live in the effective Billyharness dotenv file. It is
reported only as configured/missing and is redacted from status, doctor,
gateway errors, and exports. There is no `/auth qwen` write route.

For this exact model, thinking is always enabled. Shared reasoning settings
map to Qwen's accepted `low`, `medium`, or `xhigh` values; an off/minimal
setting becomes `low`, while high/max becomes `xhigh`. Requests use
`max_completion_tokens`, preserve assistant `reasoning_content` verbatim
between tool rounds, enable parallel tool calls when the runtime permits them,
and scrub reasoning from returned/persisted transcripts unless
`store_reasoning=true`. The wire mapping follows Qwen Cloud's
[OpenAI-compatible Chat API](https://docs.qwencloud.com/api-reference/chat/openai-chat).

Qwen Cloud Token Plan usage must follow the provider's
[Token Plan terms](https://docs.qwencloud.com/token-plan/personal/token-plan-personal-overview);
do not use the subscription path for unattended background automation.
Production canaries are explicit operator actions.

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
