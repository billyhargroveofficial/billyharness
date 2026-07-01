# Diagnostics

Billy exposes command-based diagnostics through the `diagnostics_run` tool. The
tool runs only named commands from diagnostics settings or the built-in default;
it does not accept arbitrary argv from the model.

The built-in command is:

```toml
[diagnostics.commands.go-test]
command = "go"
args = ["test", "./..."]
timeout_sec = 120
max_output_bytes = 262144
max_issues = 100
max_issues_per_file = 20
```

To override or add commands, create `$BILLYHARNESS_HOME/diagnostics.config.toml`
or point `diagnostics_config_files` / `BILLYHARNESS_DIAGNOSTICS_CONFIG_FILES`
at one or more files:

```toml
[diagnostics.commands.unit]
command = "go"
args = ["test", "./internal/tools"]
timeout_sec = 60
max_output_bytes = 131072
max_issues = 80
max_issues_per_file = 10
```

`diagnostics_run` captures combined stdout/stderr, stores the raw output as an
`output_ref`, parses common `file:line:col` and `file(line,col)` diagnostics,
sorts errors before warnings, and returns a compact `<diagnostics>` block
inline.
