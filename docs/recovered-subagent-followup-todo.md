# Recovered Subagent Follow-Up TODO

Date: 2026-07-02
Status: source-of-truth follow-up for the incomplete Codex subagent traces
recovered from `/root/.codex/sessions/2026/07/02`.

This TODO is intentionally smaller than
`docs/solo-harness-next-hardening-todo.md`. Its job is to rescue the useful
work from subagents that did not reach `task_complete`, then turn that into
bounded fixes without derailing the main hardening roadmap.

## Source Inputs

- Main roadmap: `/root/billyharness/docs/solo-harness-next-hardening-todo.md`
- Architecture map: `/root/billyharness/docs/architecture.md`
- Completed subagent reports already folded into the main roadmap:
  - Bacon: reliability/replay/cancel/backpressure.
  - Boole: Telegram streaming UX.
  - Sagan: context/compaction/memory.
  - Parfit: MCP lifecycle/config.
  - Turing: solo safety/permissions.
  - Hume: observability/debug bundles.
  - Faraday: benchmarks/regression.
- Incomplete subagent rollout files to reconcile:
  - Averroes tools/edit/shell/contracts:
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-46-35-019f23b9-9fb5-7ad1-9886-da7ae97942c2.jsonl`
  - Mill architecture/decomposition:
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-52-23-019f23be-eff3-77c0-91c3-280095cf2be4.jsonl`
  - Erdos TUI/terminal UX:
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-52-35-019f23bf-1f0b-7a82-86dc-427f02045389.jsonl`
  - Schrodinger web/search/extract:
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-53-55-019f23c0-559b-7fe0-b873-4bd036c10d33.jsonl`
  - Carver solo product/roadmap:
    `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-54-42-019f23c1-0c0e-71f0-8d73-7db47599e1cc.jsonl`

## What This Will Fix

- Tool call contract drift: invalid JSON args, weak error recovery, unclear
  shell/edit/file contracts, and inconsistent compact tool display.
- Decomposition drift: the codebase has grown after previous hygiene notes, so
  new line-count/import pressure must be measured before more features land.
- TUI regressions: terminal UX should reuse existing Bubble Tea/Lip Gloss,
  transcript export, `/toolview`, `/copy`, command registry, and selection
  primitives instead of duplicating shipped behavior.
- Web/search/extract quality: coverage exists for security and output refs, but
  product-level behavior still needs tests for query options, evidence/citation
  shape, markdown readability, and multi-backend failover.
- Product coherence: the harness needs one solo-owner roadmap that rejects
  marketplace/platform bloat while still taking the strongest Codex/OpenCode/
  Claude/Hermes patterns.

## Milestone 0 - Recover Or Close Incomplete Research (P0)

