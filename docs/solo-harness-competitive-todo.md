# Solo Harness Competitive TODO

Date: 2026-07-01
Status: new source-of-truth roadmap for the next competitive pass

This document is a clean-room implementation plan for improving Billyharness by
studying Codex CLI, Claude Code, and OpenCode. It is not a request to copy
their source code. It extracts the narrow engineering patterns that make sense
for a fast solo harness and rejects platform, marketplace, enterprise, and cloud
machinery that does not help the current owner.

## Source Repositories

- Codex: `/root/agent-research/codex`
- Claude Code leak mirror: `/root/claude-code`
- OpenCode: `/root/agent-research/opencode-current`

## Billyharness Source Documents

- `/root/billyharness/docs/architecture.md`
- `/root/billyharness/docs/harness-research-execution-todo.md`
- `/root/billyharness/docs/competitive-architecture-analysis.md`
- `/root/billyharness/docs/memory-systems-research.md`

## Solo Harness Filter

Accept a competitor idea only when it does at least one of these things:

- makes a known Telegram, TUI, gateway, or agent-loop failure impossible or
  clearly diagnosable;
- reduces latency, context bloat, provider spend, or repeated token waste on
  the hot path;
- gives TUI and Telegram one shared contract instead of duplicated rendering or
  state interpretation;
- strengthens replay, traceability, or deterministic tests;
- makes future changes easier by clarifying ownership boundaries already
  documented in `docs/architecture.md`;
- stays useful for one local owner without a team platform.

Reject by default:

- plugin marketplaces, extension stores, remote plugin sync, app-server
  compatibility layers, organization config, RBAC, policy engines, SaaS
  telemetry, cloud queues, team agents, generated SDK surfaces, hidden user-git
  state, mandatory SQLite/vector/FTS databases, shell-history ingestion,
  headless browser extraction, React/Ink terminal rewrites, and default
  auto-memory writes.

## Current Billyharness Baseline

Billyharness already has the important base layers:

- typed protocol events and JSONL session storage;
- gateway replay/follow, prompt admission, Telegram offset safety, and
  interrupt semantics;
- model/provider settings, Codex OAuth integration, DeepSeek routing, and web
  summarization outside the main agent loop;
- output refs for large tool/web results;
- bounded `fs_read_file`, `fs_edit_file`, managed shell processes, diagnostics,
  file search, and MCP discovery;
- TUI/Telegram projection through shared protocol events;
- `toolrender` for compact tool display labels;
- `trace` bundles and benchmark scaffolding;
- architecture import guards and hygiene checks.

The next pass should therefore improve observability, context correctness,
shared render contracts, replay tests, memory, and interop. It should not
restart the project around another framework.

## Competitive Findings

### Codex Patterns To Take

- Provider capability registry: provider-owned upper bounds for features,
  preferred helper models, account/auth status, context window, streaming,
  cache fields, and web/tool support.
- Turn-scoped runtime snapshots: the model call, advertised tools, config,
  context metadata, and MCP catalog should be frozen per turn.
- External session import/export: foreign chat history can be translated into a
  local event/turn model with explicit markers and approximate token counts.
- Trace and rollout thinking: keep durable event history as the source of truth,
  then derive UI and replay views from it.
- Memory model selection: small/cheap models for extraction, stronger models
  only for consolidation when explicitly enabled.

### Codex Patterns To Reject

- Cloud tasks, app server, connectors platform, enterprise approval/reviewer
  flows, first-party attestation, multi-surface app protocols, remote execution
  planes, and broad compatibility layers.

### Claude Code Patterns To Take

- Tool UI contract: every tool has compact display text, progress text,
  result text, interrupt behavior, and grouping/collapse semantics.
- Prompt-cache break diagnostics: hash system prompt sections, tool schemas,
  cache controls, model/effort, and dynamic context so cache busts explain
  themselves.
- Terminal selection polish: semantic no-select regions, wide-character safe
  coordinates, word/line selection, scrollback-safe copy, and visible highlight.
- File-based memory taxonomy: small index plus bounded topic files, user
  review first, no default automatic writes.
- Compaction guards: avoid recursive compaction, track compact failures, and
  preserve boundaries/epochs instead of mutating history invisibly.

### Claude Code Patterns To Reject

- React/Ink rewrite, team memory sync, Anthropic API-specific memory services,
  mobile/Slack/CCR surfaces, managed policies, analytics, and default forked
  memory agents.

### OpenCode Patterns To Take

- Unified command registry: local commands, prompt commands, MCP prompts, and
  skills become one searchable palette with source labels and argument hints.
- Output truncation contract: every large output gets a durable file/ref, a
  bounded head/tail preview, and a model hint to inspect with search or offset
  reads instead of inlining everything.
- Adapter event translation: one semantic event stream can drive multiple
  clients if adapters keep their own presentation state shallow.
- Context epochs: dynamic context changes should be explicit durable events or
  epochs, not invisible prompt drift.
- Fake provider and replay tests as first-class verification.

### OpenCode Patterns To Reject

- Effect/layer runtime migration, SQLite/event-sourcing platform rewrite,
  share/sync/control-plane features, plugin lifecycle, organization config,
  broad IDE/LSP platform, and worktree management as a default dependency.

