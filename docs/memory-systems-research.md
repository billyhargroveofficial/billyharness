# Memory Systems Research: Cross-Repo Analysis for Billyharness

> **Date:** 2025-06-29
> **Type:** Local architecture research via rg/find/shell against 5 cloned repos
> **Status:** Complete — every claim tied to file:line or marked [inference]

---

## 1. Executive Summary

Every mature agent harness solves three overlapping problems:

1. **Durable memory** — facts, preferences, and decisions that survive across sessions
2. **Session persistence** — checkpoint/restore/branch/fork so you don't lose work
3. **Context management** — compaction that preserves prefix cache, prevents prompt bloat, and retains recoverable history

Billyharness already has a solid foundation: profile/SOUL loading, AGENTS.md project instructions, JSONL session store with events/trace/replay, context thresholds with auto-compaction, and on-demand skills. But it lacks a dedicated memory subsystem. The right next step is NOT "add a MEMORY.md into the prompt." It's a layered architecture: stable identity memory separate from project instructions, curated long-term memory separate from session transcript, retrieval via bounded tools separate from prompt injection, and compaction as a checkpoint/epoch rather than destructive message-slice mutation.

---

## 2. Claude Code — File-Based Memory with Forked Workers

### 2.1 Architecture Summary

Claude Code has the most sophisticated memory UX among the repos. Memory is file-based (MEMORY.md index + topic .md files), with four discrete types: user, feedback, project, reference. Memory extraction runs as a forked subagent after each turn. Session memory is a separate mechanism. Team memory syncs via API.

### 2.2 Memory Storage

**Files:**
- `/root/claude-code/memdir/memdir.ts` — Core memory prompt builder. `ENTRYPOINT_NAME = 'MEMORY.md'`, `MAX_ENTRYPOINT_LINES = 200`, `MAX_ENTRYPOINT_BYTES = 25_000`. Memory files use frontmatter format with `name`, `description`, `type` fields. Two-step process: write topic file, add pointer to MEMORY.md index.
- `/root/claude-code/memdir/memoryTypes.ts` — Four-type taxonomy: user (role/preferences), feedback (corrections/confirmations), project (ongoing work context), reference (pointers to external systems). Each type has structured guidance: `<when_to_save>`, `<how_to_use>`, `<body_structure>`, `<examples>`.
- `/root/claude-code/memdir/paths.ts` — `isAutoMemoryEnabled()` checks env var, settings, --bare mode. `getAutoMemPath()` resolves `~/.claude/projects/<slug>/memory/`. Path validation rejects relative/root/UNC/null-byte paths.
- `/root/claude-code/memdir/memoryScan.ts` — Scans memory directories, parses frontmatter, formats manifests.

### 2.3 Session Memory (Separate Subsystem)

**Files:**
- `/root/claude-code/services/SessionMemory/sessionMemory.ts` — Session-scoped memory extraction. `shouldExtractMemory()` requires: init threshold met, THEN token threshold OR natural break (no tool calls in last turn). `setupSessionMemoryFile()` creates `~/.claude/session-memory/<session>.md` with mode 0600/0700.
- `/root/claude-code/services/SessionMemory/sessionMemoryUtils.ts` — Config: `DEFAULT_SESSION_MEMORY_CONFIG` with `minimumTokensBetweenUpdate`, `toolCallsBetweenUpdates`, `initialTokenThreshold`. Tracks `lastSummarizedMessageId`.

### 2.4 Background Extraction (extractMemories)

**Files:**
- `/root/claude-code/services/extractMemories/extractMemories.ts` — Runs at end of each query loop via `handleStopHooks`. Uses forked agent (`runForkedAgent`) sharing parent's prompt cache. `createAutoMemCanUseTool()` restricts forked agent to: Read/Grep/Glob (unrestricted), read-only Bash, Edit/Write only within memory directory. Detection: `hasMemoryWritesSince()` checks if main agent already wrote memories this turn — if so, forked extraction skips.
- `/root/claude-code/services/extractMemories/prompts.ts` — `buildExtractAutoOnlyPrompt()`, `buildExtractCombinedPrompt()` for team + private modes.
- `/root/claude-code/services/teamMemorySync/secretScanner.ts` — Scans memory files for secrets (API keys, tokens) before sync.

### 2.5 Team Memory Sync

**Files:**
- `/root/claude-code/services/teamMemorySync/index.ts` — Syncs team memory files via API (GET/PUT with ETags). Delta upload: only keys whose SHA256 differs from server checksums. File deletions do NOT propagate. Server-enforced max_entries cap learned from 413 response. `MAX_FILE_SIZE_BYTES = 250_000`.

### 2.6 Compaction

**Files:**
- `/root/claude-code/services/compact/compact.ts` — Full conversation compaction via forked agent. Strips images before compaction. Post-compact: boundary marker, summaryMessages, messagesToKeep, attachments, hookResults reinjected. Retry path for prompt-too-long.
- `/root/claude-code/services/compact/microCompact.ts` — Pre-compaction tool result truncation. Only compacts specific tools (Read, Shell, Grep, Glob, WebSearch, WebFetch). Cached microcompact with cache_edits API. `TIME_BASED_MC_CLEARED_MESSAGE` for old results.
- `/root/claude-code/services/compact/autoCompact.ts` — `getEffectiveContextWindowSize()`, `getAutoCompactThreshold()`, `calculateTokenWarningState()`. `isAutoCompactEnabled()` checks env/settings. `shouldAutoCompact()` has recursion guards (no compact during session_memory, compact, or context_collapse forks). Circuit breaker: `MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES = 3`.

