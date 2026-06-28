# Billyharness Master Implementation TODO

This document is the working execution plan for turning billyharness into the fast solo agent harness described in `docs/codex-research-roadmap.md`.

It is intentionally more operational than the roadmap. The roadmap explains why. This file says what to do, in what order, how to verify it, and when a task is actually done.

## Operating Rules

- Treat this file as the live goal board.
- Update checkboxes when work is completed and verified.
- Prefer small vertical slices that compile, test, commit, push, and keep gateway/Telegram usable.
- Do not copy proprietary or leaked source code. Borrow architecture ideas only.
- Keep billyharness a small Go harness, not a platform clone.
- Preserve the current fast-path UX: one binary, auto-discovered gateway, TUI, Telegram gateway, DeepSeek, Codex OAuth, permissive solo local mode.
- Every new runtime behavior must emit replayable JSONL events or be explicitly documented as UI-only.
- Every context-expanding feature must have output caps, refs, cache, or summaries by default.
- Every long-running or expensive feature must expose counters so the user can see what happened.

## Final Target

Billyharness should become:

- a fast Go agent runtime for DeepSeek and OpenAI/Codex OAuth;
- a TUI and Telegram-first harness with shared compact rendering;
- a JSONL-first replayable session system;
- a native web/search/extract/crawl agent with cheap context behavior;
- a tool/MCP gateway with lazy discovery and clear policy;
- a solo-user system that can run dangerous local tools by default but still records auditable events;
- a benchmarkable harness that can run long agent loops and compare providers.

## Definition Of Done For The Whole Goal

The goal is complete only when all of these are true:

- [ ] `go test -count=1 ./...` is green.
- [ ] Gateway starts from the binary without requiring manual `-gateway` UX in normal TUI usage.
- [ ] Telegram service starts and streams compact rich progress without flooding or stale full tool dumps.
- [ ] DeepSeek Flash/Pro and Codex OAuth provider paths still work.
- [ ] TUI and Telegram both consume the same typed event semantics for turns, steps, tools, compaction, usage, and summaries.
- [ ] Web fetch/extract/crawl do not dump large raw pages into the main loop by default.
- [ ] Context status shows active context, percentage, compaction status, and major context contributors.
- [ ] Tool lifecycle is keyed by stable ids and replayable from JSONL.
- [ ] MCP servers are configured by billyharness config, not inherited by accident.
- [x] Bench runs create replayable bundles and at least one 50-100 turn local loop can be executed.
- [ ] Documentation explains startup, dangerous permissions, Telegram setup, provider auth, MCP config, profiles, and benchmark commands.

## Current Baseline To Preserve

These features already exist or were previously completed and must not regress:

- [ ] Go CLI binary builds.
- [ ] Gateway service runs through systemd.
- [ ] Telegram service runs through systemd.
- [ ] Native tools include time, web search, web fetch, web crawl, fs list/read/search/write/mkdir, shell exec, MCP list/call.
- [ ] Dangerous local tools can be enabled for solo use.
- [ ] DeepSeek API key path works.
- [ ] Codex OAuth import/refresh path exists.
- [ ] TUI supports model/reasoning/theme/chat commands.
- [ ] TUI has slash popup and command argument picker.
- [ ] TUI supports mouse scroll and selection copy.
- [ ] TUI has light/dark theme support.
- [ ] Telegram bot supports allowed users.
- [ ] Telegram uses one progress message and compact tool rendering.
- [ ] Telegram status footer includes context and tool summary.
- [ ] Gateway JSONL persistence exists for sessions.
- [ ] Bench trace bundles exist with manifest/events/payload refs.
- [ ] Terminal-Bench export/import adapter exists.

Verification command for this section:

```sh
cd /root/billyharness
go test -count=1 ./...
systemctl --no-pager --full status billyharness-gateway.service billyharness-telegram.service
curl -fsS http://127.0.0.1:8765/health
```

## Phase 0: Master Plan And Guardrails

Purpose: create the working plan, prevent drift, and define execution discipline.

- [x] Create architecture research roadmap.
- [x] Add Claude Code / Codex / OpenCode comparison to the roadmap.
- [x] Create this master TODO document.
- [x] Add a short `docs/README.md` that links roadmap, master TODO, setup, auth, MCP, Telegram, and benchmarks.
- [x] Add a "work protocol" section to `README.md`: run tests, commit, push, restart services when runtime changes.
- [x] Add a small command or script that prints project health: git status, build status, service health, current provider/model, session dir, config path.