## Milestone 0 - Research Baseline

Goal: make the competitive pass reproducible and keep it separate from older
roadmaps.

- [x] SH-00.1 Confirm local competitor repos and current billyharness baseline.
  - source: the repository paths listed above.
  - target files: this document only.
  - acceptance: record missing repos as blockers; do not rely on internet
    sources unless the user explicitly asks for a fresh upstream comparison.
  - verification: `git status --short`; `test -d` for each source repo.
  - status: completed 2026-07-01.
  - evidence: local competitor repositories exist at
    `/root/agent-research/codex`, `/root/claude-code`, and
    `/root/agent-research/opencode-current`; no internet comparison was used.
    Billyharness is on branch `main` tracking `origin/main`; `HEAD` and
    upstream both resolved to `cbcc80cc834e44b2418dc21ae843bc9e199c15c7`
    (`cbcc80c Defer tool concurrency experiments`). The only dirty paths at
    baseline were untracked roadmap docs:
    `docs/solo-harness-competitive-goal.md` and
    `docs/solo-harness-competitive-todo.md`.
  - commit: `01230c8c3ad2da87ebc0119ae40b04de43e3a06b`.

- [x] SH-00.2 Cross-link this roadmap from the existing planning docs only if
  it becomes the active source of truth.
  - target files: optional docs only.
  - acceptance: old TODO files are not duplicated or rewritten without need.
  - verification: manual diff review.
  - status: completed 2026-07-01.
  - evidence: added a discoverability link in `docs/README.md` and a short
    status note in `docs/harness-research-execution-todo.md` pointing active
    follow-up work to this roadmap. No older checklist content was duplicated
    or rewritten.
  - commit: `47c3cc727a371017305e3a38b3ba60269a5995f3`.

## Milestone 1 - Prompt, Context, And Provider Observability (P0)

Goal: make every expensive model request understandable: what prompt sections
were included, what changed, what helper model was used, and how close the turn
is to compaction.

