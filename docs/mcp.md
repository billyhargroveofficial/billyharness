# MCP

Billyharness owns its MCP config. It does not copy Codex, Claude, OpenCode, or other tool configs by default.

Default config path:

```sh
$BILLYHARNESS_HOME/mcp.config.toml
```

When `BILLYHARNESS_HOME` is not set, the harness uses its normal home directory. On this server that is `/root/billyharness`.

## Built-ins

The default generated config contains four curated servers:

```toml
[mcp_servers.telegram]
command = "telegram-mcp-hermes"

[mcp_servers.telegram-parilka]
command = "/root/telegram-parilka-mcp/bin/telegram-parilka-mcp"

[mcp_servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env_vars = ["GITHUB_PERSONAL_ACCESS_TOKEN"]

[mcp_servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]
env_vars = ["CONTEXT7_API_KEY"]
```

Native `web_search`, `web_fetch`, `web_extract`, and `web_crawl` are built into billyharness and should not be configured as MCP servers.

## Secrets

Use `env_vars` for secrets. Billyharness reads environment variables and `$BILLYHARNESS_HOME/.env`; it does not print secret values in MCP status.

```toml
[mcp_servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env_vars = ["GITHUB_PERSONAL_ACCESS_TOKEN"]
```

## Migration Diagnostics

Billyharness can inspect common Codex, Claude-style, OpenCode-style, and
workspace MCP config files and print redacted suggestions for
`$BILLYHARNESS_HOME/mcp.config.toml`.

```sh
fast-agent-harness config mcp-migrate
fast-agent-harness config mcp-migrate -file /path/to/external-mcp.json
fast-agent-harness config mcp-migrate -json
```

This command is read-only. It does not copy or overwrite configs, and it never
prints static environment or HTTP header values from external configs. When it
finds inline env values, it suggests `env_vars` names instead; put the actual
values in the process environment or `$BILLYHARNESS_HOME/.env`.

## Status

Use `/mcp` in TUI or Telegram, or call the gateway:

```sh
curl -fsS http://127.0.0.1:8765/v1/mcp
```

Status shows config files, allowlist, native tools, server state, transport, command or URL, tool count, last error, restart count, retry count, reconnect backoff, and next retry time.

States include `connected`, `reconnected`, `failed`, `crashed`, `restarting`, `disabled`, `disconnected`, and `unsupported`.

## Tool Discovery

MCP tools are discovered lazily. The model-visible specs are the static gateway
tools from `/v1/tools`: native tools plus `tool_search`, `mcp_list_tools`, and
`mcp_call`. Connected MCP tools are not injected as direct model-visible specs.
They live in the dynamic MCP catalog and are called through `mcp_call`.

Use `tool_search` to find native and MCP tools with compact call hints:

```json
{"query":"repositories","server":"github","namespace":"mcp.github","risk":"external","include_schema":true,"max_schema_tokens":1200}
```

Use `mcp_list_tools` when you only want the dynamic MCP catalog. Both
`tool_search` and `mcp_list_tools` responses include
`model_visible_tools.kind=static_gateway_tools` and
`mcp_catalog.kind=dynamic_mcp_catalog`; MCP catalog entries report
`model_visible=false` and include the full `mcp__server__tool` name to pass to
`mcp_call`.

Filters include `server`, `namespace`, `risk`, `query`, and `include_schema`.
Schema output is capped by `max_schema_tokens`; over-budget schemas are omitted
with `schema_omitted` instead of returning broken partial JSON. Responses
include discovery metrics such as scanned native/MCP tools, returned matches,
included schema count, omitted schema count, and estimated schema tokens.

## Remote MCP

Stdio MCP is supported today. Streamable HTTP MCP config is parsed and surfaced as `unsupported` instead of silently failing.

```toml
[mcp_servers.remote]
url = "https://example.com/mcp"
```

The structured status includes `unsupported_reason`, and the human `/mcp` view prints the same reason. Bearer/OAuth remote MCP is intentionally left for a later slice.