Acceptance:

- [x] The plan is discoverable from repository root.
- [x] New work can be mapped to a checkbox in this file.
- [x] No implementation work starts without knowing which phase and item it advances.

## Phase 1: Runtime Core

Purpose: turn the current loop into a real, replayable runtime without losing speed.

### 1.1 Runtime Model

- [x] Introduce explicit `Submission`, `Run`, `Turn`, and `Step` types.
- [x] Keep existing public API compatible while adding the internal model.
- [x] Add stable ids:
  - [x] `submission_id`
  - [x] `run_id`
  - [x] `turn_id`
  - [x] `step_id`
  - [x] `call_id`
  - [x] `attempt_id`
  - [x] `parent_step_id`
- [x] Add a per-turn immutable snapshot:
  - [x] provider id
  - [x] model id
  - [x] reasoning mode
  - [x] context budget
  - [x] tool snapshot hash
  - [x] MCP status snapshot
  - [x] profile/instruction hash
  - [x] dangerous permission mode
- [x] Add input queue semantics:
  - [x] immediate run if idle
  - [x] reject or queue while active, based on policy
  - [x] cancel active run
  - [x] steer/follow-up after current model step, later if needed

Acceptance:

- [x] Existing sessions still run from TUI, Telegram, and gateway.
- [x] Replay can reconstruct run/turn/step order.
- [x] Concurrent run behavior is deterministic and tested.

### 1.2 ToolOrchestrator

Purpose: centralize permission, attempt, retry, cancellation, audit, output refs, and telemetry.

- [x] Add `internal/toolorchestrator` or equivalent package only if it pays for itself.
- [x] Define orchestration lifecycle:
  - [x] prepare
  - [x] permission decision
  - [x] attempt started
  - [x] attempt finished
  - [x] retry decision
  - [x] finalize
  - [x] cancel/abort
- [x] Add structured events:
  - [x] `tool.call_requested`
  - [x] `tool.permission_requested`
  - [x] `tool.permission_decided`
  - [x] `tool.call_started`
  - [x] `tool.call_progress`
  - [x] `tool.call_finished`
  - [x] `tool.call_failed`
  - [x] `tool.call_aborted`
  - [x] `tool.output_ref_created`
- [x] Keep `tool.audit` as a derived/security event, not the only permission record.
- [x] Add attempt metadata:
  - [x] tool name
  - [x] args summary
  - [x] risk
  - [x] permission source
  - [x] start/end timestamps
  - [x] duration
  - [x] output bytes
  - [x] output token estimate
  - [x] truncation status
  - [x] output ref id/path/hash when applicable
- [x] Ensure parallel batches update each call by `call_id`.
- [x] Ensure cancellation stops in-flight local operations where possible and records abort events.

Acceptance:

- [x] Golden event test covers one successful safe tool.
- [x] Golden event test covers one denied dangerous tool.
- [x] Golden event test covers one parallel read-only batch.
- [x] Golden event test covers cancellation while a tool is active.
- [x] TUI and Telegram can still render compact tool lines.

### 1.3 Parallel Policy

- [x] Replace broad risk-based parallel assumptions with explicit fields:
  - [x] `parallel_policy`
  - [x] `idempotent`
  - [x] `requires_exclusive_workspace`
  - [x] `rate_limit_key`
  - [x] `cancellable`
  - [x] `max_concurrency`
- [x] Make read-only fs tools parallel safe.
- [x] Make web search/fetch/crawl parallel safe only through rate-limited buckets.
- [x] Treat shell/write/mkdir as exclusive unless explicitly allowed.
- [x] Treat MCP tools as unknown/exclusive unless config or server metadata says otherwise.
- [x] Add trace metadata for why a batch was or was not parallelized.

Acceptance:

- [x] Parallel tests cover safe grouping.
- [x] Exclusive tools break batches.
- [x] Rate-limited network tools do not exceed configured concurrency.

### 1.4 Runtime Event Envelope

- [x] Move stable ids to the event envelope, not only inside `Data`.
- [x] Add schema version to persisted events.
- [x] Add monotonic `seq` per session/run event stream.
- [x] Add `ts` and `duration_ms` where applicable.
- [x] Add `source`: `agent`, `gateway`, `tui`, `telegram`, `tool`, `provider`, `mcp`, `bench`.
- [x] Add replay validator for required ids by event type.

Acceptance:

- [x] Old events still load or have a documented migration fallback.
- [x] New events validate in tests.
- [x] Bench/replay output can count turns, steps, tool calls, retries, aborts, and compactions.

## Phase 2: Config, Profiles, Providers, Auth

Purpose: stop scattering config logic across env, CLI flags, TUI, gateway, and services.

### 2.1 ResolvedConfig

- [x] Add a resolved config model with provenance.
- [x] Define precedence:
  - [x] built-in defaults
  - [x] `$BILLYHARNESS_HOME/config.toml`
  - [x] project `.billyharness/config.toml`
  - [x] `.env`
  - [x] environment variables
  - [x] CLI flags
  - [x] gateway/TUI runtime overrides
- [x] For each resolved value record:
  - [x] value
  - [x] source
  - [x] source path/key
  - [x] redaction status
  - [x] validation warning/error
- [x] Add `config inspect` command.
- [x] Add gateway endpoint for sanitized config status.
- [x] Add TUI/Telegram command to show config summary.

Acceptance:

- [x] User can answer "why is this model selected?" from config inspect output.
- [x] Secrets are never printed raw.
- [x] Tests cover precedence and redaction.

### 2.2 Profiles

- [x] Keep `SOUL.md` as the persona/instruction body.
- [x] Add profile metadata file, likely `profile.toml`.
- [x] Profile fields:
  - [x] name
  - [x] provider
  - [x] model
  - [x] reasoning
  - [x] context limit
  - [x] web summary mode
  - [x] tool policy
  - [x] MCP allowlist
  - [x] instruction fragments
  - [x] cost budget hints
- [x] Add `/profile` support in TUI if missing or incomplete.
- [x] Add `/profile` support in Telegram if missing or incomplete.
- [x] Add profile persistence per session.
- [x] Add profile hash to events and trace bundles.

Acceptance:

- [x] Switching profile changes provider/model/reasoning/instructions consistently.
- [x] Existing `billy` profile remains default.
- [x] Sessions record which profile was active.

### 2.3 Provider Catalog

- [x] Add provider catalog entries:
  - [x] DeepSeek OpenAI-compatible
  - [x] Codex/OpenAI OAuth
  - [x] OpenAI-compatible custom provider
- [x] Add model metadata:
  - [x] context window
  - [x] reasoning modes
  - [x] tool-call support
  - [x] streaming support
  - [x] token accounting fields
  - [x] cache accounting fields
  - [x] default summary model
  - [x] pricing if known/configured
- [x] Add provider capability tests.
- [x] Add provider request metadata events:
  - [x] request id
  - [x] provider id
  - [x] model id
  - [x] retries
  - [x] first delta latency
  - [x] total latency
  - [x] usage fields

Acceptance:

- [x] Provider selection is data-driven where practical.
- [x] TUI/Telegram status line does not use confusing raw provider counters.
- [x] Context and cost displays are based on documented model metadata and provider usage.

### 2.4 AuthManager

- [x] Centralize API key and OAuth resolution.
- [ ] Support sources:
  - [x] `.env`
  - [x] environment
  - [x] imported Codex/OAuth credentials
  - [x] future keyring/file store
- [x] Add refresh serialization and status.
- [x] Add redacted auth inspect output.
- [x] Add gateway auth status endpoint.
- [x] Add TUI/Telegram setup menu for DeepSeek key and Codex OAuth status.

Acceptance:

- [x] DeepSeek key can be added once and reused.
- [x] Codex OAuth refresh does not race under concurrent requests.
- [x] Auth inspect shows enough to debug without leaking secrets.

## Phase 3: MCP And Extension Surface

Purpose: make MCP owned by billyharness, visible, lazy, safe, and easy to extend.

### 3.1 MCP Config Ownership

- [x] Keep billyharness MCP config independent from random external tool configs.
- [x] Document `$BILLYHARNESS_HOME/mcp.config.toml`.
- [x] Support curated built-ins:
  - [x] telegram
  - [x] telegram-parilka
  - [x] github
  - [x] context7
- [x] Show configured, connected, failed, disabled, and unsupported servers.
- [x] Show server transport, command/url, tool count, last error, restart count, reconnect backoff.
- [x] Ensure `/mcp` in TUI and Telegram use the same status model.

Acceptance:

- [x] User can see exactly which MCP servers are connected.
- [x] Failed MCP server does not poison all tools.
- [x] Reconnect behavior is tested.

### 3.2 Remote MCP