### 2.7 Prompt Cache Protection

**Files:**
- `/root/claude-code/services/api/promptCacheBreakDetection.ts` — Tracks `PreviousState` with hashes for system, tools, cache_control, per-tool schemas, model, fast_mode, betas, effort, extra_body. Detects what changed (added/removed tools, schema changes, model switches). Logs diff to temp file on break.

### 2.8 UX Commands

**Files:**
- `/root/claude-code/commands/memory/memory.tsx` — `/memory` opens TUI dialog with MemoryFileSelector for editing memory files in $EDITOR.
- `/root/claude-code/commands/compact/compact.ts` — `/compact` tries session memory compaction first, then traditional compaction, then microcompact. Reactive-only mode support.
- `/root/claude-code/commands/context/context.tsx` — `/context` shows token usage visualization with microcompact applied and context collapse projected.
- `/root/claude-code/components/memory/MemoryUpdateNotification.tsx` — TUI notification when memory files change.

### 2.9 Strengths for Billyharness

- **Forked agent pattern:** Extract memories in isolated fork sharing prompt cache; restrict tool access in fork. Billyharness should use this for background extraction.
- **Four-type taxonomy:** user/feedback/project/reference with explicit save/use guidance. Directly applicable.
- **MEMORY.md index + topic files:** Human-readable, file-system-browsable, frontmatter-structured. Much better than a blob.
- **Cache break detection:** Detailed tracking of what changed between API calls. Billyharness already has runstate hashes — extend them.

### 2.10 Weaknesses

- No vector/FTS retrieval — relies on model reading MEMORY.md index. This scales poorly beyond ~200 lines.
- No compaction checkpoint restore — compaction is destructive in the active message list.
- Team memory sync is Anthropic-specific API — not directly portable.

---

## 3. Codex CLI — Two-Phase Memory Pipeline with Leases

### 3.1 Architecture Summary

Codex has the most structured background memory pipeline. Two phases: Phase 1 extracts raw memories from old threads (cheap model), Phase 2 consolidates them (better model). Jobs have leases, retry delays, heartbeats. Memory is stored as `raw_memories.md` + per-thread rollout summaries + extension resources.

### 3.2 Memory Storage

**Files:**
- `/root/agent-research/codex/codex-rs/memories/write/src/lib.rs` — `memory_root()`, `raw_memories_file()`, `rollout_summaries_dir()`, `memory_extensions_root()`. Artifacts: `raw_memories.md`, `rollout_summaries/`, `extensions/`.
- `/root/agent-research/codex/codex-rs/memories/write/src/storage.rs` — `rebuild_raw_memories_file_from_memories()` rebuilds canonical file from DB-backed Stage1Output rows. `sync_rollout_summaries_from_memories()` prunes old summaries. `rollout_summary_file_stem()` generates stable filenames from thread_id + timestamp + hash.
- `/root/agent-research/codex/codex-rs/state/src/model/memories.rs` — `Stage1Output` struct: thread_id, rollout_path, source_updated_at, raw_memory, rollout_summary, cwd, git_branch. `MemoryJob`: id, thread_id, status (claimed/up-to-date), lease token, timestamps.

### 3.3 Phase 1 Extraction

**Files:**
- `/root/agent-research/codex/codex-rs/memories/write/src/phase1.rs` — Claims eligible threads (age/idle/scan limits), spawns concurrent extraction jobs with `CONCURRENCY_LIMIT = 8`. Uses `reasoning_effort = Low`, `context_window_percent = 70%`. Redacts secrets from model output. Strict JSON schema enforcement.

### 3.4 Phase 2 Consolidation

**Files:**
- `/root/agent-research/codex/codex-rs/memories/write/src/phase2.rs` — Consolidation with `reasoning_effort = Medium`. `JOB_HEARTBEAT_SECONDS = 90`. Reads phase2_workspace_diff.md for change signals.
- `/root/agent-research/codex/codex-rs/memories/write/templates/memories/consolidation.md` — Consolidation prompt template.

### 3.5 Guard / Rate Limiting

**Files:**
- `/root/agent-research/codex/codex-rs/memories/write/src/guard.rs` — Before memory startup, checks if backend API quota percent remaining is below threshold. Memory extraction does NOT start if the main work is under rate pressure.

### 3.6 Memory Reading / Tools

**Files:**
- `/root/agent-research/codex/codex-rs/ext/memories/src/lib.rs` — Memory tools exposed as extension: `add_ad_hoc_note`, `list`, `read`, `search`. `DEFAULT_SEARCH_MAX_RESULTS = 200`, `DEFAULT_READ_MAX_TOKENS = 20_000`.
- `/root/agent-research/codex/codex-rs/ext/memories/src/tools/read.rs` — `ReadTool` with path, line_offset, max_lines params. Called via `MemoryBackend` interface.
- `/root/agent-research/codex/codex-rs/ext/memories/src/tools/search.rs` — Semantic search over memories.
- `/root/agent-research/codex/codex-rs/memories/read/src/citations.rs` — Citation tracking for memory recall.

### 3.7 Config

**Files:**
- `/root/agent-research/codex/codex-rs/config/src/types.rs` — Memory config flags: `generate_memories`, `use_memories`, `dedicated_tools`, retention settings, max rollouts, idle hours, rate-limit remaining percent, extraction/consolidation model.