- [x] SH-01.1 Add prompt section inventory and cache-break diagnostics.
  - inspiration: Claude Code prompt-cache break detection and OpenCode context
    epochs.
  - target files: `internal/instructions`, `internal/projectcontext`,
    `internal/runstate`, `internal/agent/model_call.go`,
    `internal/protocol`, `internal/trace`.
  - acceptance: each model request can emit or record section names, byte
    counts, approximate token counts, stable hashes, and a reason when the
    prompt prefix changes between turns.
  - acceptance: secrets and `.env` values are never included in diagnostics.
  - verification: focused tests for prompt inventory hash stability and a cache
    break caused by model/tool/context change.
  - status: completed 2026-07-01.
  - evidence: added protocol prompt-inventory/cache-break structs, runstate
    prompt-section and tool-schema inventory hashing, per-turn cache signature
    comparison, model-call event metadata, and trace replay counters/timeline
    fields. Diagnostics include section names, roles, indexes, byte counts,
    approximate token counts, and SHA-256 hashes only; arbitrary user/chat text
    is not inventoried, and project context still records `.env` variable names
    without values.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Prompt.*|Test.*Cache.*Break.*' -count=1 ./internal/runstate
    ./internal/agent ./internal/trace` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Prompt.*|Test.*Cache.*Break.*|TestReplayEventsAggregatesUsageCumulativeAndEventCounters'
    -count=1 ./internal/runstate ./internal/agent ./internal/trace` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/instructions
    ./internal/projectcontext ./internal/runstate ./internal/agent
    ./internal/protocol ./internal/trace` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `2a185b5d4d60ecd780a1f9fdb53ebc45b3e82605`.

- [x] SH-01.2 Add `/context` report v2 for CLI, TUI, and Telegram.
  - inspiration: Codex/OpenCode context-window metadata and the user's current
    Telegram status-line pain.
  - target files: `internal/clientux/context.go`,
    `internal/gateway/session_inspect.go`,
    `internal/gatewayclient/client.go`, `internal/tui/status.go`,
    `internal/telegrambot/status_html.go`, `cmd/fast-agent-harness`.
  - acceptance: the report shows context tokens/window/percent, compact
    threshold, last compaction epoch, prompt section budget, output refs,
    websum in/out, helper-model usage, current model, reasoning mode, and
    cache-hit/miss fields with clear labels.
  - verification: package tests plus one fixture with websum and compaction.
  - status: completed 2026-07-01.
  - evidence: extended the shared gateway context DTO and formatter with
    runtime model/provider/profile/reasoning/access fields, model/tool activity,
    cumulative and last-turn token/cache usage, helper/web-summary usage,
    prompt inventory/cache-break data, last compaction details, output-ref
    totals, and replay warnings. Gateway `/context`, TUI local/gateway
    `/context`, Telegram gateway status, and new CLI
    `sessions context [-dir DIR] [-json] SESSION_ID` now use the same shared
    context builder/formatter. Stored-session context replay reads JSONL events
    as source of truth and degrades with an explicit warning if event replay is
    unavailable.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Context.*Report.*|Test.*Context.*Command.*|Test.*ContextStatus.*|TestSessionsCommandListsAndInspectsStore|TestGatewayClientContextStatusUsesSharedFormatter|TestContextReportV2IncludesEventsRuntimePromptAndHelperUsage'
    -count=1 ./internal/clientux ./internal/gateway ./internal/gatewayclient
    ./internal/tui ./internal/telegrambot ./cmd/fast-agent-harness` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/clientux
    ./internal/gatewayapi ./internal/gateway ./internal/gatewayclient
    ./internal/tui ./internal/telegrambot ./cmd/fast-agent-harness` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `9bf53b14222d392ed16c33884548c85e2fe98bd3`.

- [x] SH-01.3 Make provider/model capability policy explicit.
  - inspiration: Codex provider capability registry.
  - target files: `internal/modelinfo`, `internal/provider`, `internal/config`,
    `internal/credentials`, `cmd/fast-agent-harness/config*`.
  - acceptance: provider/model metadata includes context window, max output,
    tool-call support, parallel-tool support, streaming, reasoning support,
    cache accounting fields, helper models for web summary and memory, and
    cost/subscription mode.
  - acceptance: unsupported model/provider/reasoning/helper combinations fail
    early with actionable diagnostics.
  - verification: modelinfo/config/provider tests.
  - status: completed 2026-07-01.
  - evidence: expanded `internal/modelinfo` from passive routing hints into an
    explicit capability policy with context window, max output, tool-call and
    parallel-tool support, streaming, reasoning modes, token/cache accounting
    fields, web-summary and memory helper models, and cost/subscription mode.
    Provider construction now validates model/provider/reasoning/output-cap
    requests before credential lookup or network setup, web-summary helpers
    validate their helper binding before provider creation, and model-based
    compaction carries its helper output cap into the provider binding. `config
    inspect` JSON and human output now expose the provider capability snapshot
    and validation warning.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/modelinfo
    ./internal/config ./internal/provider ./internal/credentials
    ./cmd/fast-agent-harness` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Capability.*|Test.*ProviderBinding.*|Test.*Provider.*Capability.*|Test.*Unsupported.*|Test.*Helper.*|TestConfigInspect.*'
    -count=1 ./internal/modelinfo ./internal/config ./internal/provider
    ./internal/credentials ./cmd/fast-agent-harness` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `b0885693bf0c4518e8284fd255b3044ce7b6a0da`.

- [x] SH-01.4 Record helper-model usage separately from main agent usage.
  - inspiration: Codex memory extraction model selection and Billy websum
    metrics.
  - target files: `internal/provider/web_summary.go`,
    `internal/tools/web_summary.go`, `internal/protocol`, `internal/trace`,
    `internal/telegrambot/render.go`, `internal/tui/status.go`.
  - acceptance: web summary, context compact summary, and future memory summary
    usage show as helper usage, not as main LLM turn count.
  - verification: web summary tests and trace summary tests.
  - status: completed 2026-07-01.
  - evidence: added first-class `provider.helper_usage` protocol events with
    run/call/attempt/compaction correlation. Model web summaries now emit
    helper usage before the final tool result, model-based context compaction
    emits helper usage after `context.compacted`, and web metadata carries
    model API input/output/cache-hit/cache-miss tokens separately from
    web-summary compression tokens. Shared client projection, `/context`,
    trace replay, TUI status, and Telegram footers now account helper model
    usage separately from main `provider.usage` turn totals while preserving
    legacy tool-result metadata as a replay fallback without double-counting.
    Extractive/direct web summaries remain websum compression only and do not
    inflate helper model usage.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/protocol
    ./internal/tools ./internal/provider ./internal/agent ./internal/clientux
    ./internal/clientux/projector ./internal/gatewayapi
    ./internal/gatewayclient ./internal/trace ./internal/tui
    ./internal/telegrambot` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Helper.*Usage.*|Test.*WebSummarizer.*|Test.*ToolSummary.*|TestReplayEventsAggregatesUsageCumulativeAndEventCounters|TestTUIAccountingMatchesClientUXProjector|TestRendererFooterShowsToolSummaryTokens|TestModelCompactionStrategyReplacesSummaryAndReportsModel|TestModelWebSummary.*'
    -count=1 ./internal/...` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `f5a95a62e01cd0a1823600a353dee9a26645ee6f`.

## Milestone 2 - Shared Tool UI, Output Refs, And Streaming Liveness (P0)

Goal: make tool display compact, shared, and live across TUI and Telegram, and
make silent stalls diagnosable.

- [x] SH-02.1 Upgrade `toolrender` into a tool display contract v2.
  - inspiration: Claude Code `Tool` UI traits and OpenCode truncation display.
  - target files: `internal/toolrender`, `internal/tui/transcript`,
    `internal/tui/render`, `internal/telegrambot/render.go`,
    `internal/protocol`.
  - acceptance: shared structs cover tool start/progress/result/error,
    collapse default, grouping, file/path/query/URL summary, duration, output
    ref, truncation state, and one-line preview.
  - acceptance: long URLs and JSON args are middle-truncated and never dominate
    Telegram/TUI progress bubbles.
  - verification: shared golden tests consumed by TUI and Telegram renderers.
  - status: completed 2026-07-01.
  - evidence: extended protocol `ToolCompact` with v2 display metadata for
    display version, grouping, collapse default, path/URL/query subjects, and
    one-line previews. Agent tool progress/result/output-ref compacts now
    derive or honor those fields from tool args and display metadata without
    importing presentation packages. Shared `toolrender` result summaries carry
    the v2 fields and render bounded subject lines with middle-truncated URLs,
    paths, and queries before falling back to preview text. TUI transcript
    projection applies compact collapse defaults for tool lifecycle cells, and
    Telegram rendering consumes the same compact line without leaking raw JSON
    payloads or long URL query secrets.
  - verification evidence:
    `/root/.local/go/bin/go test -count=1 ./internal/protocol
    ./internal/agent ./internal/toolrender ./internal/tui/transcript
    ./internal/telegrambot` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/toolrender
    ./internal/tui/transcript ./internal/tui/render ./internal/tui
    ./internal/telegrambot ./internal/protocol ./internal/clientux/projector
    ./internal/agent` passed;
    `/root/.local/go/bin/go test -run
    'Test.*ToolCompact.*|Test.*ToolRender.*|Test.*Compact.*URL.*|Test.*NoRaw.*JSON.*|Test.*Tool.*Display.*|Test.*Collapse.*|TestRendererUsesToolCompactResultSummary|TestProjectorApplyToolCompactLifecycleCells'
    -count=1 ./internal/...` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `0f2632074abbf82d5385cb01045e948fc676849b`.