- [x] Decide MVP:
  - [ ] either implement streamable HTTP MCP with bearer/env headers;
  - [x] or reject URL MCP during config validation with a clear unsupported diagnostic.
- [ ] If implementing remote MCP:
  - [ ] support URL transport;
  - [ ] support headers from env;
  - [ ] support bearer token;
  - [ ] add timeout and retry;
  - [ ] add status events;
  - [ ] leave OAuth/DCR for later.

Acceptance:

- [x] URL MCP configs never silently fail at runtime.
- [x] Status explains what happened.

### 3.3 Tool Search And MCP Discovery

- [ ] Keep lazy MCP discovery.
- [ ] Do not expose hundreds of MCP tools directly in the first model prompt.
- [ ] Make `tool_search` return native tools plus discovered MCP tools.
- [ ] Add filters:
  - [ ] server
  - [ ] namespace
  - [ ] risk
  - [ ] query
  - [ ] include schema
- [ ] Add output budgets for tool schemas.
- [ ] Add metrics for discovery calls and schema tokens.

Acceptance:

- [ ] Large MCP inventories do not bloat context by default.
- [ ] The model can still discover and call a specific MCP tool.

### 3.4 Hooks v0

- [ ] Add local command hooks:
  - [ ] `session_start`
  - [ ] `before_tool`
  - [ ] `after_tool`
  - [ ] `mcp_status_change`
  - [ ] `provider_retry`
  - [ ] `session_done`
- [ ] Add timeout, env, cwd, redaction, and structured event payload.
- [ ] Hooks must be disabled or no-op by default unless configured.

Acceptance:

- [ ] Hook failure is reported but does not crash the harness unless configured as fatal.
- [ ] Hook output is capped.

### 3.5 Skills v0

- [ ] Add skill directories:
  - [ ] `$BILLYHARNESS_HOME/skills`
  - [ ] project `.billyharness/skills`
- [ ] Add `skill_list`.
- [ ] Add `skill_read`.
- [ ] Do not inject all skills into every prompt.
- [ ] Add optional compatibility reader for `.claude/skills` only as input, not as copied code.

Acceptance:

- [ ] Skills are loaded on demand.
- [ ] Skill content is bounded and audited.

## Phase 4: Context, Web Economy, Summaries

Purpose: stop web/tool output from exploding context and token use.

### 4.1 Web Output Contract

- [x] Define output classes:
  - [ ] tiny direct answer
  - [x] extractive summary
  - [x] model summary
  - [x] raw excerpt
  - [x] output ref
- [x] Default web fetch/extract/crawl to cheap extractive summaries.
- [x] Large raw pages must become output refs unless explicitly requested.
- [x] Add max chars, max tokens, and max links per tool.
- [ ] Add visible metrics:
  - [x] raw bytes fetched
  - [x] extracted chars
  - [x] summary chars
  - [x] estimated tokens saved
  - [ ] cache hit/miss
  - [x] summarizer model/provider
  - [x] summary cost if model-based

Acceptance:

- [x] A normal weather/news web fetch does not add tens or hundreds of thousands of tokens to context.
- [x] User can see whether model summarization was used.

### 4.2 External Summarizer

- [x] Keep free extractive summarizer as default.
- [x] Add optional model summarizer configured by provider profile.
- [ ] Recommended defaults:
  - [x] DeepSeek profile uses cheap DeepSeek model if available.
  - [x] OpenAI/Codex profile uses configured mini model, not expensive main model.
  - [ ] Do not use Spark if the profile disables it.
- [x] Summarizer calls must not enter the main conversation context as raw pages.
- [x] Add separate metrics:
  - [x] `websum_input_tokens`
  - [x] `websum_output_tokens`
  - [x] `websum_cost`
  - [x] `websum_cache_hit`
  - [x] `websum_model`

Acceptance:

- [x] Main loop context reflects summary, not raw fetched page.
- [x] Status footer can show web summary savings.

### 4.3 Web Cache

- [ ] Add cache key:
  - [ ] URL/query
  - [ ] extraction mode
  - [ ] max budget
  - [ ] summarizer config hash
  - [ ] freshness TTL
- [ ] Store sanitized fetch metadata.
- [ ] Add cache size limit and cleanup.
- [ ] Add metrics to TUI/Telegram.

Acceptance:

- [ ] Repeated fetches do not repeatedly consume summarizer/model tokens.
- [ ] Cache can be inspected and cleared.

### 4.4 Context Guardrails

