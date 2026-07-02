/goal Objective: Implement billyharness vision input for Codex/OpenAI OAuth, Hermes-compatible local skills, and Tavily/Exa web backends without bloating the solo harness.

Source of truth:
- /root/billyharness/docs/vision-skills-search-backends-todo.md
- /root/billyharness/docs/architecture.md

Focus:
Start with P0 in /root/billyharness/docs/vision-skills-search-backends-todo.md: protocol/model capability foundation, attachments, Codex provider image serialization, gateway/session/admission, TUI vision MVP, and Telegram vision MVP. Do not start P1 skills or Tavily/Exa implementation until P0 is implemented, tested, and reflected in the TODO. Do not start P2 polish unless all P0/P1 work is done or explicitly blocked.

Execution loop:
1. Read both source files before starting and after any compaction/resume.
2. Convert open checklist items from the earliest incomplete milestone into an internal update_plan and keep exactly one item in progress.
3. Pick the highest-impact unblocked task from that milestone, implement with small scoped edits, run focused tests, and update /root/billyharness/docs/vision-skills-search-backends-todo.md with status/evidence/blockers.
4. Preserve architecture boundaries. If a new package is added, update /root/billyharness/docs/architecture.md and run the architecture guard before continuing.
5. Continue autonomously until P0/P1 are done or the same blocker repeats for three goal turns.

Constraints:
Keep JSONL as source of truth. Store attachment refs/metadata, not raw image bytes or base64. Do not expose Tavily/Exa key values. Do not add marketplaces, remote skill stores, SQLite/FTS/vector indexes, shell-history scraping, enterprise policy layers, copied competitor code, or provider-specific model-facing tools. Keep model-facing web tools as web_search/web_fetch/web_extract/web_crawl. DeepSeek is text-only: reject or clearly fallback for image input.

Verification:
Run focused package tests for every touched area. Before P0 is marked done, run:
go test -count=1 ./internal/architecture ./internal/protocol ./internal/modelinfo ./internal/attachments ./internal/provider ./internal/gatewayapi ./internal/gatewayclient ./internal/gateway ./internal/session ./internal/tui ./internal/tui/runtimeclient ./internal/telegrambot
Before P1 is marked done, run:
go test -count=1 ./internal/architecture ./internal/skills ./internal/tools ./internal/webtools ./internal/config ./internal/secrets ./internal/provider ./internal/tui ./internal/telegrambot
Before full completion, run:
go test -count=1 ./internal/...
go test -run 'Test.*(Vision|Image|Attachment|Tavily|Exa|Skill|Hermes|Capability|Redact|Replay|Admission|Telegram|TUI).*' -count=1 ./internal/...

Completion:
Codex/OpenAI OAuth accepts image input from TUI and Telegram; DeepSeek handles images explicitly as unsupported/text fallback; attachments replay safely without raw bytes in JSONL; Hermes-style skills list/view works with caps; Tavily/Exa back normalized web_search/web_extract when configured; native web remains default; TODO and architecture docs reflect the final state; required tests pass or exact blockers are documented.