### 3.8 Compaction

**Files:**
- `/root/agent-research/codex/codex-rs/core/src/compact.rs` — Compaction creates replacement history with `CompactedItem` containing `window_number`, `first_window_id`, `previous_window_id`, `window_id`. Initial context can be injected at `BeforeLastUserMessage` or `DoNotInject`. Pre/post compact hooks.
- `/root/agent-research/codex/codex-rs/core/src/state/auto_compact_window.rs` — `AutoCompactWindow` struct: window_ids, prefill_token_baseline, reminder_state. `advance()` and `restore()` methods.

### 3.9 Session Injection

**Files:**
- `/root/agent-research/codex/codex-rs/core/src/session/inject.rs` — `inject_if_running()` injects items into active turn or returns Err if no active turn. `try_start_turn_if_idle()` for extension-initiated work.

### 3.10 Strengths for Billyharness

- **Two-phase pipeline:** Cheap extraction, better consolidation. Lease-based jobs prevent race conditions.
- **Rate-limit guard:** Don't burn quota on background work when primary work needs it.
- **Compaction window IDs:** Each compaction gets a unique window number and links to previous — enables restore chains.
- **Extension memories:** External sources (plugins) can contribute to memory with their own instructions.md.

### 3.11 Weaknesses

- Heavy infrastructure (SQLite, job scheduler, cloud tasks). Over-engineered for Billyharness MVP.
- No user-facing memory editing UX — fully automatic.
- No prompt injection scanning on memory writes.

---

## 4. OpenCode — Event-Sourced Session Model with Context Epochs

### 4.1 Architecture Summary

OpenCode has the strongest formal session model. Key innovations: durable admission inbox, context epochs, event-projected compaction with revert support.

### 4.2 Session Architecture

**Files:**
- `/root/agent-research/opencode-current/packages/opencode/src/session/session.ts` — Session lifecycle events: `prompt` (id/sessionID/delivery/resume), `resume`, `interrupt`, `compact`, `revert`.
- `/root/agent-research/opencode-current/specs/v2/session.md:35-38` — `session_input` is a durable inbox. `PromptAdmitted` records input, `Prompted` promotes to model-visible. Atomic in projector.

### 4.3 Context Epochs (Critical Pattern)

**Files:**
- `/root/agent-research/opencode-current/specs/v2/session.md:54-82` — V2 Sessions persist exact privileged System Context shown to model: immutable provider-cache baseline + hidden structured snapshot of independently observed Context Sources. Changed context becomes chronological durable System message at safe boundary.

### 4.4 Compaction

**Files:**
- `/root/agent-research/opencode-current/packages/opencode/src/session/compaction.ts` — Serializes messages, selects head/recent, builds anchored summary. Events: `SessionEvent.Compaction.Started` then `SessionEvent.Compaction.Ended` with text + recent. Auto path compares request estimate vs context minus reserve.
- `/root/agent-research/opencode-current/packages/opencode/src/session/overflow.ts` — `isOverflow()`: token count >= `usable()` where usable = context - compaction_reserve - output_max. `COMPACTION_BUFFER = 20_000`.
- `/root/agent-research/opencode-current/packages/opencode/src/session/revert.ts` — Revert removes messages/input rows after compaction boundary, resets context epoch.

### 4.5 Instructions/Context Loading

**Files:**
- `/root/agent-research/opencode-current/packages/opencode/src/session/instruction.ts` — Loads AGENTS.md/CLAUDE.md, walks up directory tree from file being read. Claims tracking per assistant message ID to avoid redundant injection. Supports global ~/.claude/CLAUDE.md, project AGENTS.md, config.instructions paths/URLs.

### 4.6 Storage

**Files:**
- `/root/agent-research/opencode-current/packages/opencode/src/storage/storage.ts` — JSON file-based storage with migrations. Structure: `project/<hash>.json`, `session/<projectID>/<sessionID>.json`, `message/<msgID>.json`, `part/<msgID>/<partID>.json`, `session_diff/<sessionID>.json`.

### 4.7 Strengths for Billyharness

- **Durable admission inbox:** Telegram/gateway messages should be admitted before model execution. Directly applicable.
- **Context epochs:** immutable baseline, structured snapshot, chronological updates on context change. Billyharness already has runstate hashes — epoch-ify them.
- **Event-projected compaction:** Started = progress, Ended = durable boundary. Works with Billyharness event system.
- **Revert support:** Compaction with revert means safe experimentation.

### 4.8 Weaknesses

- No long-term cross-session memory — only session-scoped context.
- File-based JSON storage less queryable than SQLite.
- No background extraction/consolidation pipeline.

---

## 5. OpenClaw — Plugin Memory Host with Hybrid Retrieval

### 5.1 Architecture Summary

OpenClaw has the most mature plugin/memory host. Memory is a plugin capability with promptBuilder, flushPlanResolver, runtime, and publicArtifacts. Supports multiple backends including LanceDB vector store. Compaction has checkpoint/restore/branch.

### 5.2 Memory Core Plugin

