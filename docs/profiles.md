# Profiles

Profiles combine provider/model defaults with instruction fragments. The default profile is `billy`.

A profile lives under:

```text
$BILLYHARNESS_HOME/profiles/<profile>/
```

Common files:

```text
profile.toml
SOUL.md
```

`SOUL.md` is the main user instruction body for that profile. `profile.toml` stores structured defaults.

## Example

```toml
name = "billy"
provider = "deepseek"
model = "deepseek-v4-flash"
reasoning_effort = "high"
# Optional: set context_window_tokens only for a deliberate, visible override.
# Omit it to use the selected model's metadata.
web_summary_mode = "extractive"
tool_policy = "solo-full-access"
mcp_allowlist = ["telegram", "telegram-parilka", "github", "context7"]
instruction_fragments = ["SOUL.md"]
cost_budget_hints = ["prefer summaries over raw web output"]
disable_spark = true
```

## Switching

TUI:

```text
/profile billy
```

Telegram:

```text
/profile billy
```

CLI/gateway startup:

```sh
./bin/fast-agent-harness gateway -profile billy
./bin/fast-agent-harness telegram -profile billy
```

Profile switches update provider/model/reasoning/instructions consistently for the next run. Sessions record the profile hash so traces can explain which instruction set was active.

## Context Window Overrides

Profiles may set `context_window_tokens` when a profile intentionally needs a
context budget different from the selected model metadata. Billyharness labels
that value as an explicit profile override in `config inspect` and `doctor`,
including the model-derived default beside it.

Omit `context_window_tokens` for normal profiles. Legacy profile values of
`1000000` on Codex/OAuth models are treated as stale compatibility data and
re-derived from `modelinfo` rather than silently expanding the runtime window.

At run start, Billyharness assembles the prompt prefix in this order: built-in
system prompt, selected profile `SOUL.md`, bounded project context, then
AGENTS/project instructions. The project context is metadata only: roots,
package-manager hints, likely commands, instruction file hashes/byte counts,
and `.env*` variable names without values.

## Inspect

```sh
./bin/fast-agent-harness config inspect
./bin/fast-agent-harness config inspect -json
```

The inspect command shows whether values came from the selected profile, home
config, project config, environment variables, or runtime overrides. It also
prints a provenance line for provider, model, `context_window_tokens`, compact
threshold, helper model, and web backend.
