# Post-Competitive Hardening Goal Prompt

Copy-paste this into Codex as one `/goal` prompt.

```text
/goal Objective: Complete the Billyharness post-competitive hardening pass: clean roadmap/git state, decompose oversized files, make strict hygiene pass, verify architecture boundaries, run regressions, rebuild the binary, and only then touch deployed gateway/Telegram smoke checks.

Workspace: /root/billyharness

Source of truth:
- /root/billyharness/docs/post-competitive-hardening-todo.md
- /root/billyharness/docs/solo-harness-competitive-todo.md
- /root/billyharness/docs/architecture.md
- /root/billyharness/docs/harness-research-execution-todo.md

Focus:
Start with P0 only: PH-00 through PH-03. Do not start P1 deployed-service restart/smoke work or polish until P0 is green. Main P0 outcome: `go run ./cmd/fast-agent-harness hygiene -strict` passes without behavior regressions.

Current audit facts:
- `go test -count=1 ./internal/architecture` passed.
- strict hygiene fails on 6 files listed in the TODO.
- `docs/solo-harness-competitive-goal.md` is untracked.
- active solo roadmap has two `commit: pending` leftovers in final verification blocks.

Constraints:
Prefer decomposition, tests, docs, and smoke checks over features. Do not copy competitor code. Do not add framework layers, databases, schedulers, background agents, marketplace mechanics, enterprise policy, hidden git state, or UI rewrites. Keep JSONL/event replay as source of truth. Keep TUI/Telegram as protocol/projector/toolrender clients, not runtime owners.

Execution loop:
1. Read all source files before starting and after compaction/resume.
2. Convert open P0 checklist items into update_plan and keep exactly one item in progress.
3. Pick the highest-impact unblocked P0 task in order, implement scoped edits, run focused verification, then update /root/billyharness/docs/post-competitive-hardening-todo.md with status/evidence/commit/blocker notes.
4. Preserve /root/billyharness/docs/architecture.md import boundaries. If a split needs a new file/package/import edge, update docs/guards in the same change.
5. Before each commit, run git status and inspect staged diff. Commit only intentional files plus TODO/doc updates. Push each completed or blocked task and verify HEAD matches upstream.
6. If tests, build, hygiene, or push fail, record exact command/error and next action in the TODO. Continue only if another independent P0 task is unblocked.

Required P0 outcomes:
- Clean active roadmap/git hygiene.
- Split the 3 oversized source files and 3 oversized test files, or document temporary architecture exceptions.
- `go run ./cmd/fast-agent-harness hygiene -strict` passes.
- Architecture guard passes after decomposition.
- Focused package suites pass for gateway, TUI, agent, and touched packages.
- Broad `go test -count=1 ./...` is run after focused suites are green.
- `go build -o bin/fast-agent-harness ./cmd/fast-agent-harness` succeeds after runtime/CLI changes.
- CLI smoke checks are run and recorded.

Verification:
Run focused tests for touched packages. Before marking P0 complete, run:
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness hygiene -strict
go test -count=1 ./internal/agent ./internal/gateway ./internal/gatewayclient ./internal/eventlog ./internal/tui ./internal/tui/transcript ./internal/tui/render ./internal/tui/selection ./internal/tools ./internal/toolrender ./internal/telegrambot ./cmd/fast-agent-harness
go test -count=1 ./...
go build -o bin/fast-agent-harness ./cmd/fast-agent-harness

Completion:
P0 is complete only when PH-00 through PH-03 are checked off or explicitly blocked with evidence, tests/build/hygiene above pass or failures are documented, TODO status is current, and every completed/blocked task has its own pushed commit. The full goal is complete only when all unblocked P0/P1 tasks are done or blocked with exact next actions.
```
