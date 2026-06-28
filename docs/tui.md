# TUI

The TUI is the main local chat interface. In normal server setup it auto-discovers the local gateway, so this is enough:

```sh
cd /root/billyharness
./bin/fast-agent-harness tui
```

For awkward SSH terminals:

```sh
stty -ixon
./bin/fast-agent-harness tui -plain
```

`-plain` disables the richer terminal assumptions. It does not change model behavior.

## Commands

Slash commands open an autocomplete popup. Use `Tab`, `Up`, and `Down` to move through command or argument choices.

```text
/auth deepseek|codex
/auth status
/theme light|dark
/model flash|pro|gpt|spark|<model-id>
/reasoning low|medium|high|xhigh|max|off
/profile PROFILE
/toolview auto|expanded|collapsed|current|errors|hidden
/thinkview expanded|collapsed|hidden
/copy selected|last|tool|transcript|code|command
/context
/status
/new
/resume [session-id-prefix]
/fork [session-id-prefix]
/mcp
/exit
```

## Themes

Use:

```text
/theme light
/theme dark
```

The theme setting is saved under `$BILLYHARNESS_HOME/settings.json`.

## Model And Reasoning

Common model aliases:

```text
/model flash
/model pro
/model gpt
/model gpt-5.5
```

Reasoning:

```text
/reasoning low
/reasoning medium
/reasoning high
/reasoning xhigh
/reasoning max
/reasoning off
```

The footer shows active model, reasoning, access mode, active context tokens/percent, session turn/tool totals, web summary metrics, cache metrics where meaningful, cost/subscription marker, theme, and chat/profile.

## Tool And Thinking Views

Tool calls are collapsed by default. Use:

```text
/toolview collapsed
/toolview current
/toolview expanded
/toolview errors
/toolview hidden
```

Thinking blocks can be switched with:

```text
/thinkview collapsed
/thinkview expanded
/thinkview hidden
```

The default compact tool line shows status, tool name, file/url/query/server/command summary, duration, truncation, output refs, and cache or token metadata when available.

`/toolview current` keeps only the latest turn's tool cells visible. In collapsed/current/auto views, repeated context-gathering calls such as file reads, searches, web fetches, and read-only MCP lookups are grouped into a compact "Context tools" summary so a long evidence-gathering run stays readable. Switch to `/toolview expanded` when you need the full individual tool outputs.

## Copy And Selection

Mouse selection copies selected terminal text. Semantic copy commands avoid gutters, ANSI, UI chrome, and decorative wrappers:

```text
/copy selected
/copy last
/copy tool
/copy transcript
/copy code
/copy command
```

Use semantic copy when you want clean text from an assistant answer, a tool output, a code block, or the full transcript.

## Chats

`/new` starts a new gateway session. `/resume` lists or resumes saved chats by id prefix. `/fork` clones the current or selected chat into a new session.

Gateway sessions are JSONL-backed, so the session can be inspected outside the TUI:

```sh
./bin/fast-agent-harness sessions list
./bin/fast-agent-harness sessions inspect SESSION_ID
```