- [ ] Track context contributors:
  - [ ] user messages
  - [ ] assistant messages
  - [ ] reasoning summaries
  - [ ] tool outputs
  - [ ] web summaries
  - [ ] MCP outputs
  - [ ] system/instructions
- [ ] Show active context as tokens and percent.
- [ ] Show top context contributors before compaction.
- [ ] Add threshold events at 50%, 70%, 85%, 95%.
- [ ] Add `/context` in TUI and Telegram.

Acceptance:

- [ ] User can understand why context grew.
- [ ] Cache hit/miss counters are not confused with active context.

### 4.5 Compaction

- [ ] Keep deterministic compaction default for speed.
- [ ] Add optional model summary compaction strategy.
- [ ] Add structured compaction report:
  - [ ] trigger
  - [ ] reason
  - [ ] tokens before
  - [ ] tokens after
  - [ ] cut range
  - [ ] protected prefix count
  - [ ] replacement id
  - [ ] summary model if used
- [ ] Preserve tool refs and important outputs.
- [ ] Add replay tests around compaction boundaries.

Acceptance:

- [ ] Approaching 600k tokens for DeepSeek triggers controlled compaction.
- [ ] Compaction never silently drops current active task state.

## Phase 5: TUI UX

Purpose: make the terminal feel like a serious agent UI rather than a pile of strings.

### 5.1 Typed Transcript Cells

- [ ] Introduce cell types:
  - [x] user
  - [x] assistant stream
  - [x] assistant final
  - [x] thinking/reasoning
  - [x] tool call
  - [ ] tool batch
  - [x] audit/security
  - [x] compaction
  - [x] MCP status
  - [ ] run summary
  - [x] error
- [ ] Each cell has:
  - [x] stable id
  - [x] turn id
  - [x] step id
  - [x] call id if tool-related
  - [x] raw copy text
  - [ ] rich terminal text
  - [ ] collapsed/expanded state
  - [x] render cache key
- [x] Keep old rendering path while migrating if needed.

Acceptance:

- [x] Tool result updates the right cell by `call_id`.
- [x] Parallel tools do not scramble UI output.

### 5.2 Streaming Controller

- [ ] Split streaming state:
  - [ ] raw markdown source
  - [ ] stable committed region
  - [ ] mutable live tail
  - [ ] table holdback
  - [ ] code fence holdback
  - [ ] final canonical render
- [ ] Reduce full transcript re-rendering.
- [ ] Keep scroll anchored when user is at bottom.
- [ ] Preserve selection during live updates as much as possible.

Acceptance:

- [ ] Tables do not flicker into broken markdown while streaming.
- [ ] Code fences do not render as broken blocks.
- [ ] Final output is clean markdown-supported terminal text.

### 5.3 Tool Presentation

- [ ] Default tool calls collapsed.
- [ ] One-line compact summary:
  - [ ] status
  - [ ] tool name
  - [ ] target file/url/query/server
  - [ ] duration
  - [ ] truncation/ref indicator
- [ ] Group context-gathering tools when possible.
- [ ] Expand/collapse:
  - [ ] by selected cell
  - [ ] all tools
  - [ ] current turn tools
  - [ ] errors only
- [ ] Keep `/toolview` modes.

Acceptance:

- [ ] A long multi-tool run remains readable.
- [ ] Full output is available on demand without dominating default view.

### 5.4 Status Line

- [ ] Make status priority-based and width-aware.
- [ ] Include:
  - [ ] run state
  - [ ] elapsed turn time
  - [ ] model
  - [ ] reasoning
  - [ ] access mode
  - [ ] active context tokens/percent
  - [ ] session turns/tools totals
  - [ ] web summary metrics when present
  - [ ] cached summary/cache status where meaningful
  - [ ] cost/subscription
  - [ ] chat/profile
- [ ] Remove confusing raw provider counters from default footer.
- [ ] Add detailed status view command.

Acceptance:

- [ ] Footer is useful at 80, 120, and 160 columns.
- [ ] User can understand context versus provider cache metrics.

### 5.5 Action Registry And Commands

- [ ] Add action registry:
  - [ ] id
  - [ ] title
  - [ ] category
  - [ ] enabled predicate
  - [ ] keybinding
  - [ ] slash alias
  - [ ] Telegram alias if applicable
  - [ ] runner
- [ ] Make slash popup read from action registry.
- [ ] Add command palette.
- [ ] Add argument selectors for commands requiring args.
- [ ] Enter on selected command should execute or open arg selector without requiring a redundant second Enter.

