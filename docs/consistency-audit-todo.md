# Billyharness Consistency Audit TODO

Status: planned.
Created: 2026-07-02.

This TODO tracks cross-surface mismatches where Billyharness can show one
thing in Telegram, another thing in the TUI, and a third thing in gateway
diagnostics. The immediate trigger was the Codex/OAuth context-window bug:
`gpt-5.5` was fixed to `256_000` tokens in model metadata, but surrounding
fallbacks, status paths, docs, and tests still contain enough legacy 1M
assumptions that the same class of bug can come back.

The goal is not a broad rewrite. Keep JSONL/session events as source of truth,
avoid stores or schedulers, and fix drift by making the existing projections,
formatters, and config resolution sharper.

## Source Inputs

- Completed UI projection audit from subagent `019f2246-0108-7e20-a3a3-4633237add69`.
- Local code audit of model metadata, config resolution, TUI status, Telegram
  rendering, clientux context reports, gateway status, tests, and docs.
- Existing source documents:
  - `/root/billyharness/docs/competitive-improvements-todo.md`
  - `/root/billyharness/docs/vision-skills-search-backends-todo.md`
  - `/root/billyharness/docs/context.md`
  - `/root/billyharness/docs/architecture.md`

## Status Legend

- `[ ]` open
- `[~]` partially implemented, needs verification or cleanup
- `[x]` done
- `[!]` blocked and needs explicit reason

## P0: Fix User-Visible Wrong Or Misleading State

### 1. Make model context windows a single runtime source of truth

- [x] Ensure every user-facing context-window value comes from
  `internal/modelinfo.Lookup(model).ContextWindowTokens` unless the user has an
  explicit context override.
- [x] Make explicit overrides visible as overrides, not silent derived model
  defaults.
- [x] Preserve `deepseek-v4-flash` and `deepseek-v4-pro` at `1_000_000`;
  preserve Codex/OAuth defaults currently encoded as:
  - `gpt-5.5`, `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.4-nano`: `256_000`
  - `gpt-5.5-pro`, `gpt-5.4-pro`: `400_000`
  - `gpt-5.3-codex-spark`: `128_000`
- [x] Add a focused test that `/status`, `/context`, TUI inline status, and
  `config inspect` all agree for `gpt-5.5`, `gpt-5.4-mini`,
  `gpt-5.3-codex-spark`, and both DeepSeek models.

Evidence:

- Implemented runtime context-window provenance through config/runtime limits,
  Telegram status options, gateway `/context` responses, and TUI inline status.
- Explicit context-window overrides from config/env/CLI/gateway now stay
  visible as `override`; stale settings/profile 1M values still re-derive for
  Codex models.
- Preserved explicit full model id `gpt-5.3-codex-spark` at 128k while keeping
  the `spark` shorthand subject to the profile's disable-spark preference.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/modelinfo ./internal/config ./internal/telegrambot ./internal/tui ./internal/gatewayclient ./cmd/fast-agent-harness`.

Files to inspect/change:

- `internal/modelinfo/modelinfo.go`
- `internal/config/defaults.go`
- `internal/config/resolved.go`
- `internal/telegrambot/context_window.go`
- `internal/telegrambot/status_html.go`
- `internal/tui/status.go`
- `internal/gatewayclient/client.go`
- `cmd/fast-agent-harness/config_cmd.go`

Tests:

- `go test -count=1 ./internal/modelinfo ./internal/config ./internal/telegrambot ./internal/tui ./internal/gatewayclient`

### 2. Separate selected model from active session runtime model

- [x] Telegram `/status` currently uses chat state/options for the selected
  model. `/context` uses gateway events for runtime model. Make the difference
  explicit:
  - `selected model`: what the next run will use.
  - `active runtime model`: what the current session/run actually used, if known.
- [x] TUI should expose the same distinction in `/config` or `/status` when a
  gateway session is attached.
- [x] If the active runtime is unknown, show `unknown` rather than guessing from
  defaults.

Evidence:

- Telegram `/status` now renders `selected model` from chat state/options and
  `active runtime model` from gateway `/v1/sessions/{id}/status` when a session
  is attached; unavailable runtime state is shown as `unknown`.
- TUI `/status` now renders `selected model` and `active runtime model`; the
  runtime model is projected from `session.status` events, preserving JSONL as
  source of truth.
- Tests cover changed selected model after an existing runtime model in
  Telegram and TUI, plus unknown runtime fallback.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/gateway ./internal/gatewayclient ./internal/telegrambot ./internal/tui`.

Files to inspect/change:

- `internal/telegrambot/status_html.go`
- `internal/telegrambot/commands.go`
- `internal/gatewayapi/types.go`
- `internal/gateway/session_events.go`
- `internal/tui/transcript_runtime.go`
- `internal/tui/status.go`

Tests:

- Add Telegram and TUI tests where chat state is changed after a session was
  created, then assert selected/runtime values do not collapse into one label.

### 3. Remove or quarantine legacy 1M fallbacks

- [x] Keep `1_000_000` only where it is the real DeepSeek/mock default, test
  fixture data, or a clearly named fallback for unknown models.
- [x] Rename ambiguous fallback constants such as Telegram/TUI
  `defaultContextWindowTokens` to make clear whether they mean
  `unknownModelFallback`, `deepSeekDefault`, or `legacySettingsFallback`.
- [x] Update docs/examples that still imply all profiles/models use a 1M
  context window.
- [x] Add a small hygiene test or script that fails on new ambiguous hardcoded
  context-window values in runtime paths.

Evidence:

- Runtime 1M compatibility values are now named as legacy settings or unknown
  model fallback; built-in DeepSeek runtime defaults derive from `modelinfo`.
- Built-in/profile examples no longer write `context_window_tokens = 1000000`
  by default, and `docs/profiles.md` tells users to omit context overrides
  unless deliberate.
- Added `internal/config/hygiene_test.go` to reject ambiguous
  `defaultContextWindowTokens`, stale `context_limit`, and unlabelled runtime
  hardcoded 1M context-window values.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/config ./internal/telegrambot ./internal/tui ./internal/trace`.

Known stale/ambiguous locations:

- `internal/config/defaults.go`
- `internal/config/profile.go`
- `internal/tui/settings.go`
- `internal/telegrambot/render.go`
- `internal/telegrambot/context_window.go`
- `docs/profiles.md`
- `internal/telegrambot/gateway_client_test.go`
- `internal/tui/interaction_status_test.go`
- `internal/trace/trace_test.go`

Tests:

- `go test -count=1 ./internal/config ./internal/telegrambot ./internal/tui ./internal/trace`

### 4. Make context compact thresholds derived and visible everywhere

- [x] Verify `ContextCompactTokens` is always re-derived or clamped after a
  model switch unless explicitly overridden.
- [x] Show compact threshold in `/status` or `/context` with both tokens and
  percent, so a Codex 153.6k threshold cannot be confused with DeepSeek 600k.
- [x] Add a regression test for switching from DeepSeek 1M to Codex 256k with
  stale settings containing `context_window_tokens=1000000` and
  `context_compact_tokens=600000`.

Evidence:

- Added compact-threshold provenance so stale settings are derived while real
  compact overrides stay labelled as overrides.
- Telegram `/status`, TUI inline status, and shared `/context` formatting now
  show compact threshold tokens plus percent.
- Regression coverage asserts stale settings for `gpt-5.5` derive
  `context_window_tokens=256000` and `context_compact_tokens=153600`.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/config ./internal/clientux ./internal/gatewayclient ./internal/telegrambot ./internal/tui ./cmd/fast-agent-harness`.

Files:

- `internal/config/defaults.go`
- `internal/config/resolved.go`
- `internal/clientux/context.go`
- `internal/gatewayclient/client.go`
- `internal/telegrambot/status_html.go`
- `internal/tui/status.go`

## P0: Fix Usage, Cache, And Context Semantics

### 5. Rename confusing token counters in final/status lines

- [x] Do not display cumulative provider input/output as if it were active
  context.
- [x] Keep the current cleaner status concept:
  - `ctx X/Y Z%`: active conversation context estimate.
  - `cache hit/miss`: provider billing/cache counters, can exceed active
    context and should be labeled as such.
  - `last in/out` or `last call` only if needed, never as the primary context
    metric.
- [x] Add a short `/context` explanation line when provider cache counters are
  larger than active context.

Evidence:

- Telegram final/live footers now label provider cache counters as
  `cache hit`/`miss` instead of a bare hit/miss pair next to context.
- TUI inline status now labels both `cache hit` and `cache miss`.
- Gateway `/context` labels cumulative provider counters as `provider usage`
  and `provider cache`, and adds an explanation when provider cache counters
  exceed active context.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/telegrambot ./internal/tui ./internal/gatewayclient`.

