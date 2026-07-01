# Harness Research Goal Prompt

Copy-paste this into Codex as one `/goal` prompt.

```text
/goal Objective: Execute the billyharness harness-research implementation roadmap in priority order, starting with the reliability/admission/backpressure milestone and committing plus pushing each completed task.

Source of truth:
- /root/billyharness/docs/harness-research-execution-todo.md
- /root/billyharness/docs/competitive-improvements-todo.md
- /root/billyharness/docs/competitive-architecture-analysis.md
- /root/billyharness/docs/architecture-decomposition-todo.md
- /root/billyharness/docs/decomposition-next-todo.md

Focus:
Start with Milestone 1 in /root/billyharness/docs/harness-research-execution-todo.md. Do not start broad coding UX such as fs_edit_file, structured patch, managed shell, subagents, ask_user, project context registry, or session search until Milestone 1 is implemented and verified. After Milestone 1, continue through P0/P1 tasks in dependency order and defer P2 unless it is explicitly unblocked and justified by tests or benchmarks.

Execution loop:
1. Read all source files before starting and after any compaction/resume.
2. Convert open checklist items into an internal update_plan and keep exactly one item in progress.
3. Pick the highest-impact unblocked task from the earliest incomplete milestone.
4. Implement with small scoped edits, run focused verification, update /root/billyharness/docs/harness-research-execution-todo.md with status, evidence, commit hash placeholder, and any blocker.
5. After each task is completed or explicitly blocked, create one scoped git commit for that task and immediately push to the configured upstream. Do not batch unrelated tasks. Verify HEAD matches upstream after each push.
6. Before each commit, run git status and inspect staged diff. Commit only intentional files for the task plus required TODO/doc updates. Do not revert unrelated user changes.
7. If verification or push fails, stop that task, record the exact command/error in the TODO, and only continue if there is another independent unblocked task.

Constraints:
Keep JSONL as source of truth. Avoid job schedulers, mandatory SQLite/FTS/vector search, marketplace, enterprise policy, hidden user-git state, heavy UI rewrites, copied competitor code, shell-history scraping, .env value injection, and features outside the current milestone.

Verification:
Run focused package tests for each task. Before marking Milestone 1 complete, run:
go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/session ./internal/telegrambot ./internal/tui ./internal/eventlog ./internal/tools ./internal/agent ./internal/clientux/projector
go test -run 'Test.*Replay.*|Test.*Seq.*|Test.*Interrupt.*|Test.*Admission.*|Test.*InputInbox.*|Test.*Telegram.*(Admission|Offset).*|Test.*Slow.*Client.*|Test.*Backpressure.*|Test.*TUI.*(Batch|Reflow).*|Test.*FSRead.*|Test.*ToolSnapshot.*|Test.*TranscriptPairing.*|Test.*Golden.*Trace.*' -count=1 ./internal/...

Completion:
The active milestone is implemented, verified, reflected in /root/billyharness/docs/harness-research-execution-todo.md, and every completed/blocked task has its own pushed commit. The full goal is complete only when all unblocked P0/P1 tasks in the execution TODO are done or explicitly blocked with concrete reasons and next actions. Final response summarizes commit hashes, push status, changed files, tests run, and remaining blockers.
```

