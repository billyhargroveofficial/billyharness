# Billyharness Decomposition Next TODO

Source: 4-agent static review of the current codebase. Goal: fix real architectural and stability risks before adding more features.

## P0: Runtime Correctness First

- [x] Serialize runtime event emission.
  Files: `internal/protocol/envelope.go`, `internal/agent/agent.go`, `internal/agent/tool_attempt.go`, `internal/gateway/gateway.go`.
  Risk: parallel tool workers can emit concurrently, racing event seq and corrupting or interleaving NDJSON/SSE.
  Acceptance: one serialized event sink; strictly monotonic seq under parallel tools; `go test -race` clean for agent/gateway parallel-tool paths.
  - Implemented by serializing `protocol.EventEnricher.Emit` with a mutex that
    covers sequence assignment and downstream callback delivery.
  - Added a concurrent `EventEnricher` regression that asserts callbacks do not
    overlap and delivered seqs are exactly `1..N`.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/protocol`
    and `/root/.local/go/bin/go test -race ./internal/agent ./internal/gateway -run 'Parallel|Abort|SessionRun'`.

- [x] Fix compaction trigger drift after huge tool output.
  Files: `internal/agent/runtime_loop.go`, `internal/agent/compaction.go`.
  Risk: provider usage from a previous call can hide current transcript growth after tool output.
  Acceptance: compaction triggers from current estimated payload or current provider usage; add regression test for tool output crossing threshold.
  - `compactMessages` now triggers from the larger of current transcript
    estimated tokens and provider-observed prompt tokens.
  - Added `TestCompactMessagesUsesCurrentEstimateAfterHugeToolOutput` to prove a
    stale low provider usage value cannot hide a large tool result.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/protocol ./internal/agent`
    and `/root/.local/go/bin/go test -race ./internal/agent ./internal/gateway -run 'Parallel|Abort|SessionRun'`.

- [x] Fix gateway run sequence/status restore after restart.
  Files: `internal/gateway/session_store.go`, `internal/gateway/session_events.go`.
  Risk: restored sessions can reuse `run-1`, causing stale replay and duplicate run IDs.
  Acceptance: restart after one run, next run is `run-2`; persisted status/replay remains correct.
  - Restored sessions now replay persisted event records for the latest
    `session.status` and max `run_seq`, then apply that status to the in-memory
    session before accepting another run.
  - Extended the gateway persistence/restart regression to assert restored
    status has `run_seq=1` and the next run persists `run_seq=2` with a
    gateway status `run_id` ending in `:run-2`.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/gateway`
    and `/root/.local/go/bin/go test -race ./internal/agent ./internal/gateway -run 'Parallel|Abort|SessionRun'`.

- [x] Stop duplicate or unsequenced terminal failure events.
  Files: `internal/gateway/gateway.go`, `internal/agent/event_builder.go`.
  Risk: agent emits `run.failed`, gateway emits another raw failure without seq.
  Acceptance: exactly one terminal event per failed run; gateway only synthesizes failures for pre-agent setup or busy errors.
  - `streamEvents` now tracks whether a terminal `run.completed` or
    `run.failed` event has already been streamed and only synthesizes
    `run.failed` when the callback returns an error before a terminal event.
  - Added gateway stream regressions proving agent-emitted failures are not
    duplicated, setup errors still synthesize one failure, and late errors after
    completion do not append a failure.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/gateway -run 'TestStreamEvents|TestGatewaySessionStoreRestoresSessionAfterRestart|TestGatewaySessionEventsReplayAfterSeqAcrossRestart'`,
    `/root/.local/go/bin/go test -count=1 ./internal/gateway ./internal/agent`,
    and `/root/.local/go/bin/go test -race ./internal/agent ./internal/gateway -run 'Parallel|Abort|SessionRun'`.

- [x] Define and enforce the `Provider.Stream` contract.
  Files: `internal/provider/provider.go`, `internal/provider/stream.go`, `internal/agent/model_call.go`.
  Risk: callers assume event/error close ordering that providers do not document.
  Acceptance: documented channel contract; tests for close/error order; failed `model.call_finished` includes provider/model/status/attempt metadata when known.
  - Documented the provider stream contract: implementations stream events,
    close the event channel, then expose at most one terminal error before
    closing the error channel.
  - Added a shared provider stream runner used by Mock, DeepSeek, and Codex so
    production providers follow the same event/error ordering.
  - Provider terminal errors can now carry request metadata, and the agent model
    call collector merges that metadata into failed `model.call_finished`
    events without overriding streamed metadata.
  - Added provider close-order tests, DeepSeek HTTP-error metadata coverage, and
    an agent regression for failed `model.call_finished` provider/model/status/
    attempt metadata.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/provider -run 'TestProviderStreamRunner|TestDeepSeekHTTPErrorIsTypedAndRedacted|TestDeepSeekStreamRetriesRateLimitBeforeStreaming|TestCodexStream'`,
    `/root/.local/go/bin/go test -count=1 ./internal/agent -run 'TestRunMessagesEmitsRunFailedOnProviderError|TestRunMessagesEmitsProviderRetryHook|TestAgentRunLifecycleEvents'`,
    `/root/.local/go/bin/go test -count=1 ./internal/provider ./internal/agent ./internal/protocol`,
    and `/root/.local/go/bin/go test -race ./internal/agent ./internal/gateway -run 'Parallel|Abort|SessionRun'`.

- [x] Make cancellation and interruption replay-valid.
  Files: `internal/session/session.go`, `internal/gateway/session_events.go`, `internal/gateway/gateway.go`.
  Risk: cancellation can roll transcript back while event log keeps partial events; shutdown aborts can persist invalid failures.
  Acceptance: explicit rollback policy; tests for cancel during model stream, tool stream, and gateway shutdown abort.
  - Documented the session cancellation rollback policy in `Session.Run`:
    interrupted runs restore the pre-run transcript and discard the prompt plus
    any late runner messages, while callers keep the event stream terminal and
    replay-valid.
  - Gateway sessions now track the active agent run id, synthesize shutdown
    failures with that run id, and suppress duplicate later terminal events for
    the same run.
  - Added agent coverage for cancellation during model streaming, reused the
    existing active-tool cancellation lifecycle coverage, and tightened gateway
    shutdown abort coverage to replay the stored JSONL through lifecycle
    validation with exactly one `run.failed`.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/agent -run 'TestRunMessagesModelStreamCancellationIsLifecycleValid|TestRunMessagesRecordsAbortWhenActiveToolIsCanceled'`,
    `/root/.local/go/bin/go test -count=1 ./internal/gateway -run 'TestGatewayShutdownAbortRecordsActiveSessionFailure|TestGatewaySessionCancelEndpointCancelsActiveThread|TestStreamEvents'`,
    `/root/.local/go/bin/go test -count=1 ./internal/session -run 'TestCancel|TestRunQueuesFollowUpWhenPolicyAllows'`,
    `/root/.local/go/bin/go test -count=1 ./internal/session ./internal/gateway ./internal/agent ./internal/eventlog ./internal/protocol`,
    and `/root/.local/go/bin/go test -race ./internal/agent ./internal/gateway -run 'Parallel|Abort|SessionRun'`.