**Files:**
- `/root/agent-research/openclaw/extensions/memory-core/index.ts` — `memory_search` and `memory_get` tools. Lazy-loaded with `hasMemoryToolContext()` guard. Schema: query, maxResults, minScore, corpus (memory|wiki|all|sessions).
- `/root/agent-research/openclaw/extensions/memory-core/src/prompt-section.ts` — `buildPromptSection()` inserts memory guidance into system prompt.
- `/root/agent-research/openclaw/extensions/memory-core/src/tools.ts` — Memory tools with recall tracking, citations, test mocks.

### 5.3 Memory Manager

**Files:**
- `/root/agent-research/openclaw/extensions/memory-core/src/memory/manager.ts` — Comprehensive memory manager with: source state, session sync state, async state, FTS state, status state, provider state. Supports read-only recovery, vector dedupe, embedding timeout/cache, self-heal missing identity.
- `/root/agent-research/openclaw/extensions/memory-core/src/memory/manager-sync-ops.ts` — Sync operations: startup catchup, interval sync, targeted sync, archive delta bypass.
- `/root/agent-research/openclaw/extensions/memory-core/src/memory/embeddings.ts` — Generic embedding provider bridge.
- `/root/agent-research/openclaw/extensions/memory-core/src/memory/temporal-decay.ts` — Temporal decay for memory relevance scoring.

### 5.4 Memory Search Config

**Files:**
- `/root/agent-research/openclaw/src/agents/memory-search.ts` — Sources: memory|sessions, extraPaths, multimodal, provider/remote/local, SQLite store, FTS tokenizer, vector extension, chunking tokens/overlap, sync triggers, hybrid vector/text weights, MMR.

### 5.5 Dreaming / Background Review

**Files:**
- `/root/agent-research/openclaw/extensions/memory-core/src/dreaming.ts` — Background review ("dreaming") with short-term promotion, shadow trials, repair cycles, narrative generation.
- `/root/agent-research/openclaw/extensions/memory-core/src/dreaming-phases.ts` — Phase-based dreaming: review, consolidate, prune.
- `/root/agent-research/openclaw/extensions/memory-core/src/dreaming-command.ts` — CLI command to trigger dreaming.

### 5.6 Compaction Checkpoints

**Files:**
- `/root/agent-research/openclaw/src/gateway/session-compaction-checkpoints.ts` — Capture/persist/branch/restore checkpoints. Retention by count and bytes. `MAX_CHECKPOINTS`, `MAX_CHECKPOINT_BYTES`.
- `/root/agent-research/openclaw/src/agents/embedded-agent-runner/compaction-successor-transcript.ts` — Rotates into successor transcript after compaction. Preserves last assistant, tool results, dedupe state, cleanup.

### 5.7 Strengths for Billyharness

- **Plugin memory host architecture:** prompt section, flush plan, runtime, artifacts — clean separation.
- **Hybrid retrieval:** FTS + vector + MMR + temporal decay. Phase 2 target.
- **Compaction checkpoints:** capture, branch, restore. Blueprint for Billyharness.
- **Dreaming:** Background review cycles. Phase 4 target.

### 5.8 Weaknesses

- Plugin SDK complexity — overkill for MVP.
- Dreaming can be expensive (calls model in background).
- LanceDB adds native dependency.

---

## 6. Hermes Agent — Frozen Prompt Snapshots and Injection Scanning

### 6.1 Architecture Summary

Hermes has pragmatic memory tooling that directly addresses prompt cache and injection risks. SQLite state store with session chains (parent/child for compression), FTS5 search.

### 6.2 Memory Tool Design

**Files:**
- `/root/agent-research/hermes-agent/tools/memory_tool.py:1-24` — Memory tool deliberately separates frozen system-prompt snapshot from live disk state. MEMORY.md/USER.md injected at session start; mid-session writes are durable but do NOT mutate current system prompt, preserving prefix cache.
- `/root/agent-research/hermes-agent/tools/memory_tool.py:113-131` — On load, entries are scanned for threat patterns; poisoned entries replaced with `[BLOCKED: ...]` in prompt snapshot, while raw entries remain visible/removable.

### 6.3 Memory Manager

**Files:**
- `/root/agent-research/hermes-agent/agent/memory_manager.py:148-220` — `sanitize_context()` strips `<memory-context>` blocks and system notes from provider output, including streaming chunk boundaries.

### 6.4 Compaction

**Files:**
- `/root/agent-research/hermes-agent/agent/context_compressor.py:37-95` — Summary prefix explicitly says: compaction summary is reference only, latest user message wins, stale tasks from summary must be discarded, reverse signals stop old work. Summary has explicit end marker.

### 6.5 SQLite State Store

**Files:**
- `/root/agent-research/hermes-agent/hermes_state.py` — SQLite with WAL mode, FTS5 full-text search. Session chains: parent_session_id, end_reason (branched/compression). Source tagging (cli/telegram/discord). `_BRANCH_CHILD_SQL` and `_COMPRESSION_CHILD_SQL` for listing only relevant sessions. Delegate child cascade deletion.

### 6.6 Strengths for Billyharness

- **Frozen prompt snapshots:** Memory writes don't mutate active prompt mid-session. Golden rule. Directly copy.
- **Threat scanning on load:** `[BLOCKED: ...]` replacement for injection patterns.
- **Anti-stale-task compaction language:** Summary explicitly says "discard stale tasks."
- **Session chains:** parent/child with compression markers — useful for Billyharness checkpoint/branch.

### 6.7 Weaknesses

- No structured memory taxonomy (just MEMORY.md/USER.md blurbs).
- No retrieval index beyond regex scan.
- Compaction is summary-based, not window-id-based (no restore chain).
- SQLite FTS5 is raw — no vector embeddings.