Acceptance:

- [ ] `/model`, `/reasoning`, `/theme`, `/profile`, `/toolview`, `/thinkview` feel consistent.
- [ ] Commands can be extended without editing multiple unrelated switch statements.

### 5.6 Semantic Copy

- [ ] Keep mouse selection copy.
- [ ] Add semantic copy:
  - [ ] selected cell
  - [ ] last assistant answer
  - [ ] raw tool output
  - [ ] full transcript
  - [ ] code block
  - [ ] command line
- [ ] Copy raw text without gutters, decorative bullets, ANSI, or UI chrome.
- [ ] Show visible selection highlight in both themes.

Acceptance:

- [ ] Visual selection matches copied content.
- [ ] Table/cell selection does not copy broken decorations unless explicitly raw UI copy.

## Phase 6: Telegram UX

Purpose: make Telegram a real gateway, not a second-class output dump.

### 6.1 Rich Message Rendering

- [ ] Use Telegram-supported Markdown/HTML correctly.
- [ ] Escape content based on selected parse mode.
- [ ] Support:
  - [ ] bold
  - [ ] italic
  - [ ] inline code
  - [ ] code blocks
  - [ ] links
  - [ ] blockquotes if supported by mode/client
  - [ ] lists
  - [ ] LaTeX-looking formulas as readable text/code where Telegram cannot render math natively
- [ ] Do not send raw unsupported terminal markdown.
- [ ] Add tests for escaping, links, code, Cyrillic, formulas, and tool summaries.

Acceptance:

- [ ] Telegram messages render without literal `<b>` or escaped junk.
- [ ] Tables are converted to readable Telegram-safe layout instead of broken terminal boxes.

### 6.2 Progress Message

- [x] Keep one editable progress message per active run.
- [x] Throttle edits to avoid Telegram rate limits.
- [x] Show:
  - [x] run state
  - [x] model/reasoning
  - [x] elapsed time
  - [x] compact current tool list
  - [x] current assistant delta tail
  - [x] context percent
  - [x] turn totals
- [x] Truncate from the beginning for long live progress so the end keeps updating.
- [x] Remove or finalize stale progress tool details when complete.

Acceptance:

- [x] Long tool runs show movement.
- [x] Completed answer does not include giant full tool JSON unless user asks.

### 6.3 Telegram Sessions And Users

- [ ] Confirm each allowed Telegram user has separate session state by chat/user.
- [ ] Document concurrency behavior when two users use the bot simultaneously.
- [ ] Add per-user defaults:
  - [ ] profile
  - [ ] provider/model
  - [ ] reasoning
  - [ ] current chat/session
- [ ] Add `/new`, `/resume`, `/fork`, `/status`, `/context`, `/mcp`, `/model`, `/reasoning`, `/profile`, `/cancel`.
- [ ] Add admin-only commands for auth/config if needed.

Acceptance:

- [ ] Two allowed users can run independent chats without corrupting each other.
- [ ] One user's cancel does not cancel another user's run.

### 6.4 Telegram Tool UX

- [ ] Reuse shared compact tool renderer.
- [ ] Show one-line tool summaries:
  - [ ] fs path
  - [ ] web query/url
  - [ ] MCP server/tool
  - [ ] shell command summary
  - [ ] duration/status
- [ ] Hide raw args/output by default.
- [ ] Add command to request detailed tool view for last run.

Acceptance:

- [ ] Telegram remains readable during 10+ tool calls.
- [ ] User can still inspect failures.

## Phase 7: Gateway And API

Purpose: make gateway the stable control plane for TUI, Telegram, tests, and future clients.

### 7.1 Gateway Autodiscovery

- [x] TUI should auto-discover gateway by default.
- [ ] If gateway is not running:
  - [ ] auto-start when safe; or
  - [x] show precise command/service status.
- [x] Remove need to pass `-gateway http://127.0.0.1:8765` in normal use.
- [x] Add port/config fallback.

Acceptance:

- [x] `./bin/fast-agent-harness tui` works in normal setup.
- [x] Connection refused errors explain exactly what to do.

### 7.2 API Surface

- [x] Stabilize endpoints:
  - [x] health
  - [x] sessions create/list/get
  - [x] run
  - [x] cancel
  - [x] subscribe/replay
  - [x] status
  - [x] config inspect
  - [x] auth status
  - [x] MCP status
  - [x] context status
  - [x] benchmarks
