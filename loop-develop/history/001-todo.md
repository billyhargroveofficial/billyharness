# 001 TODO - Project Loop Structure And Docs Cleanup

Status: completed
Created: 2026-07-02
Completed: 2026-07-02

## Request

Create project-scope AGENTS rules for Billy's loop-development workflow, add
`loop-develop/current-todo` and `loop-develop/history`, enforce numbered TODO
files, and reduce `docs/` to architecture-only documents.

## Result

- Added root `AGENTS.md` with project scope, native-subagent rules, the 12-agent
  research workflow, TODO numbering, current/history movement rules, and docs
  policy.
- Added `loop-develop/README.md`, `loop-develop/current-todo`, and
  `loop-develop/history`.
- Moved the completed setup task into this history file as `001-todo.md`.
- Updated root docs navigation to point at architecture docs and loop-develop.
- Removed non-architecture docs from `docs/`.

## Goal Prompt

```text
/goal Objective: Apply the Billyharness project-loop structure: persist the native-subagent research workflow in AGENTS.md, create loop-develop/current-todo and loop-develop/history, enforce NNN-todo.md numbering, and keep docs architecture-only.

Workspace: /root/billyharness

Acceptance:
- AGENTS.md documents production host, clean-room research checkouts, native Codex subagents only, the 12-subagent research pattern, current-todo/history movement, NNN-todo.md numbering, and architecture-only docs policy.
- loop-develop/current-todo and loop-develop/history exist.
- docs/ contains only architecture documents and its index reflects that.
- README points to architecture docs and loop-develop instead of old tactical docs.
- Focused checks pass or failures are recorded.
```

## Verification

- `rg` check found no broken references to removed docs outside the preserved
  architecture docs and `docs/architecture.md` guard tests.
- `git diff --check && git diff --cached --check`
- `go test -count=1 ./internal/config ./internal/architecture ./cmd/fast-agent-harness`
- `go test -count=1 ./internal/agent -run TestInitialMessagesInjectProfileAsSystemContext`

Broad test note: `go test -count=1 ./...` was attempted after the cleanup. It
still fails outside this docs/layout slice in existing test areas:
`internal/agent` turn-change file count, `internal/attachments` macOS temp
symlink handling, `internal/tui` attachment path handling, and a
`internal/telegrambot` temp cleanup race.

No commit was created in this setup pass.