Files:

- `internal/telegrambot/render.go`
- `internal/tui/status.go`
- `internal/gatewayclient/client.go`
- `docs/context.md`

Tests:

- Add Telegram/TUI render tests where cache hit is greater than active context
  and assert the labels remain unambiguous.

### 6. Align tool-call counting semantics

- [x] Choose one semantic for all user-facing tool counters. Preferred:
  count `tool.call_requested` because it represents model-requested tool use.
- [x] Update `/context` to match the projector if needed; it currently counts
  `tool.call_started` while `clientux/projector` counts requested calls.
- [x] Make failed/permission-denied/skipped tool calls count consistently.

Evidence:

- `/context` now increments tool activity from `tool.call_requested`, matching
  the shared client projector used by Telegram and TUI.
- Added synthetic event replay coverage for requested-only, failed, aborted,
  and started-only tool lifecycle events across `/context`, the shared
  projector, Telegram final footer, and TUI inline status. Started-only events
  are visible as lifecycle state but do not inflate user-facing tool totals.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/clientux ./internal/clientux/projector ./internal/telegrambot ./internal/tui`.

Files:

- `internal/clientux/projector/projector.go`
- `internal/clientux/context.go`
- `internal/telegrambot/render.go`
- `internal/tui/transcript_runtime.go`

Tests:

- A synthetic event replay containing requested-only, started, failed, and
  aborted tool calls should produce identical counters in projector snapshot,
  `/context`, Telegram final footer, and TUI status.

### 7. Unify helper/web-summary usage accounting

- [x] Decide exact labels for helper work:
  - `websum in→out`: content compression done for web tools.
  - `sumapi`: provider/model tokens spent by helper summarization.
  - `api calls/cost`: external API backends such as Tavily/Exa when used.
- [x] TUI currently lacks some helper API call/cost visibility that Telegram
  already receives from `clientux/projector.Snapshot`.
- [x] Make `/context`, Telegram final footer, and TUI status use the same
  projected helper usage summary.

Evidence:

- `/context` now uses the same user-facing helper labels as the live clients:
  `websum`, `sumapi`, `helper API calls`, and `helper API cost`; legacy
  `provider_api_calls`, `provider_cost`, and `helper_api` labels are covered by
  a formatter regression test.
- TUI now projects `HelperAPICalls` and `HelperCostUSD` from the shared
  `clientux/projector.Snapshot`, includes them in local `/context`, and renders
  `helper API calls`/`helper API cost` in the inline status.
- Telegram final footer already had the target labels; gateway context tests now
  assert the same helper summary wording through the shared formatter.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/clientux ./internal/gatewayclient ./internal/telegrambot ./internal/tui`.

Files:

- `internal/clientux/projector/projector.go`
- `internal/clientux/context.go`
- `internal/telegrambot/render.go`
- `internal/tui/status.go`
- `internal/tui/transcript_runtime.go`

Tests:

- `go test -run 'Test.*Helper.*|Test.*WebSummary.*|Test.*Footer.*|Test.*Context.*' -count=1 ./internal/...`

### 8. Disambiguate cost modes

- [x] TUI `cost subscription` and Telegram `api cost` are different things.
  Make labels explicit:
  - `model cost`: metered main model estimate.
  - `subscription`: Codex/OAuth main model cost mode.
  - `helper API cost`: external backend/helper provider cost.
