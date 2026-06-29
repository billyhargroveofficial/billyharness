# billyharness

Fast Go agent harness with a gateway API, TUI chat, native tools, MCP server, and benchmark runner.

## Docs

- [Master implementation TODO](docs/master-implementation-todo.md) is the live execution plan.
- [Codex research roadmap](docs/codex-research-roadmap.md) records the architecture research and rationale.
- [Setup](docs/setup.md) covers build, gateway, TUI, Telegram, systemd, logs, and failure checks.
- [Auth](docs/auth.md) covers DeepSeek keys, Codex OAuth import, config inspect, and redaction.
- [TUI](docs/tui.md) covers commands, themes, model/reasoning, tool/thinking views, copy, and sessions.
- [Telegram](docs/telegram.md) covers bot setup, allowlist, commands, per-user sessions, throttling, and tool view.
- [MCP](docs/mcp.md) covers billyharness-owned MCP config, built-ins, discovery, and troubleshooting.
- [Profiles](docs/profiles.md) covers `profile.toml`, `SOUL.md`, switching, and inspection.
- [Benchmarks](docs/benchmarks.md) covers local loops, Terminal-Bench adapter, replay verification, and provider comparisons.

Work protocol for runtime changes:

1. Map the change to a checkbox in the master TODO.
2. Add or update focused tests.
3. Run the relevant package tests, then `go test -count=1 ./...` for broad runtime changes.
4. Run `GO_BIN=/root/.local/go/bin/go ./scripts/verify-deps.sh` when `go.mod` or `go.sum` changes.
5. Rebuild `bin/fast-agent-harness` when CLI, gateway, agent, provider, tool, TUI, or Telegram code changes.
6. Restart `billyharness-gateway.service` and `billyharness-telegram.service` when deployed runtime behavior changes.
7. Commit and push coherent verified slices.

Project health:

```bash
./bin/fast-agent-harness doctor
./bin/fast-agent-harness doctor -json
```

`doctor` prints git status, a lightweight CLI build check, systemd service health, gateway `/health`,
current provider/model/reasoning settings, session directory, and config paths.

For a non-failing local snapshot while editing, disable active checks:

```bash
./bin/fast-agent-harness doctor -build=false -services=false -gateway=false
```

## Quick start

```bash
go test ./...
go build -buildvcs=false -o bin/fast-agent-harness ./cmd/fast-agent-harness
setsid ./bin/fast-agent-harness > gateway.log 2>&1 < /dev/null &
./bin/fast-agent-harness tui
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

`/health` remains unauthenticated for readiness checks. The `run`, `chat`, and `telegram` gateway clients
read `BILLYHARNESS_GATEWAY_AUTH_TOKEN` automatically when calling a protected gateway.

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
/model flash|pro|gpt|spark|<model-id>
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

- `/auth deepseek` prompts for a DeepSeek API key and stores it in `$BILLYHARNESS_HOME/.env`.
- `/auth codex` imports an existing Codex CLI ChatGPT/OAuth login into
  `$BILLYHARNESS_HOME/auth/codex.json`.

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
`mcp_catalog.kind=dynamic_mcp_catalog` to make the boundary explicit.

## Hooks

Local command hooks can be configured in `$BILLYHARNESS_HOME/hooks.config.toml`.
They are no-op by default and emit replayable `hook.started`, `hook.finished`, and `hook.failed` events.
See [docs/hooks.md](docs/hooks.md) for the config format and supported events.

## Skills

Skills live under `$BILLYHARNESS_HOME/skills/<name>/SKILL.md` or project `.billyharness/skills/<name>/SKILL.md`.
They are loaded on demand with `skill_list` and `skill_read`; `.claude/skills` compatibility input requires `include_compat=true`.

## Web tools and cache

`web_fetch`, `web_extract`, and `web_crawl` return compact summaries by default and store full extracted text in output refs.
Compact web outputs are cached under `$BILLYHARNESS_HOME/web-cache`; inspect or clear them with `web_cache_status` and `web_cache_clear`.
See [docs/web.md](docs/web.md).

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
