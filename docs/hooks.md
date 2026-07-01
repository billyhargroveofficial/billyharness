# Hooks

Billyharness hooks are local command hooks. They are no-op by default: if no hook config exists, nothing runs.

Default config path:

```sh
$BILLYHARNESS_HOME/hooks.config.toml
```

You can also set explicit files with `BILLYHARNESS_HOOKS_CONFIG_FILES` or `FAST_AGENT_HOOKS_CONFIG_FILES`.

## Config

Use one table per hook:

```toml
[hooks.before_tool.audit]
command = "sh"
args = ["-c", "jq . >/tmp/billyharness-before-tool.json"]
timeout_sec = 2
max_output_bytes = 4096
fatal = false
env = { STATIC_VALUE = "example" }
env_vars = ["PATH"]
cwd = "/root/billyharness"
```

Supported hook events today:

- `session_start`
- `user_prompt_submit`
- `before_tool`
- `after_tool`
- `mcp_status_change`
- `provider_retry`
- `session_done`

The hook process receives a JSON payload on stdin:

```json
{"event":"before_tool","hook":"audit","payload":{"tool_name":"fs_read_file","call_id":"call_1"}}
```

`user_prompt_submit` runs after `session_start` and before the first model call
for a submitted prompt. JSON stdout may return:

```json
{"decision":"block","reason":"missing ticket id"}
```

or bounded prompt context/mutation:

```json
{"additional_context":"Use package-local tests.", "updated_prompt":"Run the focused tests."}
```

Non-JSON stdout from `user_prompt_submit` is treated only as bounded additional
model-visible context. It cannot block or rewrite the prompt.

`mcp_status_change` runs once with a `snapshot` phase for each known MCP server at run start, then again with a `change` phase when the MCP manager observes a state transition such as `connected`, `failed`, `crashed`, `restarting`, `reconnected`, `disconnected`, or `unsupported`. Its payload includes `server_name`, `transport`, `connected`, `state`, `tool_count`, retry/restart counters, and redacted error fields when present.

Billyharness also sets environment variables such as `BILLYHARNESS_HOOK_EVENT`, `BILLYHARNESS_HOOK_NAME`, `BILLYHARNESS_CALL_ID`, `BILLYHARNESS_ATTEMPT_ID`, and `BILLYHARNESS_TOOL_NAME` when those values exist.

## Runtime Behavior

Hook output is capped by `max_output_bytes`, redacted for common secrets, and emitted as replayable JSONL events:

- `hook.started`
- `hook.finished`
- `hook.failed`

Hook failures are reported but do not stop the run unless the hook has `fatal = true`.