- [x] Add typed response structs and tests.
- [x] Add event replay cursor: `after_seq`.

Acceptance:

- [x] TUI and Telegram can reconnect and replay missed session events.
- [x] Gateway API does not expose secrets.

### 7.3 Service Management

- [x] Ensure systemd units are documented.
- [x] Ensure graceful shutdown drains active sessions or records aborts.
- [x] Add service health command.
- [x] Add log locations and useful journal commands to docs.

Acceptance:

- [x] Restart does not leave orphan gateway/Telegram processes.
- [x] User can debug service failures without guessing.

## Phase 8: Storage, Replay, Trace

Purpose: make every session and benchmark explainable after the fact.

### 8.1 Session Store

- [x] JSONL remains canonical.
- [x] Add or verify files:
  - [x] manifest
  - [x] history JSONL
  - [x] events JSONL
  - [x] payload refs
  - [x] config snapshot
  - [x] model/provider snapshot
  - [x] MCP snapshot
- [x] Add schema version.
- [x] Add migration/fallback for older sessions.
- [x] Add session inspector command.

Acceptance:

- [x] A session can be replayed without API access.
- [x] Old sessions do not crash the loader.

### 8.2 Trace Bundles

- [x] Include:
  - [x] manifest
  - [x] event stream
  - [x] payload refs
  - [x] result rows
  - [x] config snapshot
  - [x] provider/model metadata
  - [x] profile hash
  - [x] MCP status
  - [x] sanitized tool outputs or refs
- [x] Add verifier:
  - [x] event ids complete
  - [x] payload refs exist
  - [x] payload hashes match
  - [x] result aggregates match events
  - [x] no raw secrets

Acceptance:

- [x] Bench result can be audited from files only.

### 8.3 Optional Indexes

- [x] Add rebuildable indexes only after JSONL replay is stable.
- [ ] Possible indexes:
  - [x] session list
  - [ ] search
  - [ ] tool calls
  - [ ] cost/usage
  - [ ] errors
- [x] Index corruption must not destroy canonical session data.

Acceptance:

- [x] Deleting indexes and rebuilding restores same visible session list.

## Phase 9: Benchmarks And Performance

Purpose: prove the harness is fast and effective, not just nice to look at.

### 9.1 Local Long-Loop Bench

- [x] Create a 50-100 turn local agent benchmark.
- [x] Include app-building tasks and file-edit tasks.
- [x] Include web-search tasks with output caps.
- [x] Include MCP/tool discovery tasks.
- [x] Include cancellation/resume tests.
- [x] Record:
  - [x] success/failure
  - [x] total time
  - [x] first delta latency
  - [x] model latency p50/p95
  - [x] tool latency p50/p95
  - [x] parallel batch latency
  - [x] context growth
  - [x] compaction count
  - [x] web summary savings
  - [x] cost or subscription marker

Acceptance:

- [x] Benchmark runs locally from a documented command.
- [x] Output bundle passes replay verifier.

### 9.2 Provider Comparison

- [x] Compare DeepSeek Flash versus DeepSeek Pro.
- [x] Compare Codex/OpenAI OAuth path when available.
- [x] Track:
  - [x] quality outcome
  - [x] elapsed time
  - [x] tool correctness
  - [x] token/context growth
  - [x] cost/subscription
  - [x] failure modes
- [x] Keep provider-specific bugs in separate report.

Acceptance:

- [ ] User can decide which model to use for coding versus normal chat.

### 9.3 Performance Hotspots

- [ ] Profile TUI render on long transcript.
- [ ] Profile Telegram edit throttling.
- [ ] Profile web fetch/extract.
- [ ] Profile JSONL append/replay.
- [ ] Profile provider streaming.
- [ ] Add backpressure tests for event stream.

Acceptance:

- [ ] Long runs do not degrade into unusable UI.
- [ ] Streaming stays visibly alive.

## Phase 10: Documentation

Purpose: make the project usable after sleep, reboot, or a week away.

- [ ] Root `README.md` quickstart.
- [ ] `docs/setup.md`:
  - [x] build
  - [x] run gateway
  - [x] run TUI
  - [x] run Telegram
  - [x] systemd
- [ ] `docs/auth.md`:
  - [ ] DeepSeek key
  - [ ] Codex OAuth
  - [ ] config inspect
  - [ ] redaction
- [ ] `docs/mcp.md`:
  - [ ] config format
  - [ ] allowed servers
  - [ ] Telegram Parilka
  - [ ] GitHub
  - [ ] Context7
  - [ ] troubleshooting
