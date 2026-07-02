# Solo Harness Next Hardening Goal

Use this as a compact `/goal` prompt. The long checklist lives in
`docs/solo-harness-next-hardening-todo.md` so the goal stays resumable after
context compaction.

```text
/goal Objective: Execute the Billyharness solo-harness next hardening roadmap: make run interruption/recovery, context/compaction/cache safety, and Telegram/TUI streaming correctness production-grade without adding platform bloat.

Source of truth:
- /root/billyharness/docs/solo-harness-next-hardening-todo.md
- /root/billyharness/docs/architecture.md
- /root/billyharness/docs/post-competitive-hardening-todo.md
- /root/billyharness/docs/solo-harness-competitive-todo.md

Focus:
Start with P0 Milestone 1 in /root/billyharness/docs/solo-harness-next-hardening-todo.md, then P0 Milestone 2, then P0 Milestone 3. Do not start P1/P2 work until all unblocked P0 items are implemented, verified, and reflected in the TODO. Preserve the solo-harness filter: JSONL remains source of truth; avoid schedulers, mandatory databases, cloud/team/platform features, plugin marketplaces, hidden git state, copied competitor code, and broad UI rewrites.

Execution loop:
1. Read all source files before starting and after any compaction/resume.
2. Convert open checklist items into an internal update_plan and keep exactly one item in progress.
3. Pick the highest-impact unblocked task from the earliest incomplete P0 milestone.
4. Implement with small scoped edits, run focused tests, update /root/billyharness/docs/solo-harness-next-hardening-todo.md with status, evidence, commit hash placeholder, split items, or blockers.
5. Before each commit, run git status and inspect the staged diff. Commit only intentional files for the task plus required TODO/doc updates, then push to the configured upstream.
6. If verification or push fails, record the exact command/error and next action in the TODO before moving on. Continue only if another independent unblocked task exists.

Required P0 outcomes:
- Interrupt replacement works during long tools and never leaks canceled context into the replacement run.
- Incomplete JSONL tails are repaired by appended recovery events, not by mutating history.
- Live stream gaps are explicit and clients recover by replaying durable events.
- Partial assistant output is preserved across cancel/crash without merging into later answers.
- Run/session terminal states are explicit and consumed by TUI/Telegram.
- Compaction is reversible, epoch-aware, and raw history remains recoverable.
- Cache breaks are diagnosed by model/tool/context/profile/prompt-section causes.
- Web/extract/crawl summaries run in a bounded helper lane with separate usage and output refs.
- Context reserve policy prevents provider hard-limit overflows.
- Telegram/TUI render state is run-id guarded, stale tools are impossible, edits are throttled/coalesced, typing reflects real liveness, and final rich messages are idempotent.

Verification:
Run focused package tests for each touched area. Before marking P0 complete, run:
/root/.local/go/bin/go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/session ./internal/telegrambot ./internal/tui ./internal/eventlog ./internal/tools ./internal/agent ./internal/clientux/projector ./internal/runstate ./internal/provider ./internal/webtools
/root/.local/go/bin/go test -run 'Test.*Replay.*|Test.*Seq.*|Test.*Interrupt.*|Test.*Admission.*|Test.*InputInbox.*|Test.*Telegram.*|Test.*Slow.*Client.*|Test.*Backpressure.*|Test.*TUI.*|Test.*Compaction.*|Test.*Cache.*|Test.*Web.*Summary.*|Test.*ToolSnapshot.*|Test.*TranscriptPairing.*|Test.*Golden.*Trace.*|Test.*Crash.*Repair.*|Test.*Stream.*Gap.*' -count=1 ./internal/...
/root/.local/go/bin/go test -count=1 ./...
/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict -repo /root/billyharness

Completion:
All unblocked P0 items in /root/billyharness/docs/solo-harness-next-hardening-todo.md are implemented, verified, committed, pushed, and marked completed with evidence. P1 items are completed, split into a later roadmap, or blocked with concrete reasons. Final response summarizes commit hashes, push status, changed files, tests run, TODO updates, and remaining blockers.
```