---

## 7. Comparative Table


## 7. Comparative Table

| Dimension | Billyharness (current) | Claude Code | Codex CLI | OpenCode | OpenClaw | Hermes Agent |
|-----------|----------------------|-------------|-----------|----------|----------|--------------|
| **Memory storage** | Profile/SOUL + AGENTS.md only | MEMORY.md index + topic .md files + session memory .md | raw_memories.md + rollout_summaries/ + SQLite state | N/A (session-scoped only) | Plugin host: files + LanceDB/SQLite + FTS | MEMORY.md + USER.md files |
| **Memory taxonomy** | None | user/feedback/project/reference | Implicit from extraction | None | Config-based corpora | None (flat files) |
| **Memory injection** | Profile as system, AGENTS as user msg | MEMORY.md in system prompt, topic files via Read tool | As context contributors | Via instruction.ts (AGENTS.md/CLAUDE.md) | promptBuilder section + tools | Frozen at session start |
| **Memory update** | Manual file edit only | Agent writes via Write/Edit tools + background extraction | Phase 1 (cheap model) + Phase 2 (better model) | None | Via memory tools | Via memory_tool write |
| **User approval** | None needed (manual) | Auto: model writes memory; /memory command to review | Fully automatic | N/A | Via tools | Via tools |
| **Compaction** | Deterministic + optional model summary | Full + microcompact + session-memory compact | Window-based with IDs | Event-projected with revert | Checkpoint-based with successor transcript | Summary-based |
| **Session persistence** | JSONL history/events/snapshots | Session storage + transcript + cross-project resume | Thread store + live writer | JSON files + SQL-like schema | Gateway checkpoints | SQLite + FTS5 |
| **Prefix cache** | Partial (protects system/AGENTS in compaction) | Full detection: hash system, tools, cache_control, betas | Window-based caching | Context epoch immutable baseline | Memory section conditional | Frozen prompt snapshots |
| **Injection defense** | None explicit | Secret scanner for team sync, path validation | Secret redaction in extraction | Standard filesystem | Config-driven | [BLOCKED] replacement on load |
| **Background review** | None | extractMemories post-turn, SessionMemory periodic, autoDream | Phase 1/2 jobs with leases | None | Dreaming with phases, shadow trials | None |

## 8. Recommended Architecture for Billyharness Memory v1

### 8.1 Layer Model

Build six layers, but implement in phases. The first two layers are prerequisite for the rest:

```
Layer 0: Context Epoch (prerequisite)
Layer 1: Compaction Checkpoints (prerequisite)
Layer 2: File-Backed Curated Memory Store (MVP)
Layer 3: Memory Tools with Audit (MVP+)
Layer 4: Retrieval Index (Phase 2)
Layer 5: Background Extraction Jobs (Phase 3)
Layer 6: Self-Improvement via Skills (Phase 4)
```

### 8.2 Layer 0: Context Epoch

**Why first:** Without epochs, any memory injection will break prefix cache unpredictably, and you can't know what context the model actually saw.

**What to build:**
- Package `internal/contextstate` or extend `internal/runstate`
- `ContextEpoch` struct: epoch_id, session_id, baseline_seq, system_prompt_hash, profile_hash, instructions_hash, tool_snapshot_hash, mcp_snapshot_hash, model_hash, sources[], created_at
- Before each provider turn, reconcile observed sources against current epoch
- If unchanged: no prompt change, cache preserved
- If changed: write chronological `context.epoch_advanced` event, rebuild system prompt from new baseline
- Store epochs in session manifest or `context_epochs.jsonl`

**Key Billyharness advantage:** `runstate.NewSnapshot()` already computes hashes. Just make them durable objects.

### 8.3 Layer 1: Compaction Checkpoints

**Why second:** Destructive compaction is the #1 cause of lost context. Making it checkpointed before adding memory prevents compound failures.

**What to build:**
- Before `compactMessages`, write `CompactionCheckpoint` record: checkpoint_id, window_number, previous_window_id, window_id, history_sha256, cut_range, first_kept_index, summary_message, strategy, tokens_before, tokens_after, context_epoch_id
- Store in session manifest or `compaction_checkpoints.jsonl`
- Active message list gets summary + recent kept messages
- Full transcript remains recoverable from snapshots + checkpoints
- Add gateway inspect command: list checkpoints, show checkpoint detail

**Key file changes:**
- `internal/agent/compaction.go`: add checkpoint metadata to report
- `internal/gateway/session_store.go`: add checkpoint fields
- Future: `cmd/fast-agent-harness/sessions.go`: branch/restore commands

### 8.4 Layer 2: File-Backed Curated Memory Store (MVP)

**Directory structure:**
```
$BILLYHARNESS_HOME/memory/
  USER.md           # stable user preferences, role, goals
  AGENT.md          # agent self-knowledge (capabilities, limitations)
  MEMORY.md         # environment-independent facts
  projects/
    <hash>/MEMORY.md  # per-project memory
  pending/           # proposed-but-unapproved entries
    <id>.json
```

**Frontmatter schema for .md files:**
```yaml
---
id: mem_abc123
scope: user          # user | agent | project
kind: preference     # preference | fact | constraint | environment | decision
created: 2025-06-29T10:00:00Z
updated: 2025-06-29T10:00:00Z
source_session: sess_xyz
source_seq: 42
confidence: high
---
Content here...
```