- [ ] `docs/tui.md`:
  - [ ] commands
  - [ ] themes
  - [ ] model/reasoning
  - [ ] tool/thinking views
  - [ ] copy/selection
- [ ] `docs/telegram.md`:
  - [ ] bot token setup
  - [ ] allowed users
  - [ ] commands
  - [ ] sessions
  - [ ] throttling
- [x] `docs/benchmarks.md`:
  - [x] local bench
  - [x] Terminal-Bench adapter
  - [x] replay verifier
  - [x] provider comparison

Acceptance:

- [ ] A new shell on the server can start and test the system using docs only.

## Phase 11: Final Hardening

- [ ] Run full tests.
- [ ] Run race-sensitive tests where practical.
- [ ] Run benchmark smoke.
- [ ] Rebuild binary.
- [ ] Restart gateway service.
- [ ] Restart Telegram service.
- [ ] Health check gateway.
- [ ] Send a Telegram smoke prompt if allowed and safe.
- [ ] Run TUI smoke if terminal supports it.
- [ ] Verify git status clean.
- [ ] Commit and push all changes.

Final verification command group:

```sh
cd /root/billyharness
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
sudo systemctl restart billyharness-gateway.service billyharness-telegram.service
systemctl --no-pager --full status billyharness-gateway.service billyharness-telegram.service
curl -fsS http://127.0.0.1:8765/health
git status --short
```

## Recommended Execution Order

Do not do this as one giant rewrite. Use this order:

1. Phase 0 docs and health command.
2. Phase 1.2 ToolOrchestrator minimal slice.
3. Phase 1.4 event envelope ids and replay validation.
4. Phase 5.1 typed transcript cells keyed by `call_id`.
5. Phase 6.2 Telegram progress cleanup and beginning-truncation.
6. Phase 4.1 web output contract and metrics.
7. Phase 4.2 external summarizer metrics.
8. Phase 2.1 ResolvedConfig.
9. Phase 2.2 profiles.
10. Phase 2.3 provider catalog.
11. Phase 2.4 AuthManager.
12. Phase 3.1 MCP config ownership polish.
13. Phase 7 gateway API/autodiscovery polish.
14. Phase 8 replay/session export hardening.
15. Phase 9 benchmark loops.
16. Phase 10 docs.
17. Phase 11 final hardening.

## Per-Iteration Checklist

Before coding:

- [ ] Identify the phase and exact checkbox.
- [ ] Inspect current files instead of trusting memory.
- [ ] Check `git status --short`.
- [ ] Decide whether subagents help and split non-overlapping scopes if used.

During coding:

- [ ] Keep public behavior compatible unless the old behavior is actively harmful.
- [ ] Add or update focused tests.
- [ ] Keep events replayable.
- [ ] Keep output bounded.
- [ ] Do not leak secrets in logs, traces, Telegram, or TUI.

Before final response:

- [ ] Run relevant tests.
- [ ] Run `go test -count=1 ./...` when runtime/protocol/tool changes are broad.
- [ ] Build binary when CLI/runtime changes.
- [ ] Restart services when gateway/Telegram/runtime changes.
- [ ] Check service health if restarted.
- [ ] Update this TODO document if checkboxes are completed or new requirements appear.
- [ ] Commit and push completed coherent work.
- [ ] Report what changed, what was verified, and what remains.

## Known Strategic Non-Goals

- [ ] Do not implement a full marketplace until local plugin contracts are stable.
- [ ] Do not make SQLite canonical before JSONL replay is mature.
- [ ] Do not inject all MCP or skill schemas into every model request.
- [ ] Do not clone Codex app-server compatibility unless a concrete client needs it.
- [ ] Do not build enterprise/org policy unless solo usage stops being the main target.
- [ ] Do not optimize for theoretical multi-user SaaS at the cost of local speed.

## Next Immediate Slice

Start here after this document lands:

- [x] Implement minimal `ToolOrchestrator` around existing tool execution.
- [x] Add `attempt_id` and permission decision events.
- [x] Keep old compact renderer working.
- [x] Add tests for successful safe tool, denied dangerous tool, and parallel batch event order.
- [x] Run `go test -count=1 ./internal/agent ./internal/tools ./internal/gateway ./internal/telegrambot ./internal/tui`.
- [x] If green, run `go test -count=1 ./...`.
- [x] Rebuild and restart services only if runtime binary changed.