- [x] Verify Codex/OAuth with helper model calls does not display helper cost
  as the main model cost.
- [x] Verify DeepSeek metered mode includes cache-hit/cache-miss pricing only
  for provider-reported tokens.

Evidence:

- TUI main-model cost now renders as `model cost $...`, `model cost n/a`, or
  `subscription`; helper backend cost renders separately as
  `helper API cost`.
- `/context`, Telegram footer, and TUI status now use `helper API calls` and
  `helper API cost` so helper backend spend cannot be confused with main model
  cost.
- Provider tests verify Codex/OAuth helper summaries produce zero helper API
  cost while DeepSeek helper summaries use provider cache-hit/cache-miss pricing
  when those counters are reported, falling back to input pricing otherwise.
- Bench comparison reports label cost mode as `model cost metered`,
  `subscription`, or `model cost n/a` rather than `subscription cost`.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/gatewayclient ./internal/clientux ./internal/provider ./internal/bench`.
- Tests: `/root/.local/go/bin/go test -count=1 ./internal/telegrambot`.

Files:

- `internal/modelinfo/modelinfo.go`
- `internal/tui/status.go`
- `internal/telegrambot/render.go`
- `internal/provider/web_summary.go`
- `internal/bench/provider_compare.go`

## P0: Fix Per-Run Projection And Telegram/TUI Drift

### 9. Reset per-message tool progress, keep chat cumulative totals

- [ ] For every new Telegram input, tool progress must start empty for that run.
- [ ] Cumulative `agent turns` and `tools` may remain chat/session totals, but
  running tool rows must be only the current run.
- [ ] Final message should not carry old tool lines or old assistant text from a
  previous run.
- [ ] Add a regression test with two Telegram messages in one chat where the
  first run uses tools and the second run has no tools; the second live/final
  messages must not show first-run tools.

Files:

- `internal/telegrambot/runner.go`
- `internal/telegrambot/render.go`
- `internal/telegrambot/progress_stream.go`
- `internal/telegrambot/bot_test.go`
- `internal/telegrambot/runner_test.go`

### 10. Standardize interrupt/new-message behavior

- [ ] When a new Telegram message arrives while a run is active, cancel or
  interrupt the active run, wait for stale events to stop, and then admit the
  new input.
- [ ] Ensure late events from the interrupted run cannot update the new
  progress message.
- [ ] Include run id or input sequence in progress/update routing, not just
  chat/session id.
- [ ] Preserve `/cancel` as an explicit cancellation command.

Files:

- `internal/telegrambot/bot.go`
- `internal/telegrambot/state_runtime.go`
- `internal/telegrambot/runner.go`
- `internal/gateway/session_events.go`
- `internal/gateway/session_store.go`

Tests:

- Existing interrupt tests should be extended with late-success and late-delta
  events after cancellation.
- `go test -run 'Test.*Telegram.*Interrupt.*|Test.*Late.*|Test.*Stale.*' -count=1 ./internal/telegrambot ./internal/gateway`

### 11. Make streaming liveness visible and monotonic

- [ ] While a run is active, Telegram should continuously show:
  - typing indicator where Telegram allows it;
  - progress edit heartbeat;
  - elapsed time;
  - live tail text when assistant deltas arrive;
  - compact tool progress when tools are active.
- [ ] If no assistant deltas arrive for a while but tools are running, progress
  must still edit with elapsed time.
- [ ] If assistant text is long and truncated, keep the tail, not the head, so
  progress visibly moves.

Files:

- `internal/telegrambot/progress_runtime.go`
- `internal/telegrambot/progress_stream.go`
- `internal/telegrambot/render.go`
- `internal/telegrambot/rich_stream.go`
- `internal/tools/web.go`

Tests:

- Add fake-clock or short-interval tests for progress edits during tool-only
  waits and long assistant tails.

### 12. Centralize TUI/Telegram event presentation policy

- [ ] Define one event presentation policy for which lifecycle events affect:
  transcript, compact progress, status line, final footer, and `/context`.
- [ ] TUI and Telegram currently filter different subsets of run/tool/status
  events. Remove accidental differences while preserving client-specific style.
- [ ] Do not show raw low-level tool lifecycle noise in the TUI unless the user
  chooses full tool view.

Files:

- `internal/clientux/projector/projector.go`
- `internal/tui/transcript_runtime.go`
- `internal/tui/transcript/projector.go`
- `internal/telegrambot/render.go`
- `internal/toolrender/toolrender.go`

Tests:

- Golden event trace rendered through TUI and Telegram projectors should agree
  on counts and visible event categories.

## P1: Config, Auth, MCP, Vision, And Docs Consistency

### 13. Make config provenance visible in diagnostics

- [ ] `config inspect` already records derived values; extend user-facing
  status/diagnostics so model, provider, context window, compact threshold,
  helper model, and web backend have visible source labels when requested.
- [ ] Add a strict check that warns if a saved setting overrides model-derived
  context without an explicit source.

Files:

- `internal/config/resolved.go`
- `internal/config/summary.go`
- `internal/config/diagnostics.go`
- `cmd/fast-agent-harness/config_cmd.go`
- `cmd/fast-agent-harness/doctor.go`

### 14. Normalize profile metadata and docs

- [ ] Profile metadata currently can set `context_window_tokens`. Decide if
  profiles should be allowed to override model limits; if yes, label it loudly.
- [ ] Update built-in profile examples and docs to use `context_window_tokens`
  rather than stale `context_limit` wording.
- [ ] Add tests for profile model switch to Codex and DeepSeek with and without
  explicit context override.

Files:

- `internal/config/profile.go`
- `docs/profiles.md`
- `docs/setup.md`

### 15. Make MCP status reflect the same config used by runtime

- [ ] `/mcp` output should show the config path and allowed servers from the
  same runtime config used by the gateway.
- [ ] Ensure native web tools stay separate from MCP tools in status output.
- [ ] Verify only Telegram, Telegram Parilka, GitHub, and Context7 are loaded
  by default unless config explicitly changes it.

Files:

- `internal/tools/mcp.go`
- `internal/tui/actions.go`
- `internal/telegrambot/commands.go`
- `mcp.config.toml`
- `docs/mcp.md`

### 16. Align vision support across Codex, Telegram, and TUI

- [ ] Codex/OAuth models marked vision-capable should accept Telegram photos
  and TUI attachments consistently.
- [ ] DeepSeek text-only models should fail early with a clear message before
  sending unsupported image payloads to the provider.
- [ ] `/model` and `/status` should show capability hints from the same
  `modelinfo` source, not local strings.

Files:

- `internal/modelinfo/modelinfo.go`
- `internal/telegrambot/media.go`
- `internal/telegrambot/status_html.go`
- `internal/tui/actions.go`
- `internal/agent/model_call.go`

### 17. Redact and classify auth/provider status consistently

- [ ] DeepSeek API key status and Codex OAuth status should be redacted and
  classified the same way in `doctor`, Telegram `/auth`, TUI auth flows, and
  gateway `/v1/auth/status`.
- [ ] Cost mode should derive from active provider/model, not from whichever
  auth method was most recently configured.

Files:

- `internal/credentials`
- `internal/telegrambot/status_html.go`
- `internal/telegrambot/commands.go`
- `cmd/fast-agent-harness/doctor.go`
- `internal/modelinfo/modelinfo.go`

### 18. Keep web tool budgets hard and observable

- [ ] Verify `web_fetch`, `web_extract`, and `web_crawl` never inject raw large
  pages into the main agent context when summarization/output refs should be
  used.
- [ ] Surface per-run web summary metrics in `/context`, Telegram, and TUI.
- [ ] Add tests where a large page produces a short tool result, an output ref,
  and non-zero web summary metrics.

Files:

- `internal/tools/web.go`
- `internal/provider/web_summary.go`
- `internal/clientux/context.go`
- `internal/telegrambot/render.go`
- `internal/tui/status.go`

### 19. Service lifecycle and duplicate process checks

- [ ] `doctor` should detect duplicate gateway/Telegram processes or stale pid
  files where possible.
- [ ] Auto gateway discovery should use the same URL normalization everywhere.
- [ ] Docs should give one canonical command for foreground TUI and systemd
  gateway/Telegram operation.

Files:

- `cmd/fast-agent-harness/service_cmd.go`
- `cmd/fast-agent-harness/doctor.go`
- `internal/gateway/ready.go`
- `internal/gatewayclient/client.go`
- `docs/setup.md`
- `docs/telegram.md`

## P1: Tests And Hygiene Guards

### 20. Add cross-surface golden status tests

- [ ] Build a small canonical event trace containing:
  - run started;
  - assistant deltas before and after a tool call;
  - tool requested/started/finished;
  - provider usage update;
  - helper usage;
  - context threshold;
  - run completed.
- [ ] Replay it through:
  - `clientux/projector`;
  - Telegram renderer;
  - TUI transcript/status projection;
  - `/context` report builder.
- [ ] Assert counts, context labels, cache labels, tool summaries, and final
  text are compatible.

Candidate package:

- `internal/clientux/consistency_test.go` or package-specific tests if imports
  would cycle.

### 21. Add hardcoded-value hygiene tests

- [ ] Add a test or script to flag ambiguous hardcoded model limits, stale
  `context_limit`, and status labels that bypass shared formatters.
- [ ] Allow fixture/test data only with local comments or whitelist.

Candidate files:

- `internal/config/hygiene_test.go`
- `cmd/fast-agent-harness/doctor.go`
- `docs/context.md`

### 22. Centralize compact number and percent formatting

- [ ] `compactInt`, `compactNumber`, and context percent formatting exist in
  several packages with different precision. Consolidate where practical or
  document why client-specific variants remain.
- [ ] Keep style adapters thin: Telegram Markdown/HTML escaping remains local,
  terminal color/style remains local.

Files:

- `internal/telegrambot/render.go`
- `internal/tui/status.go`
- `internal/tui/transcript_runtime.go`
- `internal/tui/transcript/run_summary.go`
- `internal/gatewayclient/client.go`
- `internal/toolrender/toolrender.go`

### 23. Persist or replay full projected usage for restored TUI sessions

- [ ] Saved TUI sessions currently preserve some counters but can lose helper
  metrics and last context details.
- [ ] Either persist the full projected usage snapshot or rebuild it from
  replayed events on resume.
- [ ] Reopened TUI status should match gateway `/context` for the same session.

Files:

- `internal/tui/sessions.go`
- `internal/tui/transcript_runtime.go`
- `internal/clientux/projector/projector.go`

## Verification Gate

Focused package tests for this TODO:

```bash
go test -count=1 ./internal/modelinfo ./internal/config ./internal/gateway ./internal/gatewayclient ./internal/session ./internal/telegrambot ./internal/tui ./internal/clientux ./internal/clientux/projector ./internal/tools ./internal/toolrender ./internal/provider
```

Cross-cutting regression tests:

```bash
go test -run 'Test.*Context.*Window.*|Test.*Status.*Context.*|Test.*Selected.*Runtime.*|Test.*Cache.*Context.*|Test.*Tool.*Count.*|Test.*Helper.*Usage.*|Test.*Telegram.*Stale.*|Test.*Interrupt.*Late.*|Test.*Golden.*Status.*|Test.*Vision.*Capability.*' -count=1 ./internal/...
```

Full verification before marking this TODO complete:

```bash
go test -count=1 ./...
./bin/fast-agent-harness config inspect -json
./bin/fast-agent-harness doctor -strict -services=false -gateway=false
```

## Completion Criteria

- No user-facing status path can show Codex/OAuth `gpt-5.5` as 1M context
  unless an explicit user override says so and labels it as an override.
- Telegram `/status`, `/context`, final message footers, and TUI status use
  consistent labels for active context, cache hit/miss, helper/web summary
  tokens, cost mode, agent turns, and tools.
- New Telegram messages cannot display tool progress or assistant text from a
  previous run.
- New/changed tests cover the exact regressions above.
- Docs reflect the same terminology users see in the product.