- [x] RS-00.1 Extract all useful text from incomplete Codex rollout files.
  - acceptance: for each incomplete file, extract the last meaningful
    `agent_message`, list any URLs/files it touched, and record whether it has
    enough evidence to become implementation work.
  - suggested command:
    `jq -s -r '[.[] | select(.type=="event_msg" and .payload.type=="agent_message") | .payload.message // empty] | last // ""' <rollout.jsonl>`
  - recovery log, 2026-07-02:
    - command used for each trace:
      `jq -s -r '[.[] | select(.type=="event_msg" and .payload.type=="agent_message") | .payload.message // empty] | last // ""' <rollout.jsonl>`
    - Averroes
      `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-46-35-019f23b9-9fb5-7ad1-9886-da7ae97942c2.jsonl`:
      last meaningful message says web search output was sparse and the agent
      was switching to direct `curl` reads of official docs and GitHub raw
      files. Earlier useful messages identify concrete evidence and patterns:
      local paths `/root/billyharness`, `/root/research/openai-codex`, local
      tool/tool-output/transcript/access-mode packages; public queries for
      Codex apply-patch/sandbox/docs, Claude Code tool permissions and tool
      UI separation, OpenCode tool permissions/output storage/session
      projector, Aider edit formats/repo map, Cline/Roo/Continue tool docs.
      Recovered finding is actionable: Billyharness already has typed
      lifecycle events, compact render metadata, output refs, and access modes,
      so the bounded work is contract hardening rather than greenfield tool
      redesign.
    - Mill
      `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-52-23-019f23be-eff3-77c0-91c3-280095cf2be4.jsonl`:
      last meaningful message says strict hygiene and architecture guard both
      passed, but tracked Go files had grown from the older docs' 286 to 315.
      Earlier messages identify pressure areas: `gateway`, `tui`, `tools`,
      `telegrambot`, `agent`, `bench`, and `config`; the guard catches direct
      import violations but broad allowed lists can still hide owner pressure.
    - Erdos
      `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-52-35-019f23bf-1f0b-7a82-86dc-427f02045389.jsonl`:
      last meaningful message says existing TUI primitives include Bubble
      Tea/Lip Gloss, `/toolview`, `/copy`, transcript export, a client UX
      action registry, and `internal/tui`; useful upstream query topics were
      Claude Code terminal shortcuts/selection/copy/status/tool display and
      OpenCode keybinds/TUI details. Actionable finding: TUI work should reuse
      those shipped primitives and add regression coverage rather than
      duplicate command, copy, transcript, or tool-view logic.
    - Schrodinger
      `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-53-55-019f23c0-559b-7fe0-b873-4bd036c10d33.jsonl`:
      last meaningful message says existing web-tool tests already cover DNS
      rebinding, redirects to private hosts, provider auth mapping, retries,
      compact outputs, output refs, model-summary accounting, and cache
      invalidation. Earlier messages identify implementation shape: DuckDuckGo
      Lite native search, local fetch, optional Tavily/Exa search/extract,
      cache keys with summarizer settings, strong public-host validation, and
      weaker product behavior around provider freshness/options,
      citation/evidence metadata, markdown readability, and backend failover.
    - Carver
      `/root/.codex/sessions/2026/07/02/rollout-2026-07-02T18-54-42-019f23c1-0c0e-71f0-8d73-7db47599e1cc.jsonl`:
      only useful text says it was gathering public docs and using the
      `openai-docs` skill for Codex-specific product/roadmap claims. Search
      queries covered Codex, Claude Code, OpenCode, Aider, Cline, Continue, and
      Hermes product surfaces, but no final findings survived.
    - rollout metadata: all five traces contain only `agent_message`,
      `task_started`, `token_count`, `user_message`, and, for Averroes/Erdos/
      Carver, `web_search_end` query metadata. No completed final reports or
      direct edit/test outputs were present.
  - mapped to: RS-01.1, RS-01.2, RS-02.1, RS-02.2, RS-03.1, RS-03.2,
    RS-04.1, RS-04.2, and NH-00.1.
  - commit: pending.
  - status: completed.

- [x] RS-00.2 Decide whether to rerun missing agents.
  - acceptance: if a trace only says "working" and has no actionable evidence,
    either rerun that research with a tight scope or mark it closed with no
    additional action.
  - decision, 2026-07-02:
    - do not rerun Averroes, Mill, Erdos, or Schrodinger broadly; their partial
      traces contain enough scoped evidence to become the RS implementation
      items below.
    - do not rerun Carver; the trace contains no final product findings beyond
      the already documented solo-harness filter, and rerunning broad
      competitor/product research would risk platform/marketplace bloat outside
      this follow-up's scope.
  - commit: pending.
  - status: completed.

- [x] RS-00.3 Map recovered findings into either this TODO or the main roadmap.
  - acceptance: every useful recovered finding points at a concrete RS or NH
    item; no "interesting research" remains floating only in chat history.
  - mapping, 2026-07-02:
    - Averroes accepted into RS-01.1/RS-01.2 for malformed tool-call recovery,
      stable mutating-tool contracts, compact display metadata, bounded output
      refs, and replay-safe results.
    - Mill accepted into RS-02.1/RS-02.2 for fresh package/file/import
      measurement before any decomposition, with splits allowed only for real
      owner-boundary pressure.
    - Erdos accepted into RS-03.1/RS-03.2 for a TUI primitive audit and
      regression tests around noisy tool events, gateway/SSH input, selection,
      slash commands, and compact tool rendering.
    - Schrodinger accepted into RS-04.1/RS-04.2 for product-level web/search/
      extract tests and a deterministic backend failover policy.
    - Carver closed with no additional RS item; the surviving trace adds no
      concrete finding beyond the main roadmap's solo harness filter and
      platform-bloat rejection.
    - NH-00.1 in the main hardening roadmap is updated as completed so the
      reconciliation source of truth points back here instead of leaving the
      evidence in Codex logs.
  - commit: pending.
  - status: completed.