- [x] SH-02.2 Audit output-ref behavior across resume/replay.
  - inspiration: OpenCode durable truncation directory and Codex rollout traces.
  - target files: `internal/tooloutput`, `internal/tools`,
    `internal/agent/transcript.go`, `internal/gateway/session_store.go`,
    `internal/trace`.
  - acceptance: large web/shell/fs/MCP outputs are stored out of band, replay
    never re-inlines the full body, summaries carry stable refs, and missing
    refs are visible as warnings instead of silent context bloat.
  - verification: tests with at least 500k chars of tool output and resume.
  - status: completed 2026-07-01.
  - evidence: kept JSONL/session history as the source of truth while making
    large artifact refs more diagnosable. The agent prompt now treats large
    shell, filesystem, diagnostics, and MCP outputs like web outputs: bounded
    previews plus lazy `output_ref` artifacts, read only when exact evidence is
    needed. Gateway stored-session inspection now emits structured
    `output_ref_warnings` with seq/run/tool/ref/reason details and lifts those
    into human warnings for missing paths, directories, size mismatches, and
    SHA-256 mismatches. Trace replay now counts output refs, bytes, missing
    refs, hash mismatches, and warning rows from durable
    `tool.output_ref_created` events. Regression coverage includes 500k+
    output-ref fixtures and proves resumed session history keeps only the
    bounded preview/ref pointer instead of re-inlining the full body.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Output.*Ref.*|Test.*Replay.*Output.*Ref.*|TestStoredSessionResumeKeepsLargeOutputRefPreviewAndWarnsMissingArtifact|TestGatewaySessionInspectorVerifiesOutputRefs|TestRunMessagesStoresLargeToolOutputAndSendsPreview|TestSystemPromptDocumentsTerminalSafeMarkdown'
    -count=1 ./internal/agent ./internal/gateway ./internal/trace` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/tooloutput
    ./internal/tools ./internal/agent ./internal/gateway ./internal/trace
    ./cmd/fast-agent-harness` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `a2b2beade759b408d3e0e99fdd94219523874a11`.

- [x] SH-02.3 Add stream liveness watchdog events.
  - inspiration: user-observed Telegram "stuck while still working" failure and
    Codex/OpenCode explicit stream lifecycle.
  - target files: `internal/provider`, `internal/agent/model_call.go`,
    `internal/protocol`, `internal/telegrambot/progress_stream.go`,
    `internal/tui/transcript_runtime.go`.
  - acceptance: if no visible model/tool/progress event arrives for a
    configured interval while a run is active, clients get a lightweight
    `still_running`/heartbeat event with elapsed time and current phase.
  - acceptance: heartbeat events do not pollute model transcript.
  - verification: fake provider stall test and Telegram/TUI projector tests.
  - status: completed 2026-07-01.
  - evidence: added typed `stream.still_running` protocol events carrying
    run/turn/step/tool correlation, current phase, elapsed time, idle time,
    interval, count, and a compact message. Agent runs now wrap the enriched
    event emitter with a run-scoped watchdog derived from
    `stream_idle_timeout_sec`, so silent model streams, long tool/hook phases,
    and other active-run gaps emit lightweight heartbeat progress without
    touching model-visible messages. Telegram renders the heartbeat as an
    updatable live-progress status row and event pulse label. TUI flushes the
    heartbeat immediately into the status line while deliberately excluding it
    from transcript projection.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Still.*Running.*|Test.*Liveness.*|TestRunMessagesEmitsStillRunningDuringProviderStall|TestRendererShowsStillRunningProgress|TestStillRunningEventUpdatesStatusWithoutTranscriptBlock'
    -count=1 ./internal/agent ./internal/telegrambot ./internal/tui` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/protocol
    ./internal/agent ./internal/telegrambot ./internal/tui ./internal/provider`
    passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `6a7a26b9ec91f1b3facee90b60485dd7dfe0dc77`.