## P1: Telegram Stability

- [x] Decompose Telegram run lifecycle.
  Files: `internal/telegrambot/runner.go`, `internal/telegrambot/state_runtime.go`, `internal/telegrambot/delivery.go`.
  Extract phases: scope resolution, interrupt previous run, replay catch-up, live stream, gateway run, finalization.
  Acceptance: new message interrupts active run and executes the latest prompt; old tools/text never leak into new progress.
  - [x] Extract replay catch-up into `replayRunCatchup`, keeping missed
    gateway-event accounting and stale cursor dedupe out of the main message
    handler.
  - [x] Verified interruption/replay behavior with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestNewMessageInterruptsActiveRunAndRunsLatestPrompt|TestSupersededTelegramRunDoesNotRenderLateOldAnswer|TestTelegramReplayCatchupDoesNotLeakOldRunIntoNewProgress|TestTelegramReplaysMissedGatewayEventsBeforeRun|TestTelegramDropsReplayAndLiveEventsAtOrBeforeCursor|TestTelegramRunShowsTypingAndAnimatedWorkingPulse'`.
  - [x] Extract scope/default state resolution into `resolveRunState`, covering
    legacy state lookup, profile/model/reasoning defaults, owned session
    creation, and state persistence before replay/live run phases.
  - [x] Verified state-sensitive behavior with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestNewMessageInterruptsActiveRunAndRunsLatestPrompt|TestSupersededTelegramRunDoesNotRenderLateOldAnswer|TestTelegramReplayCatchupDoesNotLeakOldRunIntoNewProgress|TestTelegramStateSeparatesUsersInSameChat|TestTelegramResumeFiltersSessionsByOwner|TestTelegramFork'`.
  - [x] Extract interrupt previous run and gateway cancel handoff into
    `interruptActiveRunForInput`, keeping input sequence advancement, local
    context cancellation, gateway session cancellation, and interrupt logging
    outside the live run body.
  - [x] Verified interrupt behavior with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestNewMessageInterruptsActiveRunAndRunsLatestPrompt|TestSupersededTelegramRunDoesNotRenderLateOldAnswer|TestCancelCommandBypassesActiveRunLock|TestTelegramStateSeparatesUsersInSameChat'`.
  - [x] Extract live stream renderer/progress setup into
    `telegramLiveRunView`, which owns the placeholder message, renderer,
    tool-progress state, typing indicator, progress edit loop, missing-session
    renderer reset, and terminal stop/final-error handling.
  - [x] Verified live progress behavior with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestNewMessageInterruptsActiveRunAndRunsLatestPrompt|TestSupersededTelegramRunDoesNotRenderLateOldAnswer|TestTelegramRunShowsTypingAndAnimatedWorkingPulse|TestTelegramRunStreamsInlineToolProgress|TestTelegramReplayCatchupDoesNotLeakOldRunIntoNewProgress|TestTelegramRunLiveProgressLimit|TestTelegramRunCoalescesProgressEdits'`.
  - [x] Extract gateway run with missing-session retry into
    `runGatewaySessionWithRetry`, including owned session recreation, state
    reset, live-view reset callback, and retry against the new session id.
  - [x] Added `TestTelegramRunRecreatesMissingGatewaySessionAndRetries` and
    verified with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestTelegramRunRecreatesMissingGatewaySessionAndRetries|TestTelegramReplayCatchupDoesNotLeakOldRunIntoNewProgress|TestTelegramRunShowsTypingAndAnimatedWorkingPulse|TestTelegramRunStreamsInlineToolProgress|TestNewMessageInterruptsActiveRunAndRunsLatestPrompt|TestSupersededTelegramRunDoesNotRenderLateOldAnswer'`.
  - [x] Extract finalization/edit delivery into `deliverRunFinal`, keeping rich
    final output, fallback status edit, first final chunk edit, and additional
    chunk sends out of the main run handler.
  - [x] Verified final delivery and the complete Telegram package with
    `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestTelegramRunRecreatesMissingGatewaySessionAndRetries|TestTelegramRunShowsTypingAndAnimatedWorkingPulse|TestTelegramRunStreamsInlineToolProgress|TestTelegramReplayCatchupDoesNotLeakOldRunIntoNewProgress|TestTelegramRendererFinal|TestTelegramRich|TestSupersededTelegramRunDoesNotRenderLateOldAnswer'`
    and `/root/.local/go/bin/go test -count=1 ./internal/telegrambot`.

