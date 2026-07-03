# Billyharness Project Instructions

## Project Scope

Billyharness is a fast solo-owner Go harness with a gateway, TUI, Telegram
adapter, JSONL replay, compact native tools, lazy MCP, and Codex/DeepSeek
provider paths. Keep it small, inspectable, and production-friendly.

Production runs on:

```sh
ssh root@82.23.163.16
cd /root/billyharness
```

Competitor research checkouts live outside the repo:

```sh
/root/agent-research/codex
/root/agent-research/opencode-current
/root/agent-research/claude-code
```

Use those only for clean-room comparison of architecture, contracts, tests, UX,
and capability gaps. Do not copy competitor source code into Billyharness.

## Native Subagents Only

When Billy asks to launch subagents, use only native Codex subagents through the
built-in multi-agent tools. Do not emulate subagents with external CLIs,
`codex exec`, `opencode`, `kimi`, shell wrappers, tmux sessions, or recreated
CLI-subagent skills.

For broad research, Billy's normal workflow is to ask for 12 subagents. Launch
12 native Codex research workers when he explicitly asks for that shape. Omit
`reasoning_effort` unless Billy asks for a reasoning level or the task clearly
needs one.

## Loop Development Workflow

All tactical TODO and goal-prompt work lives in `loop-develop`, not in `docs`.

Directory layout:

```text
loop-develop/
  current-todo/
  history/
```

Todo files use three-digit numbering and the fixed filename shape
`NNN-todo.md`, for example `001-todo.md`. Pick the next number by scanning both
`loop-develop/current-todo` and `loop-develop/history`; never reuse a number.

Keep at most one active implementation TODO in `loop-develop/current-todo`
unless Billy explicitly asks for parallel tracks. A current TODO should include:

- source research summary;
- concrete checklist;
- target files and architecture boundaries;
- verification commands;
- a copy-ready Codex `/goal` prompt.

The normal loop is:

1. Billy asks for research, often with 12 native Codex subagents.
2. Main chat consolidates findings into `loop-develop/current-todo/NNN-todo.md`
   with a goal prompt.
3. Billy starts a new chat and gives Codex the goal prompt.
4. The implementation chat works the TODO to completion.
5. Billy returns to the main chat and asks to verify whether everything is good.
6. After verification passes, move the completed TODO from
   `loop-develop/current-todo` to `loop-develop/history`, preserving its number.

When moving a TODO to history, append the final status, evidence, commands run,
commit/push state if relevant, and remaining blockers if any.

## Docs Policy

`docs/` is architecture-only. Keep durable architecture maps, clean-room
architecture research, and package-boundary documents there. Do not put active
TODOs, goal prompts, setup runbooks, feature notes, or temporary research logs
in `docs/`.

Current work belongs in `loop-develop/current-todo`; completed work belongs in
`loop-develop/history`.

## Verification

Before saying a loop item is done, inspect the real worktree, run focused tests
for touched packages, and run `git diff --check`. For runtime changes, also run
the relevant broader Go test set and rebuild the binary when CLI/gateway/TUI,
Telegram, provider, tool, or agent code changes.