- [x] SH-02.4 Add managed-process dashboard polish.
  - inspiration: Claude tool progress and OpenCode process/output refs.
  - target files: `internal/tools/shell_process.go`,
    `internal/tools/diagnostics.go`, `internal/toolrender`,
    `internal/telegrambot/commands.go`, `internal/tui/commands.go`.
  - acceptance: running background processes can be listed, tailed, stopped,
    and rendered with stable IDs, ports/URLs when detected, elapsed time, and
    output refs.
  - verification: shell process tests and command renderer tests.
  - status: completed 2026-07-01.
  - evidence: added a `shell_processes` process-dashboard tool over the
    existing Billy-owned background shell registry, while keeping
    `shell_output` as the bounded tail/read primitive and `shell_kill` as the
    scoped stop primitive. Process snapshots now carry stable shell ids,
    command/cwd/pid, running or exited state, elapsed time, retained output
    cursors, bounded tail preview, detected localhost URLs/ports from retained
    output, and the latest output-ref artifact created by `shell_output`.
    Gateway exposes the same read-only dashboard at `/v1/processes`, and TUI
    `/processes` plus Telegram `/processes` render the compact shared status.
    `toolrender` also has deterministic compact call labels for
    `shell_processes`.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Shell.*(Background|Output|Kill|Process|Dashboard|RegistryClose|MaxLive).*|TestPlanModeFiltersAndDeniesWriteExecuteExternalTools'
    -count=1 ./internal/tools` passed;
    `/root/.local/go/bin/go test -run
    'TestCallLine.*|Test.*ToolRender.*Shell.*|TestCallLineSnapshotsCommonTools'
    -count=1 ./internal/toolrender` passed;
    `/root/.local/go/bin/go test -run
    'TestGatewayManagedProcessesEndpointUsesSharedRegistry|TestGatewayToolsExposeMCPRegistry'
    -count=1 ./internal/gateway` passed;
    `/root/.local/go/bin/go test -run
    'TestProcessesCommandShowsGatewayDashboard|TestTelegramProcessesCommandShowsManagedProcessDashboard|TestGatewayClientProcessStatusReturnsDashboardText|TestTelegramCommandMetadataDrivesHelpAndBypass'
    -count=1 ./internal/tui ./internal/telegrambot` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/protocol
    ./internal/tools ./internal/toolrender ./internal/gatewayapi
    ./internal/gateway ./internal/gatewayclient ./internal/tui
    ./internal/telegrambot ./internal/agent` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `b7c0c5a169ab55aee1503d18deba60ed9128ad27`.

## Milestone 3 - Golden Replay And Adapter Parity (P0)

Goal: prove TUI, Telegram, CLI, gateway, and trace all agree on the same
canonical event stream.

