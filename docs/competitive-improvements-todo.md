# Competitive Improvements TODO

This roadmap turns the competitive architecture analysis into bounded work for
Billyharness. Each item is scoped to solo-harness value and explicitly filters
out marketplace, enterprise, SaaS, and platform bloat.

Priority guide:

- `P0`: reliability or token/latency bug class that can corrupt runs, waste large
  context, or break Telegram/gateway usage.
- `P1`: high leverage UX, maintainability, or correctness improvements with low
  bloat risk.
- `P2`: useful after P0/P1 invariants exist, or only if benchmarks justify it.

## P0

### 1. Durable Gateway Follow Loop

- priority: P0
- source inspiration: OpenCode durable wake-loop, Codex lossless/best-effort
  stream split
- billyharness target files: `internal/gateway/gateway.go`,
  `internal/gateway/session_events.go`, `internal/gatewayclient/client.go`,
  `internal/clientux/projector/projector.go`, `internal/eventlog/eventlog.go`
- expected benefit: replay after reconnect becomes lossless and duplicate-free;
  slow live clients cannot silently miss durable events.
- bloat risk: low if JSONL remains source of truth and live hub becomes wake
  signal for persisted session events.
- acceptance criteria: follow mode subscribes before replay, repeatedly replays
  `seq > cursor` from store until caught up, then waits for wake/context;
  client can detect seq gaps; duplicate terminal lifecycle events are rejected.