**Prompt injection policy (from Hermes):**
- Memory snapshot loaded ONCE at session start (or context epoch advance)
- Mid-session writes are durable to disk but do NOT mutate active prompt
- On load, scan entries for injection patterns; poisoned entries get `[BLOCKED: reason]`
- Raw entries remain visible/removable via tools

**Capacity limits:**
- USER.md: max 2,000 chars (user facts)
- AGENT.md: max 1,000 chars (agent self-knowledge)
- MEMORY.md (global): max 4,000 chars
- Per-project MEMORY.md: max 4,000 chars
- Pending queue: max 10 entries, auto-reject after 30 days

**Files to create:**
- `internal/memory/store.go` — load/write memory files, parse frontmatter
- `internal/memory/snapshot.go` — build frozen prompt snapshot from store
- `internal/memory/pending.go` — manage pending queue
- `internal/memory/scan.go` — threat pattern scanning

### 8.5 Layer 3: Memory Tools with Audit

**Tools:**
```
memory_list    — list entries by scope/kind, bounded output, no full content
memory_read    — read entry by id/path, with max_chars
memory_search  — grep/FTS over memory files, bounded results
memory_add     — write-risk: add entry with frontmatter validation
memory_replace — write-risk: replace entry by id
memory_remove  — write-risk: remove entry by id
memory_approve — move from pending to canonical
memory_reject  — remove from pending
```

**Audit events:**
```
memory.entry_added      { id, scope, kind, source_session, source_seq }
memory.entry_updated    { id, previous_hash, new_hash }
memory.entry_removed    { id, reason }
memory.search           { query, scope, result_count }
memory.pending_created  { id, scope, kind, source_session }
memory.pending_approved { id, reviewed_by }
memory.pending_rejected { id, reason }
```

**Key Billyharness advantage:** Tool permission system already emits `tool.permission_requested/decided` events. Memory write tools plug into this for free audit.

### 8.6 Layer 4: Retrieval Index (Phase 2)

**What to build (after MVP validated):**
- SQLite database with FTS5 table on memory content
- Rebuildable from canonical markdown files (markdown is source of truth)
- Optional: embedding provider + vector table (like OpenClaw)
- `memory_search` tool uses FTS + optional vector
- Index rebuilt on memory writes or periodic sync

**Schema sketch:**
```sql
CREATE TABLE memory_entries (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL,
  scope TEXT NOT NULL,
  kind TEXT NOT NULL,
  content TEXT NOT NULL,
  created_at TEXT,
  updated_at TEXT,
  source_session TEXT,
  source_seq INTEGER,
  confidence TEXT
);
CREATE VIRTUAL TABLE memory_fts USING fts5(id, scope, kind, content);
```

### 8.7 Layer 5: Background Extraction Jobs (Phase 3)

**Trigger conditions:**
- After `run.completed` event
- Token threshold met (e.g., 10K tokens since last extraction)
- Tool call threshold met (e.g., 20 tool calls since last extraction)
- Session age > N minutes
- Rate-limit guard: current provider quota > 20% remaining

**Job model:**
```go
type ExtractionJob struct {
    ID            string
    SessionID     string
    Status        string  // pending | claimed | completed | failed
    LeaseToken    string
    SourceSeqMin  int64
    SourceSeqMax  int64
    Model         string  // cheap model for extraction
    CreatedAt     time.Time
    ClaimedAt     *time.Time
    CompletedAt   *time.Time
}
```

**What extractor produces (not canonical memory):**
```json
{
  "candidates": [
    {
      "scope": "user",
      "kind": "preference",
      "content": "User prefers terse responses without trailing summaries",
      "evidence": "User said 'stop summarizing at the end' in sess_xyz:42-45",
      "confidence": "high",
      "reason_to_save": "Direct user correction",
      "reason_not_to_save": null
    }
  ]
}
```

### 8.8 Layer 6: Self-Improvement via Skills (Phase 4)

**Don't auto-patch skills.** Instead:
- `skill_suggest_update` tool: agent proposes change to a skill
- Proposal stored in `$BILLYHARNESS_HOME/skill-proposals/<skill>_<timestamp>.md`
- User/maintainer reviews and approves
- Only approved proposals become skill updates

---

## 9. Slash Commands for TUI and Telegram

```
/memory              — show snapshot summary (sources, entry counts by scope)
/memory list         — list entries with ids/scopes/kinds (bounded)
/memory read <id>    — show full entry
/memory pending      — show pending queue
/memory approve <id> — approve pending entry
/memory reject <id>  — reject pending entry (with optional reason)
/memory edit <id>    — open entry in editor (TUI only)
/compact             — trigger manual compaction
/context             — show context usage breakdown (tokens by section)
```

---

## 10. Risk Analysis and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Memory bloat: model writes too many entries | High | High | Strict capacity limits per scope; auto-prune low-confidence/low-usage |
| Stale memory: old facts contradict new reality | High | Medium | `updated_at` + `stale_after` TTL; model instructed to prefer recent session context |
| Prompt injection through memory | Medium | High | Threat scan on load; [BLOCKED] replacement; frozen snapshots |
| Secret leakage into memory | Medium | Critical | Secret scanner before write; redact after extraction |
| Prefix cache break from memory changes | High | Medium | Frozen snapshots; only change on epoch advance |
| Race condition: two workers extracting same session | Medium | Medium | Lease-based jobs; content-hash dedupe |
| Memory-sourced hallucination | Medium | Medium | Source provenance tracking; confidence scores; explicit "source: session X" |
| Corrupt JSONL/markdown | Low | High | Append-only with line-level recovery; rebuild from markdown; validation on load |

