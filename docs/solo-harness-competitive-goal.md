# Solo Harness Competitive Goal Prompt

Copy-paste this into Codex as one `/goal` prompt.

```text
/goal Objective: Implement the next Billyharness solo-harness competitive roadmap: take the strongest reliability, context, tool-display, replay, memory, and interop patterns from Codex CLI, Claude Code, and OpenCode while rejecting platform bloat.

Workspace: /root/billyharness

Source of truth:
- /root/billyharness/docs/solo-harness-competitive-todo.md
- /root/billyharness/docs/architecture.md
- /root/billyharness/docs/harness-research-execution-todo.md
- /root/billyharness/docs/competitive-architecture-analysis.md
- /root/billyharness/docs/memory-systems-research.md

Focus:
Start with P0 only: SH-00 through SH-03. Do not start P1/P2 work such as memory MVP, import/export, MCP migration, command registry unification, selection polish, early tool execution, subagents, LSP, SQLite/FTS/vector index, or app-server/ACP adapters until all P0 tasks are implemented, verified, and reflected in the TODO.

Solo constraints:
Keep Billyharness a fast local solo binary. Keep JSONL/event replay as source of truth. Do not copy competitor source code. Do not add marketplace/cloud/enterprise policy, SaaS telemetry, hidden user-git state, React/Ink TUI rewrite, mandatory SQLite/vector DB, shell-history ingestion, .env value injection, headless browser extraction, or default auto-memory writes.

Execution loop:
1. Read every source file before starting and after compaction/resume.
2. Convert open P0 checklist items into update_plan and keep exactly one item in progress.
3. Pick the highest-impact unblocked P0 task in milestone order, implement it with small scoped edits, run focused verification, then update /root/billyharness/docs/solo-harness-competitive-todo.md with status, evidence, commit hash placeholder, split items, or blockers.
4. Preserve /root/billyharness/docs/architecture.md boundaries. If a task needs a new package or import edge, update architecture docs and guards in the same scoped change.
5. Before each commit, run git status and inspect staged diff. Commit only intentional files plus required TODO/doc updates. Push each completed or blocked task and verify HEAD matches upstream.
6. If tests or push fail, record exact command/error and next action in the TODO. Continue only if another independent P0 task is unblocked.

P0 required outcomes:
- Prompt section inventory and cache-break diagnostics.
- /context report v2 for CLI, TUI, and Telegram.
- Explicit provider/model capability policy and helper-model usage accounting.
- Shared toolrender display contract v2 for TUI and Telegram.
- Output-ref replay/resume audit for large web/shell/fs/MCP outputs.
- Stream liveness watchdog events for silent stalls.
- Managed-process dashboard polish.
- Canonical golden run bundle, adapter parity snapshots, and fake-provider regression suite.

Verification:
Run focused tests for touched packages. Before marking P0 complete, run:
go test -count=1 ./internal/agent ./internal/provider ./internal/config ./internal/modelinfo ./internal/tools ./internal/toolrender ./internal/tooloutput ./internal/clientux ./internal/clientux/projector ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot ./internal/trace ./internal/eventlog
go test -run 'Test.*Prompt.*|Test.*Cache.*Break.*|Test.*Context.*Report.*|Test.*Capability.*|Test.*Helper.*Usage.*|Test.*ToolRender.*|Test.*Output.*Ref.*|Test.*Replay.*|Test.*Resume.*|Test.*Liveness.*|Test.*Golden.*|Test.*Adapter.*Parity.*|Test.*Fake.*Provider.*' -count=1 ./internal/...

Completion:
P0 is complete only when all SH-00 through SH-03 tasks are checked off or explicitly blocked with concrete evidence, tests above pass or failures are documented, TODO status is current, and every completed/blocked task has its own pushed commit. The full goal is complete only when all unblocked P0/P1 items in the TODO are done or blocked with exact next actions.
```