- [x] Harden per-user/per-chat isolation.
  Files: `internal/telegrambot/state_runtime.go`, `internal/telegrambot/session_owner.go`.
  Replace stringly chat keys with typed `ChatScope`.
  Acceptance: same chat with two allowed users has isolated cancel/resume/fork/new state; legacy ownerless sessions have documented migration behavior.
  - Added typed `ChatScope` with stable `Key` and `LegacyKey` methods, then
    routed message handling, command dispatch, cancel handoff, state resolution,
    session ownership, and owner visibility checks through that scope.
  - Kept legacy persisted key strings unchanged through compatibility wrappers
    (`chatKey`, `userChatKey`, `messageChatKey`) and added a key-format
    regression for chat/thread/user scopes.
  - Existing `docs/telegram.md` documents legacy chat-key fallback and
    ownerless legacy/solo session visibility for `/resume` and `/fork`.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestTelegramChatScopeKeysPreserveLegacyFormat|TestTelegramStateSeparatesUsersInSameChat|TestTelegramCancelIsScopedToUser|TestTelegramLegacyChatStateMigratesToUserKey|TestTelegramResumeFiltersSessionsByOwner|TestTelegramFork|TestNewMessageInterruptsActiveRunAndRunsLatestPrompt'`
    and `/root/.local/go/bin/go test -count=1 ./internal/telegrambot`.

- [x] Fix Telegram edit throttling and coalescing.
  Files: `internal/telegrambot/progress_runtime.go`, `internal/telegrambot/progress_stream.go`, `internal/telegrambot/client.go`.
  Risk: spinner-only heartbeats can burn edit quota.
  Acceptance: content-dirty edits separate from heartbeat edits; first/final flush forced; 429 backoff respected; typing indicator preserved.
  - Progress edit ticks now call the renderer in dirty-only mode; initial and
    stop/final flushes remain forced, so spinner-only heartbeat changes no
    longer consume edit quota.
  - Added `TestProgressEditsSkipHeartbeatOnlyTicks` to prove heartbeat-only
    ticks do not edit while final flush still does.
  - Existing client retry-after coverage continues to verify Telegram 429
    backoff, and live-run tests preserve typing/progress behavior.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestProgressEditsFakeClockCoalescesBurstAndFlushesFinal|TestProgressEditsSkipHeartbeatOnlyTicks|TestProgressEditsFakeClockKeepsUTF16Limit'`,
    `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestTelegramRunShowsTypingAndAnimatedWorkingPulse|TestTelegramRunStreamsInlineToolProgress|TestTelegramRunCoalescesProgressEdits|TestClientRetryAfterUsesFakeTimer'`,
    and `/root/.local/go/bin/go test -count=1 ./internal/telegrambot`.

- [x] Add a Telegram rich streaming abstraction.
  Files: `internal/telegrambot/render.go`, `internal/telegrambot/delivery.go`, `internal/telegrambot/markdown.go`.
  Acceptance: live progress, optional rich preview, and final rich output have separate limits and fallback behavior.
  - Added `RichStream` with separate live-preview and final-output limits.
    `LivePreview` is optional and returns empty before assistant content, while
    `FinalChunks` retains the existing `Working...` fallback.
  - Routed `Renderer.FinalRichMarkdownChunks` and final rich delivery through
    the new abstraction while keeping HTML fallback cleanup behavior unchanged.
  - Added rich stream limit/fallback tests and kept existing final rich markdown
    split/cleanup coverage.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestRichStream|TestRendererFinalRichMarkdown|TestFinishRichCleansFreshRichMessageBeforeHTMLFallback'`
    and `/root/.local/go/bin/go test -count=1 ./internal/telegrambot`.

- [x] Ensure assistant text between tool calls is rendered as separate progress segments.
  Files: `internal/telegrambot/render.go`, `internal/clientux/projector`, `internal/protocol`.
  Risk: multiple assistant deltas between tool batches collapse into one confusing block.
  Acceptance: text/tool/text/tool flows are visibly separated in Telegram progress and final output.
  - The shared projector now treats `tool.call_requested` as a boundary for
    subsequent assistant deltas, so providers that emit text/tool/text in one
    model call produce blank-line-separated assistant segments.
  - Added projector and Telegram renderer regressions for text/tool/text/tool
    flows, verifying live progress and final output preserve the visible
    separation while tool progress remains separate.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/clientux/projector -run 'TestProjectorSeparatesAssistantTextAcrossToolBoundaries|TestProjectorSeparatesAssistantTextAcrossModelTurns'`,
    `/root/.local/go/bin/go test -count=1 ./internal/telegrambot -run 'TestRendererSeparatesAssistantContentAcrossToolBoundaries|TestRendererSeparatesAssistantContentAcrossModelTurns|TestRendererUsesProjectedAssistantTextForFinalAndLiveMessages'`,
    `/root/.local/go/bin/go test -count=1 ./internal/clientux/projector`,
    and `/root/.local/go/bin/go test -count=1 ./internal/telegrambot`.

- [x] Review Telegram update admission durability.
  Files: `internal/telegrambot/poller.go`, `internal/telegrambot/runner.go`.
  Risk: updates are acknowledged before handling; process crash after ack can drop a user message.
  Acceptance: document current at-most-once behavior or add a durable admission queue.
  - Documented current at-most-once Telegram polling in `docs/telegram.md`,
    including the exact crash window: the next offset is persisted before the
    handler goroutine creates/updates gateway session state.
  - Added a poller comment at the `ackOffset` call so the code matches the
    documented no-durable-admission-queue policy.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/telegrambot`.

