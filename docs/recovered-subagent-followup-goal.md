# Recovered Subagent Follow-Up Goal

Use this prompt when the goal is to recover and act on the incomplete Codex
subagent traces before continuing the broader hardening roadmap.

```text
/goal Objective: Recover the incomplete Billyharness Codex subagent research traces and turn the useful findings into bounded fixes or explicit roadmap items.

Source of truth:
- /root/billyharness/docs/recovered-subagent-followup-todo.md
- /root/billyharness/docs/solo-harness-next-hardening-todo.md
- /root/billyharness/docs/architecture.md

Focus:
Start with Milestone 0 in /root/billyharness/docs/recovered-subagent-followup-todo.md. Reconcile the incomplete Averroes, Mill, Erdos, Schrodinger, and Carver rollout files listed there before implementing broader work. After Milestone 0, proceed in order through tool contracts, architecture/decomposition pressure, TUI regressions, web/search/extract quality, and solo product coherence. Do not add platform, marketplace, team, cloud, or broad framework features.

Execution loop:
1. Read all source files before starting and after any compaction/resume.
2. Extract useful text from the listed rollout JSONL files with jq, then update the TODO with what was recovered, rerun, accepted, or closed.
3. Convert open TODOs into an internal update_plan and keep exactly one item in progress.
4. Pick the highest-impact unblocked item from the earliest incomplete milestone, implement scoped edits, run focused verification, and update the TODO with status, evidence, commit hash placeholder, split items, or blockers.
5. Before each commit, run git status and inspect the staged diff. Commit only intentional files plus required TODO/doc updates, then push.
6. If verification or push fails, record the exact command/error and next action in the TODO before moving to another independent task.

Required outcomes:
- All incomplete subagent traces are reconciled and no useful research remains only in Codex chat logs.
- Tool-call JSON/schema failures produce compact recoverable errors.
- Mutating tools have stable contracts, compact display metadata, bounded output refs, and replay-safe results.
- Fresh architecture/file-size/import pressure is measured before any decomposition.
- TUI work reuses existing transcript, command registry, copy, selection, and toolview primitives.
- Web/search/extract behavior has product-level tests for query options, citations/evidence, readability, summaries, and backend failover.
- Accepted findings are folded into /root/billyharness/docs/solo-harness-next-hardening-todo.md or explicitly closed.

Verification:
Run focused package tests for touched areas. Before completion, run:
/root/.local/go/bin/go test -count=1 ./internal/tools ./internal/agent ./internal/protocol ./internal/tui ./internal/telegrambot ./internal/webtools ./internal/provider ./internal/architecture
/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict -repo /root/billyharness
If runtime behavior changes, also run /root/.local/go/bin/go test -count=1 ./...

Completion:
Every RS item in /root/billyharness/docs/recovered-subagent-followup-todo.md is completed, blocked with exact reason/next action, or moved into the main hardening roadmap with a concrete target. Final response summarizes commits, push status, files changed, tests run, and remaining blockers.
```
