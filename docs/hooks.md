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
- `before_tool`
- `after_tool`
- `session_done`

Reserved but not wired yet:

- `mcp_status_change`
- `provider_retry`

The hook process receives a JSON payload on stdin:

```json
{"event":"before_tool","hook":"audit","payload":{"tool_name":"fs_read_file","call_id":"call_1"}}
```

Billyharness also sets environment variables such as `BILLYHARNESS_HOOK_EVENT`, `BILLYHARNESS_HOOK_NAME`, `BILLYHARNESS_CALL_ID`, `BILLYHARNESS_ATTEMPT_ID`, and `BILLYHARNESS_TOOL_NAME` when those values exist.

## Runtime Behavior

Hook output is capped by `max_output_bytes`, redacted for common secrets, and emitted as replayable JSONL events:

- `hook.started`
- `hook.finished`
- `hook.failed`

Hook failures are reported but do not stop the run unless the hook has `fatal = true`.