- [x] SH-03.1 Create a canonical golden run bundle.
  - inspiration: Codex rollout/session exports and OpenCode replay tests.
  - target files: `internal/trace`, `internal/eventlog`,
    `internal/testkit`, `internal/clientux/projector`,
    `cmd/fast-agent-harness`.
  - acceptance: a fixture contains user messages, assistant streaming chunks,
    thinking, web summary, several tools, a large output ref, interruption,
    compaction threshold, and final usage.
  - acceptance: the bundle can be replayed without provider/network access.
  - verification: `go test -run 'Test.*Golden.*Trace|Test.*Replay.*'`.
  - status: completed 2026-07-01.
  - evidence: extended the existing canonical agent-loop JSONL fixture into a
    replay bundle with explicit offline replay metadata and the system/user
    messages used to seed the run. The fixture now covers assistant content
    streaming, reasoning/thinking, web-summary output with a 524k expected
    output-ref artifact, MCP output, an interrupted `shell_exec` cleanup path,
    compaction threshold/compaction events, final provider usage, and terminal
    run completion. Shared testkit helpers load the bundle without adding
    runtime imports, and trace/projector/TUI/Telegram golden tests replay the
    same durable events without provider or network access.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Golden.*Trace|Test.*Replay.*|TestGoldenRunBundleIncludesReplayInputs'
    -count=1 ./internal/testkit ./internal/trace
    ./internal/clientux/projector ./internal/tui ./internal/telegrambot`
    passed;
    `/root/.local/go/bin/go test -count=1 ./internal/testkit
    ./internal/trace ./internal/eventlog ./internal/clientux/projector
    ./internal/tui ./internal/telegrambot` passed;
    `/root/.local/go/bin/go test -run
    'Test.*Golden.*Trace|Test.*Replay.*|TestGoldenRunBundleIncludesReplayInputs'
    -count=1 ./cmd/fast-agent-harness` passed with no matching tests to run;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `eca9998743afa179deb89c4728f6519f9138d21a`.

- [x] SH-03.2 Add adapter parity snapshot tests.
  - inspiration: shared adapter event translation in OpenCode and Billy's
    existing projector.
  - target files: `internal/clientux/projector`, `internal/tui/transcript`,
    `internal/telegrambot/render.go`, `internal/toolrender`.
  - acceptance: the same golden bundle produces expected compact tool cells,
    status/context lines, final message body, and no stale previous-run tools
    in both Telegram and TUI projections.
  - verification: focused snapshot/golden tests.
  - status: completed 2026-07-01.
  - evidence: added golden-bundle parity assertions across the shared client
    projector, TUI transcript projector, Telegram renderer, and `toolrender`.
    The tests replay the same offline bundle and assert the final assistant
    body, compact web/MCP/shell tool lines, context threshold line, compaction
    cell, output-ref summary, interrupted shell cleanup, and stale previous-run
    tool exclusion. TUI transcript projection now suppresses raw `mcp_call`
    argument JSON in favor of the shared compact call line before appending the
    result.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Golden.*(Trace|Bundle|Project|Render|Parity).*|Test.*Adapter.*Parity.*|TestGoldenBundle.*Parity.*'
    -count=1 ./internal/clientux/projector ./internal/tui/transcript
    ./internal/telegrambot ./internal/toolrender` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/clientux/projector
    ./internal/tui/transcript ./internal/telegrambot ./internal/toolrender`
    passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `72e5f5cf55edb09d2fc7c51bde3f8d6e2844347b`.

- [x] SH-03.3 Add provider replay/fake-provider regression suite.
  - inspiration: OpenCode fake provider and Billy bench traces.
  - target files: `internal/provider`, `internal/agent`, `internal/bench`,
    `internal/testkit`.
  - acceptance: fake streams can simulate slow chunks, invalid tool args,
    partial JSON, provider retries, and cancellation; tests assert terminal
    events and transcript pairing.
  - verification: provider/agent package tests.
  - status: completed 2026-07-01.
  - evidence: added an isolated `internal/testkit/fakeprovider` scripted
    provider helper for fake provider streams with delayed chunks, terminal
    errors, repeated scripts, request capture, and deterministic cancellation
    start signals. Provider tests now exercise slow fake streams, partial tool
    JSON deltas, retry metadata, request capture, and cancellation through the
    public provider stream contract. Agent tests replay fake-provider streams
    that combine reasoning/content chunks, invalid tool arguments, partial JSON
    recovery, provider retry metadata, and cancellation; they assert terminal
    lifecycle events and validate provider-request transcript pairing before
    each model call.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Fake.*Provider.*|Test.*FakeProvider.*|Test.*Replay.*Fake.*|Test.*Cancellation.*Terminal.*'
    -count=1 ./internal/testkit/fakeprovider ./internal/provider
    ./internal/agent` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/testkit
    ./internal/testkit/fakeprovider ./internal/provider ./internal/agent`
    passed;
    `/root/.local/go/bin/go test -count=1 ./internal/bench` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `24335a14059ee84bf35d026edd5155d0ad630f44`.

P0 broad verification:

- [x] Run required P0 package suite:
  - `go test -count=1 ./internal/agent ./internal/provider ./internal/config ./internal/modelinfo ./internal/tools ./internal/toolrender ./internal/tooloutput ./internal/clientux ./internal/clientux/projector ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot ./internal/trace ./internal/eventlog`
  - status: completed 2026-07-01.
  - evidence: `/root/.local/go/bin/go test -count=1 ./internal/agent
    ./internal/provider ./internal/config ./internal/modelinfo ./internal/tools
    ./internal/toolrender ./internal/tooloutput ./internal/clientux
    ./internal/clientux/projector ./internal/gateway ./internal/gatewayclient
    ./internal/tui ./internal/telegrambot ./internal/trace ./internal/eventlog`
    passed on pushed `HEAD` `24335a14059ee84bf35d026edd5155d0ad630f44`.

- [x] Run required P0 targeted regex suite:
  - `go test -run 'Test.*Prompt.*|Test.*Cache.*Break.*|Test.*Context.*Report.*|Test.*Capability.*|Test.*Helper.*Usage.*|Test.*ToolRender.*|Test.*Output.*Ref.*|Test.*Replay.*|Test.*Resume.*|Test.*Liveness.*|Test.*Golden.*|Test.*Adapter.*Parity.*|Test.*Fake.*Provider.*' -count=1 ./internal/...`
  - status: completed 2026-07-01.
  - evidence: `/root/.local/go/bin/go test -run
    'Test.*Prompt.*|Test.*Cache.*Break.*|Test.*Context.*Report.*|Test.*Capability.*|Test.*Helper.*Usage.*|Test.*ToolRender.*|Test.*Output.*Ref.*|Test.*Replay.*|Test.*Resume.*|Test.*Liveness.*|Test.*Golden.*|Test.*Adapter.*Parity.*|Test.*Fake.*Provider.*'
    -count=1 ./internal/...` passed on pushed `HEAD`
    `24335a14059ee84bf35d026edd5155d0ad630f44`.
  - commit: `db97f9fa9e90c816a41f2e3ea68c54d025227c4c`.

## Milestone 4 - Manual Solo Memory MVP (P1)

Goal: add useful memory without background magic or prompt bloat.

- [x] SH-04.1 Add file-based memory store with small index and topic files.
  - inspiration: Claude Code memory taxonomy and Codex memory artifact layout.
  - target files: new `internal/memory`, `internal/config`,
    `internal/projectcontext`, `docs/memory-systems-research.md`.
  - acceptance: memory lives under Billyharness config/profile directories,
    has index entries with type/topic/summary/path, enforces file size caps,
    rejects path traversal, and injects only a small summary unless explicitly
    read.
  - verification: memory store tests.
  - status: completed 2026-07-01.
  - evidence: added `internal/memory` as a small file-backed memory loader over
    `$BILLYHARNESS_HOME/memory/MEMORY.md` and
    `$BILLYHARNESS_HOME/profiles/<profile>/memory/MEMORY.md`. Index entries
    use explicit `type`, `topic`, `summary`, and relative `path` fields; topic
    paths are normalized under the memory root, absolute paths and `..`
    traversal are rejected, index/topic/render byte caps are enforced, and
    prompt-like memory summaries are blocked before rendering. Initial agent
    messages now inject a frozen `# Memory context` user message after
    profile/SOUL and before project context/AGENTS only when memory files
    exist, and the prompt contains summaries/paths/cap flags without reading
    topic bodies. Prompt inventory/cache-break diagnostics, compaction
    protected-prefix handling, `/context` source buckets, config inspection,
    and architecture import guards now recognize the memory context.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Memory.*|TestInitialMessagesInjectMemory.*|TestCompactMessagesReportsProtectedPrefixPolicyAndCompactedBudget|TestSnapshotInstructionHashIncludesProtectedUserContext|TestPromptInventoryIsStableAndOmitsArbitraryUserText|TestContextStatusClassifiesSourcesAndThresholds'
    -count=1 ./internal/memory ./internal/config ./internal/agent
    ./internal/runstate ./internal/clientux` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/memory
    ./internal/projectcontext ./internal/tools ./internal/config
    ./internal/agent ./internal/runstate ./internal/clientux
    ./internal/architecture` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/architecture` passed.
  - commit: `ba176c47d5156f6a5fa0168dcdf3b6495ab8f62f`.

- [x] SH-04.2 Add manual memory commands and tools.
  - inspiration: Claude `/memory` UX but without React dialogs or auto writes.
  - target files: `internal/tools`, `internal/promptcommands`,
    `internal/tui/commands.go`, `internal/telegrambot/commands.go`,
    `cmd/fast-agent-harness`.
  - acceptance: commands/tools support list/search/read/add/replace/remove with
    preview and confirmation boundaries appropriate for TUI/Telegram.
  - verification: command/tool tests.
  - status: completed 2026-07-01.
  - evidence: added shared manual memory operations in `internal/memory` for
    list, search, read, add, replace, and remove over the existing home/profile
    file store. Add/replace/remove are preview-only unless `confirm=true`;
    topic paths remain rooted under the memory directory, summaries still pass
    prompt-like rejection, and no automatic extraction, database, vector index,
    or background write path was added. `internal/tools` now exposes read-only
    memory list/search/read tools and write-risk memory add/replace/remove
    tools through the existing policy and plan-mode filtering. CLI
    `fast-agent-harness memory ...`, TUI `/memory ...`, Telegram `/memory ...`,
    shared action metadata, and `toolrender` compact call labels use the same
    operation contract. Architecture import guards were updated for the new
    `memory` edges from tools, TUI, and Telegram.
  - verification evidence:
    `/root/.local/go/bin/go test -run
    'Test.*Memory.*|TestCallLineSnapshotsCommonTools|TestActionRegistryBacksSlashCommandsAndHelp|TestTelegramCommandMetadataDrivesHelpAndBypass'
    -count=1 ./internal/memory ./internal/tools ./internal/toolrender
    ./internal/tui ./internal/telegrambot ./cmd/fast-agent-harness
    ./internal/architecture` passed;
    `/root/.local/go/bin/go test -count=1 ./internal/memory ./internal/tools
    ./internal/toolrender ./internal/promptcommands ./internal/tui
    ./internal/telegrambot ./cmd/fast-agent-harness ./internal/architecture`
    passed.
  - commit: pending.

- [ ] SH-04.3 Defer automatic memory extraction behind explicit command.
  - inspiration: Codex/Claude extraction pipelines, rejected as default.
  - target files: docs first; code only after manual MVP is stable.
  - acceptance: no background memory writes run by default; a future
    `/memory extract` task has a clear design with helper model, budget, and
    review gate.
  - verification: config tests proving default disabled.
  - status: open.

## Milestone 5 - Interop Without Platform Bloat (P1)

Goal: import useful data from other agents and configs without becoming a
compatibility platform.

- [ ] SH-05.1 Add external session import/export diagnostics.
  - inspiration: Codex external-agent session importer.
  - target files: `internal/session`, `internal/trace`,
    `cmd/fast-agent-harness/sessions.go`.
  - acceptance: import can convert simple user/assistant JSONL or markdown
    transcripts into Billy messages/events with an explicit imported marker and
    approximate token counts.
  - acceptance: unsupported tool-call formats are reported, not guessed.
  - verification: import tests with Codex/OpenCode/Claude-style simple samples.
  - status: open.

- [ ] SH-05.2 Add MCP config migration diagnostics.
  - inspiration: Codex external MCP config migration tests.
  - target files: `internal/config/mcp.go`,
    `cmd/fast-agent-harness/config*`, docs.
  - acceptance: command scans known local config locations, reports compatible
    stdio/http MCP servers, redacts env values, and prints Billy config
    suggestions without auto-overwriting.
  - verification: config migration fixture tests.
  - status: open.

- [ ] SH-05.3 Unify command registry/search.
  - inspiration: OpenCode command registry.
  - target files: `internal/promptcommands`, `internal/mcpclient`,
    `internal/tui/commands.go`, `internal/telegrambot/commands.go`,
    `cmd/fast-agent-harness`.
  - acceptance: slash commands, local markdown prompt commands, MCP prompts,
    profiles, and built-in actions show through one registry with source,
    description, argument hints, and availability.
  - verification: command registry tests and TUI/Telegram menu tests.
  - status: open.

## Milestone 6 - UX Polish With Hard Boundaries (P1)

Goal: borrow the parts of mature TUIs that improve daily solo use without a UI
framework migration.

- [ ] SH-06.1 Add raw/rich transcript toggle parity for TUI and CLI export.
  - inspiration: Codex raw/rich modes and Claude selection reliability.
  - target files: `internal/tui/transcript`, `internal/tui/commands.go`,
    `cmd/fast-agent-harness/sessions.go`.
  - acceptance: raw mode preserves exact event text/refs for debugging; rich
    mode stays compact and readable; copy behavior is tested.
  - verification: transcript render tests.
  - status: open.

- [ ] SH-06.2 Harden selection and copy with semantic no-select regions.
  - inspiration: Claude Code terminal selection state.
  - target files: `internal/tui/selection`, `internal/tui/transcript`,
    `internal/tui/render`.
  - acceptance: status lines, hidden thinking markers, and collapsed tool chrome
    can be excluded from copy while visible highlight remains correct for ANSI
    and wide Unicode text.
  - verification: selection tests with Cyrillic, emoji, ANSI, tables, and
    scrolled content.
  - status: open.

- [ ] SH-06.3 Add compact command palette source labels and argument menus.
  - inspiration: OpenCode command hints and Billy's current slash-command UX.
  - target files: `internal/tui/commands.go`, `internal/promptcommands`,
    `internal/mcpclient`, `internal/telegrambot/commands.go`.
  - acceptance: choosing `/model`, `/reasoning`, `/theme`, `/profile`,
    `/memory`, or MCP prompt can complete arguments in one action; source labels
    are visible but not noisy.
  - verification: command menu tests.
  - status: open.

## Milestone 7 - Deferred Experiments (P2)

These are useful only after P0/P1 proves the base is stable.

- [-] SH-07.1 Early tool execution during streaming.
  - reason: valuable for latency, but dangerous until tool snapshots, transcript
    pairing, cancellation, and replay tests are extremely strong.
  - next action: design doc plus fake-provider benchmark first.

- [-] SH-07.2 Stateless subagent tool for local research.
  - reason: useful for solo deep dives, but easy to turn into a context and
    process explosion.
  - next action: define strict input/output budget, no durable queue, no hidden
    memory writes.

- [-] SH-07.3 LSP/IDE diagnostics.
  - reason: command diagnostics already exist; full IDE platform is not proven
    necessary.
  - next action: benchmark simple `go test`, `rg`, and compiler output parsing
    before adding LSP.

- [-] SH-07.4 SQLite/FTS or vector index.
  - reason: JSONL and file-based summaries are currently enough.
  - next action: only reconsider after measured JSONL/session search limits.

- [-] SH-07.5 ACP/app-server compatibility.
  - reason: not needed for one local owner.
  - next action: only build a tiny adapter if a concrete client requires it.

## Verification Matrix

Use focused tests for each task. Before closing each milestone, run the relevant
subset below.

- Prompt/provider/context:
  `go test -count=1 ./internal/instructions ./internal/projectcontext ./internal/runstate ./internal/provider ./internal/modelinfo ./internal/config ./internal/clientux ./internal/trace`
- Tool display/output refs:
  `go test -count=1 ./internal/toolrender ./internal/tooloutput ./internal/tools ./internal/tui ./internal/telegrambot ./internal/agent`
- Replay/adapters:
  `go test -count=1 ./internal/eventlog ./internal/trace ./internal/clientux/projector ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot`
- Memory:
  `go test -count=1 ./internal/memory ./internal/projectcontext ./internal/tools ./internal/config`
- Interop/config:
  `go test -count=1 ./internal/session ./internal/trace ./internal/config ./cmd/fast-agent-harness`
- Broad gate before marking P0 done:
  `go test -count=1 ./internal/agent ./internal/provider ./internal/config ./internal/modelinfo ./internal/tools ./internal/toolrender ./internal/tooloutput ./internal/clientux ./internal/clientux/projector ./internal/gateway ./internal/gatewayclient ./internal/tui ./internal/telegrambot ./internal/trace ./internal/eventlog`

If a package does not exist yet, create it only when the milestone reaches the
task that needs it.

## Completion Policy

- A task is done only when implementation, focused tests, and this document's
  status/evidence fields are updated.
- A task may be split if the split produces independently testable work.
- A task may be blocked only with exact command/error, concrete reason, and next
  action.
- Each completed or explicitly blocked implementation task should be committed
  separately when this roadmap is used as an active Codex goal.
- Do not mark P0 complete while any SH-01, SH-02, or SH-03 open item remains
  unimplemented or unblocked.