- verification commands:
  - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/eventlog ./internal/clientux/projector`
  - `go test -run 'Test.*Replay.*|Test.*Seq.*|Test.*Lifecycle.*' -count=1 ./internal/gateway ./internal/eventlog`
  - `go test -run '^$' -bench 'BenchmarkGatewaySessionJSONL(Append|Replay)' -benchmem ./internal/gateway`

### 2. Gateway-Level Interrupt Policy

- priority: P0
- source inspiration: Codex active-turn interrupt, OpenCode run coordinator,
  Claude remote control cleanup
- billyharness target files: `internal/gatewayapi/types.go`,
  `internal/gateway/gateway.go`, `internal/session/session.go`,
  `internal/gateway/session_events.go`, `internal/telegrambot/runner.go`,
  `internal/telegrambot/render.go`, `internal/gatewayclient/client.go`
- expected benefit: new Telegram messages can reliably replace old runs without
  `ErrBusy`, and stale tool rows are cleared at the gateway boundary.
- bloat risk: low; add `InterruptPolicy=interrupt` and `CancelAndWait`, not a
  full queueing/workflow engine.
- acceptance criteria: one active run per session; interrupt cancels old run,
  emits terminal run/tool cleanup, waits briefly for idle, then starts the new
  run or fails clearly; Telegram still ignores late old-run events.
- verification commands:
  - `go test -count=1 ./internal/gateway ./internal/session ./internal/telegrambot ./internal/gatewayclient`
  - `go test -run 'Test.*Interrupt.*|Test.*CancelAndWait.*|Test.*Stale.*Tool.*' -count=1 ./internal/gateway ./internal/session ./internal/telegrambot`

### 3. Hard Inline Budgets For Web And Large Tool Outputs

- priority: P0
- source inspiration: Claude microcompact/output refs, OpenCode bounded web
  fetch, Codex external context/output-token policy
- billyharness target files: `internal/tools/web_core.go`,
  `internal/tools/web_handlers.go`, `internal/tools/tools.go`,
  `internal/agent/agent.go`, `internal/agent/compaction.go`,
  `internal/clientux/context.go`
- expected benefit: a single `web_fetch` or crawl cannot inflate the active
  transcript to hundreds of thousands of tokens.
- bloat risk: low; enforce budgets and refs instead of adding browser or DB
  systems.
- acceptance criteria: default `web_fetch`, `web_extract`, and `web_crawl`
  inline JSON stays below configured token/char budget; full text is stored via
  `output_ref`; user `include_text/full_text/max_tokens` cannot bypass the cap.
- verification commands:
  - `go test -count=1 ./internal/tools ./internal/agent ./internal/clientux`
  - `go test -run 'Test.*Web.*Budget.*|Test.*OutputRef.*|Test.*Context.*Tool.*' -count=1 ./internal/tools ./internal/agent ./internal/clientux`
  - `go test -run '^$' -bench 'Benchmark(CompactFetchedPage|CompactCrawl)' -benchmem ./internal/tools`

### 4. Turn-Level Tool Snapshot And Transcript Pairing

- priority: P0
- source inspiration: Codex request-scoped tool context, OpenCode snapshot-tool
  race tests, Claude tool-result pairing validation
- billyharness target files: `internal/tools/tools.go`,
  `internal/agent/runtime_loop.go`, `internal/agent/model_call.go`,
  `internal/agent/tool_attempt.go`, `internal/agent/transcript_pairing.go`,
  `internal/runstate/runstate.go`
- expected benefit: model-visible tools and executable tools cannot drift during
  a turn; malformed transcripts are caught before provider calls.
- bloat risk: low if implemented as a small `ToolSet` snapshot and validator.
- acceptance criteria: tool specs, handlers, parallel metadata, and MCP catalog
  are frozen per provider turn; new/removed tools are visible only on the next
  turn; no orphan tool results or missing tool results before the next assistant
  message.
- verification commands:
  - `go test -count=1 ./internal/agent ./internal/tools ./internal/mcpclient`
  - `go test -run 'Test.*ToolSnapshot.*|Test.*TranscriptPairing.*|Test.*ToolResult.*' -count=1 ./internal/agent ./internal/tools`

### 5. Canonical End-To-End Event Trace

- priority: P0
- source inspiration: Codex snapshot/golden TUI tests, OpenCode replay/fake
  provider tests
- billyharness target files: `internal/testkit/events.go`,
  `internal/testkit/testdata/traces/agent_loop_full.jsonl`,
  `internal/trace/trace_test.go`,
  `internal/clientux/projector/projector_test.go`,
  `internal/tui/tui_test.go`,
  `internal/telegrambot/render_test.go`
- expected benefit: one fixture proves event contracts across storage, replay,
  projection, TUI, and Telegram.
- bloat risk: medium if too many golden snapshots are added; keep one canonical
  trace plus focused package tests.
- acceptance criteria: fixture includes reasoning/content deltas, parallel
  tools, web output ref, MCP call, usage, context threshold, compaction, and
  terminal completion; all clients project it without duplicated usage or raw
  payload leaks.
- verification commands:
  - `go test -count=1 ./internal/testkit ./internal/trace ./internal/clientux/projector ./internal/tui ./internal/telegrambot`
  - `go test -run 'Test.*Golden.*Trace.*' -count=1 ./internal/...`

### 6. Provider Binding Denylist And Auth-Source Discipline

- priority: P0
- source inspiration: Codex provider/auth boundary and project config denylist,
  OpenCode resolved route, Claude auth-source discipline
- billyharness target files: `internal/config/resolved.go`,
  `internal/config/projections.go`, `internal/config/diagnostics.go`,
  `internal/provider/provider.go`, `internal/credentials/credentials.go`,
  `internal/provider/codex_auth.go`, `internal/codexauth/codexauth.go`,
  `internal/modelinfo/modelinfo.go`
- expected benefit: project-local config cannot redirect secrets or make the
  wrong provider read the wrong credential source.
- bloat risk: low; no provider marketplace or generic registry.
- acceptance criteria: project config cannot override base URLs, credential
  files, Codex auth files, refresh/auth URLs, or client IDs; Codex and DeepSeek
  credential paths are isolated; explicit provider/model conflicts produce the
  chosen warning/error behavior.
- verification commands:
  - `go test -count=1 ./internal/config ./internal/provider ./internal/credentials ./internal/codexauth ./internal/modelinfo`
  - `go test -run 'Test.*Denylist.*|Test.*ProviderBinding.*|Test.*Credentials.*' -count=1 ./internal/config ./internal/provider ./internal/credentials`

## P1

### 7. Shared ToolCompact Display Contract

- priority: P1
- source inspiration: Codex model-output versus UI-cell split, OpenCode tool
  state/output paths, Claude compact tool summaries
- billyharness target files: `internal/protocol/types.go`,
  `internal/toolrender/toolrender.go`, `internal/tooloutput/tooloutput.go`,
  `internal/agent/tool_attempt.go`, `internal/clientux/projector/projector.go`,
  `internal/telegrambot/render.go`, `internal/tui/transcript/projector.go`
- expected benefit: TUI and Telegram render the same bounded tool state without
  duplicating string heuristics or leaking raw JSON args.
- bloat risk: medium if display contract grows UI flags; keep identity,
  lifecycle, title/detail/summary, category/verb/target, error, ref, metrics,
  and hints only.
- acceptance criteria: compact display is optional, markup-free, deterministic,
  single-line bounded, no raw JSON fallback; result/progress/output-ref events
  update one `CallID`.
- verification commands:
  - `go test -count=1 ./internal/toolrender ./internal/tooloutput ./internal/agent ./internal/clientux/projector ./internal/telegrambot ./internal/tui/transcript`
  - `go test -run 'Test.*ToolCompact.*|Test.*NoRaw.*JSON.*|Test.*OutputRef.*' -count=1 ./internal/...`

### 8. MCP Catalog Change Lifecycle

- priority: P1
- source inspiration: OpenCode and Claude full relist on `tools/list_changed`,
  Codex lazy MCP exposure
- billyharness target files: `internal/mcpclient/jsonrpc.go`,
  `internal/mcpclient/stdio.go`, `internal/mcpclient/server.go`,
  `internal/mcpclient/manager.go`, `internal/tools/tools.go`,
  `internal/tools/discovery/discovery.go`, `docs/mcp.md`
- expected benefit: live MCP tool changes, crashes, and reconnects do not leave
  stale tools visible in the gateway.
- bloat risk: low if limited to stdio tool catalog lifecycle; do not add remote
  OAuth/prompts/resources/plugins.
- acceptance criteria: manager owns raw catalog; registry cache syncs from
  manager; `tools/list_changed` triggers full relist and atomic swap; close or
  crash removes active specs; removed tools become unknown until rediscovered.
- verification commands:
  - `go test -count=1 ./internal/mcpclient ./internal/tools ./internal/config`
  - `go test -run 'Test.*MCP.*ListChanged.*|Test.*MCP.*Reconnect.*|Test.*Stale.*Tool.*' -count=1 ./internal/mcpclient ./internal/tools`

### 9. TUI Raw/Rich, Clipboard, Markdown, Palette

- priority: P1
- source inspiration: Codex raw/rich transcript and clipboard, OpenCode command
  palette, Claude semantic copy/selection
- billyharness target files: `internal/tui/tui.go`,
  `internal/tui/actions.go`, `internal/tui/settings.go`,
  `internal/tui/render/markdown.go`, `internal/tui/render/status.go`,
  `internal/tui/selection/selection.go`, `internal/tui/transcript/*`
- expected benefit: better copy/debug UX and less markdown/streaming flicker
  without adopting a heavy TUI framework.
- bloat risk: low if implemented in current renderer and selection packages.
- acceptance criteria: raw transcript mode has no chrome/ANSI; OSC52/tmux/SSH
  clipboard handles UTF-8 and caps; fenced markdown/table streaming is stable;
  palette hides disabled actions and shows keybindings.
- verification commands:
  - `go test -count=1 ./internal/tui ./internal/tui/...`
  - `go test -run 'Test.*Clipboard.*|Test.*Raw.*Transcript.*|Test.*Markdown.*|Test.*Palette.*' -count=1 ./internal/tui/...`
  - `go test -run '^$' -bench 'Benchmark(TUI|RenderAssistantMarkdown|Selection)' -benchmem ./internal/tui/...`

### 10. Structured Cached Web Search And Tokenizer Extraction

- priority: P1
- source inspiration: OpenCode browserless bounded fetch, Claude URL cache and
  redirect metadata, Codex explicit web tool contract
- billyharness target files: `internal/webtools/client.go`,
  `internal/webtools/summary.go`, `internal/tools/web_core.go`,
  `internal/tools/web_handlers.go`, `internal/tools/webcache.go`,
  `internal/tools/tools.go`
- expected benefit: faster and clearer free web search/fetch with bounded
  outputs, no headless browser, and fewer malformed HTML extraction failures.
- bloat risk: low to medium if using a small tokenizer dependency; reject
  Playwright/headless browser.
- acceptance criteria: capped reader never buffers more than max+1; search
  output includes provider/query/count/timing/cache/no-results metadata; result
  URLs pass public validation; extraction skips active/hidden content and caps
  links.
- verification commands:
  - `go test -count=1 ./internal/webtools ./internal/tools`
  - `go test -run 'Test.*Capped.*|Test.*WebSearch.*|Test.*HTML.*Extract.*|Test.*Crawl.*' -count=1 ./internal/webtools ./internal/tools`
  - `go test -run '^$' -bench 'Benchmark(FetchPage|Extract|CompactFetchedPage|WebSearch)' -benchmem ./internal/tools ./internal/webtools`

### 11. Token-Based Compaction Tail And Context Diagnostics

- priority: P1
- source inspiration: Codex body-after-prefix accounting, OpenCode token-budget
  tail, Claude microcompact/context analyzer
- billyharness target files: `internal/agent/compaction.go`,
  `internal/agent/context_threshold.go`, `internal/clientux/context.go`,
  `docs/context.md`
- expected benefit: recent tail cannot preserve a huge tool result just because
  it is within the last N messages; context UI identifies raw inline bloat.
- bloat risk: low if limited to counters and deterministic pruning markers.
- acceptance criteria: compaction keeps last user turns within tail token cap;
  tool-call/result adjacency is preserved; context diagnostics show protected
  prefix, body tokens, largest inline tool outputs, and raw web text flags.
- verification commands:
  - `go test -count=1 ./internal/agent ./internal/clientux`
  - `go test -run 'Test.*Compaction.*Tail.*|Test.*Context.*Diagnostics.*|Test.*Tool.*Adjacency.*' -count=1 ./internal/agent ./internal/clientux`

### 12. Local Manual Memory MVP

- priority: P1
- source inspiration: Claude file-based memory taxonomy, Codex memory summary
  plus topic files, OpenCode context source separation
- billyharness target files: `internal/memory/*`,
  `internal/instructions/instructions.go`, `internal/config/profile.go`,
  `internal/agent/compaction.go`, `internal/clientux/context.go`,
  `docs/memory.md`, `docs/profiles.md`, `docs/context.md`
- expected benefit: persistent solo hints without bloating `SOUL.md` or
  `AGENTS.md`; prompt growth stays bounded by summary-only injection.
- bloat risk: medium if auto-extraction/vector DB/background jobs appear; MVP
  must be local files and manual commands only.
- acceptance criteria: memory disabled means no prompt message; enabled memory
  injects capped `memory_summary.md` after project instructions; search/read are
  bounded; topic files validate frontmatter type; writes happen only by explicit
  command.
- verification commands:
  - `go test -count=1 ./internal/memory ./internal/instructions ./internal/config ./internal/agent ./internal/clientux`
  - `go test -run 'Test.*Memory.*|Test.*Instructions.*Memory.*|Test.*Compaction.*Memory.*' -count=1 ./internal/...`

### 13. Structured Provider Retry Events And Stream Collector Cleanup

- priority: P1
- source inspiration: OpenCode visible retry status, Claude retry-yield status,
  Codex bounded provider retry layer
- billyharness target files: `internal/provider/retry.go`,
  `internal/provider/provider.go`, `internal/agent/model_call.go`,
  `internal/protocol/types.go`, `internal/agent/tool_attempt.go`
- expected benefit: retry delays become visible/cancellable and stream
  collection allocates less under high delta volume.
- bloat risk: low; one typed event and a small collector cleanup.
- acceptance criteria: retry callback emits attempt/delay/reason/request id
  before sleep; cancel during retry sleep exits fast; default tool attempts do
  not emit fake retry phases when retries are disabled; content/reasoning stream
  collection uses builders.
- verification commands:
  - `go test -count=1 ./internal/provider ./internal/agent`
  - `go test -run 'Test.*Retry.*Scheduled.*|Test.*NoToolRetryDecision.*' -count=1 ./internal/provider ./internal/agent`
  - `go test -run '^$' -bench 'BenchmarkCollectModelCallStream|BenchmarkParse.*SSE' -benchmem ./internal/provider ./internal/agent`

### 14. Project Hygiene And Focused File Splits

- priority: P1
- source inspiration: anti-bloat audit from all three systems
- billyharness target files: `.gitignore`, `cmd/fast-agent-harness/hygiene.go`,
  `internal/tui/tui.go`, `internal/tui/*`, `internal/tools/tools.go`,
  `internal/tools/*`, `docs/architecture.md`
- expected benefit: source review is not polluted by runtime artifacts, and
  large files shrink by ownership without creating new frameworks.
- bloat risk: low if splits are mechanical and preserve package boundaries.
- acceptance criteria: hygiene reports no missing tracked files or unignored
  runtime artifacts; TUI split preserves gateway seq dedupe, secret redaction,
  status rendering, resume/fork behavior; tools split preserves one registry and
  central output bounding.
- verification commands:
  - `go test -count=1 ./internal/architecture ./internal/tui ./internal/tools`
  - `go run ./cmd/fast-agent-harness hygiene -repo /root/billyharness`
  - `go test -run '^$' -bench 'BenchmarkTUI|BenchmarkTool' -benchmem ./internal/tui ./internal/tools`

## P2

### 15. Early Tool Execution For Codex Responses

- priority: P2
- source inspiration: Codex starts tools on completed output items, Claude
  streaming tool executor, OpenCode native runtime ordering tests
- billyharness target files: `internal/provider/provider.go`,
  `internal/provider/codex_provider.go`, `internal/agent/model_call.go`,
  `internal/agent/runtime_loop.go`, `internal/agent/tool_attempt.go`
- expected benefit: lower latency when model emits a complete tool call before
  `response.completed`.
- bloat risk: medium; event ordering and transcript pairing must already be
  enforced before this lands.
- acceptance criteria: complete tool-call provider event can start a tool before
  model stream closes; out-of-order tool completion is drained and appended in
  model call order; no next model call starts before all in-flight tools finish.
- verification commands:
  - `go test -count=1 ./internal/provider ./internal/agent`
  - `go test -run 'Test.*EarlyTool.*|Test.*PreserveModelOrder.*' -count=1 ./internal/provider ./internal/agent`
  - `go test -run '^$' -bench 'Benchmark.*EarlyTool|BenchmarkParallelBatch' -benchmem ./internal/agent`

### 16. Input-Aware Tool Parallelism And Interrupt Behavior

- priority: P2
- source inspiration: Codex parallel/exclusive tool runtime, Claude
  `isConcurrencySafe` and `interruptBehavior`
- billyharness target files: `internal/tools/tools.go`,
  `internal/agent/runtime_loop.go`, `internal/agent/tool_attempt.go`,
  `internal/protocol/types.go`
- expected benefit: safe read/search tools can run faster while writes and
  process-backed tools keep conservative cancellation semantics.
- bloat risk: medium if this becomes a policy language; keep it to small Go
  callbacks/defaults.
- acceptance criteria: default behavior matches existing static metadata;
  selected tools can inspect args for parallel safety; interrupt behavior is
  `cancel`, `block`, or `wait_cleanup`; completed tools are not rewritten as
  aborted after cancellation.
- verification commands:
  - `go test -count=1 ./internal/tools ./internal/agent`
  - `go test -run 'Test.*InputAware.*|Test.*InterruptBehavior.*|Test.*Cancellation.*CompletedTool.*' -count=1 ./internal/tools ./internal/agent`

### 17. Body-After-Prefix Context Epoch Counters

- priority: P2
- source inspiration: Codex auto compact window/prefix baseline, OpenCode
  context epochs
- billyharness target files: `internal/agent/context_threshold.go`,
  `internal/agent/compaction.go`, `internal/runstate/runstate.go`,
  `internal/clientux/context.go`, `docs/context.md`
- expected benefit: better cached-prefix strategy and clearer context threshold
  events without mixing provider cache tokens into active context.
- bloat risk: medium; do not build a full context-manager subsystem.
- acceptance criteria: context events expose protected prefix tokens, body
  tokens, tokens until body compaction, provider cache read/write separately,
  and compaction window metadata if available.
- verification commands:
  - `go test -count=1 ./internal/agent ./internal/runstate ./internal/clientux`
  - `go test -run 'Test.*Prefix.*Body.*|Test.*ContextEpoch.*|Test.*ProviderCache.*' -count=1 ./internal/agent ./internal/clientux`

### 18. Benchmark Smoke Metrics

- priority: P2
- source inspiration: OpenCode benchmark scripts with machine-readable metrics,
  Codex high-volume stream benchmarks
- billyharness target files: `scripts/bench-smoke.sh`,
  `internal/bench/*`, `docs/benchmarks.md`
- expected benefit: easy trend tracking for TUI reflow, gateway replay, provider
  SSE parsing, web compaction, and MCP discovery without hard CI thresholds.
- bloat risk: low if it is a thin script over existing Go benchmarks.
- acceptance criteria: script emits stable `METRIC name value unit` lines and
  runs without live provider/network dependencies.
- verification commands:
  - `go test ./internal/tui ./internal/gateway ./internal/provider ./internal/tools ./internal/mcpclient -run '^$' -bench 'Benchmark(TUI|Gateway|Parse|Compact|MCP)' -benchmem`
  - `./scripts/bench-smoke.sh`

### 19. Durable Input Inbox Recovery Extensions

- priority: P2
- source inspiration: OpenCode durable prompt admission, Codex submission queue
- billyharness target files: `internal/gateway/gateway.go`,
  `internal/gateway/session_store.go`, `internal/session/session.go`,
  `internal/telegrambot/runner.go`, `cmd/fast-agent-harness/sessions.go`
- expected benefit: operator-visible repair/retry for ambiguous admitted inputs
  after the P0 durable admission MVP exists.
- bloat risk: medium; do not turn admission into a general job scheduler.
- acceptance criteria: depends on B1/B2; promoted-but-not-terminal inputs are
  listed as ambiguous after restart; CLI can inspect, retry, or drop with an
  audit event; unpromoted admitted inputs remain automatically retryable.
- verification commands:
  - `go test -count=1 ./internal/gateway ./internal/session ./internal/telegrambot ./cmd/fast-agent-harness`
  - `go test -run 'Test.*InputInbox.*|Test.*Admission.*|Test.*Ambiguous.*Prompt.*|Test.*Idempotent.*Prompt.*' -count=1 ./internal/...`

### 20. Manual Memory Consolidation

- priority: P2
- source inspiration: Codex memory summary/topic files, Claude memory taxonomy
- billyharness target files: `internal/memory/*`, `cmd/fast-agent-harness/*`,
  `docs/memory.md`
- expected benefit: user-triggered cleanup of local memory files after the MVP
  exists, without background agents.
- bloat risk: medium; reject vector DB and auto-extraction default.
- acceptance criteria: command proposes or writes bounded summary/topic updates
  only when invoked explicitly; obvious secrets are rejected; generated summary
  preserves memory type and source metadata.
- verification commands:
  - `go test -count=1 ./internal/memory ./cmd/fast-agent-harness`
  - `go test -run 'Test.*Memory.*Consolidate.*|Test.*Memory.*Secret.*' -count=1 ./internal/memory ./cmd/fast-agent-harness`

## Tool Gap Addendum

This addendum integrates the second research pass from the attached tool-gap
analyses. It keeps the earlier P0 reliability roadmap intact, but adds the
solo-developer features Billy is missing compared with Claude Code, Codex, and
OpenCode. These are still filtered through the same anti-bloat rule: local,
bounded, testable, no marketplace, no enterprise policy platform.

### A1. Bounded File Reads With Line Windows

- priority: P0
- source inspiration: Claude/OpenCode `Read offset/limit`
- billyharness target files: `internal/tools/tools.go`,
  `internal/tools/fs_read.go`, `internal/tools/tools_test.go`,
  `internal/toolrender/toolrender.go`
- expected benefit: large source files stop flooding context; the model gets
  stable line anchors for follow-up edits.
- bloat risk: low; text slicing only, no image/PDF/notebook reader.
- acceptance criteria: legacy `{"path":...}` behavior remains exact; optional
  `offset` is 1-indexed; `limit` is bounded; windowed reads return numbered
  lines, long-line truncation, `next_offset`, `total_lines`, and `truncated`;
  outside-workspace, sensitive-path, symlink, and binary handling stay safe.
- verification commands:
  - `go test -count=1 ./internal/tools ./internal/toolrender`
  - `go test -run 'Test.*FSRead.*(Offset|Limit|Line|Sensitive|Symlink|Legacy)' -count=1 ./internal/tools`
  - `go test -run '^$' -bench 'BenchmarkFSRead(File|Window)' -benchmem ./internal/tools`

### A2. Turn-Scoped Checkpoints, Diff, And Undo MVP

- priority: P0
- source inspiration: OpenCode step snapshots and session revert, Codex turn
  diff tracking and active-turn rollback guard, Claude file-history snapshots
  and destructive command warnings
- billyharness target files: `internal/checkpoint/*`,
  `internal/agent/tool_attempt.go`, `internal/agent/runtime_loop.go`,
  `internal/protocol/types.go`, `internal/eventlog/eventlog.go`,
  `internal/gateway/session_events.go`, `internal/gateway/session_store.go`,
  `internal/tui/actions.go`, `internal/telegrambot/commands.go`
- expected benefit: mutating agent steps can be previewed and rolled back
  per turn, with compact changed-file metadata and no user worktree surprises.
- bloat risk: medium; keep API to `Track`, `Patch`, `Diff`, `Preview`,
  `Restore`, `Cleanup`, latest/stepwise undo only.
- acceptance criteria: storage lives in Billy session/data storage and never
  writes hidden commits, branches, stash, `git reset`, or user `.git` state;
  user `git status` is not changed by checkpoint creation; dirty
  tracked/untracked files before the run are preserved; `fs_write_file`,
  `fs_make_dir`, `fs_edit_file`, future `fs_apply_patch`, and mutating
  `shell_exec` steps create compact turn-change events keyed by
  session/run/turn; shell-created/modified/deleted files are detected through
  read-only `git status/diff` where available plus a bounded mtime/hash scan
  fallback; large and binary patches are capped and stored by output ref;
  preview writes nothing; undo restores only files in the patch record;
  same-file user edits after the agent patch create a conflict and no partial
  restore; undo is denied during an active run unless the run is aborted first.
- verification commands:
  - `go test -count=1 ./internal/checkpoint ./internal/agent ./internal/eventlog ./internal/gateway ./internal/tui ./internal/telegrambot`
  - `go test -run 'Test.*Checkpoint.*|Test.*Turn.*Diff.*|Test.*Rollback.*|Test.*Undo.*|Test.*Preview.*|Test.*Conflict.*|Test.*Shell.*Changed.*' -count=1 ./internal/...`
  - `go test -run '^$' -bench 'Benchmark(Checkpoint|Rollback)' -benchmem ./internal/checkpoint`

### A3. Agent Work Plan State

- priority: P1
- source inspiration: Claude `TodoWrite`, Codex `update_plan`, OpenCode
  `todowrite`
- billyharness target files: `internal/tools/tools.go`,
  `internal/tools/plan.go`, `internal/protocol/types.go`,
  `internal/clientux/projector/projector.go`,
  `internal/toolrender/toolrender.go`, `internal/tui/transcript/projector.go`,
  `internal/telegrambot/render.go`, `docs/tui.md`
- expected benefit: the model can track multi-step work explicitly instead of
  losing progress inside its hidden reasoning.
- bloat risk: low to medium; do not turn this into a project-management DB.
- acceptance criteria: `todo_write` accepts bounded todos with
  `id/status/content/priority`; valid statuses are enforced; at most one item is
  `in_progress`; compact output is deterministic; state is session/transcript
  scoped and reconstructed from events/tool metadata; TUI and Telegram show
  active/completed counts and current `in_progress` without raw JSON.
- verification commands:
  - `go test -count=1 ./internal/tools ./internal/clientux/projector ./internal/toolrender ./internal/tui/transcript ./internal/telegrambot`
  - `go test -run 'Test.*Todo.*|Test.*PlanState.*' -count=1 ./internal/...`

### A4. Exact File Edit Tool

- priority: P1
- source inspiration: Claude `Edit`/MultiEdit, OpenCode exact edit
- billyharness target files: `internal/tools/tools.go`,
  `internal/tools/fs_edit.go`, `internal/tools/tools_test.go`,
  `internal/agent/agent_test.go`, `internal/toolrender/toolrender.go`
- expected benefit: small code edits no longer require full-file rewrites,
  reducing token use and accidental unrelated changes.
- bloat risk: low if exact-only; high if fuzzy matching sneaks in.
- acceptance criteria: `fs_edit_file` accepts `path`, optional
  `expected_sha256`, and bounded `edits[]` with `old_string`, `new_string`,
  `replace_all`; all edits apply sequentially in memory and write once via
  temp+rename; no partial writes; missing match fails; duplicate match fails
  unless `replace_all`; unchanged edit fails; dangerous-tool policy blocks
  before handler; result returns summary and before/after hashes, never full
  file content.
- verification commands:
  - `go test -count=1 ./internal/tools ./internal/agent ./internal/toolrender`
  - `go test -run 'Test.*FSEdit.*|Test.*Exact.*Replacement.*|Test.*DangerousTools.*|Test.*ParallelMetadata.*' -count=1 ./internal/tools ./internal/agent`
  - `go test -run '^$' -bench 'BenchmarkFSEdit' -benchmem ./internal/tools`

### A5. Native Grep And Glob Tools

- priority: P1
- source inspiration: Claude `Grep`/`Glob`, OpenCode bounded ripgrep tools
- billyharness target files: `internal/tools/tools.go`,
  `internal/tools/fs_search.go`, `internal/tools/tools_test.go`,
  `internal/toolrender/toolrender.go`, `internal/toolrender/toolrender_test.go`,
  `internal/tui/transcript/projector.go`, `docs/tui.md`
- expected benefit: the model stops using `shell_exec rg/find` for basic code
  navigation; searches become bounded, read-only, parallel-safe, and compactly
  rendered.
- bloat risk: low; no indexing daemon or IDE layer.
- acceptance criteria: `fs_grep` supports regex, invalid-regex errors, include
  glob, output modes, before/after/context lines, case-insensitive mode,
  `limit`, `offset`, max column cap, binary/large-file skip, sensitive-path skip,
  and workspace boundaries; `fs_glob` supports recursive patterns, file/dir/both
  type filter, modified/name sorting, deterministic truncation, and read-only
  parallel metadata.
- verification commands:
  - `go test -run 'TestFS(Grep|Glob).*' -count=1 ./internal/tools`
  - `go test -run 'Test.*ToolRender.*(Grep|Glob)' -count=1 ./internal/toolrender ./internal/tui/transcript`
  - `go test -run '^$' -bench 'BenchmarkFS(Grep|Glob)' -benchmem ./internal/tools`

### A6. Fuzzy File Resolver, `fs_find_files`, And TUI `@file`

- priority: P1
- source inspiration: Claude `FileIndex`, OpenCode `find.files`, Codex fuzzy
  file search ranking
- billyharness target files: `internal/filesearch/*`,
  `internal/tools/tools.go`, `internal/tools/tools_test.go`,
  `internal/config/projections.go`, `internal/toolrender/toolrender.go`,
  `internal/tui/file_mentions.go`, `internal/tui/actions.go`,
  `internal/tui/tui.go`, `internal/tui/tui_test.go`, `docs/tui.md`
- expected benefit: users and the model can find files by partial basename/path
  without exact paths, while TUI users can insert exact file paths quickly.
- bloat risk: medium if this grows watchers or a database; keep an on-demand
  cache with TTL/signature.
- acceptance criteria: `fs_find_files` returns ranked relative paths with type,
  score, and truncation; exact basename outranks path contains; respects
  workspace roots and sensitive skips; uses `git ls-files` when available,
  `rg --files` fallback, and `WalkDir` fallback; TUI `@` token opens file popup
  outside slash mode, `Tab/Enter` inserts exact relative path, `Esc` dismisses,
  stale async results are ignored, and no file content is auto-injected.
- verification commands:
  - `go test -count=1 ./internal/filesearch ./internal/tools ./internal/tui`
  - `go test -run 'Test.*File(Search|Resolver|FindFiles).*|TestTUI.*(AtFile|FileMention|Popup)' -count=1 ./internal/filesearch ./internal/tools ./internal/tui`
  - `go test -run '^$' -bench 'Benchmark(FileResolver|FileIndex|TUIAtFile)' -benchmem ./internal/filesearch ./internal/tui`

### A7. Run Access Modes And Plan Mode

- priority: P1
- source inspiration: OpenCode plan/build agents, Claude Plan Mode, Codex Plan
  Mode separation from checklist tools
- billyharness target files: `internal/config/config.go`,
  `internal/config/projections.go`, `internal/config/resolved.go`,
  `internal/agent/runtime_loop.go`, `internal/agent/transcript.go`,
  `internal/tools/policy.go`, `internal/gatewayapi/types.go`,
  `internal/gateway/gateway.go`, `internal/gateway/session_events.go`,
  `internal/clientux/actions.go`, `internal/tui/actions.go`,
  `internal/tui/tui.go`, `internal/telegrambot/commands.go`, `docs/tui.md`,
  `docs/setup.md`
- expected benefit: a real read-only analysis mode, not a best-effort prompt
  instruction.
- bloat risk: medium; do not turn this into an enterprise permission engine.
- acceptance criteria: `access_mode=build|guarded|plan`; default remains build
  or current behavior; plan mode filters model-visible tool specs to read/search
  tools and hard-denies write/execute/external calls even if auto-approval is
  enabled; mode is recorded in run snapshots/events/status; TUI, gateway, CLI,
  and Telegram can start a plan-mode run; prompt guidance explicitly separates
  `todo_write` progress tracking from plan-mode access policy.
- verification commands:
  - `go test -count=1 ./internal/config ./internal/agent ./internal/tools ./internal/gateway ./internal/tui ./internal/telegrambot`
  - `go test -run 'Test.*AccessMode.*|Test.*PlanMode.*|Test.*ReadOnly.*|Test.*Dangerous.*Denied.*' -count=1 ./internal/...`

### A8. Managed Shell Runtime For Dev Servers

- priority: P1
- source inspiration: Claude `Bash(run_in_background)` plus output/stop tools,
  Codex managed process IDs, OpenCode owner-bound background jobs
- billyharness target files: `internal/tools/tools.go`,
  `internal/tools/shell_process.go`, `internal/tooloutput/tooloutput.go`,
  `internal/protocol/types.go`, `internal/agent/tool_attempt.go`,
  `internal/agent/runtime_loop.go`, `internal/tools/tools_test.go`,
  `docs/setup.md`
- expected benefit: dev servers and watchers can keep running while the agent
  continues; no forced timeout for `npm run dev`, `vite`, `go run`, etc.
- bloat risk: medium; no PTY, stdin, durable restart, terminal emulator, or
  remote process API.
- acceptance criteria: `shell_exec` remains backward compatible and supports
  `background=true`, or equivalent `shell_start`; `shell_output` polls bounded
  output by cursor/tail and returns `output_ref`; `shell_kill` kills only opaque
  Billy-owned process IDs; Unix uses process-group termination with grace then
  kill; `Registry.Close()` terminates managed processes; max-live-process cap
  and exited-process reaping prevent leaks; unavailable in plan mode.
- verification commands:
  - `go test -count=1 ./internal/tools ./internal/tooloutput ./internal/agent`
  - `go test -run 'Test.*Shell.*(Background|Output|Kill|ProcessGroup|OutputRef|RegistryClose|MaxLive).*' -count=1 ./internal/tools ./internal/agent`
  - `go test -run '^$' -bench 'Benchmark.*(Shell|ToolOutput).*' -benchmem ./internal/tools ./internal/tooloutput`

### A9. Command-Based Diagnostics Feedback MVP

- priority: P1
- source inspiration: OpenCode write/edit/apply_patch diagnostics feedback,
  Claude bounded passive diagnostics, Codex patch verification diagnostics
- billyharness target files: `internal/diagnostics/*`,
  `internal/tools/diagnostics.go`, `internal/tools/tools.go`,
  `internal/agent/runtime_loop.go`, `internal/agent/tool_attempt.go`,
  `internal/protocol/types.go`, `internal/toolrender/toolrender.go`,
  `internal/tui/transcript/projector.go`, `internal/telegrambot/render.go`,
  `internal/config/config.go`, `internal/config/projections.go`,
  `internal/config/diagnostics.go`, `internal/config/resolved.go`,
  `docs/diagnostics.md`
- expected benefit: the model gets compiler/typecheck feedback as structured,
  bounded data instead of ad hoc `shell_exec` logs.
- bloat risk: low to medium if command-based only; no LSP client, watcher,
  auto-install, or IDE protocol.
- acceptance criteria: `diagnostics_run` executes only configured/default
  diagnostic commands; captures exit code, duration, raw output ref, and parsed
  issues; parses common `file:line:col` diagnostics with raw fallback; caps
  issues per file and total; sorts errors before warnings; returns compact
  `<diagnostics>` text plus metadata; failed commands never dump unbounded output
  into transcript.
- verification commands:
  - `go test -count=1 ./internal/diagnostics ./internal/tools ./internal/agent ./internal/toolrender ./internal/tui/transcript ./internal/telegrambot ./internal/config`
  - `go test -run 'Test.*Diagnostics.*|Test.*Compiler.*|Test.*OutputRef.*' -count=1 ./internal/...`
  - `go test -run '^$' -bench 'Benchmark(ParseDiagnostics|DiagnosticsRun)' -benchmem ./internal/diagnostics ./internal/tools`

### A10. Local Custom Slash Prompt Commands

- priority: P1
- source inspiration: OpenCode markdown commands, Claude prompt commands,
  Billy TUI action registry
- billyharness target files: `internal/promptcommands/commands.go`,
  `internal/promptcommands/commands_test.go`, `internal/tui/actions.go`,
  `internal/tui/tui.go`, `internal/tui/tui_test.go`, `docs/commands.md`
- expected benefit: users can define `/review`, `/test`, `/release-note`, and
  similar prompt workflows without recompiling Billy.
- bloat risk: low if limited to local markdown templates; no marketplace, shell
  interpolation, model override, agent override, or allowed-tools frontmatter.
- acceptance criteria: loads `${BILLYHARNESS_HOME}/commands/*.md` and
  `<workspace>/.billyharness/commands/*.md`; parses optional frontmatter
  `description` and `argument_hint`; command name comes from filename; built-ins
  cannot be shadowed; slash popup and `/help` show custom prompt commands;
  `$ARGUMENTS` and `$1`..`$9` expand deterministically; invocation re-enters the
  normal prompt send path and respects busy/gateway checks; transcript/events
  record original command plus expanded prompt length/hash; template output is
  capped with a clear status error.
- verification commands:
  - `go test -count=1 ./internal/tui ./internal/promptcommands`
  - `go test -run 'Test.*Custom.*Slash.*|Test.*CommandTemplate.*|Test.*Slash.*Popup.*' -count=1 ./internal/...`

### A11. `user_prompt_submit` Hook

- priority: P1
- source inspiration: Codex `UserPromptSubmit`, Claude user prompt submit hooks,
  Billy existing hook runner
- billyharness target files: `internal/config/config.go`,
  `internal/config/config_test.go`, `internal/hooks/hooks.go`,
  `internal/hooks/hooks_test.go`, `internal/agent/runtime_loop.go`,
  `internal/agent/transcript.go`, `internal/agent/agent_test.go`,
  `internal/session/session.go`, `internal/gateway/gateway.go`,
  `docs/hooks.md`
- expected benefit: local scripts can inject bounded context, deliberately
  rewrite a prompt, or block a wrong/unsafe prompt before model tokens are spent.
- bloat risk: medium if it becomes a policy engine; keep one synchronous local
  hook event, bounded JSON output, no prompt elicitation, no marketplace.
- acceptance criteria: `user_prompt_submit` is a valid hook event; runs after
  `session_start` and before the first model call for each submitted prompt;
  hook payload includes prompt, cwd, model id, profile, submission/run ids,
  source, access mode, and optional slash command metadata; JSON stdout supports
  `decision:"block"`, `reason`, `additional_context`, and optional bounded
  `updated_prompt`; non-JSON stdout can only become additional context; block
  causes no provider call and does not persist blocked prompt as model history;
  additional context is inserted as an explicit model-visible hook context
  message; hook events expose caps, block reason, context length, and updated
  prompt length/hash.
- verification commands:
  - `go test -count=1 ./internal/config ./internal/hooks ./internal/agent ./internal/session ./internal/gateway`
  - `go test -run 'Test.*UserPromptSubmit.*|Test.*PromptHook.*|Test.*Hook.*Block.*|Test.*AdditionalContext.*' -count=1 ./internal/...`
  - `go test -run '^$' -bench 'BenchmarkHookRunner|BenchmarkPromptHookParse' -benchmem ./internal/hooks ./internal/agent`

### A12. Redo State And Destructive Git Command Guardrails

- priority: P1
- source inspiration: OpenCode redo/revert state, Claude destructive git command
  warnings
- billyharness target files: `internal/checkpoint/*`,
  `internal/session/session.go`, `internal/gateway/session_inspect.go`,
  `internal/tui/sessions.go`, `internal/tui/actions.go`,
  `internal/telegrambot/commands.go`, `internal/tools/tools.go`,
  `internal/tools/tools_test.go`, `internal/toolrender/toolrender.go`
- expected benefit: after `/undo`, users can inspect and `/redo`; shell commands
  that would destroy checkpoint baselines are blocked or require explicit
  approval.
- bloat risk: low to medium; one linear redo stack and a small git-command
  pattern table, no branching UI.
- acceptance criteria: `/redo` restores the last undone agent patch and is
  cleared by a new mutating run; session inspect shows reverted state, files,
  and stats; destructive commands such as `git reset --hard`, `git clean -f`,
  `git checkout .`, `git restore .`, `git stash drop/clear`, and force-push are
  blocked or require explicit user approval; harmless `git status` is not
  blocked.
- verification commands:
  - `go test -count=1 ./internal/checkpoint ./internal/session ./internal/gateway ./internal/tui ./internal/telegrambot ./internal/tools ./internal/toolrender`
  - `go test -run 'Test.*Redo.*|Test.*Destructive.*Git.*|Test.*Git.*Guardrail.*' -count=1 ./internal/...`

### A13. Structured Patch Tool

- priority: P2
- source inspiration: Codex `apply_patch`, OpenCode `apply_patch`
- billyharness target files: `internal/tools/patch.go`,
  `internal/tools/patch_test.go`, `internal/tools/tools.go`,
  `internal/tools/tools_test.go`
- expected benefit: multi-hunk add/delete/update operations without shell/sed
  and without full-file rewrites.
- bloat risk: medium; parser/runtime can sprawl quickly.
- acceptance criteria: clean-room parser for a small structured patch format
  with begin/end, add/delete/update; all paths pass workspace safety; all hunks
  verify before first write; invalid patch never mutates files; deterministic
  summary reports added/modified/deleted files; no shell interception or
  marketplace/custom-tool dependency.
- verification commands:
  - `go test -count=1 ./internal/tools`
  - `go test -run 'Test.*ApplyPatch.*|Test.*StructuredPatch.*' -count=1 ./internal/tools`
  - `go test -run '^$' -bench 'BenchmarkApplyPatch' -benchmem ./internal/tools`

### A14. Minimal Stateless Subagent Tools

- priority: P2
- source inspiration: OpenCode `TaskTool`, Codex `spawn_agent`/`wait_agent`,
  Claude sync `AgentTool`
- billyharness target files: `internal/subagent/*`,
  `internal/tools/tools.go`, `internal/tools/schema.go`,
  `internal/agent/runtime_loop.go`, `internal/agent/tool_attempt.go`,
  `internal/protocol/types.go`, `internal/config/config.go`,
  `internal/session/session.go`, `internal/tooloutput/*`,
  `internal/toolrender/*`, `docs/architecture.md`
- expected benefit: parallel research/review workers without bloating parent
  transcript.
- bloat risk: medium; keep stateless, depth 1, no `send_input`, no `resume`,
  no remote/team/worktree agents.
- acceptance criteria: `spawn_agent` creates child worker from role, description,
  and prompt; `wait_agent` waits with clamped timeout and returns
  completed/failed/aborted/timeout; `close_agent` cancels one worker; max active
  workers default to 3; max depth is 1; child inherits provider/profile/runtime
  snapshot and access mode; child registry excludes subagents by default;
  explorer role is read-only; parent cancel cancels children; parent transcript
  stores only compact result plus `output_ref`.
- verification commands:
  - `go test -count=1 ./internal/subagent ./internal/tools ./internal/agent ./internal/protocol ./internal/config ./internal/session`
  - `go test -run 'Test.*Subagent.*|Test.*SpawnAgent.*|Test.*WaitAgent.*|Test.*Explorer.*ReadOnly.*|Test.*AgentCancellation.*' -count=1 ./internal/...`
  - `go test -race ./internal/subagent ./internal/agent -run 'Test.*Cancel.*|Test.*Subagent.*|Test.*Limit.*'`

### A15. Opt-In AI Memory Candidate Extraction

- priority: P2
- source inspiration: Codex phase-1 extraction, Claude `extractMemories`
- billyharness target files: `internal/memory/extractor.go`,
  `internal/memory/candidates.go`, `internal/memory/pending.go`,
  `internal/agent/event_builder.go`, `internal/gateway/session_store.go`,
  `internal/secrets/redact.go`, `internal/clientux/actions.go`,
  `internal/tui/*`, `internal/telegrambot/commands.go`, `docs/memory.md`
- expected benefit: reusable preferences and project facts can be drafted after
  sessions without bloating `SOUL.md` or requiring manual summarization.
- bloat risk: medium; disabled by default, candidate-only, no direct canonical
  memory mutation.
- acceptance criteria: no provider call when disabled; enabled extractor runs
  best-effort after completed run; strict candidate schema includes scope, kind,
  content, evidence, source session/seq, confidence, reason to save, and reason
  not to save; secrets are redacted; only pending candidates are written; user
  can list/read/approve/reject candidates; approval promotes to canonical
  memory with provenance; bounded sessions/tokens.
- verification commands:
  - `go test -count=1 ./internal/memory ./internal/agent ./internal/gateway ./internal/secrets ./internal/clientux ./internal/tui ./internal/telegrambot`
  - `go test -run 'Test.*Memory.*Candidate.*|Test.*Extraction.*Disabled.*|Test.*Secret.*|Test.*Memory.*Approve.*' -count=1 ./internal/...`

### A16. Deferred Diagnostics, LSP, Memory, And Backup Extensions

- priority: P2
- source inspiration: OpenCode lazy LSP service, Claude diagnostics registry,
  Codex memory consolidation, Claude file-history fallback
- billyharness target files: `internal/diagnostics/store.go`,
  `internal/lsp/*`, `internal/memory/consolidator.go`,
  `internal/memory/index.go`, `internal/checkpoint/filebackup.go`,
  `cmd/fast-agent-harness/doctor.go`
- expected benefit: future incremental diagnostics, memory consolidation/search,
  and non-git rollback fallback once the smaller MVPs are stable.
- bloat risk: high; each sub-feature must remain opt-in and behind an existing
  interface.
- acceptance criteria: async diagnostics dedupe and inject once before a model
  turn; LSP manager supports only configured stdio servers with no auto-downloads
  and diagnostics-only scope; memory consolidation is user-triggered first,
  locked, rollback-safe, and summary-capped; memory search index rebuilds from
  markdown and defers vectors; non-git file backups are storage-capped and warn
  when shell changes cannot be tracked completely.
- verification commands:
  - `go test -count=1 ./internal/diagnostics ./internal/lsp ./internal/memory ./internal/checkpoint ./cmd/fast-agent-harness`
  - `go test -run 'Test.*Diagnostics.*Dedup.*|Test.*LSP.*Lifecycle.*|Test.*Memory.*Consolidat.*|Test.*Memory.*Index.*|Test.*FileBackup.*' -count=1 ./internal/...`

## Runtime Productivity Addendum

This addendum integrates the third research pass from the additional
architecture note. It focuses on solo coding productivity under long-running
Telegram/TUI use: admitted inputs must not disappear, slow clients must not
stall execution, sessions must be searchable, and the agent needs bounded
project context plus a simple way to ask the user for clarification.

### B1. Durable Prompt Admission And Input Inbox MVP

- priority: P0
- source inspiration: OpenCode durable `session_input`, Codex submission IDs,
  Claude early prompt persistence
- billyharness target files: `internal/gateway/session_store.go`,
  `internal/gateway/session_inputs.go`, `internal/gatewayapi/types.go`,
  `internal/gateway/gateway.go`, `internal/gatewayclient/client.go`,
  `internal/session/session.go`
- expected benefit: a TUI/Telegram prompt is fsynced before execution starts,
  so process restarts can see whether it is pending, promoted, ambiguous, or
  completed.
- bloat risk: medium; keep per-session JSONL only, not a general job scheduler.
- acceptance criteria: each gateway session has append-only `inputs.jsonl`;
  `POST /v1/sessions/{id}/inputs` admits `input_id`, prompt, delivery policy,
  and client metadata; same `input_id` with same body is idempotent; same id
  with different body conflicts; admission returns only after fsync; promotion
  is recorded before `Session.Run`; existing `/run` remains a compatibility
  wrapper that admits a generated id then promotes/runs; unpromoted admitted
  inputs survive `LoadAll` and can retry; promoted-but-not-terminal inputs are
  marked ambiguous, not silently replayed.
- verification commands:
  - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/session`
  - `go test -run 'Test.*Admission.*|Test.*InputInbox.*|Test.*Idempotent.*Prompt.*|Test.*Promot.*|Test.*Ambiguous.*' -count=1 ./internal/...`

### B2. Telegram Durable Admission And Offset Safety

- priority: P0
- source inspiration: OpenCode admitted input IDs, Billy Telegram replay risks,
  Claude queued prompt UX
- billyharness target files: `internal/telegrambot/poller.go`,
  `internal/telegrambot/runner.go`, `internal/telegrambot/state_runtime.go`,
  `internal/telegrambot/store.go`, `internal/telegrambot/admission_store.go`,
  `internal/gatewayclient/client.go`, `docs/telegram.md`
- expected benefit: Telegram updates are not acknowledged past the bot until
  the user intent is durably admitted or intentionally ignored.
- bloat risk: medium to high; keep it to per-update admission/ack state and
  current latest-input interrupt semantics.
- acceptance criteria: Telegram update offset advances only after durable
  admission or explicit ignore; duplicate Telegram updates do not duplicate
  runs; latest-input interrupt/supersede still works; admitted pending input id
  is visible in logs/status; gateway admission failure leaves enough state to
  retry safely after restart.
- verification commands:
  - `go test -count=1 ./internal/telegrambot ./internal/gatewayclient`
  - `go test -run 'Test.*Telegram.*(Admission|Offset|Duplicate|Interrupt|Superseded).*' -count=1 ./internal/telegrambot`

### B3. Slow-Client Backpressure And Run/Event Decoupling

- priority: P0
- source inspiration: OpenCode durable tail/wake split, Codex event queue split,
  Claude bounded uploader/backpressure
- billyharness target files: `internal/gateway/gateway.go`,
  `internal/gateway/session_events.go`, `internal/gatewayclient/client.go`,
  `internal/tui/gateway_session.go`, `internal/eventlog/eventlog.go`,
  `docs/architecture.md`
- expected benefit: a slow or blocked `/run` HTTP response, gateway client, or
  TUI channel cannot stall the active agent run.
- bloat risk: medium if it becomes a new async run framework; low if live
  streams remain bounded progress channels and JSONL is the source of truth.
- acceptance criteria: execution continues when the live response writer blocks
  or overflows; live stream clients receive an explicit gap/drop signal or
  reconnect hint; clients can recover by replaying `/events?after_seq=last_seq`;
  gateway follow subscribes before replay and never relies on live events for
  durability; slow-stream tests prove run completion and exact final transcript.
- verification commands:
  - `go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/eventlog`
  - `go test -run 'Test.*Slow.*Client.*|Test.*Backpressure.*|Test.*Replay.*Gap.*|Test.*Run.*Completes.*' -count=1 ./internal/...`

### B4. Batched TUI Event Apply And Reflow

- priority: P0
- source inspiration: OpenCode latest-per-key UI queues, Claude short stream
  buffers
- billyharness target files: `internal/tui/tui.go`,
  `internal/tui/gateway_session.go`, `internal/tui/transcript/projector.go`,
  `internal/tui/tui_test.go`
- expected benefit: high-volume token streams stop causing one full TUI reflow
  per event.
- bloat risk: low; this is a tick/batch boundary inside the current TUI, not a
  new framework.
- acceptance criteria: TUI applies event batches on a short tick and flushes
  immediately for terminal/tool-boundary events; 1000 assistant deltas produce
  the same final transcript with far fewer reflows; cursor/seq dedupe and
  selection/copy behavior stay stable.
- verification commands:
  - `go test -count=1 ./internal/tui ./internal/tui/transcript`
  - `go test -run 'Test.*TUI.*(Batch|Reflow|Seq|Dedupe).*' -count=1 ./internal/tui ./internal/tui/transcript`
  - `go test -run '^$' -bench 'Benchmark(TUIBatch|TUIReflow|RenderAssistantMarkdown)' -benchmem ./internal/tui/...`

### B5. Rebuildable Session Search And Diagnostics Side Index

- priority: P1
- source inspiration: OpenCode materialized session/event projections, Codex
  typed turn/item shapes, Claude visible transcript search
- billyharness target files: `internal/gateway/session_index.go`,
  `internal/gateway/session_index_diagnostics.go`,
  `internal/gateway/session_store.go`,
  `internal/clientux/projector/projector.go`,
  `internal/trace/trace.go`
- expected benefit: local search/filtering across hundreds of JSONL sessions
  without replacing JSONL as the source of truth.
- bloat risk: medium; rebuild-only capped JSON/JSONL side files, no SQLite,
  FTS, vector DB, daemon, or live write path for v0.
- acceptance criteria: rebuild emits capped text rows, tool rows, error rows,
  run rows, and usage rows; text rows index visible user/assistant content and
  skip system/profile noise by default; tool/error rows include session/run/
  turn/step/call ids, tool name, status, duration, error, output refs, and
  capped args preview; usage aggregation matches projector semantics and does
  not double-count cumulative provider updates; corrupt or missing index never
  breaks session list/inspect.
- verification commands:
  - `go test -count=1 ./internal/gateway ./internal/clientux/projector ./internal/trace`
  - `go test -run 'Test.*Session.*Index.*|Test.*Index.*(Tool|Error|Usage|Search).*|Test.*Usage.*Cumulative.*' -count=1 ./internal/...`

### B6. CLI Session Queries Over The Side Index

- priority: P1
- source inspiration: Claude transcript search UX, Codex typed item filters,
  OpenCode session projections
- billyharness target files: `cmd/fast-agent-harness/sessions.go`,
  `internal/gateway/session_index_diagnostics.go`, `docs/setup.md`
- expected benefit: practical commands for questions like "where did the agent
  edit auth", "show failed shell_exec", "which runs timed out", and "which
  sessions used the most context".
- bloat risk: low; read-only query commands over a rebuildable local index.
- acceptance criteria: `sessions search QUERY`, `sessions tools`,
  `sessions errors`, `sessions usage`, and `sessions runs` support human and
  JSON output, limits, session filters, and missing-index guidance; commands
  never read full output refs unless explicitly asked.
- verification commands:
  - `go test -count=1 ./cmd/fast-agent-harness ./internal/gateway`
  - `go test -run 'Test.*Sessions.*(Search|Tools|Errors|Usage|Runs).*' -count=1 ./cmd/fast-agent-harness ./internal/gateway`

### B7. Interactive `ask_user` MVP

- priority: P1
- source inspiration: Codex `RequestUserInput`, OpenCode `QuestionV2`, Claude
  `AskUserQuestion`
- billyharness target files: `internal/protocol/types.go`,
  `internal/protocol/envelope.go`, `internal/agent/runtime_loop.go`,
  `internal/tools/tools.go`, `internal/tools/ask_user.go`,
  `internal/gatewayapi/types.go`, `internal/gateway/gateway.go`,
  `internal/gateway/session_events.go`, `internal/gatewayclient/client.go`,
  `internal/tui/tui.go`, `internal/telegrambot/runner.go`,
  `internal/telegrambot/types.go`
- expected benefit: the agent can pause and clarify instead of guessing,
  failing, or forcing a Telegram user to cancel/restart.
- bloat risk: medium; cap the schema and allow one pending request per session
  initially.
- acceptance criteria: model-visible `ask_user` tool supports 1-3 bounded
  questions, optional short header, 2-4 options with descriptions, and optional
  freeform answer; events `user_input.requested`, `user_input.answered`, and
  `user_input.rejected` carry request/session/run/turn/call correlation;
  gateway answer/reject endpoints unblock the tool call; TUI answer input
  resumes the same run; when Telegram has a pending request, the next
  non-command message from the same chat/thread/user answers it instead of
  interrupting; pending request clears on cancel or terminal run event; stale
  pending state after restart expires cleanly.
- verification commands:
  - `go test -count=1 ./internal/protocol ./internal/agent ./internal/tools ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot`
  - `go test -run 'Test.*AskUser.*|Test.*UserInput.*(Requested|Answered|Rejected).*|Test.*Telegram.*Pending.*Question.*' -count=1 ./internal/...`

### B8. Minimal Project Context Registry

- priority: P1
- source inspiration: OpenCode `SystemContextRegistry`, Codex bounded context
  fragments, Claude `/init` project discovery
- billyharness target files: `internal/projectcontext/*`,
  `internal/agent/transcript.go`, `internal/agent/compaction.go`,
  `internal/clientux/context.go`, `internal/config/config.go`,
  `internal/instructions/instructions.go`, `docs/context.md`,
  `docs/profiles.md`
- expected benefit: fewer first-turn discovery commands and better project
  grounding without stuffing README contents into every prompt.
- bloat risk: medium; no watcher, DB, README ingestion, remote instruction
  fetch, shell-history scraping, or `.env` value injection.
- acceptance criteria: snapshot detects cwd, workspace root, git root, package
  manager from lockfiles/manifests, likely test/build commands with source and
  confidence, AGENTS/README/instruction source metadata with hashes/byte counts,
  env sample filenames plus variable names only, and truncation/cap flags;
  rendered fragment is capped, escaped, and injected after profile/SOUL but
  before AGENTS/project instructions; `/context` shows `project_context` token
  and source counts; no secret values are included.
- verification commands:
  - `go test -count=1 ./internal/projectcontext ./internal/agent ./internal/clientux ./internal/config ./internal/instructions`
  - `go test -run 'Test.*ProjectContext.*|Test.*Env.*Hints.*|Test.*Instruction.*Metadata.*|Test.*Context.*Project.*' -count=1 ./internal/...`

### B9. Context Epoch Reconcile

- priority: P1
- source inspiration: OpenCode context epochs, Codex world-state diffs, Billy
  protected-prefix compaction
- billyharness target files: `internal/runstate/context_epoch.go`,
  `internal/runstate/runstate.go`, `internal/session/session.go`,
  `internal/gateway/session_store.go`, `internal/agent/transcript.go`,
  `internal/agent/compaction.go`, `internal/clientux/context.go`
- expected benefit: project context stays stable across turns/restarts and only
  changes when the bounded snapshot changes.
- bloat risk: medium; keep one active JSON snapshot per session, not a context
  projector subsystem.
- acceptance criteria: first run records a project context epoch and hashes the
  rendered baseline; unchanged context injects nothing new; changed context
  injects one bounded "Project context updated" fragment; failed observation
  preserves the prior snapshot; restart reuses stored epoch; runstate hashes
  include project instruction/context hashes so AGENTS changes are visible.
- verification commands:
  - `go test -count=1 ./internal/runstate ./internal/session ./internal/gateway ./internal/agent ./internal/clientux`
  - `go test -run 'Test.*ContextEpoch.*|Test.*ProjectContext.*Reconcile.*|Test.*Instruction.*Hash.*' -count=1 ./internal/...`

### B10. Delta Coalescing Before Durable Append And Render

- priority: P1
- source inspiration: Codex completed-output traces, Claude stream accumulation,
  OpenCode durable event/wake separation
- billyharness target files: `internal/agent/model_call.go`,
  `internal/gateway/session_store.go`, `internal/protocol/types.go`,
  `internal/clientux/projector/projector.go`,
  `internal/telegrambot/progress_stream.go`
- expected benefit: fewer JSONL fsyncs, smaller replay files, and less
  Telegram/TUI churn for high-chunk streams.
- bloat risk: medium because event cadence semantics change; keep final
  transcript semantics identical and first-token latency bounded.
- acceptance criteria: consecutive assistant/reasoning deltas are coalesced by
  time/size threshold and flushed on tool/usage/terminal boundaries; final
  assistant/reasoning text exactly matches uncoalesced streams; first visible
  delta latency remains bounded; 2000 provider chunks produce far fewer
  persisted delta events; seq ordering and replay remain valid.
- verification commands:
  - `go test -count=1 ./internal/agent ./internal/gateway ./internal/clientux/projector ./internal/telegrambot`
  - `go test -run 'Test.*Delta.*Coalesc.*|Test.*Assistant.*Final.*Text.*|Test.*Replay.*Coalesc.*' -count=1 ./internal/...`
  - `go test -run '^$' -bench 'Benchmark(ModelStream|SessionJSONLAppend|ReplayCoalesced)' -benchmem ./internal/agent ./internal/gateway`

### B11. Minimal Turn Diff Display And Preview UX

- priority: P1
- source inspiration: Claude `/diff` dialog, Codex turn-diff notifications,
  OpenCode staged revert preview
- billyharness target files: `internal/clientux/projector/projector.go`,
  `internal/tui/transcript/*`, `internal/tui/actions.go`,
  `internal/telegrambot/render.go`, `internal/telegrambot/commands.go`,
  `internal/toolrender/*`, `internal/checkpoint/*`
- expected benefit: users see what a turn changed without opening git manually,
  and can request full preview before undo.
- bloat risk: low; compact summaries plus output refs, no large visual diff UI.
- acceptance criteria: transcript shows concise per-turn summary such as files,
  additions/deletions, binary/large markers, and shell-changed count; full patch
  is available through output ref/API/command, not inline; Telegram and TUI can
  request revert preview; display state is reconstructed from durable
  turn-change events.
- verification commands:
  - `go test -count=1 ./internal/clientux/projector ./internal/tui ./internal/telegrambot ./internal/toolrender ./internal/checkpoint`
  - `go test -run 'Test.*Turn.*Diff.*Display.*|Test.*Revert.*Preview.*|Test.*Patch.*OutputRef.*' -count=1 ./internal/...`

### B12. JSONL Scale Benchmark And Sparse Seq Index Gate

- priority: P1
- source inspiration: Codex JSONL plus metadata indexes, Claude paginated replay
  guards, OpenCode projection benchmarks
- billyharness target files: `internal/eventlog/jsonl.go`,
  `internal/gateway/session_store_benchmark_test.go`,
  `internal/gateway/session_store.go`, `scripts/bench-smoke.sh`,
  `docs/benchmarks.md`
- expected benefit: long sessions keep predictable append and tail-replay
  behavior before storage complexity is added.
- bloat risk: low for benchmarks; medium only if a sparse `.idx` is justified.
- acceptance criteria: benchmarks cover 10k and 100k event appends, tail replay
  after high seq, output-ref-heavy events, and coalesced/uncoalesced streams;
  stable `METRIC` lines are emitted; add rebuildable sparse seq offset index
  only if full scan misses the documented target; no SQLite migration.
- verification commands:
  - `go test -run '^$' -bench 'Benchmark(SessionJSONL|EventJSONL|ReplayAfterSeq)' -benchmem ./internal/gateway ./internal/eventlog`
  - `./scripts/bench-smoke.sh`

### B13. MCP Elicitation Bridge

- priority: P2
- source inspiration: Codex `ResolveElicitation`, Claude MCP elicitation UI,
  Billy MCP stdio focus
- billyharness target files: `internal/mcpclient/*`,
  `internal/protocol/types.go`, `internal/gateway/gateway.go`,
  `internal/tui/tui.go`, `internal/telegrambot/runner.go`
- expected benefit: MCP servers can request simple user clarification through
  the same TUI/Telegram path as `ask_user`.
- bloat risk: high; defer until B7 is stable and support only simple text/form
  accept-decline first.
- acceptance criteria: MCP elicitation maps to common pending input events;
  reply/decline routes back to the MCP server; unsupported forms decline
  cleanly with a clear reason; no remote OAuth/prompt/resource platform is
  introduced.
- verification commands:
  - `go test -count=1 ./internal/mcpclient ./internal/protocol ./internal/gateway ./internal/tui ./internal/telegrambot`
  - `go test -run 'Test.*MCP.*Elicit.*|Test.*UserInput.*MCP.*|Test.*Unsupported.*Form.*' -count=1 ./internal/...`

### B14. Last Billy Commands Project Context Source

- priority: P2
- source inspiration: Codex `UserShellCommand`, Claude frozen git/status
  context, the additional project-context research note
- billyharness target files: `internal/projectcontext/commands.go`,
  `internal/tools/tools.go`, `internal/gateway/session_events.go`,
  `internal/gateway/session_store.go`, `internal/tooloutput/*`
- expected benefit: the model remembers recent Billy-run verification commands
  without rerunning them or reading shell history.
- bloat risk: low if only last N summaries are stored and no raw output is
  injected.
- acceptance criteria: stores last N Billy `shell_exec` commands with argv/cwd,
  exit code, duration, timestamp, output hash/ref, and truncation flag; caps
  total rendered size; never scrapes user shell history; never injects raw log
  output into the project context fragment.
- verification commands:
  - `go test -count=1 ./internal/projectcontext ./internal/tools ./internal/gateway ./internal/tooloutput`
  - `go test -run 'Test.*Last.*Command.*|Test.*ProjectContext.*Command.*|Test.*Shell.*OutputRef.*' -count=1 ./internal/...`

### B15. Cross-Adapter Slow-Client Stress Harness

- priority: P2
- source inspiration: competitor long-stream/backpressure regression suites and
  Billy Telegram/TUI gateway risks
- billyharness target files: `internal/testkit/*`, `internal/gateway/*`,
  `internal/tui/*`, `internal/telegrambot/*`, `internal/provider/*`,
  `scripts/bench-smoke.sh`
- expected benefit: prevents regressions where long provider streams, slow TUI,
  Telegram 429s, or replay gaps break final output.
- bloat risk: low; focused tests and benchmarks, no new runtime subsystem.
- acceptance criteria: fake provider emits 10k chunks; TUI event channel can be
  blocked; Telegram client returns 429/backoff; `/events` replay resumes after
  drop; run completes; final assistant content and tool state are correct; max
  memory and goroutine growth stay bounded by test assertions or metrics.
- verification commands:
  - `go test -count=1 ./internal/testkit ./internal/gateway ./internal/tui ./internal/telegrambot ./internal/provider`
  - `go test -run 'Test.*Slow.*Client.*|Test.*Telegram.*429.*|Test.*Long.*Stream.*|Test.*Replay.*After.*Drop.*' -count=1 ./internal/...`
  - `go test -run '^$' -bench 'Benchmark(LongStream|SlowClient|TelegramThrottle)' -benchmem ./internal/gateway ./internal/tui ./internal/telegrambot`

## Explicit Non-Goals

- Plugin marketplace or extension store.
- Enterprise RBAC, org policy, compliance audit trails, SaaS telemetry.
- Remote MCP OAuth/prompts/resources unless local stdio tools are already
  stable and a concrete use case exists.
- Headless browser web extraction.
- Full SQLite event store replacement before JSONL replay benchmarks fail.
- Background memory extraction by default.
- New TUI framework or React/Ink clone.
- Generic provider platform beyond the current practical providers.
- Shell interpolation in custom command templates.
- PTY/stdin terminal emulation for background shell MVP.
- Hidden commits, stash, branches, or `git reset` in the user's repository for
  undo/redo.
- Full LSP server catalog, auto-downloads, watchers, or IDE feature set before
  command-based diagnostics are proven useful.
- Nested subagents, remote/team agents, durable subagent queues, or parent
  transcript injection of full child transcripts.
- AI memory writing directly to canonical memory without user review.
- Durable input admission as a general job scheduler, background workflow
  engine, or multi-tenant task queue.
- SQLite/FTS/vector search as the required session-index v0.
- Scraping user shell history, injecting `.env` values, or ingesting full README
  prose into project context by default.
- Rich MCP elicitation forms before the simple `ask_user` path is proven.

## Suggested First Milestone

Implement the first reliability/admission/backpressure milestone before adding
broader coding UX:

1. Durable gateway follow loop.
2. Gateway interrupt policy.
3. Durable prompt admission/input inbox MVP.
4. Telegram durable admission and offset safety.
5. Slow-client run/event decoupling.
6. Batched TUI event apply and reflow.
7. Web/tool inline budgets.
8. Turn tool snapshot and transcript pairing.
9. Canonical event trace tests.
10. Bounded `fs_read_file` windows.

This milestone should make Billyharness harder to confuse, cheaper in context,
resistant to slow clients, and safer to drive from Telegram/VPS before adding
new UX surface.

Before enabling broad write-tool ergonomics (`fs_edit_file`, managed shell, or
structured patch), implement A2 turn-scoped checkpoints/diff/undo or explicitly
accept that those features cannot be rolled back safely yet.