## Milestone 1 - Tool Contracts, Edit, And Shell Recovery (P0)

- [ ] RS-01.1 Audit tool schemas and argument validation.
  - target areas: `internal/protocol`, `internal/tools`, `internal/agent`.
  - acceptance: malformed tool-call JSON, unknown fields, wrong types, missing
    required fields, and provider-specific function-call quirks return compact,
    actionable errors that the agent can recover from.
  - verification: focused malformed-tool-call tests, including the Telegram
    observed `shell_exec had invalid JSON args` failure.
  - status: open.

- [ ] RS-01.2 Harden mutating tool contracts.
  - target areas: `fs_write_file`, `fs_edit_file`, `fs_make_dir`,
    `shell_exec`, managed processes, checkpoint/preview if touched.
  - acceptance: write/edit/shell tools expose stable preview/result metadata,
    compact display labels, bounded output refs, and safe retry semantics.
  - verification: tool contract tests plus replay/projector tests for compact
    tool summaries.
  - status: open.

## Milestone 2 - Architecture And Decomposition Pressure (P0)

- [ ] RS-02.1 Capture fresh package/file-size/import baseline.
  - acceptance: record current Go file count, largest files, largest packages,
    and architecture guard status after the recent work.
  - verification:
    `/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict -repo /root/billyharness`
    and `/root/.local/go/bin/go test -count=1 ./internal/architecture`.
  - status: open.

- [ ] RS-02.2 Split only if a real owner boundary is found.
  - acceptance: any split updates `docs/architecture.md` when package
    responsibility changes; line-count-only churn is rejected.
  - status: open.

## Milestone 3 - TUI/Terminal UX Regression Pass (P1)

- [ ] RS-03.1 Audit existing TUI primitives before adding UI.
  - acceptance: document which existing primitives own tool views, copy,
    command registry, transcript export, selection, statusline, and markdown
    rendering, then avoid duplicate logic.
  - status: open.

- [ ] RS-03.2 Add regression tests for noisy tool events and SSH input.
  - acceptance: active runs do not dump raw tool event rows unless requested;
    keyboard input, mouse selection, slash command menus, and compact tool
    rendering behave in gateway mode.
  - status: open.

## Milestone 4 - Web/Search/Extract Quality And Failover (P1)

- [ ] RS-04.1 Add product-level web tool tests.
  - acceptance: tests cover provider query options, citation/evidence shape,
    markdown readability, table/list/code fallback, and summary/output-ref
    behavior.
  - status: open.

- [ ] RS-04.2 Define backend failover policy.
  - acceptance: native web, Tavily, Exa, and provider-backed summaries have a
    deterministic priority, timeout, budget, and error-reporting policy.
  - status: open.

## Milestone 5 - Solo Product Coherence (P1)

- [ ] RS-05.1 Consolidate recovered fixes into the main hardening roadmap.
  - acceptance: the main roadmap gets only the fixes that passed the solo
    harness filter; marketplace/platform/team features stay rejected.
  - status: open.

- [ ] RS-05.2 Close this follow-up.
  - acceptance: all RS items are completed, blocked with exact reason, or
    moved into `solo-harness-next-hardening-todo.md` with a concrete target.
  - status: open.

## Verification Gate

Run focused package tests for each touched area. Before closing this follow-up,
run at minimum:

```sh
/root/.local/go/bin/go test -count=1 ./internal/tools ./internal/agent ./internal/protocol ./internal/tui ./internal/telegrambot ./internal/webtools ./internal/provider ./internal/architecture
/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict -repo /root/billyharness
```

If runtime behavior changes, also run:

```sh
/root/.local/go/bin/go test -count=1 ./...
```

## Completion Criteria

This follow-up is complete when every incomplete subagent trace is reconciled,
all accepted findings are implemented or moved into the main roadmap, tests are
recorded, and no useful research exists only in Codex chat logs.