## P2: TUI Decomposition

- [x] Split `internal/tui/tui.go` below 1,500 LOC.
  Extract gateway/session HTTP flow, auth flow, status line, chat/session operations, and slash command palette.
  Acceptance: `tui.go` keeps Bubble Tea orchestration only: model state, `Init`, `Update`, `View`, and message routing.
  - Completed the first TUI split pass by extracting auth, status, chat/session
    operations, gateway session flow, slash commands, theme/style construction,
    mouse selection controller state, and transcript/block/rendering helpers.
  - Moved the remaining transcript/block/rendering helper tail into
    `internal/tui/transcript_runtime.go`; `internal/tui/tui.go` is now 1,249
    lines, below the 1,500-line budget.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'TestTUITranscript|TestTranscript|TestRenderBlock|TestRunSummary|TestTool|TestStatus|TestSlash|TestCommandPalette|TestChatCommands|TestAuth|TestGateway'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Extract gateway/session HTTP flow from TUI.
  Files/functions: `sessionReadyMsg`, `replayEventsMsg`, `createSessionCmd`, `replayGatewayEventsCmd`, `gatewayRunRequest`, `runGateway`, `fetchGatewayMessages*`, `gatewayJSON`, `gatewayRequest`.
  Acceptance: TUI gateway code lives in a focused file/client boundary; `lastGatewayEventSeq` replay behavior is unchanged.
  - Moved TUI gateway session creation, replay, run request construction, live
    gateway execution, session message fetches, and generic gateway HTTP helpers
    into `internal/tui/gateway_session.go`.
  - Kept replay cursor behavior unchanged by carrying `lastGatewayEventSeq`
    through the moved `replayGatewayEventsCmd` path; `tui.go` dropped to 3,383
    lines after this slice.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'Test(Gateway|ProfileSlashCommand|NewChat|ResumeChat)'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Extract auth flow from TUI.
  Files/functions: `authResultMsg`, `handleAuthCommand`, `cancelAuthInput`, `authSaveCmd`, `authCodexImportCmd`, `authStatusCmd`.
  Acceptance: secret input is not rendered or persisted accidentally; auth tests remain green.
  - Moved TUI auth result messages, slash-command auth handling, secret input
    cancellation, DeepSeek save, Codex import, auth status loading, and auth
    status formatting into `internal/tui/auth.go`.
  - `tui.go` now delegates auth behavior through the same `Model` methods and
    dropped to 3,799 lines.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'TestAuth|Test.*Auth|TestConfigCommandShowsSanitizedGatewaySummary'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Extract status/run-status rendering from TUI.
  Files/functions: `inlineStatusView`, `runStatusView`, `runStateText`, `spinner`, `contextText`, `costText`, `prices`.
  Acceptance: status line remains two-line, width-aware, and stable in light/dark themes.
  - Moved status and run-status helpers into `internal/tui/status.go`, keeping
    the same `Model` methods and behavior while reducing `tui.go` from 4,075
    to 3,945 lines.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'Test.*Status|Test.*Cost|Test.*Context|Test.*RunStatus'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Extract chat/session operations from TUI.
  Files/functions: `newChat`, `resumeChat`, `forkChat`, `applyChatSession`, `sessionsText`, `saveCurrentSession`.
  Acceptance: profile switch/new chat does not preserve stale gateway/session/accounting state.
  - Moved model-level chat/session operations into `internal/tui/sessions.go`,
    keeping gateway replay/create decisions at the same method boundaries while
    reducing `tui.go` to 3,544 lines.
  - Added a shared fresh-chat reset helper used by `/new` and `/profile`, plus
    regressions that stale gateway session ids, replay cursors, transcript
    blocks, usage counters, and per-run accounting do not survive a fresh chat.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'Test(NewChat|ProfileSlashCommand|ResumeChat|GatewaySession|GatewayReplay)'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Extract slash command palette from TUI.
  Files: `internal/tui/tui.go`, `internal/tui/actions.go`.
  Acceptance: `Update` delegates command handling; key behavior for `Tab`, `Esc`, `Enter`, and textarea input is unchanged.
  - Moved slash command types, filtering, argument completion, navigation,
    command dispatch, popup rendering, and popup height calculation into
    `internal/tui/commands.go`; `actions.go` remains the action registry.
  - `tui.go` now keeps only the update/send call sites for slash handling and
    dropped to 2,988 lines after this slice.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'Test(Slash|CommandPalette|ActionRegistry|ResizeDoesNotReserveHiddenSlashPopup|ChatCommands)'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Replace local TUI `block` as canonical transcript state.
  Files: `internal/tui/tui.go`, `internal/tui/transcript`.
  Acceptance: `transcript.Cell` is canonical; conversion helpers mostly disappear; collapsed state survives persistence.
  - Split after the first TUI size pass: `tui.go` is below budget, and the
    remaining transcript runtime now stores canonical `transcript.Cell` values
    directly instead of a local block adapter.
  - [x] Separate UI-only rich terminal render cache
    (`richTerminalText`/`richTerminalCacheKey`) from transcript cells, likely
    into a model-side cache keyed by stable cell id.
    - Removed the rich terminal cache fields from the local TUI `block`
      adapter and moved rendered rich text/cache keys into `Model.richRenderCache`
      keyed by stable cell id.
    - New/resumed chats now clear the UI-only cache, while encode/decode keeps
      persisted transcript cells free of render-cache state.
    - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'TestTranscriptCellsUseModelRichTerminalTextCache|TestRenderBlockCachedReturnsCacheWithoutMutatingModel|TestResizeWithoutWidthChangeDoesNotReflowTranscript|TestPrintableInputDoesNotReflowTranscript|TestTUITranscriptProjector'`
      and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.
  - [x] Change `Model.blocks` to `[]transcript.Cell` and remove the local
    `block` struct.
    - `Model.blocks` is now `[]transcript.Cell`; direct tests and render call
      sites use canonical cell field names.
    - The local `block` struct was removed from `internal/tui/tui.go`.
  - [x] Delete or shrink `transcriptCellFromBlock`, `blockFromTranscriptCell`,
    `transcriptCellsFromBlocks`, and `blocksFromTranscriptCells`.
    - Removed the identity conversion helpers and kept only
      `refreshedTranscriptCells`, which normalizes derived metadata on projected
      canonical cells before assigning them to model state.
  - [x] Keep collapsed/collapseSet persistence covered by the existing decode
    tests while changing the backing type.
    - Verified collapsed persistence through
      `TestCollapsedToolStateSurvivesPersistence` after the backing type change.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'TestTUITranscriptProjectionMatchesProjector|TestTranscriptCellsUseModelRichTerminalTextCache|TestRenderBlockCachedReturnsCacheWithoutMutatingModel|TestCollapsedToolStateSurvivesPersistence|TestToolBlocksRenderCodexActivityStyle|TestResumeChat'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Make transcript rendering pure.
  Files: `internal/tui/tui.go`, `internal/tui/render`.
  Acceptance: rendering does not mutate `Model`; cache updates are explicit; long transcript benchmarks do not regress materially.
  - Changed cached block rendering to return rendered text plus an explicit
    `render.CellCache`; `reflow` now applies returned cache entries to the
    model-owned rich render cache instead of mutating transcript block fields.
  - Added a regression proving `renderBlockCached` does not mutate model cache
    fields directly, while `reflow` applies the returned cache explicitly.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'TestTranscriptCellsUseModelRichTerminalTextCache|TestRenderBlockCachedReturnsCacheWithoutMutatingModel|TestResizeWithoutWidthChangeDoesNotReflowTranscript|TestPrintableInputDoesNotReflowTranscript'`,
    `/root/.local/go/bin/go test -run '^$' -bench 'BenchmarkTUIReflowLongTranscriptCached' -benchtime=1x ./internal/tui`,
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Persist and reuse `transcript.Projector`.
  Files: `internal/tui/tui.go`, `internal/tui/transcript`.
  Risk: rebuilding projector from blocks on every event can drift IDs after replay/resume.
  Acceptance: TUI transcript projection still matches `internal/tui/transcript.Projector`.
  - Added a persistent `*transcript.Projector` to the TUI model. Projected
    events now reuse it instead of rebuilding from `m.blocks` for every event.
  - TUI block changes that happen outside the projector mark it stale, and chat
    reset/resume paths explicitly rehydrate it from the current blocks. Run
    summary cells now use `Projector.ApplyRunSummary`.
  - Added a TUI regression proving projector reuse across projected events and
    rehydration after a manual status block, while preserving the existing
    projection-vs-projector equivalence test.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/transcript -run 'TestTUITranscriptProjector|TestTUITranscriptProjection|TestTranscriptCell|TestProjector|TestRunSummary|TestResumeChat'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Extract mouse selection controller.
  Files: `internal/tui/tui.go`, `internal/tui/selection`.
  Acceptance: ANSI-aware selection/copy tests live in `selection`; TUI only wires mouse/key events.
  - Added `selection.Controller` plus viewport/mouse lifecycle helpers to
    `internal/tui/selection`, with package coverage for begin/drag/release
    clamping and existing ANSI/copy behavior.
  - Replaced TUI-local `selecting`, `selectStart`, and `selectEnd` fields with
    the package controller; mouse click/drag/release handlers now delegate
    state transitions to `selection`.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui/selection`,
    `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'Test(TranscriptSelection|ReflowPreservesSelection|Mouse|CopySelection|ToolAndThinkingBlocksRender|CommandPalette)'`,
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Move TUI theme/style construction out of `tui.go`.
  Files: `internal/tui/tui.go`.
  Acceptance: `tui.go` no longer owns color/style construction.
  - Moved TUI theme palette data, `themeStyles`, `styles()`, and
    `newThemeStyles` into `internal/tui/theme.go`; `tui.go` now only consumes
    computed styles from view/layout/rendering paths.
  - `tui.go` dropped to 2,706 lines after the extraction.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'Test(DefaultsToDarkTheme|SlashCommands|LightThemeStatusLineUsesThemeBackground|InlineStatus|RunStatus|TranscriptSelectionHighlightBothThemes|TranscriptSelectionIsVisiblyHighlighted|ToolAndThinkingBlocksRender|ToolBlocksRenderCodexActivityStyle|CommandPalette)'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

- [x] Expand terminal markdown/table golden tests.
  Files: `internal/tui/render/markdown.go`, `internal/tui/render/*_test.go`.
  Acceptance: tables, links, code, wide graphemes, streaming table holdback, and 40/80/120 column widths remain stable.
  - Added render-package markdown goldens covering links, inline code,
    Markdown tables, wide graphemes, exact table output at 40/80/120 columns,
    long-table truncation width bounds, and live streaming table holdback until
    an explicit boundary.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tui/render -run 'TestRenderTerminalMarkdown|TestRenderAssistantMarkdown|TestStreamingMarkdownState|TestMarkdownTableRow'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tui ./internal/tui/render ./internal/tui/transcript ./internal/tui/selection ./internal/tui/runtimeclient`.

## P3: Tools, MCP, Web

- [x] Make MCP catalog ownership explicit.
  Files: `internal/tools/tools.go`, `internal/mcpclient/manager.go`, `internal/mcpclient/server.go`.
  Acceptance: registry subscribes to catalog changes; exposes catalog version/staleness; tool search/list cannot observe stale data silently.
  - Added a manager-owned `mcpclient.CatalogSnapshot` carrying catalog version,
    tools, instructions, and collisions; registry sync now mirrors one
    versioned manager snapshot instead of separately reading tools and
    instructions.
  - Registry construction subscribes to manager catalog changes and mirrors the
    changed catalog immediately; registry instructions and MCP tool snapshots
    are read under the same lock to keep background catalog updates race-safe.
  - `tool_search` and `mcp_list_tools` now include `mcp_catalog` version,
    tool count, stale flag, and collisions in JSON output and result metadata.
  - Added `TestRegistrySubscribesToMCPCatalogChanges`, proving a reconnecting
    manager catalog change updates registry search results before any explicit
    registry refresh path runs.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tools -run 'TestRegistrySubscribesToMCPCatalogChanges|TestMCPGatewayRefreshesCatalogAfterReconnect|TestMCPGatewayRefreshesCatalogAfterOptionalStartupFailure|TestMCPGatewayListsServerStatusesAndValidatesStdioCalls|TestToolSearchFindsNativeAndMCPTools'`,
    `/root/.local/go/bin/go test -count=1 ./internal/mcpclient -run 'TestStdioReconnectRefreshesCatalogAndEmitsChange|TestCatalog|TestToolName'`,
    and `/root/.local/go/bin/go test -count=1 ./internal/mcpclient ./internal/tools`.

- [x] Handle MCP server-side catalog changes.
  Files: `internal/mcpclient/jsonrpc.go`, `internal/mcpclient/server.go`, `internal/mcpclient/manager.go`.
  Acceptance: notifications such as tools/list changes trigger catalog refresh without deadlocks; old tools are rejected after refresh.
  - Stdio JSON-RPC requests now recognize server notifications with no response
    id and dispatch `notifications/tools/list_changed`/`tools/list_changed`
    without treating them as call responses.
  - Managed MCP servers refresh `tools/list` asynchronously after list-change
    notifications so the refresh waits for the active request lock instead of
    deadlocking inside the request reader.
  - Catalog refresh updates the managed server specs/status, emits a manager
    catalog change, and flows through the registry catalog listener so old tools
    disappear from search/list/call mirrors after refresh.
  - Added `TestStdioToolsListChangedNotificationRefreshesCatalog`, proving a
    server-side list-change notification replaces `mcp__fake__echo` with
    `mcp__fake__new_echo` without a reconnect.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/mcpclient -run 'TestStdioToolsListChangedNotificationRefreshesCatalog'`,
    `/root/.local/go/bin/go test -count=1 ./internal/mcpclient -run 'TestStdio(ReconnectRefreshesCatalogAndEmitsChange|ToolsListChangedNotificationRefreshesCatalog|ReconnectFailureBackoffIsDeterministic)|TestCatalog|TestToolName'`,
    and `/root/.local/go/bin/go test -count=1 ./internal/mcpclient ./internal/tools`.

- [x] Formalize web summary as a separate service outside the main loop.
  Files: `internal/tools/web_core.go`, `internal/provider/web_summary.go`, `internal/webtools`.
  Acceptance: summary has timeout, no tools, separate request metadata, concurrency bounds, cache participation, fallback, and telemetry.
  - Moved the tools-side model summary call boundary into
    `internal/tools/web_summary.go`; web compaction now calls a focused summary
    service instead of embedding summarizer request construction in
    `web_core.go`.
  - `webtools.SummaryRequest` now carries request id, tool name, timeout, and
    an explicit `AllowTools=false` contract for provider-backed web summaries.
  - The tools-side summary service applies the configured timeout around both
    queue wait and summarizer execution, and bounds concurrent model summaries
    with a small semaphore before calling the injected summarizer.
  - Existing compact output cache behavior, provider error fallback to
    extractive summaries, and `websum_*`/`tool_summary_*` telemetry remain
    covered; added a regression for request metadata, no-tools contract,
    timeout propagation, and concurrency gating.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tools -run 'TestModelWebSummarizer|TestModelWebSummaryServiceMetadataTimeoutAndConcurrency|TestWebSummaryExtractiveModeDoesNotNeedSummarizer|TestTinyDirectWebAnswerAvoidsSummaryBloatAndModelSummarizer'`,
    `/root/.local/go/bin/go test -count=1 ./internal/provider -run 'TestWebSummarizerUsesProviderAndRecordsUsage'`,
    and `/root/.local/go/bin/go test -count=1 ./internal/tools ./internal/provider ./internal/webtools ./internal/mcpclient`.

- [x] Split `internal/tools/web_core.go`.
  Target files: `web_fetch.go`, `web_crawl.go`, `web_compact.go`, `web_summary.go`, `web_metadata.go`, `web_html.go`.
  Acceptance: public JSON contracts for `web_search`, `web_fetch`, `web_extract`, and `web_crawl` remain unchanged.
  - Split the web tool internals by ownership:
    `web_core.go` now keeps orchestration for compact fetch/crawl results,
    `web_compact.go` owns compact DTOs and extractive text shaping,
    `web_fetch.go` owns public-safe HTTP fetch/search helpers,
    `web_crawl.go` owns crawl traversal, `web_html.go` owns HTML/link parsing,
    `web_metadata.go` owns output refs and metadata, and `web_summary.go` owns
    the model summary service boundary.
  - New line counts are `web_core.go` 218, `web_compact.go` 667,
    `web_crawl.go` 87, `web_fetch.go` 75, `web_html.go` 146,
    `web_metadata.go` 213, and `web_summary.go` 121.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tools -run 'Test(ParseSearchResults|Web|ModelWeb|TinyDirect|ToolSearchFindsNativeAndMCPTools)'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tools`.

- [x] Stabilize compact tool rendering contract.
  Files: `internal/toolrender`, `internal/telegrambot/render.go`, `internal/agent/tool_attempt.go`.
  Acceptance: Telegram and TUI consume the same bounded summary shape; large outputs use `output_ref`; raw payloads do not leak into progress views.
  - Added `toolrender.ResultSummary` and `ResultSummaryFor` as the shared
    bounded display contract for tool results. Success summaries are metadata
    only, while error summaries use the existing compact snippet limit.
  - Telegram progress rendering and TUI transcript projection now consume the
    same summary shape instead of duplicating result-line logic.
  - Large-output ownership stays in the agent/tool output path: successful
    renderer summaries surface `output_ref`, duration, cache, token, and byte
    metadata without embedding raw payloads in progress views.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/agent -run 'TestToolLargeOutputCreatesOutputRef|TestCompactionSummaryPreservesToolOutputRefs'`,
    `/root/.local/go/bin/go test -count=1 ./internal/toolrender ./internal/telegrambot ./internal/tui/transcript ./internal/agent -run 'Test(ResultKeyAndLineCompactsMetadata|ToolResultsDoNotRenderFullPayload|ToolProgress|Projector|OutputRef|ToolOutputRef|RunMessagesRecordsOutputRef)'`,
    and `/root/.local/go/bin/go test -count=1 ./internal/toolrender ./internal/telegrambot ./internal/tui/transcript ./internal/agent`.

- [x] Tighten output artifact ownership.
  Files: `internal/tooloutput`, `internal/tools/web_core.go`.
  Acceptance: typed artifact metadata, permission invariants, deletion/cache-miss behavior, and renderer summaries are covered by tests.
  - Added `tooloutput.ArtifactMetadata`, storage-owned output-ref metadata key
    constants, `StatMetadata`, and `AddMetadataForPath` so hash/stat
    diagnostics, plaintext/permission fields, and public map keys have one
    package owner.
  - Routed web metadata and managed agent tool-output metadata through the
    `tooloutput` boundary; `tool.output_ref.created` now reads storage-owned
    key constants when projecting event payload fields.
  - Added caller-side coverage for missing web artifacts recording
    `output_ref_hash_error` while preserving the advertised `output_ref`, and
    kept cache deletion behavior covered by the existing web cache lifecycle
    regression.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tooloutput`,
    `/root/.local/go/bin/go test -count=1 ./internal/tools -run 'TestWebCompactionStoresFullTextOutOfBand|TestWebOutputMetadataReportsMissingArtifact|TestWebCacheKeyAndLifecycle|TestCompactCrawlResultReturnsSingleOutputRef|TestWebToolGoldenJSONContracts'`,
    `/root/.local/go/bin/go test -count=1 ./internal/agent -run 'TestToolLargeOutputCreatesOutputRef|TestCompactionSummaryPreservesToolOutputRefs'`,
    `/root/.local/go/bin/go test -count=1 ./internal/toolrender -run 'TestResultKeyAndLineCompactsMetadata'`,
    and `/root/.local/go/bin/go test -count=1 ./internal/tooloutput ./internal/tools ./internal/agent ./internal/toolrender`.

- [x] Clarify static native tools vs dynamic MCP catalog.
  Files: `internal/tools/tools.go`, docs/API output.
  Acceptance: docs/API names make clear that model-visible specs are stable gateway tools, while MCP catalog is discovered through `tool_search`/`mcp_list_tools`.
  - Added `model_visible_tools` to `tool_search` and `mcp_list_tools`
    responses with `kind=static_gateway_tools`,
    `includes_dynamic_mcp_tools=false`, and the gateway discovery tools that
    expose MCP without injecting raw MCP specs.
  - Labeled `mcp_catalog` responses as `kind=dynamic_mcp_catalog` with
    `model_visible=false`, and mirrored those names into result metadata.
  - Updated `tool_search`, `mcp_list_tools`, and `mcp_call` descriptions plus
    README/MCP docs so `/v1/tools` is described as stable gateway specs while
    dynamic MCP tools are discovered and called lazily.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/tools -run 'TestLazyMCPGatewayHidesRawSpecsAndCanCallTool|TestToolSearchFindsNativeAndMCPTools|TestMCPGatewayListsServerStatusesAndValidatesStdioCalls|TestRegistrySubscribesToMCPCatalogChanges|TestMCPGatewayRefreshesCatalogAfterOptionalStartupFailure'`
    and `/root/.local/go/bin/go test -count=1 ./internal/tools`.

## P4: Hygiene, Guards, Tests

- [ ] Make repo commit-safe.
  Check deleted tracked files, untracked new packages, scripts, and docs.
  Acceptance: `git ls-files -d` is empty; all new source/docs/scripts are tracked or intentionally ignored.
  - [x] Removed deleted tracked-file evidence by leaving package-anchor files at
    `internal/tui/markdown.go` and `internal/tui/usage.go` after their logic
    moved into focused TUI runtime/render packages.
  - [x] Verified `git ls-files -d` is empty and placeholder compatibility with
    `/root/.local/go/bin/go test -count=1 ./internal/tui -run 'TestRenderTerminalMarkdown|TestRunSummary|TestStatus|TestTUITranscript'`.
  - [ ] Remaining commit-safety evidence: `git ls-files --others
    --exclude-standard` reports 105 untracked source/docs/scripts paths from
    the decomposition work. These need staging/tracking or an explicit ignore
    decision before this item is complete; no runtime artifacts are included in
    that count.

- [x] Promote size/import hygiene into tests.
  Files: `internal/architecture/architecture_test.go`, `cmd/fast-agent-harness/hygiene.go`.
  Acceptance: over-budget files fail unless explicitly allowlisted; missing tracked Go files fail strict/test mode.
  - Strict hygiene now fails on missing tracked Go files as well as
    unallowlisted over-budget files.
  - `fast-agent-harness hygiene` reads the file-size exception table from
    `docs/architecture.md`, reports documented over-budget files separately as
    allowed large source files, and keeps strict mode green for those explicit
    exceptions.
  - Added command tests for documented large-file exceptions and missing
    tracked Go files.
  - Verified with `/root/.local/go/bin/go test -count=1 ./cmd/fast-agent-harness -run 'TestHygiene'`,
    `/root/.local/go/bin/go test -count=1 ./internal/architecture ./cmd/fast-agent-harness`,
    and `/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict -repo /root/billyharness`.

- [x] Make architecture guard data-driven from `docs/architecture.md`.
  Acceptance: every package boundary row is enforced; docs and tests cannot drift silently.
  - `internal/architecture` now parses the `docs/architecture.md` package map
    and enforces direct internal imports from each row's "Allowed internal
    imports" column instead of duplicating hand-coded package rules.
  - The guard also fails when `go list ./internal/...` finds a package missing
    from the map or when the map names a package that no longer exists.
  - Updated `docs/architecture.md` for real package-map drift found by the new
    guard: added `internal/testkit`, `internal/tools/discovery`, the
    `tools -> tools/discovery` edge, `gateway -> modelinfo`, and
    `tui -> clientux/projector`.
  - Verified with `/root/.local/go/bin/go test -count=1 ./internal/architecture`,
    `/root/.local/go/bin/go test -count=1 ./cmd/fast-agent-harness -run 'TestHygiene'`,
    `/root/.local/go/bin/go test -count=1 ./internal/architecture ./cmd/fast-agent-harness`,
    and `/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict -repo /root/billyharness`.

- [ ] Split huge tests by ownership.
  Files: `internal/tui/tui_test.go`, `internal/tools/tools_test.go`, `internal/telegrambot/bot_test.go`, `internal/agent/agent_test.go`, `internal/gateway/gateway_test.go`.
  Acceptance: no `_test.go` over 1,200 LOC unless explicitly allowlisted.
  - [x] Split `internal/tools/tools_test.go` into focused tools, web, and MCP
    test files:
    `internal/tools/tools_test.go` 641 LOC,
    `internal/tools/web_test.go` 767 LOC,
    `internal/tools/mcp_test.go` 838 LOC.
    Removed the stale `internal/tools/tools_test.go` size exception from
    `docs/architecture.md`.
  - [x] Split `internal/agent/agent_test.go` into focused agent, compaction,
    and tool-attempt test files:
    `internal/agent/agent_test.go` 713 LOC,
    `internal/agent/compaction_test.go` 420 LOC,
    `internal/agent/tool_attempt_test.go` 988 LOC.
    Removed the stale `internal/agent/agent_test.go` size exception from
    `docs/architecture.md`.
  - [ ] Remaining over-budget test files:
    `internal/tui/tui_test.go` 2,896 LOC,
    `internal/telegrambot/bot_test.go` 2,074 LOC,
    `internal/gateway/gateway_test.go` 1,904 LOC.
  - Verified current split work with `/root/.local/go/bin/go test -count=1 ./internal/tools`,
    `/root/.local/go/bin/go test -count=1 ./internal/agent`,
    `/root/.local/go/bin/go test -count=1 ./internal/agent ./internal/tools ./internal/architecture ./cmd/fast-agent-harness`,
    and `/root/.local/go/bin/go run ./cmd/fast-agent-harness hygiene -strict -repo /root/billyharness`.

- [ ] Finish config decomposition.
  Files: `internal/config/config.go`, `internal/config/resolved.go`.
  Acceptance: config responsibilities split into types/defaults/env-dotenv/MCP/hooks/profile/resolution; projection tests prove defensive copies and normalization.

- [ ] Replace full-config constructors with settings constructors.
  Files: `internal/agent`, `internal/tools`, `internal/gateway`, `internal/bench`.
  Acceptance: runtime paths compose projections/settings; full `config.Config` remains mostly at CLI/config-resolution edges.

- [ ] Decompose CLI main.
  Files: `cmd/fast-agent-harness/main.go`.
  Acceptance: `main.go` becomes thin command dispatch; command files are grouped by domain.

- [ ] Normalize build commands in docs and doctor.
  Files: `README.md`, `docs/setup.md`, `scripts/verify-deps.sh`, `cmd/fast-agent-harness/doctor.go`.
  Acceptance: docs use `/root/.local/go/bin/go` or `GO_BIN`; doctor/hygiene flow is documented.

- [ ] Update docs to reflect live architecture state.
  Files: `docs/architecture.md`, `docs/architecture-decomposition-todo.md`, `docs/decomposition-next-todo.md`.
  Acceptance: first-pass TODO stays historical; this file owns active work; current line counts and package boundaries are accurate.

## Verification

- [ ] `git status --short --branch`
- [ ] `git ls-files -d`
- [ ] `/root/.local/go/bin/go test -race ./internal/agent ./internal/gateway -run 'Parallel|Abort|SessionRun'`
- [ ] `/root/.local/go/bin/go test -count=1 ./internal/telegrambot ./internal/tui/... ./internal/tools/...`
- [ ] `/root/.local/go/bin/go test -count=1 ./internal/architecture ./internal/config ./internal/provider ./internal/eventlog ./internal/protocol`
- [ ] `/root/.local/go/bin/go test -count=1 ./...`
- [ ] `/root/.local/go/bin/go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`
- [ ] `./bin/fast-agent-harness hygiene -strict -repo /root/billyharness`
- [ ] `./bin/fast-agent-harness doctor -strict -services=false -gateway=false`