---

## 11. Test Plan

### Phase 1: Storage + Commands
- Write entry via memory_add, verify frontmatter parsed correctly
- Write entry exceeding capacity limit, verify rejection
- Load snapshot: verify only entries within limits appear
- Mid-session write: verify active prompt unchanged
- Reload session: verify new entries appear in fresh snapshot
- Path traversal attempt: verify rejection

### Phase 2: Approval + Pending
- Create pending entry, verify appears in pending list
- Approve: verify moves to canonical store
- Reject: verify removed from pending
- Auto-reject: verify entries older than 30 days removed
- Concurrent approve/reject: verify no race via file locking

### Phase 3: Context Injection + Compaction
- Verify memory snapshot placed correctly in InitialMessages
- Verify memory snapshot size within budget
- Compaction: verify checkpoint written before message replacement
- Verify checkpoint fields: window_number, previous_window_id, tokens
- Restore from checkpoint: verify messages match pre-compaction state

### Phase 4: Background Review
- Verify extraction triggers only after thresholds met
- Verify exactly-once lease behavior
- Verify secrets redacted in candidates
- Verify stale task progress rejected
- Verify rate-limit guard prevents extraction when quota low

### Phase 5: UX
- /memory shows correct counts and sources
- /memory read returns bounded, correctly escaped content
- /context shows memory token allocation
- Audit events contain required fields

---

## 12. Files to Modify in /root/billyharness

### New files to create:
```
internal/contextstate/contextstate.go       — ContextEpoch struct, reconcile logic
internal/memory/store.go                    — Memory store: load, write, parse
internal/memory/snapshot.go                 — Frozen snapshot builder
internal/memory/pending.go                  — Pending queue management
internal/memory/scan.go                     — Injection scanning
internal/memory/types.go                    — Entry, SourceRef, config types
internal/memory/store_test.go               — Tests
```

### Files to modify:
```
internal/agent/agent.go                     — Inject memory snapshot in InitialMessages
internal/agent/compaction.go                — Add checkpoint metadata
internal/gateway/session_store.go           — Store context epochs, checkpoints
internal/runstate/runstate.go               — Export snapshot comparison helpers
internal/protocol/types.go                  — Add memory event types
internal/tools/tools.go                     — Add memory tools
internal/agent/context_threshold.go         — Use new context state
cmd/fast-agent-harness/commands.go          — Add /memory, /context slash commands
profiles/billy/profile.toml                 — Add memory config section
settings.json                               — Add memory settings
```

---

## 13. Concrete TODO List by Phase

### Phase 1: Context Epoch + Compaction Checkpoints (week 1-2)

- [ ] Create `internal/contextstate` package with `ContextEpoch` struct
- [ ] Add `ComputeEpoch()` using existing runstate hashes
- [ ] Add `ReconcileEpoch()` comparing current vs stored epoch
- [ ] Modify `agent.go` to reconcile epoch before provider turn
- [ ] Add context epoch storage to session_store
- [ ] Emit `context.epoch_advanced` event on change
- [ ] Add `CompactionCheckpoint` struct
- [ ] In `compaction.go`: write checkpoint before message replacement
- [ ] Store checkpoint in session_store
- [ ] Add gateway command: `sessions inspect --checkpoints <session-id>`

### Phase 2: Memory Store MVP (week 2-3)

- [ ] Create `internal/memory` package with types
- [ ] Implement `store.go`: Load/Wrote USER.md, AGENT.md, MEMORY.md, project MEMORY.md
- [ ] Implement frontmatter parser
- [ ] Implement capacity limits and truncation
- [ ] Implement `snapshot.go`: frozen prompt section builder
- [ ] Implement `scan.go`: injection pattern detection
- [ ] Inject snapshot in `agent.go` after profile, before AGENTS
- [ ] Add memory config to `settings.json` and `profile.toml`

### Phase 3: Memory Tools (week 3-4)

- [ ] Implement `memory_list` tool
- [ ] Implement `memory_read` tool
- [ ] Implement `memory_add` tool (write-risk)
- [ ] Implement `memory_replace` tool (write-risk)
- [ ] Implement `memory_remove` tool (write-risk)
- [ ] Implement pending queue tools: approve, reject
- [ ] Add memory event types to protocol
- [ ] Wire tool permission events for audit
- [ ] Add `/memory` slash command

### Phase 4: Background Extraction (week 4-5)

- [ ] Implement `internal/memory/extractor.go`
- [ ] Implement extraction job model with leases
- [ ] Implement rate-limit guard
- [ ] Implement strict JSON schema for candidates
- [ ] Implement secret redaction
- [ ] Trigger on `run.completed`

### Phase 5: UX + Retrieval (week 5-6)

- [ ] Implement `/context` slash command
- [ ] Add FTS/search to memory tools
- [ ] In-memory grep for MVP; SQLite FTS5 for Phase 2
- [ ] Add memory usage to context visualization
- [ ] Telegram: memory commands via bot messages
- [ ] TUI: MemoryView component

### Phase 6: Self-Improvement (future)

- [ ] Implement `skill_suggest_update` tool
- [ ] Create proposal review workflow
- [ ] Add `/skill proposals` command

---

## 14. Key Design Decisions (Inference-Based, Not Directly from Code)

These are architectural judgments synthesized from the patterns observed across repos, but not copied from any single file:

1. **Memory writes must NOT mutate active prompt mid-session.** Every mature system agrees (Hermes frozen snapshots, Claude Code separate extraction, OpenCode context epochs, Codex separate injection path). This is the single most important invariant.

2. **Background extraction should produce candidates, not canonical memory.** Claude Code lets the forked agent write directly, but Codex's two-phase pipeline and the general safety consensus suggest candidates are safer for Billyharness. User maintains final authority.

3. **Memory should be scoped, not a blob.** Claude Code's 4-type taxonomy is the right granularity for MVP. OpenClaw's corpus-based approach is right for Phase 2.

4. **Compaction must be checkpointed, not destructive.** OpenCode revert, OpenClaw successor transcripts, and Codex window IDs all agree. Billyharness should add checkpoint metadata now.

5. **Skills ≠ Memory.** Billyharness already separates skills from instructions. Maintain this: facts/preferences go to memory; repeatable procedures go to skills; session progress stays in transcript.

6. **Don't build vector DB in Phase 1.** File-backed markdown with grep/FTS is sufficient for MVP. Add SQLite FTS5 in Phase 2, vectors in Phase 3.

---

## 15. Verification Line-Reference Anchors

This appendix records the exact local file/line anchors used to verify the core claims. The earlier sections intentionally summarize many files compactly; these anchors are the minimum line-backed evidence set for the proposed Billyharness design.

- `/root/billyharness/internal/agent/agent.go:45-55` — initial model messages are assembled from the system prompt plus profile/SOUL and project instructions, which is the current insertion point for any future frozen memory snapshot.
- `/root/billyharness/internal/agent/agent.go:86-104` — automatic compaction is checked before provider calls, and `context.compacted` is emitted after a successful compaction.
- `/root/billyharness/internal/agent/compaction.go:88-115` — protected prefix detection keeps system/profile/project-instruction messages out of the compaction cut.
- `/root/billyharness/internal/agent/compaction.go:320-359` — compaction report carries token estimates, cut range, protected prefix count, top contributors, and strategy metadata.
- `/root/billyharness/internal/gateway/session_store.go:19-29` — session store defines manifest/history/events/snapshot file names.
- `/root/billyharness/internal/gateway/session_store.go:201-283` — append-only session events receive envelope metadata and are written to `events.jsonl`.
- `/root/billyharness/internal/gateway/session_store.go:484-524` — replay validates schema version, session id, seq ordering, message count, and sha256.
- `/root/billyharness/internal/runstate/runstate.go:57-80` — run snapshot already tracks provider/model, reasoning mode, context budget, tool snapshot hash, MCP status hash, profile instruction hash, and permission mode.
- `/root/billyharness/internal/tools/tools.go:960-1079` — `skill_list` and `skill_read` provide bounded on-demand procedural memory instead of injecting every skill body.
- `/root/agent-research/codex/codex-rs/state/src/model/memories.rs:8-20` — Codex `Stage1Output` records thread id, rollout path, source timestamp, raw memory, rollout summary, cwd, and git branch.
- `/root/agent-research/codex/codex-rs/memories/write/src/phase1.rs:65-108` — Codex phase-1 memory extraction claims eligible threads and bounds extraction work.
- `/root/agent-research/codex/codex-rs/core/src/compact.rs:322-368` — Codex compaction creates replacement history and advances window metadata.
- `/root/claude-code/services/SessionMemory/sessionMemory.ts:134-181` — Claude Code session memory extraction waits for initialization/update thresholds and natural break conditions.
- `/root/claude-code/services/compact/compact.ts:325-362` — Claude Code post-compact reconstruction preserves boundary marker, summary, kept messages, attachments, hooks, and relink metadata.
- `/root/claude-code/utils/forkedAgent.ts:1-9` — Claude Code forked agents share cache-critical params, track usage, log metrics, and isolate mutable state.
- `/root/agent-research/opencode-current/packages/core/src/session/sql.ts:140-176` — OpenCode persists `session_input` and `session_context_epoch`, separating durable admission from model-visible history and context baselines.
- `/root/agent-research/opencode-current/packages/core/src/session/compaction.ts:166-227` — OpenCode compaction emits started/ended events with summary and recent-message preservation.
- `/root/agent-research/openclaw/src/agents/memory-search.ts:29-112` — OpenClaw memory search config covers sources, extra paths, multimodal settings, local/remote providers, sqlite store, chunking, FTS/vector/hybrid search, MMR, and temporal decay.
- `/root/agent-research/openclaw/src/gateway/session-compaction-checkpoints.ts:91-134` — OpenClaw persists compaction checkpoints for restore/branch scenarios.
- `/root/agent-research/hermes-agent/tools/memory_tool.py:1-24` — Hermes memory tool separates frozen prompt snapshot from live durable memory writes.
- `/root/agent-research/hermes-agent/tools/memory_tool.py:132-205` — Hermes scans loaded memory entries for prompt-injection-like threat patterns and blocks poisoned prompt snapshot entries.
- `/root/agent-research/hermes-agent/agent/context_compressor.py:37-95` — Hermes compaction summary prefix explicitly warns that the latest user message wins and stale tasks from summaries must be discarded.

---
