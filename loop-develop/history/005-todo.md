# 005 TODO - Solo Harness Simplification: Delete Corporate Ceremony, Keep It Awesome

Status: completed/verified
Created: 2026-07-04
Owner loop: Claude multi-agent research workflow (72 Sonnet agents: 12 subsystem
readers, 6 dimension finders, 1 dedup pass, 45 adversarial verifiers, 8 drafters)

## Request

Billy asked: study Billyharness as what it actually is — a solo-owner harness —
and strip the corporate ceremony that crept in while chasing "production-grade"
standards. Keep real VPS-facing security (gateway bearer auth, Telegram
owner/private-chat checks, MCP trust boundaries, secret redaction). Delete or
collapse everything that exists to impress an imaginary compliance team:
manifest bureaucracy, incident-artifact pipelines, one-time migration tools
kept forever, dead metadata, duplicated layers. Then sharpen the daily loop:
faster startup, better TUI/Telegram UX, deploy/rollback that is one command.

This TODO is intentionally large (38 verified tasks across 7 milestones) so a
long implementation loop can grind through it. Work P0 first, then P1, then P2.
Every task was adversarially verified against the actual code by an independent
agent before inclusion; file paths and line numbers were confirmed at research
time (2026-07-04) and may drift a few lines as earlier tasks land.

## Relationship To 004

`004-todo.md` is still in `current-todo`. This TODO intentionally REVERSES part
of 004's Milestone 6: 004 planned to strengthen the docguard Stop hook and
manifest metadata (P1.17, P1.18, P1.20); this TODO deletes that entire layer
instead (P0.1, P0.2) because verification confirmed it has zero implementing Go
code and generates its own busywork backlog. Where 004 and 005 conflict, 005
wins once Billy starts this loop. Tasks P0.1/P0.2 include striking the mooted
004 items so the two files stay consistent. Do not move either TODO to history
until Billy asks for final verification.

## Source Research Summary

### Workflow Shape

Pipeline: 12 subsystem readers mapped the repo (96 raw candidates) → 6 dimension
finders (corporate-ceremony deletion, architecture collapse, solo daily-driver
UX, speed, solo-grade reliability, meta-process simplification) produced 53
proposals → dedup dropped 8 → 45 went to adversarial verification, where each
verifier opened the actual files and tried to refute the proposal → 38 confirmed,
7 killed. Confirmed tasks were then expanded into executable checklists by 7
milestone drafters plus 1 completeness critic (backlog section at the end).

### Subsystem Map (12 readers)

- ui-tui (~22k LOC with tests): Bubble Tea TUI plus TWO parallel event-to-state
  projection layers (`clientux/projector` 826 LOC and `tui/transcript/projector`
  639 LOC are near-duplicate state machines). `transcript_runtime.go` (1450) mixes
  accounting, projection, rendering, and view-mode logic. `displayfmt` (88 LOC)
  is the one well-sized, well-reused package.
- tools (~8k LOC): ~30 native tools. `tools.go` (1557) is a god-file mixing
  registry bootstrap, MCP catalog sync, and fs primitives. `web_compact.go` (670)
  hand-rolls an extractive summarizer with ~15 timing/token metadata fields per
  page. Test files often exceed the source they test.
- gateway (9.2k non-test LOC over FOUR packages): `gatewaybase` (136 LOC) is
  re-implemented nearly verbatim inside `gatewayclient` and re-wrapped in
  `gateway/url.go` — 3-way duplication of the same 9 symbols. `session_inspect.go`
  (1272) + `session_index_diagnostics.go` (894) both do forensic diagnostics.
  `gatewayapi` genuinely earns its keep (breaks an import cycle).
- telegram (~13k LOC): rich renderer + runner state machine are sound;
  `admission_store.go` is a durable audit ledger that NOTHING ever reads back —
  the biggest grown-not-designed spot.
- agent-core: the mapping agent for this group returned a placeholder instead of
  real analysis, so `internal/agent` / `runtimehost` / `protocol` are
  under-covered in this TODO. The completeness critic flagged it: see the first
  backlog bullet ("Audit internal/agent's core turn loop", ~8.1k lines, zero
  tasks touch it). Treat that as the seed of 006.
- providers: Codex/DeepSeek/Mock behind one interface; descriptive metadata
  fields were added and never pruned once simpler fields covered the same fact
  (`InputModalities` vs `VisionInput`); FedRAMP plumbing exists for a compliance
  regime a solo owner will never enter.
- config-secrets (~4.9k LOC): 9-layer precedence resolver tracking per-key
  source/warning/error provenance for 46 keys × 86 env aliases, most of which
  appear in no docs. `mcp_migration.go` (776 LOC) is a one-time import tool kept
  forever. `secrets/redact.go` is tight, purposeful, real security — untouched.
- persistence (6 packages): `internal/session` is misleadingly named in-memory
  run-concurrency control; `runstate` carries two never-constructed types (Turn,
  Step); `trace.ReplaySummary` has ~40 fields driven by a 140-line observe()
  switch; checkpoint does two full recursive read+hash scans per shell_exec.
- mcp-hooks-skills: `mcpstatus` is a 227-line package wrapping one function;
  `mcpclient/server.go` repeats the same clone/unlock/publish pattern at 14-16
  call sites; skills import writes a provenance JSON nothing reads.
- docs-system: 6,487 lines of docs/manifest/hook ceremony grown in a single
  ~2-day burst: 823-line hand-maintained `docs-manifest.json`, a 181-line
  "generated" reference-plan that is neither generated nor a backlog, and a
  387-line turn-blocking Stop hook enforcing them. Zero implementing Go code.
- ops-ci: `doctor.go` (1152) mixes auth, gateway probes, systemd, PID files, and
  journal grepping; `incident.go` (426) bundles 10+ redacted artifacts nothing
  consumes; a dated production-inventory snapshot went stale the day it was
  committed; deploy/rollback remains a 6-step manual SSH recipe.
- glue: mostly healthy small packages; the real weight is the hand-maintained
  44-row import table in `docs/architecture.md` scraped by
  `architecture_test.go` on every test run.

### Rejected During Verification

Seven proposals were killed by adversarial verifiers; recorded here so future
loops do not re-propose them:

- "Delete crash-loop journal scanning" — refuted: the journal check is the SOLE
  source of an actual crash-loop fail signal; the NRestarts check is purely
  informational. Deleting it removes real detection.
- "Add sparse seq-offset index for session replay" — refuted on value: ADR 0001
  explicitly accepts full-scan replay; benchmark re-run measured ~224ms at an
  extreme 100k-event upper bound. Premature abstraction.
- "Cache SnapshotWithToolPolicy" — refuted: benchmarked at ~10-28us per round
  (~1-3ms per 100-round turn), dwarfed by LLM latency; the proposed cache key
  would silently break mcp_status_changed detection.
- "Collapse Codex retry counters" — refuted: meta.Retries is well-defined and
  test-covered; the fix would conflate a budget counter with a reporting
  counter. Only a few provably-dead lines exist.
- "Unify TUI/Telegram reconnect loops" — refuted: both already share the same
  GET /v1/sessions/{id}/events path; the claimed divergence hazard cannot occur
  in the current call graph.
- "Shrink the Stop-hook docguard to two checks" — refuted as proposed (the
  manifest was still alive and 004 planned to strengthen it), but superseded by
  the stronger verified finding: P0.1 deletes the whole layer instead.
- "Share git/JSON plumbing across the two Stop hooks" — verifier returned an
  unusable placeholder verdict; moot anyway once P0.1 removes docguard_stop.py
  and only loop_research_stop.py remains.

## Solo Canon For This Loop

- One owner, one VPS, two clients (TUI, Telegram). Every layer must justify
  itself against that reality, not against an imaginary org chart.
- Deletion is the preferred refactor. A feature nothing reads back is not an
  asset; it is a liability with tests.
- Keep real security: gateway bearer auth, Telegram owner/private-chat checks,
  MCP trust boundaries and risk gating, secret redaction. None of these are
  corporate ceremony — they protect an internet-reachable box.
- Durable JSONL events stay the source of truth; do not add databases, indexes,
  or caching layers for problems measured in milliseconds.
- Docs describe what exists. Anything documenting a future system with no code
  behind it gets deleted, not "clarified".
- Meta-process (hooks, manifests, guards) must be smaller than the work it
  guards. When in doubt, the guard goes.
- Speed of the daily loop beats completeness of the audit trail. A solo owner
  debugs with `doctor`, session JSONL, and `git log` — optimize those three.
- Every task ends with its Verification block green, `git diff --check` clean,
  and the broader suite passing before moving on.

## Task Index And Sequencing

- P0 Milestone 1 — Delete Docsgen/Docguard Ceremony And Speed Up Checkpoint
  Scans: P0.1-P0.3
- P1 Milestone 2 — Delete Point-In-Time And One-Off Ceremony: P1.1-P1.6
- P1 Milestone 3 — Delete Dead Compliance Plumbing And Collapse Duplicate
  Packages: P1.7-P1.12
- P1 Milestone 4 — Fast Startup, Telegram Signal, And Daily-Loop Housekeeping:
  P1.13-P1.20
- P2 Milestone 5 — Delete Unread Provenance Ceremony And Tighten Process Docs:
  P2.1-P2.6
- P2 Milestone 6 — Collapse Duplicated Docs, Metadata, And Code Paths:
  P2.7-P2.12
- P2 Milestone 7 — Telegram Command Discoverability And Production Deploy
  Safety: P2.13-P2.15

Sequencing rules (from the completeness critic; full detail in the backlog
section at the end):

- P1.5 deletes `admission_store.go`; P1.14 optimizes the same file. If P1.5 has
  landed, SKIP P1.14 entirely.
- P1.6 and P2.3 overlap on deleting architecture.md's "Guarded Rules" section.
  Land P1.6 first; when reaching P2.3, apply only its `architecture_test.go`
  delta if the prose is already gone.
- `internal/gateway/gateway.go` is edited by P1.9, P1.13, P2.2, and P2.9 — land
  these serially, never in parallel worktrees.
- `docs/architecture.md` is edited by P1.6, P1.12, P2.3, P2.4, P2.7 — do P2.4
  (replace the markdown-scraped allowlist with a Go map) AFTER the table
  restructures land.
- P1.10 moves `internal/tools/web_metadata.go`; P2.8 edits it. Land P1.10 first.

## P0 Milestone 1 - Delete Docsgen/Docguard Ceremony And Speed Up Checkpoint Scans

Goal: rip out the docsgen/docguard meta-documentation layer (manifest,
generated reference-plan, documentation-system routing table, and the
turn-blocking Stop hook that enforces them) that has zero implementing Go
code, then cut the double full-tree content-read cost that
`checkpoint.Begin`/`Complete` pays on every `shell_exec` call.

### P0.1 Delete the docsgen/docguard meta-documentation layer and its turn-blocking Stop hook

Finding: `agent-index/docs-manifest.json` (823 lines), `agent-index/generated/reference-plan.md`
(181 lines, opens "this is a handwritten design note for future docsgen and
docguard... not an implementation backlog"), and `docs/documentation-system.md`
(214 lines) describe a docsgen/docguard system with zero implementing Go code
— `grep -rn "docs-manifest\|reference-plan\|docguard" --include=*.go` returns
nothing repo-wide. The only real enforcement, `internal/architecture`,
parses `docs/architecture.md` directly and never touches these files.
`.codex/hooks/docguard_stop.py` (386 lines) is wired into `.codex/hooks.json`'s
`Stop` array (lines 5-11, alongside `loop_research_stop.py`) and can
`emit_json({"decision": "block", ...})` (lines 90-101) on every turn end based
on regex heuristics (`ACTIVE_WORK_PATTERNS` lines 27-40, `check_manifest`
lines 223-234). `loop-develop/current-todo/004-todo.md` already carries
Milestone 6 with P1.17/P1.18/P1.20 (lines 979, 1005, 1046) reworking this
same dead system — it is generating its own busywork backlog.

Target files:

- `agent-index/docs-manifest.json`
- `agent-index/generated/reference-plan.md`
- `docs/documentation-system.md`
- `.codex/hooks/docguard_stop.py`
- `.codex/hooks.json`
- `.agents/rules/documentation.md`

Checklist:

- Delete the entire `agent-index/` directory: `docs-manifest.json`,
  `generated/reference-plan.md`, `generated/repo-map.md`, `README.md`.
- Delete `docs/documentation-system.md` and `.agents/rules/stop-hook-docguard.md`
  (the hook's own 242-line design doc).
- Remove the `docguard_stop.py` `Stop` hook entry from `.codex/hooks.json`
  (lines 5-11), keeping the `loop_research_stop.py` entry untouched.
- Delete `.codex/hooks/docguard_stop.py` and its `__pycache__` directory.
- Rewrite `.agents/rules/documentation.md` as a short paragraph on where docs
  live and when to touch them, with no manifest/generation ceremony; drop the
  "Stop hook docguard" row from `.agents/rules/README.md`.
- Edit `AGENTS.md` (lines 109-110) to drop the `documentation-system.md` and
  `docs-manifest.json` bullets.
- Edit `llms.txt` to remove the Stop-hook-docguard link (line 20), the
  Documentation-system link (line 49), and the Machine Indexes section
  (lines 53-61).
- Edit `docs/README.md` (lines 40, 78-91), `docs/architecture/security-model.md:411`
  (`jq empty agent-index/docs-manifest.json`), and
  `docs/architecture/tui-and-clientux.md:322` to drop their references.
- Edit `docs/research/README.md:29` to drop its manifest clause.
- In `loop-develop/current-todo/004-todo.md`, strike or mark moot P1.17/P1.18
  under Milestone 6 since the feature no longer exists.
- `grep -rn "docs-manifest\|reference-plan\|documentation-system\|docguard\|agent-index"`
  across the repo (excluding `loop-develop/history`) and fix any stragglers.

Verification:

```sh
go build ./...
go test -count=1 ./internal/architecture
git diff --check
grep -rn "docs-manifest.json\|reference-plan.md\|documentation-system.md\|docguard\|agent-index" . --include=*.md --include=*.go --include=*.json --include=*.py --include=*.txt | grep -v loop-develop/history
```

### P0.2 Delete agent-index/ and its manifest-check ceremony; llms.txt already covers the orientation

Finding: `docguard_stop.py`'s `check_manifest()` (lines 223-234) only runs
`json.load()` on `agent-index/docs-manifest.json` — no other Go or Python code
reads any field from it (`grep -rn agent-index --include=*.go` is empty).
`agent-index/generated/repo-map.md` (50 lines) is a hand-seeded duplicate of
`docs/architecture.md`'s guarded Package Map, marked "Seeded manually...
Replace with a generated file once docsgen exists," and
`generated/reference-plan.md` designs that future system with zero
implementing code. `llms.txt`'s "Start Here"/"Architecture Canon" sections
already give equivalent, more compact orientation, so nothing is lost by
deletion. Blast radius beyond the obvious files: `docs/README.md` (Machine
Indexes, lines 78-91), `docs/research/README.md:29`,
`docs/architecture/security-model.md:411`, and
`loop-develop/current-todo/004-todo.md` (active P1.20, line 1046, "Refresh
manifest/source metadata or clarify semantics").

Target files:

- `agent-index/docs-manifest.json`
- `agent-index/README.md`
- `agent-index/generated/reference-plan.md`
- `agent-index/generated/repo-map.md`
- `docs/README.md`
- `llms.txt`
- `loop-develop/current-todo/004-todo.md`

Checklist:

- Delete the `agent-index/` directory: `docs-manifest.json`, `README.md`,
  `generated/reference-plan.md`, `generated/repo-map.md` (skip if already
  removed by P0.1).
- `AGENTS.md` (line 110): remove the `agent-index/docs-manifest.json` bullet.
- `.agents/rules/documentation.md`: remove the manifest + reference-plan Read
  Order steps and renumber; drop the `agent-index/` row under Where Content
  Belongs.
- `.agents/rules/stop-hook-docguard.md`: remove `agent-index` from Docs
  Touched, Docs-Sensitive paths, and its "Manifest JSON" section (skip if
  already deleted by P0.1).
- `docs/README.md`: remove the Machine Indexes `agent-index` links (lines
  78-80) and the `docs-manifest.json` mention (line 91).
- `docs/architecture/security-model.md:411`: drop `jq empty
  agent-index/docs-manifest.json` from Verification Anchors, keeping `git
  diff --check` and `go test -count=1 ./internal/architecture`.
- `docs/research/README.md:29`: drop the `agent-index/docs-manifest.json`
  clause from the legacy-file-move rule.
- `llms.txt`: remove the Machine Indexes section (lines 53-61) and the
  reference-plan.md Coverage Note link (line 68).
- `docguard_stop.py`: delete `MANIFEST`, `check_manifest()`, and its call
  site, and strip `agent-index` handling from `is_doc_surface()`/
  `is_docs_sensitive_change()` (skip if already deleted by P0.1).
- `loop-develop/current-todo/004-todo.md`: resolve P1.20 (line 1046) with a
  one-line note that `agent-index` was removed; leave Evidence Log history
  untouched.
- `grep -rn agent-index .` (excluding `loop-develop/history`) to confirm no
  live references remain.

Verification:

```sh
git diff --check
go test -count=1 ./internal/architecture
go build ./...
go vet ./...
grep -rn 'agent-index' --include=*.md --include=*.go --include=*.json --include=*.py . | grep -v loop-develop/history
```

### P0.3 Make checkpoint's post-execution (after) scan fingerprint-first instead of re-reading every file's full content

Finding: `checkpoint.Begin` (`internal/checkpoint/checkpoint.go:124`) and
`Tracker.Complete` (line 137) both call `snapshotTargets` (line 471), which
for `shell_exec` (the only `Recursive: true` target, `targetsForTool` lines
447-458) does `filepath.WalkDir` and, per regular file, calls `snapshotPath`
(line 531) which unconditionally does `os.ReadFile` (line 552) +
`sha256.Sum256` (line 556) + base64-encode (line 562) — the `MaxFileBytes`
check (line 560) only gates whether `ContentBase64` gets populated, not
whether the file gets read. This runs twice per `shell_exec` call via
`internal/agent/tool_attempt.go` (`Begin` at line 105, `Complete` at line
271). Measured: ~94ms overhead on this repo's 431 tracked files. Deferring
ALL reads to diff-time is not viable: by `Complete()` time `shell_exec` has
already run and may have overwritten files, so the true pre-image can't be
recovered retroactively, and undo/redo (`internal/gateway/gateway.go:318-319`,
`handleSessionUndo`/`handleSessionRedo`) depends on that pre-image. Keep full
capture at `Begin`; make only the after/`Complete` scan cheap via
`Lstat`-only fingerprinting (mtime+size+mode), re-reading full content only
for paths whose fingerprint changed vs. `before`.

Target files:

- `internal/checkpoint/checkpoint.go`
- `internal/checkpoint/checkpoint_test.go`
- `internal/agent/tool_attempt.go`

Checklist:

- Add an unexported mtime map the `Tracker` keeps alongside `before` (from
  `info.ModTime()` during `Begin`'s walk) — do not change the serialized
  `FileState`/`PatchRecord` JSON schema (lines 99-108).
- Keep `Begin`'s `snapshotTargets` call (line 130) exactly as-is — the only
  point where the true pre-image can be captured.
- Add `snapshotTargetsFast(opts, targets, before, beforeMTimes)` used only by
  `Tracker.Complete`: `os.Lstat`-only walk; for each path, if new or its
  (mtime, size, mode) differs from `before`'s, fall back to `snapshotPath`
  for that one path; otherwise reuse `before[path]` verbatim.
- Wire `Tracker.Complete` (line 141) to call `snapshotTargetsFast` instead of
  `snapshotTargets`.
- Handle deletions the same way the existing recursive branch does (lines
  486-489): `os.Lstat` `IsNotExist` still yields `FileState{}`.
- Extend `internal/checkpoint/checkpoint_test.go` alongside
  `TestCheckpointShellChangedDetectsCreatedModifiedDeletedFiles` (line 312):
  build a tree of ~2000 untouched files plus 1 modified/1 added/1 deleted,
  assert identical `PatchRecord.Files` output to today and that full reads
  during `Complete` are O(changed) not O(total).
- Run `go test ./internal/checkpoint/...` to confirm restore/redo/conflict
  tests are unchanged (undo/redo must be bit-identical for real edits).
- Run `internal/agent` tool-attempt tests to confirm `turnChangeTracker.Complete`
  call sites (tool_attempt.go lines 105, 271) and `recordTurnChange`/
  `turnChangeEvent` (lines 653, 687) are unaffected.
- Optional bonus: in `Begin`'s unavoidable full-read path, skip `os.ReadFile`
  for files whose `Lstat` size already exceeds `opts.MaxFileBytes`, just
  record size/mode/mtime and mark `Large: true`.
- Manually verify: run `shell_exec` in a repo with a few thousand tracked
  files and confirm latency drops, then drive undo (`POST
  /v1/sessions/{id}/undo`) then redo on a `shell_exec`-driven edit to confirm
  content restores correctly end-to-end.

Verification:

```sh
go build ./...
go test ./internal/checkpoint/... -v
go test ./internal/agent/... -run TestToolAttempt -v
go test ./internal/gateway/... -run Undo -v
go test ./internal/gateway/... -run Redo -v
go vet ./internal/checkpoint/... ./internal/agent/...
```

## P1 Milestone 2 - Delete Point-In-Time And One-Off Ceremony

Goal: remove artifacts and code paths that exist only to produce a one-time,
hand-verified snapshot or a one-time migration/audit pass rather than to serve
Billy's daily solo loop. Each deletion here is fully reproducible on demand
(`doctor -json`, a live `ssh`, a throwaway script) so nothing durable is lost -
only the bureaucracy of maintaining it in git is lost.

### P1.1 Delete the stale dated production-inventory snapshot and its backlinks

Finding: `ops/production-inventory-2026-07-04.md` is a one-time, hand-verified
SSH snapshot added in a single commit (`git log --oneline -- ops/production-inventory-2026-07-04.md`
shows exactly one commit, `aa974b9 Capture production service inventory`),
timestamped `2026-07-04T16:16:50Z` inside the file itself (line 3). It freezes
hostnames, the production git commit (`ce3f2c943ee2a08672a858a701cafa0a9e4f62fc`),
binary SHA-256 (`02c1e3cd...`), systemd unit paths, and unauthenticated
route-probe results (`/v1/config 200`, `/v1/mcp 200`, `/v1/tools 200`,
`/v1/processes 200`, lines 70-72). The doc's own text (lines 73-76) already
admits it is stale/misleading: "do not treat these route status codes as the
desired post-deploy auth behavior" because `main` has since gained gateway
read-route hardening. `grep -rn "production-inventory" --include='*.go' .`
returns nothing - no Go code references the file. It is linked from
`ops/README.md` (line 20 in the Runbooks list, and line 46-47 in First
Response), `ops/production-services.md` (line 7 and lines 15-16), and
`loop-develop/current-todo/004-todo.md` line 1839 (a historical evidence-log
entry for the completed P0.16 slice, not an active task). All of these will
rot the next time the VPS state changes. This is exactly the
incident-grade/point-in-time-artifact ceremony the harness owner wants hunted
for a solo tool - `doctor -json` plus a one-off `ssh` already reproduces
everything reproducible, and the parts that aren't (kernel version, unit file
paths) are trivia nobody needs preserved in git.

Target files:

- `ops/production-inventory-2026-07-04.md`
- `ops/README.md`
- `ops/production-services.md`
- `loop-develop/current-todo/004-todo.md`

Checklist:

- `git rm ops/production-inventory-2026-07-04.md`.
- Edit `ops/README.md`: remove the `- [Production inventory - 2026-07-04](production-inventory-2026-07-04.md): ...` bullet from the Runbooks list (line 20-22).
- Edit `ops/README.md`: in First Response, replace the sentence "Production is described by the project contract as `root@82.23.163.16` under `/root/billyharness`. The most recent live inventory is `ops/production-inventory-2026-07-04.md`:" (lines 45-47) with a pointer to running `./bin/fast-agent-harness doctor -json` and a live `ssh root@82.23.163.16` instead of a dated doc; keep the existing `ssh`/`cd` code fence that follows.
- Edit `ops/production-services.md` line 3-7 (the "Last verified" preamble): remove "Live production service facts were checked over SSH in `ops/production-inventory-2026-07-04.md`." and replace with a one-line note that facts were checked live over SSH, without naming a persisted file.
- Edit `ops/production-services.md` lines 14-17 (Production Entrypoint): remove "The current dated inventory is `ops/production-inventory-2026-07-04.md`; verify host identity again before changing production:" and replace with "Verify host identity live before changing production:" directly above the existing `ssh`/`cd` fence.
- Edit `ops/production-services.md` line 62-63 ("See the dated inventory for binary checksum, commit, doctor output, and route probe details.") - remove or replace with "Re-run `doctor -json` and `systemctl cat` on the host for current binary checksum, commit, and route probe details."
- Edit `loop-develop/current-todo/004-todo.md` line 1839: this is a historical evidence-log entry for the already-completed P0.16 slice, not a live pointer - do not delete the log entry, just reword the sentence so it no longer names a file that will not exist (e.g. "The dated source-of-truth inventory was recorded at `ops/production-inventory-2026-07-04.md` (later deleted as stale point-in-time ceremony; see P1.1).").
- Run `grep -rn "production-inventory" --include='*.md' --include='*.go' .` after all edits to confirm zero remaining references outside this milestone's own text.
- If Billy wants a fresh point-in-time snapshot later, generate it as a throwaway file outside git (e.g. `/tmp` or the loop scratchpad) rather than committing a new dated doc.

Verification:

```sh
grep -rn "production-inventory" --include='*.md' --include='*.go' .
git status --short ops/ loop-develop/current-todo/004-todo.md
go build ./...
```

### P1.2 Delete the incident-bundle CLI command (cmd/fast-agent-harness/incident.go)

Finding: `cmd/fast-agent-harness/incident.go` (426 lines) implements
`fast-agent-harness incident collect`, dispatched from `main.go` line 53-54
(`case "incident": return incidentCmd(os.Args[2:])`) and advertised in
`usage()` at line 94. `incidentCmd` (line 61) calls `incidentCommand` (line
65) which calls `incidentCollectCommand` (line 82) and
`collectIncidentBundleFromResolved` (line 136) to package doctor/config/auth/
mcp/session-inspect/session-context/session-transcript/logs into a 10+ file
redacted bundle directory plus a manifest, via `incidentBundleWriter`'s
`writeJSON`/`writeText`/`copyTextFile`/`writeFile` methods (lines 286-360) and
`collectIncidentMCP`/`collectIncidentLogs` (lines 361-395). This is
postmortem/handoff packaging built for a team; a solo owner already has
`doctor -json`, `sessions inspect`, `sessions context`, `sessions export`, and
`sessions debug` (all separately listed in `main.go`'s `usage()`) covering the
same underlying data without a manifest/bundle step. `grep -rn "incidentCmd\|incidentCollectCommand\|incidentBundleWriter" cmd/fast-agent-harness/*.go`
confirms only `main.go`'s dispatch/usage lines and `incident.go` itself
reference these symbols. `cmd/fast-agent-harness/incident_test.go` (195
lines) covers this command via `TestIncidentCollectWritesRedactedBundle`, but
also contains `TestSessionsExportRedactsTranscriptSurfaces` (line 103), which
is genuinely about `sessions export` redaction (calls `sessionsCommand([]string{"export", ...})`,
not the incident bundle) and must not be deleted with the rest of the file -
`cmd/fast-agent-harness/main_test.go` already has adjacent `sessions export`
coverage around lines 562-585, so this test belongs there instead.

Target files:

- `cmd/fast-agent-harness/incident.go`
- `cmd/fast-agent-harness/incident_test.go`
- `cmd/fast-agent-harness/main.go`
- `cmd/fast-agent-harness/main_test.go`
- `ops/README.md`

Checklist:

- Remove `case "incident": return incidentCmd(os.Args[2:])` (main.go lines 53-54) from `run()` in `cmd/fast-agent-harness/main.go`.
- Remove the `fmt.Println("  incident collect -session SESSION_ID -out DIR [-dir SESSION_DIR] [-repo DIR] [-logs=true] [-mcp=true] [-json]")` line (main.go line 94) from `usage()`.
- Before deleting `incident_test.go`, move `TestSessionsExportRedactsTranscriptSurfaces` (lines 103-140) into `cmd/fast-agent-harness/main_test.go` near the existing `sessions export` coverage (around line 562), along with the `createIncidentTestSession` helper (lines 142-179, rename to something export-neutral such as `createSessionsExportTestSession` if desired) and `assertIncidentRedacted` (lines 181-195, rename similarly if desired); adjust imports in `main_test.go` accordingly (`gateway`, `provider`, `tools` may already be imported there - check before adding duplicates).
- Delete `cmd/fast-agent-harness/incident.go` entirely (426 lines: `incidentCmd`, `incidentCommand`, `incidentCollectCommand`, `collectIncidentBundleFromResolved`, `incidentBundleWriter` and its methods, `collectIncidentMCP`, `collectIncidentLogs`, `incidentBundleReadme`, `redactIncidentText`, `cleanIncidentBundleRelPath`, `incidentUsage`).
- Delete `cmd/fast-agent-harness/incident_test.go` entirely once the sessions-export test has been relocated.
- `grep -rn "incident" cmd/fast-agent-harness/*.go` to confirm zero remaining references (other than the word appearing in unrelated prose, if any).
- Confirm `createIncidentTestSession`/`fakeDoctorRunner`/`doctorRunnerKey` are not silently orphaned: `fakeDoctorRunner` and `doctorRunnerKey` are defined in `doctor_test.go` (lines 18, 37) and used there independently of `incident_test.go`, so they stay untouched; only the renamed session-creation helper moves with the test.
- Confirm `doctor.go`'s shared helpers (`doctorCommandRunner`, `runDoctorCommand`/`collectDoctorReportFromResolved`, `doctorManagedServices`, etc.) remain referenced by `doctor.go`/`doctor_test.go` independent of `incident.go` (they are - `incident.go` only consumed `doctorCommandRunner` as a parameter type).
- Update `ops/README.md`'s "First Response" section (line 39-43), which currently shows `./bin/fast-agent-harness incident collect -session SESSION_ID -out /tmp/billyharness-incident`: remove that block and its "To preserve a redacted local bundle for one failed session" lead-in, and remove `incident collect` from the "Prefer status commands that already sanitize values" list in Secrets And Redaction (line 61).
- Run `gofmt -l` on all touched files to catch unused imports left behind in `main_test.go`/`main.go`.

Verification:

```sh
go build ./...
go test ./cmd/fast-agent-harness/...
go vet ./cmd/fast-agent-harness/...
grep -rn "incident" cmd/fast-agent-harness/*.go
grep -rn "incident collect" ops/README.md
```

### P1.3 Delete the one-time MCP config migration scanner (config mcp-migrate)

Finding: `internal/config/mcp_migration.go` is 776 lines implementing a
multi-format JSON+TOML scanner over Codex/Claude/opencode config candidates
(`defaultMCPMigrationCandidates`, line 155), with key-alias parsing
(`parseTOMLMCPMigration` line 232, `parseJSONMCPMigration` line 271), and
TOML-suggestion rendering (`renderMCPMigrationTOML` line 431). It is reached
only via `config mcp-migrate`/`mcp-migration` in
`cmd/fast-agent-harness/config_cmd.go` line 21-22
(`case "mcp-migrate", "mcp-migration": return configMCPMigrateCommand(args[1:], stdout)`),
whose body (`configMCPMigrateCommand`, line 144) calls
`config.ScanMCPMigration`/`config.FormatMCPMigrationReport`. `grep -rln`
across every helper defined in the file (`cleanStringMap`, `sortedMapKeys`,
`sortedAnyMapKeys`, `stringValue`, `commandAndArgsValue`, `stringListValue`,
`stringMapValue`, `boolValue`, `optionalBoolValue`, `numberValue`,
`quoteBareOrDottedKey`, `isTOMLBareKey`, `tomlStringList`, `tomlStringMap`)
confirms each is used only inside `mcp_migration.go` itself - nothing else in
`internal/config` or any other package depends on them, so the whole file is
a clean, isolated deletion. It is exercised only by
`TestScanMCPMigrationReportsSuggestionsAndRedactsValues`
(`internal/config/mcp_hooks_config_test.go:110-201`, cleanly bounded before
the next `func TestLoadHooksParsesCommandHooks` at line 202) and
`TestConfigMCPMigrateCommandPrintsRedactedSuggestions`
(`cmd/fast-agent-harness/main_test.go:322-364`, bounded before
`func isolateRuntimeConfig` at line 366). It performs no writes and touches no
gateway auth, Telegram owner checks, or MCP trust-boundary code - it only
reads other tools' config files and prints a redacted TOML suggestion. This
is a one-time migration scanner Billy already used once; a pure ceremony/dead-
weight removal matching the north star.

Target files:

- `internal/config/mcp_migration.go`
- `cmd/fast-agent-harness/config_cmd.go`
- `cmd/fast-agent-harness/main.go`
- `internal/config/mcp_hooks_config_test.go`
- `cmd/fast-agent-harness/main_test.go`

Checklist:

- Delete `internal/config/mcp_migration.go` entirely (776 lines: `MCPMigrationOptions`/`Report`/`File`/`Server`/`Suggestion` types, `ScanMCPMigration`, `defaultMCPMigrationCandidates`, `dedupeMCPMigrationCandidates`, `parseMCPMigrationFile`, `parseTOMLMCPMigration`, `parseJSONMCPMigration`, `jsonMCPContainers`, `migrationRawServerFromJSON`, `migrationServerFromRaw`, `mcpMigrationSuggestion`, `renderMCPMigrationTOML`, `FormatMCPMigrationReport`, and all its private string/bool/number/TOML-rendering helpers).
- In `cmd/fast-agent-harness/config_cmd.go`: remove the `case "mcp-migrate", "mcp-migration": return configMCPMigrateCommand(args[1:], stdout)` branch (lines 21-22).
- In `cmd/fast-agent-harness/config_cmd.go`: delete the `configMCPMigrateCommand` function body (starting line 144 through its closing brace, roughly lines 144-165).
- In `cmd/fast-agent-harness/config_cmd.go`: remove the `fmt.Fprintln(w, "       fast-agent-harness config mcp-migrate [-file FILE] [-json]")` usage line (line 170).
- In `cmd/fast-agent-harness/main.go`: remove the `fmt.Println("  config mcp-migrate [-file FILE] [-json]")` help line (line 77).
- In `internal/config/mcp_hooks_config_test.go`: delete `TestScanMCPMigrationReportsSuggestionsAndRedactsValues` (lines 110-201, bounded exactly before the next `func TestLoadHooksParsesCommandHooks` at line 202) - do not touch the surrounding `TestLoadMCPServersRejectsUnknownToolRisk`/`TestLoadMCPServersParsesRemoteAsUnsupportedDiagnostic`/`TestLoadHooksParsesCommandHooks` tests.
- In `cmd/fast-agent-harness/main_test.go`: delete `TestConfigMCPMigrateCommandPrintsRedactedSuggestions` (lines 322-364, bounded before `func isolateRuntimeConfig` at line 366) - do not touch `isolateRuntimeConfig` or neighboring tests (`TestDoctorRejectsInvalidRuntimeConfig` above, `TestMemoryCommandAddAndList` below).
- After deletions, run `goimports`/`go vet` on the four edited/deleted-from files to catch now-unused imports (e.g. `bytes`, `path/filepath`, or a TOML-adjacent import) in `mcp_hooks_config_test.go`, `main_test.go`, and `config_cmd.go`.
- Run `go build ./...` and `go vet ./...` to confirm no dangling references to `ScanMCPMigration`, `MCPMigrationOptions`, `MCPMigrationReport`, or `FormatMCPMigrationReport` remain anywhere.

Verification:

```sh
go build ./...
go vet ./...
go test ./internal/config/...
go test ./cmd/fast-agent-harness/...
grep -rn "mcp-migrate\|MCPMigrat\|mcp_migration" --include='*.go' . | grep -v '.git' || echo CLEAN
```

### P1.4 Delete internal/config/hygiene_test.go (banned-word/literal grep ceremony)

Finding: `internal/config/hygiene_test.go` (170 lines) is pure naming-hygiene
ceremony wired into the real CI gate (`go test -count=1 ./...` in
`.github/workflows/ci.yml` line 43 runs on every push/PR - this is a distinct
mechanism from the separate "Strict hygiene" step at line 51-52, which
invokes `go run ./cmd/fast-agent-harness hygiene -repo "$GITHUB_WORKSPACE" -strict`
and is unaffected by this deletion). Its four tests
(`TestNoAmbiguousContextWindowRuntimeFallbacks` line 12,
`TestNoStaleContextLimitTerminologyInRuntimeAndLiveDocs` line 41,
`TestModelContextWindowLiteralsStayInModelInfo` line 53,
`TestStatusFormatterLabelsStayCanonical` line 70) walk every tracked `.go`
file under `cmd/` and `internal/` via `runtimeGoFiles`/`readRepoFile` (lines
121, 154) and `regexp`/`strings.Contains` and fail the build on banned
substrings like `"context_limit"` or a bare numeric `ContextWindowTokens`
literal outside `internal/modelinfo/modelinfo.go` (see
`assertNoAmbiguousMillionTokenContextWindow`, line 95, and its escape-hatch
checks for the literal comment strings `"hygiene: allow context_limit"` and
`"hygiene: allow context-window literal"` at lines 46 and 62). This tests
spelling/vocabulary, not behavior, requires hand-maintained escape hatches
whenever legitimate code needs the term, and matches the exact docguard/
hygiene-gate anti-pattern this repo is meant to hunt. It touches zero security
surface (no gateway auth, Telegram owner checks, MCP trust, or secret
redaction involved). `grep -rn "hygiene: allow" --include='*.go' .` confirms
no file anywhere currently uses either escape-hatch marker, so nothing is
silently relying on them. The file's private helpers
(`repoRoot`/`runtimeGoFiles`/`runtimeAndLiveDocFiles`/`readRepoFile`) are
local to this file; `internal/architecture/architecture_test.go` has its own,
independent `repoRoot` (line 92) that is unaffected by this deletion.

Target files:

- `internal/config/hygiene_test.go`

Checklist:

- Delete `internal/config/hygiene_test.go` entirely (all 170 lines, all 4 test functions and their private helpers `repoRoot`, `runtimeGoFiles`, `runtimeAndLiveDocFiles`, `readRepoFile`, `assertNoAmbiguousMillionTokenContextWindow`).
- Run `grep -rn "hygiene: allow" --include='*.go' .` to reconfirm no other file references the escape-hatch comment markers this test looked for (already verified: none found) - if any turn up unexpectedly, leave the comment text alone since nothing enforces it anymore.
- Check the one genuinely load-bearing invariant among the four tests: `TestModelContextWindowLiteralsStayInModelInfo` wanted numeric `ContextWindowTokens` literals confined to `internal/modelinfo/modelinfo.go`. Read `internal/modelinfo/modelinfo.go` to confirm that's already naturally true (it defines the model table) - if so, no replacement guard is needed; the convention is self-evident from the package's purpose.
- Do not add a replacement grep test. If Billy later wants a compile-time guard, that would mean changing the `ContextWindowTokens` field to a type constructible only from `internal/modelinfo` (unexported field + constructor), not a new test file - out of scope for this deletion.
- Leave `docs/architecture.md` and the `cmd/fast-agent-harness hygiene` CLI tool untouched - that is the separate, unrelated mechanism invoked as "Strict hygiene" in `.github/workflows/ci.yml` line 51-52, not affected by this deletion.
- Run `go build ./...` to confirm nothing else in the `config` package referenced symbols from the deleted file.
- Run `go test ./internal/config/...` to confirm the package still compiles and passes with the file gone.
- Run `go vet ./...` to catch any stray references.
- Run the full `go test -count=1 ./...` suite locally to confirm CI's "Tests" step will pass without this file.

Verification:

```sh
go build ./...
go test ./internal/config/...
go vet ./...
go test -count=1 ./...
grep -rn "hygiene: allow" --include='*.go' .
git diff --stat internal/config/hygiene_test.go
```

### P1.5 Delete the Telegram admission ledger (admission_store.go) and its redundant assertions

Finding: `internal/telegrambot/admission_store.go` (151 lines) implements
`telegramAdmissionStore`, which only ever appends
(`RecordIgnored` line 56, `RecordAdmitted` line 78, `RecordAbandoned` line
102); `lastSeqLocked` (line 131) replays the whole unrotated file on every
append purely to self-validate sequence/schema, and no other code parses the
file back. But this isn't inert: append errors propagate -
`poller.go` line 83's `ackIgnoredUpdate` returns early on
`b.admit.RecordIgnored(update, reason)` error, skipping `b.ackOffset`;
`runner.go` line 277's `admitTelegramPromptUpdate` returns an error from
`b.admit.RecordAdmitted(...)` failure; and `bot.go` line 90-91's `New()` fails
bot construction entirely if `admit.RecordAbandoned(...)` (called from
`state_runtime.go` line 242 inside `reconcilePendingInputsOnStartup`) hits a
corrupt/gapped ledger. `runner.go` line 294 already logs
update/chat/session/input/state/duplicate/skip_run for every admitted prompt
via `log.Printf`, making the JSONL record largely redundant with existing
logs. `poller_test.go` has 5 call sites reading the ledger back
(`readTelegramAdmissionRecords(t, admissionPathForState(statePath))` at lines
154, 250, 353, 638, and `readTelegramAdmissionRecords(t, newTelegramAdmissionStore(statePath).path)`
at line 550, plus the `admissionPathForState` helper itself at lines 770-771
and `readTelegramAdmissionRecords` at line 829), but in every case the same
fact is already checked via offset/chat-state/harness-channel assertions in
the same test, so no unique behavior coverage is lost by deleting them. The
store is also documented in `docs/architecture/telegram-and-operator-surfaces.md`
(linked at line 28 and described at lines 214-220).

Target files:

- `internal/telegrambot/admission_store.go`
- `internal/telegrambot/state_runtime.go`
- `internal/telegrambot/poller.go`
- `internal/telegrambot/runner.go`
- `internal/telegrambot/bot.go`
- `internal/telegrambot/poller_test.go`
- `docs/architecture/telegram-and-operator-surfaces.md`

Checklist:

- `internal/telegrambot/bot.go`: remove the `admit *telegramAdmissionStore` field from `Bot` (line 44), drop the `admit := newTelegramAdmissionStore(opts.StatePath)` call (line 90), the `admit:    admit,` struct-literal line (line 99), and update the `reconcilePendingInputsOnStartup(context.Background(), &state, store, admit, harness)` call (line 91) to drop the `admit` argument.
- `internal/telegrambot/state_runtime.go`: drop the `admit *telegramAdmissionStore` parameter from `reconcilePendingInputsOnStartup` (line 207) and remove the `admit.RecordAbandoned(...)` call at line 242, keeping the existing `log.Printf("telegram abandoned pending input after restart ...")` and the chat-state clearing/save logic unchanged.
- `internal/telegrambot/poller.go`: remove the `b.admit.RecordIgnored(update, reason)` call (line 83) in `ackIgnoredUpdate` and call `b.ackOffset(update.UpdateID)` unconditionally (do not gate acking on any store result).
- `internal/telegrambot/runner.go`: remove the `b.admit.RecordAdmitted(...)` call and its error-return branch around line 277-279 in `admitTelegramPromptUpdate`; keep the existing `log.Printf` at line 294 (already logs update/chat/key/session/input/state/duplicate/skip_run).
- Delete `internal/telegrambot/admission_store.go` entirely (`newTelegramAdmissionStore`, `RecordIgnored`, `RecordAdmitted`, `RecordAbandoned`, `append`, `lastSeqLocked`, `telegramPromptHash`, and the `telegramAdmissionRecord` type).
- `internal/telegrambot/poller_test.go`: delete the `admissionPathForState` (lines 770-771) and `readTelegramAdmissionRecords` (line 829 onward) helpers and the 5 `records := readTelegramAdmissionRecords(...)` assertion blocks at lines 154, 250, 353, 550, 638 (including the `newTelegramAdmissionStore(statePath).path` call at line 550); leave the surrounding offset/chat-state/harness-channel assertions in place since they already verify the same behavior.
- `internal/telegrambot/bot_test.go` and any other test file: grep for remaining `admit` field references from struct literals constructing `Bot` directly and remove them.
- `docs/architecture/telegram-and-operator-surfaces.md`: remove the "Admission records are stored..." paragraph (lines 214-220) and both `admission_store.go` links (lines 28 and 218), rephrasing the admission-flow description to say ignored/admitted/abandoned events are logged via `log.Printf`, not persisted.
- Run `gofmt`/`goimports` on all touched files to catch now-unused imports (e.g. `crypto/sha256`, `encoding/hex` in `admission_store.go` disappear with the file; check `poller.go`/`runner.go`/`state_runtime.go`/`bot.go` for anything that became unused).
- Note in a commit message or runbook aside (not a required code step) that operators may delete any leftover `*.admissions.jsonl` files from `$BILLYHARNESS_HOME`.

Verification:

```sh
gofmt -l internal/telegrambot/
go build ./internal/telegrambot/...
go vet ./internal/telegrambot/...
go test ./internal/telegrambot/... -run TestTelegram -v
go test ./internal/telegrambot/...
grep -rn "telegramAdmissionStore\|RecordIgnored\|RecordAdmitted\|RecordAbandoned\|admissions.jsonl" internal/telegrambot/ docs/
```

### P1.6 Delete architecture.md's dead-prose "Guarded Rules" section, folding any gaps into the Package Map table

Finding: `docs/architecture.md` lines 85-96 ("File Size Budget Exceptions")
cite phase owners like `P1.2/P1.7/P1.8/P1.10` for a one-person project -
process bookkeeping with no automated enforcement of the exception table
itself, but these ARE live tracked backlog items (confirmed via
`grep -n 'P1\.[0-9]' loop-develop/current-todo/004-todo.md` matching entries
such as "### P1.7 Separate native strict schema validation..."), so that
table stays untouched. Lines 98-114 ("Guarded Rules") restate import facts
already present in the Package Map table's "Forbidden imports and owner
notes" column (lines 14-65) as unenforced prose -
`internal/architecture/architecture_test.go` only parses that table (no
`grep -n "eventlog\|must directly import"` hits in the test file), so this
prose section can silently drift from what's actually enforced. Cross-checking
each bullet: the `protocol`/`eventlog`/`clientux`/`clientux/projector`/
`gatewayapi`/`gatewayclient`/`gatewaybase`/`tui/render`/`tui/selection`/
`tui/transcript`/`tools`/`telegrambot`/`tui` bullets (lines 100-110, 112-114)
already restate facts present in the corresponding Package Map rows (e.g. row
21 `clientux`, row 33 `gatewaybase`, row 64 `tui/transcript`). The one
exception is line 111, "`internal/trace` and `internal/gateway` must
directly import `eventlog`" - this is a positive requirement (must include),
not a restriction, and it isn't restated anywhere else in the table's
"Forbidden imports" prose (row 31 `gateway` and row 59 `trace` list `eventlog`
only as an *allowed* import, which is weaker than *must* import it directly).

Target files:

- `docs/architecture.md`

Checklist:

- Open `docs/architecture.md` and read the `## Guarded Rules` section (currently lines 98-114, the remainder of the file after "File Size Budget Exceptions").
- For each Guarded Rules bullet, find the matching Package Map row (line 12 onward) and confirm its "Forbidden imports and owner notes" column already states the same fact - for `protocol`, `eventlog`, `clientux`, `clientux/projector`, `gatewayapi`, `gatewayclient`, `gatewaybase`, `tui/render`, `tui/selection`, `tui/transcript`, `tools`, `telegrambot`, and `tui` rows this is already true, so no edit is needed there.
- Handle the one bullet that is NOT purely import-related: "`internal/trace` and `internal/gateway` must directly import `eventlog`" - append it verbatim (or a short paraphrase, e.g. "Must directly import `eventlog`; do not reintroduce separate lifecycle validation.") to the `trace` (row 59) and `gateway` (row 31) rows' "Forbidden imports and owner notes" cells before deleting the section.
- Delete the entire `## Guarded Rules` heading and all its bullet lines (lines 98-114) from `docs/architecture.md`.
- Do NOT touch the "File Size Budget Exceptions" table (lines 78-96) or its `P1.2/P1.7/P1.8/P1.10` "Current exception owner" references - these are live tracked backlog items, not stale bureaucracy, so removing them would lose real traceability for no benefit.
- Re-read the full file after edits to confirm no other section still references "Guarded Rules" and that the Package Map table's column count/formatting is unchanged (still 4 columns, pipe-aligned).
- Run `go test ./internal/architecture/...` to confirm `TestInternalPackageBoundaries` (or equivalently named test) still passes and the table is still parsed correctly.
- Grep the rest of the repo for any other doc that links to the `## Guarded Rules` anchor and update those links if any exist.

Verification:

```sh
go test ./internal/architecture/...
go vet ./internal/architecture/...
grep -n "Guarded Rules" docs/architecture.md
grep -n 'P1\.[0-9]' loop-develop/current-todo/004-todo.md
```

## P1 Milestone 3 - Delete Dead Compliance Plumbing And Collapse Duplicate Packages

Goal: keep shrinking Billyharness toward the solo-tool north star by deleting
code that only exists to serve an imaginary enterprise/compliance surface
(FedRAMP flag threading) and by collapsing four families of hand-duplicated
helper packages (`gatewaybase`, `session`, `tooloutput`, and the
eventCallID/metadata/TodoState decoders) into the single real package that
already owns each concept. Finish by retiring two stale legacy research docs
and the disclaimer scaffolding that only exists to explain why they are stale.

### P1.7 Delete FedRAMP compliance plumbing from Codex auth/provider code

Finding: `internal/codexauth/codexauth.go:69-82` defines `FedRAMPFromClaims`,
which reads `chatgpt_account_is_fedramp` off either the top-level JWT claims or
the nested `https://api.openai.com/auth` claim. It is called from 6 sites in
`internal/provider/codex_auth.go`: the `codexAuth.FedRAMP bool` field (line 32),
set in `loadCodexAuth` (line 56), in `refresh()`'s `idFedRAMP` branch (lines
141-144) and its `if !a.FedRAMP` fallback (lines 148-150), in
`readCodexAuthFile` (line 219) and its `if !auth.FedRAMP` fallback (lines
231-233), and in the anonymous PAT whoami metadata struct's
`FedRAMP bool \`json:"chatgpt_account_is_fedramp"\`` field (line 257) consumed
in `hydratePAT` (line 266). It is threaded further into
`internal/provider/codex_provider.go`: `codexAuthSnapshot.FedRAMP` (line 50),
the conditional `if auth.FedRAMP { httpReq.Header.Set("X-OpenAI-Fedramp", "true") }`
(lines 153-155), and the snapshot copy in `authSnapshot()` (line 216). This is
OpenAI's Enterprise/GovCloud ChatGPT compliance flag; for Billy's personal Codex
account it is always false with no code path that ever sets it true. Matching
dead assertions live in `codexauth_test.go:10-30`
(`TestJWTClaimsExtractAccountFedRAMPAndExpiration`), `codex_auth_test.go` (JSON
payloads at lines 24, 65, 114, 214, 285 and `auth.FedRAMP` assertions at lines
49, 175, 222, 307), and `codex_provider_test.go` (`FedRAMP: true` literal at
line 75).

Target files:

- `internal/codexauth/codexauth.go`
- `internal/codexauth/codexauth_test.go`
- `internal/provider/codex_auth.go`
- `internal/provider/codex_auth_test.go`
- `internal/provider/codex_provider.go`
- `internal/provider/codex_provider_test.go`

Checklist:

- Delete `FedRAMPFromClaims` (`codexauth.go:69-82`) entirely.
- In `codexauth_test.go`, delete the FedRAMP assertions and the
  `chatgpt_account_is_fedramp` claim from the test JWT payload in
  `TestJWTClaimsExtractAccountFedRAMPAndExpiration`; rename/trim the test to
  cover only account ID + expiration if that is all that remains.
- Remove the `FedRAMP bool` field from `codexAuth` (`codex_auth.go:32`) and its
  assignment in `loadCodexAuth` (line 56).
- Remove the `idFedRAMP` branch in `refresh()` (lines 141-144) and the
  `if !a.FedRAMP { a.FedRAMP = ... }` fallback (lines 148-150).
- Remove the assignment in `readCodexAuthFile` (line 219) and its
  `if !auth.FedRAMP` fallback (lines 231-233).
- Remove the `FedRAMP bool` json field from the anonymous whoami metadata
  struct (line 257) and its assignment in `hydratePAT` (line 266).
- Remove `FedRAMP bool` from `codexAuthSnapshot` (line 50), the
  `X-OpenAI-Fedramp` header block (lines 153-155), and the copy in
  `authSnapshot()` (line 216) in `codex_provider.go`.
- Strip `chatgpt_account_is_fedramp` from every test JSON payload and delete
  the `auth.FedRAMP` assertions in `codex_auth_test.go` (4 assertion sites plus
  payload-only occurrences); delete the `X-OpenAI-Fedramp` header expectation
  and `FedRAMP: true` literal in `codex_provider_test.go`.
- Grep for any remaining `fedramp`/`FedRAMP` matches under `internal/` to
  confirm full removal before moving on.

Verification:

```sh
grep -rni fedramp internal/ || true
go build ./...
go vet ./internal/provider/... ./internal/codexauth/...
go test ./internal/provider/... ./internal/codexauth/... -run Codex -v
go test ./internal/provider/... ./internal/codexauth/...
```

### P1.8 Collapse internal/gatewaybase into internal/gatewayapi; delete the 3x-duplicated URL/auth/ready helpers

Finding: `internal/gatewaybase/gatewaybase.go` (136 LOC) defines
`NormalizeBaseURL`, `AuthTokenFromEnv`, `SetAuthHeader`,
`SetAuthHeaderFromEnv`, `UnavailableHint`, `WaitForReady`, plus the
`GatewayAuthTokenEnv`/`LegacyGatewayAuthTokenEnv` consts, once.
`internal/gateway/url.go` re-declares 4 of those functions plus the two consts
as one-line forwards (lines 11-30); `internal/gatewayclient/client.go`
re-declares the identical set a second time (lines 161-207). Worse,
`DoWithReadyRetry`/`UnavailableError`/the connection-refused check are
hand-duplicated with real (not forwarded) logic in both
`internal/gateway/ready.go:13-68` and `internal/gatewayclient/client.go:70-73,
146-208` — functionally identical `client.Do` + `ECONNREFUSED` + retry-after-
`WaitForReady` bodies. Neither `gateway` nor `gatewayclient` needs
`gatewaybase` as a fourth package: both already import `gatewayapi` directly
(confirmed via `client.go`'s heavy `gatewayapi.SessionOwner`/`RunRequest`/etc.
usage), and `gatewayapi` today is just `types.go` importing only `protocol`.
Verified zero import-cycle risk: `internal/config` imports only `modelinfo`,
and `internal/serviceops` has zero internal imports, so `gatewayapi` can safely
depend on both.

Target files:

- `internal/gatewaybase/gatewaybase.go`
- `internal/gateway/url.go`
- `internal/gateway/ready.go`
- `internal/gatewayclient/client.go`
- `internal/gatewayapi/types.go`
- `docs/architecture.md`

Checklist:

- Create `internal/gatewayapi/net.go` (package `gatewayapi`) containing the
  `GatewayAuthTokenEnv`/`LegacyGatewayAuthTokenEnv` consts,
  `NormalizeBaseURL`, `AuthTokenFromEnv`, `SetAuthHeader`,
  `SetAuthHeaderFromEnv`, `UnavailableHint`, `WaitForReady`,
  `DoWithReadyRetry`, `UnavailableError` (with `Error()`/`Unwrap()`), and
  unexported `healthOK`/`normalizeClientHost`/`isConnectionRefused` — copy
  bodies verbatim from `gatewaybase.go` plus `ready.go:13-68`.
- Add `internal/config` and `internal/serviceops` imports to `gatewayapi`
  (confirmed no cycle).
- Delete `internal/gatewaybase/gatewaybase.go` and the now-empty
  `internal/gatewaybase/` directory.
- In `internal/gateway/url.go`: delete the forwarding functions/consts and the
  `gatewaybase` import (lines 8-30); keep `RequiresAuthForAddr`, `addrHost`,
  `isLoopbackHost`, `isLoopbackRemoteAddr` untouched.
- Delete `internal/gateway/ready.go` entirely; update `gateway.go` and any
  other in-package call sites to `gatewayapi.UnavailableError` /
  `gatewayapi.UnavailableHint` / `gatewayapi.WaitForReady` /
  `gatewayapi.DoWithReadyRetry`.
- In `internal/gatewayclient/client.go`: delete the `UnavailableError` type +
  methods (lines 70-73, 146-159), the six forwarding functions and duplicate
  `DoWithReadyRetry` (lines 161-208), and the `gatewaybase` import/const
  aliases; update the `do` method and `New` to call `gatewayapi.X` directly.
- Update external call sites to `gatewayapi.X`: `cmd/fast-agent-harness/doctor.go`,
  `service_cmd.go`, `gateway_client_cmd.go`, `run_cmd.go`, `incident_test.go`,
  `sessions.go`, `main_test.go`, `internal/telegrambot/gateway_client.go`.
- Merge `internal/gateway/url_test.go` + `ready_test.go`'s
  `TestNormalizeBaseURL`/`TestSetAuthHeaderFromEnv`/
  `TestUnavailableHintIncludesRecoveryCommands`/
  `TestDoWithReadyRetryWrapsConnectionRefused` and the duplicate
  `TestNormalizeBaseURL` in `internal/gatewayclient/client_test.go` into a new
  `internal/gatewayapi/net_test.go`; delete `ready_test.go` and the moved
  cases, keeping `RequiresAuthForAddr` tests where they are.
- Update `docs/architecture.md`: delete the `internal/gatewaybase` row (line
  33) and its Guarded Rules bullet (line 106); remove `gatewaybase` from the
  gateway (line 31) and gatewayclient (line 34) Allowed-imports cells and
  bullets; add `config`, `serviceops` to the `gatewayapi` row (line 32) and
  bullet (line 104).

Verification:

```sh
go build ./...
go vet ./...
go test ./internal/architecture/... ./internal/gateway/... ./internal/gatewayclient/... ./internal/gatewayapi/... ./internal/telegrambot/...
go test ./cmd/fast-agent-harness/...
grep -rn "gatewaybase" --include="*.go" . docs/architecture.md
```

### P1.9 Dissolve internal/session: move the run-lock into internal/gateway and the transcript importer into cmd/fast-agent-harness

Finding: `internal/session` bundles two unrelated single-consumer features.
`session.go`'s in-memory run-concurrency lock (`Session`/`Runner`/
`RunnerFunc`/`InputPolicy`/`InputDecision`/`ErrBusy`, 267 LOC) is used only by
`internal/gateway/gateway.go` and `session_events.go` (plus their tests).
`importer.go`'s transcript importer (`ImportTranscript`/`ImportOptions`/
`ImportFormatAuto`) is used only by `cmd/fast-agent-harness/sessions.go`'s
`sessions import` subcommand. Because `internal/gateway/gateway.go:79` already
declares its own `type Session struct` (the persisted gateway session, whose
field `Thread *sessionpkg.Session` at line 83 is exactly the moved lock type),
every one of the 5 importing files —
`gateway.go:31,83,806,832,847`, `session_events.go:13,58`,
`gateway_test.go:29,177,187,231`, `session_events_test.go:25` (5 call sites),
`cmd/fast-agent-harness/sessions.go:19,634,649` — must alias the import as
`sessionpkg`, purely to dodge the name collision, for zero cycle-avoidance
benefit.

Target files:

- `internal/session/session.go`
- `internal/session/importer.go`
- `internal/gateway/gateway.go`
- `internal/gateway/session_events.go`
- `cmd/fast-agent-harness/sessions.go`
- `docs/architecture.md`

Checklist:

- Create `internal/gateway/run_thread.go` with `session.go`'s content
  verbatim except: `package session` -> `package gateway`; rename
  `type Session struct` -> `type runThread struct` to stop colliding with
  `gateway.go:79`'s own `Session`; rename `New`/`NewWithOptions` ->
  `newRunThread`/`newRunThreadWithOptions` (unexported, sole callers are in
  `gateway`); keep `Runner`/`RunnerFunc`/`InputPolicy`/`InputDecision`/
  `Options`/`ErrBusy` exported since `gateway_test.go` and
  `session_events_test.go` reference `RunnerFunc`/`ErrBusy` directly and none
  collide with existing `gateway` identifiers (verified via grep).
- In `gateway.go`: delete the `sessionpkg` import (line 31); change
  `Thread *sessionpkg.Session` (line 83) to `Thread *runThread`; replace
  `sessionpkg.RunnerFunc(...)` (line 806) and `sessionpkg.ErrBusy` (lines 832,
  847) with the local unqualified names.
- In `session_events.go`: delete the `sessionpkg` import (line 13); replace
  `sessionpkg.New(messages)` (line 58) with `newRunThread(messages)`.
- In `gateway_test.go` and `session_events_test.go`: delete the `sessionpkg`
  import; replace `sessionpkg.New(...)`/`sessionpkg.RunnerFunc(...)` call
  sites with the renamed local identifiers.
- Create `cmd/fast-agent-harness/session_importer.go` with `importer.go`'s
  content verbatim, changing only `package session` -> `package main`;
  confirm no collisions with existing `main` identifiers (none of
  `ImportOptions`/`ImportResult`/`ImportDiagnostics`/`ImportWarning`/
  `ImportTranscript`/`ImportFormat*` exist elsewhere in that package).
- Move `internal/session/importer_test.go`'s cases into a new
  `cmd/fast-agent-harness/session_importer_test.go`, changing
  `package session` -> `package main`.
- In `sessions.go`: delete the `sessionpkg` import (line 19); call
  `ImportTranscript`/`ImportOptions`/`ImportFormatAuto` unqualified (lines
  634, 649).
- Delete `internal/session/` entirely (`session.go`, `session_test.go`,
  `importer.go`, `importer_test.go`) and remove the directory.
- Update `docs/architecture.md`: delete the `internal/session` row (line 50);
  remove `session` from `internal/gateway`'s Allowed-imports cell (line 31).

Verification:

```sh
go build ./...
go vet ./...
go test ./internal/gateway/...
go test ./cmd/fast-agent-harness/...
grep -rn "internal/session" --include='*.go' . || echo 'no remaining references'
```

### P1.10 Fold internal/tooloutput into internal/tools (fix the architecture-boundary test and a test-helper name clash)

Finding: `internal/tooloutput/tooloutput.go` (234 LOC, imports only
`internal/config`) defines `Store`, `Stat`, `Exists`, `StatMetadata`,
`AddMetadataForPath`, `Ref`, `StoreRequest`, `ArtifactMetadata`. It has exactly
6 non-test consumers: `internal/tools/{web_metadata,diagnostics,webcache,
shell_process}.go` and `internal/agent/{agent,tool_attempt}.go` — both agent
files already import `internal/tools` directly (`agent.go:22-23`,
`tool_attempt.go:14-15`), so the separate `tooloutput` import there is pure
redundancy. Two gaps a naive move would hit: (1) `tooloutput_test.go:138`
declares `func assertMode(t *testing.T, path string, want os.FileMode)`, and
`internal/tools/tools_test.go:1132` already declares an identical function in
`package tools` — moving the test file verbatim duplicates it and fails to
compile; (2) `docs/architecture.md`'s Package Map is mechanically enforced by
`internal/architecture/architecture_test.go` (parses the map against
`go list ./internal/...`), and it lists `tooloutput` as an allowed import for
BOTH the `internal/agent` row AND the `internal/tools` row (line 16 and line
57), not just agent's.

Target files:

- `internal/tooloutput/tooloutput.go`
- `internal/tools/web_metadata.go`
- `internal/tools/diagnostics.go`
- `internal/tools/webcache.go`
- `internal/tools/shell_process.go`
- `internal/agent/agent.go`
- `internal/agent/tool_attempt.go`
- `docs/architecture.md`

Checklist:

- Confirm baseline is clean: `go build ./...` and
  `go test ./internal/tools/... ./internal/agent/... ./internal/tooloutput/... ./internal/architecture/...`.
- Create `internal/tools/output_ref.go` (package `tools`) with
  `tooloutput.go`'s content; rename `Ref`->`OutputRef`, `Store`->
  `StoreOutput`, `StoreRequest`->`OutputStoreRequest`, `Stat`->
  `StatOutputRef`; leave `Exists`, `StatMetadata`, `AddMetadataForPath`,
  `ArtifactMetadata`, and the `MetadataOutputRef*` consts named as-is (none
  collide with anything in `internal/tools`).
- Create `internal/tools/output_ref_test.go` (package `tools`) porting
  `tooloutput_test.go`'s three tests with the renamed symbols — do NOT copy
  `assertMode`; call the existing `tools_test.go:1132` helper instead.
- Update `web_metadata.go`, `diagnostics.go`, `webcache.go`,
  `shell_process.go`, `web_test.go`, `shell_process_test.go`, `tools_test.go`:
  drop the `internal/tooloutput` import and unqualify every call site
  (`Store`->`StoreOutput`, `Ref`->`OutputRef`, `StoreRequest`->
  `OutputStoreRequest`, `Stat`->`StatOutputRef`).
- Update `internal/agent/agent.go` and `tool_attempt.go`: delete the
  `internal/tooloutput` import line (both already import `internal/tools`);
  replace `tooloutput.Ref`/`.Store`/`.StoreRequest`/`.Stat`/
  `.MetadataOutputRef*` with `tools.OutputRef`/`.StoreOutput`/
  `.OutputStoreRequest`/`.StatOutputRef`/`.MetadataOutputRef*`.
- Delete `internal/tooloutput/` (`tooloutput.go`, `tooloutput_test.go`)
  entirely.
- Update `docs/architecture.md`'s Package Map: delete the
  `internal/tooloutput` row; remove `tooloutput` from BOTH the
  `internal/agent` cell (line 16) AND the `internal/tools` cell (line 57).
- Run `go test ./internal/architecture/...` specifically — it is the real
  enforcement gate that fails the build if a listed package no longer exists.
- Optionally sync stale prose mentions of `internal/tooloutput` in
  `docs/architecture/tools-mcp-and-policy.md`,
  `docs/architecture/security-model.md`,
  `docs/competitive-architecture-analysis.md`, and
  `agent-index/docs-manifest.json`'s related_code list.

Verification:

```sh
go build ./...
go vet ./internal/tools/... ./internal/agent/...
go test ./internal/tools/... ./internal/agent/... ./internal/architecture/...
go test ./...
```

### P1.11 Collapse eventCallID/metadata*/TodoState decoders into internal/protocol

Finding: `eventCallID` is byte-for-byte identical in all 3 sites —
`internal/tui/transcript_runtime.go:895`, `internal/tui/transcript/projector.go:619`,
`internal/clientux/projector/projector.go:582` (all three do
`event = protocol.EnrichEvent(event, protocol.EventEnvelope{}); return strings.TrimSpace(event.CallID)`).
There are actually THREE diverging metadata-decoder families, not two:
`internal/toolrender/toolrender.go:1030,1048,1080` (`metadataString` handles
`fmt.Stringer` + `Sprint` fallback; `metadataBool` handles
bool/string/int/int64/float64); `internal/clientux/context.go:733,744,755`
(`metadataString` only handles string/`json.Number`, silently returns `""`
otherwise; `metadataBool` only handles bool/string, silently returns `false`
for int/float); and `internal/clientux/projector/projector.go:630,655` (a
THIRD `metadataInt64`/`metadataBool` pair with its own bug: the `float64`
branch at line 641 only converts `value > 0`, silently dropping negative or
zero float metadata to 0 where toolrender's version would not).
`TodoStateFromMetadata`/`DecodeTodoState`/`recountTodoState`
(`toolrender.go:260-278,313`) are duplicated logic-for-logic as
`todoStateFromMetadata`/`recountTodoState` in
`internal/clientux/projector/projector.go:787-816`. `internal/protocol` has
zero internal-package imports (confirmed via grep) and already defines both
`Event` (with `CallID`) and `TodoState`, plus `EnrichEvent`
(`internal/protocol/envelope.go:76`), confirming it is the correct leaf home.

Target files:

- `internal/protocol/types.go`
- `internal/toolrender/toolrender.go`
- `internal/clientux/context.go`
- `internal/clientux/projector/projector.go`
- `internal/tui/transcript_runtime.go`
- `internal/tui/transcript/projector.go`

Checklist:

- Add `internal/protocol/metadata.go` with `func EventCallID(event Event) string`
  (`EnrichEvent` + `TrimSpace(event.CallID)`), and port
  `toolrender.go:1030-1093`'s `metadataString`/`metadataInt`/`metadataBool`
  verbatim as `MetadataString`/`MetadataInt64`/`MetadataBool` — the single
  canonical (Stringer+Sprint-fallback) implementation.
- Add `func DecodeTodoState(value any) (TodoState, bool)` porting
  `toolrender.go:267-278` verbatim, and `func (s TodoState) Recount() TodoState`
  porting `recountTodoState` (`toolrender.go:313-330`) verbatim.
- `transcript_runtime.go:895` — delete the local `eventCallID`; replace call
  sites (lines 521, 839) with `protocol.EventCallID(event)`.
- `internal/tui/transcript/projector.go:619` — delete the local `eventCallID`;
  replace call sites (lines 119, 137, 159) with `protocol.EventCallID(event)`.
- `internal/clientux/projector/projector.go:582` — delete the local
  `eventCallID`; replace call sites (lines 347, 390, 413, 439, 481) with
  `protocol.EventCallID(event)`.
- `toolrender.go:1030-1093` — delete `metadataString`/`metadataInt`/
  `metadataBool`; replace call sites with `protocol.MetadataString`/
  `protocol.MetadataInt64`/`protocol.MetadataBool`. Keep `TodoStateFromMetadata`
  (line 260) as a thin wrapper delegating to `protocol.DecodeTodoState`; delete
  the local `DecodeTodoState`/`recountTodoState`, replacing
  `TodoStateSummary`'s call with `state.Recount()`.
- `clientux/context.go:733-766` — delete `metadataString`/`metadataBool`/
  `metadataInt64`; replace the ~20 call sites (lines 249-323, 518-522) with
  `protocol.MetadataString`/`protocol.MetadataBool`/`protocol.MetadataInt64`.
  Before merging, grep where `compaction_id`/`summary_strategy`/`reason`/
  `*_hash`/`tool_summary_external_model_used` are written into metadata maps to
  confirm no producer emits a type this file's narrower decoders used to
  silently coerce to empty/false.
- `clientux/projector/projector.go:630-669` — delete the second
  `metadataInt64`/`metadataBool` pair (note its `value > 0` guard silently
  zeroes non-positive floats, a real behavior difference); replace call sites
  (lines 249-268, 523-537) with `protocol.MetadataInt64`/`protocol.MetadataBool`
  and confirm no caller relied on the negative/zero-float-drops-to-0 quirk.
- `clientux/projector/projector.go:787-816` — delete `todoStateFromMetadata`
  and `recountTodoState`; replace the call site (line 501) with
  `protocol.DecodeTodoState(result.Metadata["todo_state"])` followed by
  `state.Recount()` as needed.
- Diff `toolrender_test.go` and `projector_test.go` output before/after to
  catch any metadata-type edge case exposed by the behavior unification.

Verification:

```sh
go build ./...
go test ./internal/protocol/...
go test ./internal/toolrender/...
go test ./internal/clientux/...
go test ./internal/clientux/projector/...
go test ./internal/tui/...
go test ./internal/tui/transcript/...
go vet ./internal/protocol/... ./internal/toolrender/... ./internal/clientux/... ./internal/tui/...
```

### P1.12 Delete/archive the two legacy research docs and collapse the docs/ disclaimer layers pointing at them

Finding: `docs/codex-research-roadmap.md` (192 lines) + `docs/competitive-architecture-analysis.md`
(517 lines) + `docs/research/README.md` (31 lines) = 740 lines of admittedly
non-canonical content sitting in `docs/`, which `docs/documentation-system.md`
itself says should be "architecture canon, not implementation scratch space."
The "historical, not current truth" warning is restated redundantly in 5
places: `docs/README.md`'s "Research" section (line 62), `docs/research/README.md`
(whose sole purpose is this warning), `docs/documentation-system.md` (Document
Types > Research, line 106, plus a whole "Migration Notes" section, line 207),
`llms.txt`'s Optional section (lines 84-89), and root `README.md`'s Docs
section (lines 9-11). `agent-index/docs-manifest.json` (lines 671, 687, 707)
and `.codex/hooks/docguard_stop.py`'s `ALLOWED_ACTIVE_WORK_DOCS` set (line 20)
also hardcode `docs/research/README.md` and need updating.
`loop-develop/current-todo/004-todo.md` already has an open, unimplemented
P1.21 item ("Keep README/docs research files clearly historical", lines
1066-1084) that chose the lighter "just keep labels clear" fix over deletion —
this change should resolve/supersede it rather than leave two contradictory
instructions live at once.

Target files:

- `docs/codex-research-roadmap.md`
- `docs/competitive-architecture-analysis.md`
- `docs/research/README.md`
- `docs/README.md`
- `docs/documentation-system.md`
- `llms.txt`

Checklist:

- Read both legacy docs fully; confirm each "Target Shape / What to Borrow /
  comparison" point already appears in `docs/architecture.md`,
  `docs/architecture/*.md`, or an existing ADR under `docs/adr/`; fold in any
  gap before touching the files.
- Move the two files into `loop-develop/history/` (e.g.
  `loop-develop/history/research-codex-roadmap.md`,
  `loop-develop/history/research-competitive-architecture-analysis.md`) as
  dated artifacts, or delete outright if the prior step shows everything
  actionable was already extracted.
- Delete `docs/research/README.md`.
- Remove the "Research" section from `docs/README.md` (lines ~62-70).
- In `docs/documentation-system.md`: drop the `research/` entry and the two
  file lines from the "docs/ Hierarchy" code block, delete the "The top-level
  research files are legacy..." paragraph (line ~65), delete the whole
  "Migration Notes" section (line ~207), and update/remove the "Research"
  subsection under "Document Types" (line ~106).
- Delete the three `llms.txt` "Optional" bullets (Research routing, Codex
  research roadmap, Competitive architecture analysis; lines 84-89).
- Update root `README.md`'s Docs section (lines 9-11) to drop the two dead
  links.
- Remove or repoint the three `agent-index/docs-manifest.json` entries for
  `docs/research/README.md` (line 671), `docs/codex-research-roadmap.md`
  (line 687), and `docs/competitive-architecture-analysis.md` (line 707) to
  wherever the files land.
- Remove `"docs/research/README.md"` from `ALLOWED_ACTIVE_WORK_DOCS` in
  `.codex/hooks/docguard_stop.py` (line 20).
- Resolve `loop-develop/current-todo/004-todo.md`'s P1.21 item in the same
  change — mark it superseded/done with an Evidence Log entry noting the
  files were moved/deleted, or delete the item outright.
- Grep the whole repo for dangling references to the old paths, excluding the
  new `loop-develop/history/` location and the historical record in
  `loop-develop/history/002-todo.md`.

Verification:

```sh
git diff --check
rg -n "codex-research-roadmap|competitive-architecture-analysis|docs/research/README" --glob '!loop-develop/history/**' .
jq . agent-index/docs-manifest.json >/dev/null
python3 -m py_compile .codex/hooks/docguard_stop.py
/root/.local/go/bin/go test -count=1 ./internal/architecture/...
/root/.local/go/bin/go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
```

## P1 Milestone 4 - Fast Startup, Telegram Signal, And Daily-Loop Housekeeping

Goal: kill the O(n)-over-history costs that make gateway startup and every
Telegram message pay for the *entire* lifetime of the store (lazy session
replay, cached admission sequence numbers), then close the solo-dx feedback
gaps that make the daily loop annoying (no transcript/session search, silent
media drops, no ping when a background job finishes), and finish with the
small reliability-lite cleanups (unbounded attachments dir, duplicated
mcpclient status-publish call sites) that turn into real incidents if left
alone long enough.

### P1.13 Defer full session history/status replay out of gateway startup; keep listing cheap via manifest

Finding: `NewServerWithOptionsFromSettings` calls `s.store.LoadAllWithDiagnostics()`
inline at `internal/gateway/gateway.go:184`, before `s.routes()` (line 203) runs
and before `ListenAndServe` is ever reachable, so the process cannot accept a
single connection until every session directory under `DefaultSessionStoreDir()`
(gateway.go:252) has been fully replayed. `LoadAllWithDiagnostics`
(session_store.go:172) `os.ReadDir`'s the store dir and, per entry, calls
`loadSessionDir` (session_store.go:662), which runs `replaySessionHistory`
(session_store.go:725, full `history.jsonl`) and `replaySessionStatus`
(session_store.go:826, full `events.jsonl`) end to end -- cost is O(total bytes
of every session ever created), unbounded, no pruning. A manifest-only stub is
not a sufficient fix on its own: `sessionSummary()` (gateway.go:1349-1367) and
`gatewayapi.SessionSummary` (internal/gatewayapi/types.go:191-209) need
Model/Provider/Profile/ReasoningEffort/AccessMode/LastEvent/LastEventAt/
LastError/RunSeq/DroppedEvents -- fields populated only via `replaySessionStatus`
over `events.jsonl`, not present in `sessionManifest` (session_store.go:52-69,
which today only has SessionID/CreatedAt/UpdatedAt/MessageCount/Owner/
HistorySHA256). Skipping replay without extending the manifest would make
`GET /v1/sessions` trigger a full replay per session on first list call anyway
(likely seconds after restart when TUI/Telegram opens), defeating the point.
Also note `saveLocked()` (session_store.go:539) already does a full
`replaySessionHistory` on every single `Save()` to compute lastSeq/HistorySHA256
-- a separate, pre-existing per-turn cost this change must not worsen and
should piggyback on once a session is materialized.

Target files:

- `internal/gateway/gateway.go`
- `internal/gateway/session_store.go`
- `internal/gateway/session_authz.go`
- `internal/gateway/session_store_test.go`
- `internal/gateway/session_store_benchmark_test.go`

Checklist:

- Extend `sessionManifest` (session_store.go:52) with the SessionStatus-derived
  fields needed for listing: Model, Provider, Profile, ReasoningEffort,
  AccessMode, LastEvent, LastEventAt, LastError, ModelCalls, ToolCalls,
  DroppedEvents, RunSeq.
- Update every `writeSessionManifest` call site (`AppendEvent` ~line 302-352,
  `saveLocked` ~line 539-600) to persist the current SessionStatus snapshot into
  the manifest whenever it changes, so the manifest stays authoritative for
  summary/listing without any JSONL replay.
- Add a cheap index pass, e.g. `loadSessionManifestOnly(dir)`, that reads only
  `manifest.json` per directory -- no `replaySessionHistory`/`replaySessionStatus`
  calls -- and returns a stub `*Session` carrying ID/Created/Owner/status-from-
  manifest with `Thread` left nil/unmaterialized.
- Change `LoadAllWithDiagnostics` (session_store.go:172) to call the manifest-only
  pass instead of `loadSessionDir`, so `gateway.go:184`'s constructor call returns
  without touching any `history.jsonl`/`events.jsonl` content.
- Add a lazy-materialize path invoked from the single choke point `s.session(id)`
  (gateway.go:1300, reached via `sessionForRequest` in session_authz.go:19-30):
  if the looked-up `*Session` is a stub, call `loadSessionDir` for just that ID
  under the existing `s.mu` lock, swap the fully-loaded Session into
  `s.sessions`, and return it.
- Audit every call site that dereferences `session.Thread` (gateway.go's
  sessionResponse/RunMessage helpers, session_events.go's stream handlers) to
  confirm they all route through `s.session(id)`/`sessionForRequest` so none
  silently operate on a still-stub Session with a nil Thread.
- Make `saveLocked` (session_store.go:539) reuse the in-memory lastSeq/
  HistorySHA256 once a session has been loaded/materialized instead of
  re-running `replaySessionHistory` from disk on every save.
- Add a manifest migration fallback so old manifests missing the new status
  fields degrade gracefully (empty/zero summary fields) instead of failing
  `readSessionManifest`'s schema_version check.
- Add a test in `session_store_test.go` with N synthetic session directories of
  varying JSONL sizes asserting `LoadAllWithDiagnostics` returns in near-constant
  time regardless of total JSONL bytes, `GET /v1/sessions` returns correct
  Model/Provider/LastEvent summaries without triggering replay, and
  `GET /v1/sessions/{id}` still returns correct full history after a lazy load.
- Extend `session_store_benchmark_test.go`'s `BenchmarkGatewaySessionJSONLReplay`
  pattern to measure startup/list time vs. total stored session bytes before and
  after the change.

Verification:

```sh
go test -count=1 ./internal/gateway/... -run TestSessionStore -v
go test -count=1 ./internal/gateway/... -run TestGatewayReadiness -v
go test -count=1 ./internal/gateway/...
go test ./internal/gateway/... -bench BenchmarkGatewaySessionJSONLReplay -run '^$' -benchtime=1x
go build ./...
```

### P1.14 Cache admission log sequence number in memory instead of replaying admissions.jsonl on every Telegram update

Note: P1.5 deletes `admission_store.go` entirely. If P1.5 has already landed,
SKIP this task — it is moot.

Finding: `telegramAdmissionStore.append()` (internal/telegrambot/admission_store.go:120)
unconditionally calls `s.lastSeqLocked()` (line 131), which runs
`eventlog.ReplayJSONL` (internal/eventlog/jsonl.go:108) -- opening the file,
scanning every line, `json.Unmarshal`-ing each record -- before every single
append. `append()` sits on the blocking message-handling path: `RecordIgnored`
(poller.go:37, `ackIgnoredUpdate`), `RecordAdmitted` (runner.go), and
`RecordAbandoned` (state_runtime.go, startup reconciliation) all call it. The
file (`<state>.admissions.jsonl`) is append-only and never rotated or
truncated -- `eventlog.AppendJSONL` only opens with `O_APPEND` -- so cost grows
without bound: O(total messages ever recorded) work per incoming message, for
the life of the bot process. No other caller of `lastSeqLocked` exists, so
caching the sequence number in memory is safe and self-contained.

Target files:

- `internal/telegrambot/admission_store.go`
- `internal/telegrambot/admission_store_test.go`

Checklist:

- Add `lastSeq int64` and `seqLoaded bool` fields to `telegramAdmissionStore`
  (admission_store.go:18-21), guarded by the struct's existing `mu sync.Mutex`.
- In `append()` (admission_store.go:120-129), replace the unconditional
  `lastSeq, err := s.lastSeqLocked()` with: if `!s.seqLoaded`, call
  `s.lastSeqLocked()` once, store the result in `s.lastSeq`, set
  `s.seqLoaded = true`, and return on error; otherwise read `s.lastSeq` directly
  with no replay.
- After a successful `eventlog.AppendJSONL(s.path, record)` in `append()`, set
  `s.lastSeq = record.Seq` before returning nil, so the next call never needs to
  replay again.
- Leave `lastSeqLocked()` (admission_store.go:131-146) unchanged -- it stays the
  one-time cold-start path and still performs the existing schema-version and
  sequence-gap corruption checks (`eventlog.NewCorruptionError`).
- No constructor change is required: `telegramAdmissionStore` is created once
  per `Bot` in bot.go and immediately used by `reconcilePendingInputsOnStartup`
  (state_runtime.go, calling `RecordAbandoned`), so the first real `append()`
  call at startup naturally performs the one-time replay and populates the
  cache.
- Add `internal/telegrambot/admission_store_test.go`: pre-seed
  `admissions.jsonl` with N valid sequential records, construct a
  `telegramAdmissionStore{path: ...}` directly, call `append()` several times,
  and assert the resulting records have strictly increasing `Seq` continuing
  from N, plus that only one full-file read occurs (e.g. a wrapper counting
  `os.Open` calls on the target path, or a timing comparison between a large
  pre-seeded file and a tiny one showing it no longer scales with file size).
- Add a corruption-path regression case: seed a file with a sequence gap or bad
  `schema_version` and confirm the first `append()` still returns the same
  `CorruptionError` as before -- behavior unchanged, just called once instead of
  every time.
- Run `go test ./internal/telegrambot/... ./internal/eventlog/...` to confirm
  existing sequencing/dedup/corruption tests in poller_test.go (which read back
  records via `readTelegramAdmissionRecords`) still pass unchanged.
- Do not add file rotation/truncation in this change -- it's a separate concern
  from the O(n)-per-message replay bug and needs its own design (retention
  policy, who reads the tail back); keep this fix scoped to the in-memory cache.

Verification:

```sh
go build ./...
go test ./internal/telegrambot/... -v
go test ./internal/eventlog/... -v
go vet ./internal/telegrambot/...
```

### P1.15 Add /find + ctrl+f transcript search using the viewport's existing (unused) highlight API

Finding: `actionRegistry()` (internal/tui/actions.go:43) has no search action;
navigation is only PageUp/PageDown and block.next/previous, none of which call
`viewport.SetYOffset` anywhere in `internal/tui/`. The vendored
`charm.land/bubbles/v2` v2.1.0 `viewport.Model` used as `m.viewport` already
ships a complete find/highlight/scroll-into-view API --
`SetHighlights([][]int)`, `HighlightNext()`, `HighlightPrevious()`,
`ClearHighlights()`, `EnsureVisible()` -- and `m.viewport.HighlightStyle`/
`SelectedHighlightStyle` are already configured in tui.go, yet there are zero
call sites for `SetHighlights`/`HighlightNext`/`HighlightPrevious` anywhere in
the repo: unused, ready-made scaffolding. Two real gotchas: (1) `SetContentLines`
(invoked by every `m.reflow()` via `viewport.SetContent`,
transcript_runtime.go:1006) calls `ClearHighlights()` internally, so a live-
streaming turn will silently wipe an active find unless reflow re-applies it;
(2) `commands.go:207-209` lowercases slash args for every command except two
ids special-cased there (`vision.attach`, `transcript.export`), so `/find` must
join that list or queries get mangled. Separately, vim-style bare `n`/`N` keys
are infeasible: `handleKeyAction` (tui.go:471) intercepts every `KeyPressMsg`,
including bare letters, before the textarea sees it, and every existing binding
in the registry is modifier-based (`ctrl+`, `alt+`, pgup/pgdown, enter)
specifically to avoid stealing normal typing.

Target files:

- `internal/tui/actions.go`
- `internal/tui/transcript_runtime.go`
- `internal/tui/commands.go`
- `internal/tui/tui.go`
- `internal/tui/find_test.go`

Checklist:

- In `internal/tui/tui.go`, add `findQuery string`, `findMatches [][]int`,
  `findMatchIndex int` fields to the Model struct near the existing
  viewport/selected fields.
- In `transcript_runtime.go`, add `func (m *Model) applyFindQuery(query string)
  (int, error)` that compiles `(?i)` + `regexp.QuoteMeta(query)` (or the raw
  pattern if it's already valid regex) against `m.viewportContent` (built at the
  end of `reflow()`, ~line 1004), stores results in `m.findMatches`, resets
  `m.findMatchIndex` to 0, calls `m.viewport.SetHighlights(matches)`, and
  returns the match count.
- In `reflow()`, right after `m.viewport.SetContent(m.viewportContent)` (~line
  1006), if `m.findQuery != ""`, recompute matches against the new content and
  call `m.viewport.SetHighlights(matches)` again, since `SetContentLines` clears
  highlights internally on every reflow triggered by a streaming event.
- In `actions.go`, add actionSpec id `"transcript.find"`, category `"ui"`, slash
  `"/find"`, slashArgs `"[query]"`; its run calls `applyFindQuery`, sets
  `m.status` to `"match 1/N for '<query>'"` or `"no matches for '<query>'"`, and
  on a hit calls `m.viewport.HighlightNext()` to jump to the first match.
- Add keybinding `"ctrl+f"` (confirmed unused) whose keyRun calls
  `m.viewport.HighlightNext()`, advances `m.findMatchIndex` mod
  `len(m.findMatches)`, and updates `m.status` -- falling back to opening the
  palette pre-filled with `"/find "` when `m.findQuery` is empty; add `"alt+f"`
  for `HighlightPrevious`. Do not bind bare `"n"`/`"N"` -- `handleKeyAction`
  intercepts every keystroke ahead of the textarea.
- In `commands.go`, add `"transcript.find"` to the raw-arg exception list
  alongside `"vision.attach"`/`"transcript.export"` (~line 207-209) so the query
  is not lowercased before being stored/displayed.
- Set `keySummary` (`"search transcript"` / `"next match"`) and `summary` fields
  on the new actionSpec so `helpText()`/`keybindingHelpLines()` surface it
  automatically -- no separate help wiring needed.
- Add `internal/tui/find_test.go` (package tui) using `newTestModel(t)`: seed
  `m.blocks` directly, call `m.reflow(true)`, invoke `actionForSlash("/find")` +
  `action.run`, and table-test: multi-match with wrap-around via repeated
  ctrl+f, no-match status text, and a case confirming reflow mid-stream (via
  `m.applyEvent` + `m.reflow`) does not drop the active highlight.

Verification:

```sh
go build ./internal/tui/... ./internal/clientux/...
go test ./internal/tui/... -run TestFind -v
go test ./internal/tui/... -run TestGoldenStatusTraceMatchesProjectorTelegramTUIAndContext
go vet ./internal/tui/...
```

### P1.16 Add content/title search fallback to /resume and /fork (currently ID-prefix only)

Finding: `loadChatSession` (internal/tui/sessions.go:76-98) matches only
`session.ID == prefix` or `strings.HasPrefix(session.ID, prefix)`; there is no
scan of `Title` or `Messages` content anywhere in the file. `resumeChat`/
`forkChat` (sessions.go:193-256) call `loadChatSession` directly and surface a
bare "session not found"/"ambiguous" error with no snippets -- and both already
lowercase their `prefix` argument before calling it (sessions.go:197, 226), so
any content search added here must stay case-insensitive to match. `sessionsText()`
(sessions.go:333) shows only the most-recently-updated sessions by a 54-char
truncated first-prompt title (`sessionTitle`/`oneLinePreview`,
transcript_runtime.go:1321) -- no session-count cap or pruning exists anywhere,
so the list silently loses older sessions over time by not showing them.
`protocol.Message` (internal/protocol/types.go:30-38) and `MessagePart`
(internal/protocol/message_parts.go:26-30) already have `Content` and
`Parts[].Text` fields loaded into memory by `listChatSessions`
(sessions.go:100), so a content search is mechanically straightforward with no
new I/O. `actions.go:559,573` confirm the `/resume` and `/fork` slashArgs hints
are literally `"[id-prefix]"`. No existing search/grep command exists anywhere
in `internal/tui`. This is a purely local TUI feature gap with no security
surface (doesn't touch gateway auth, Telegram checks, or MCP boundaries).

Target files:

- `internal/tui/sessions.go`
- `internal/tui/commands.go`
- `internal/tui/actions.go`
- `internal/tui/sessions_search_test.go`

Checklist:

- Add `type sessionMatch struct { session chatSession; snippet string }` to
  sessions.go.
- Add `func searchChatSessions(dir, query string) ([]sessionMatch, error)` that
  calls `listChatSessions(dir)` and, for each session, does a case-insensitive
  substring match against `session.Title` and each `session.Messages[i].Content`
  plus each `session.Messages[i].Parts[j].Text`; on first match build a ~60-80
  char snippet around the match offset via a new `snippetAround(text, query
  string, radius int) string` helper.
- Extract the existing ID/prefix matching loop out of `loadChatSession`
  (sessions.go:85-90) into `func matchByIDPrefix(sessions []chatSession, prefix
  string) []chatSession` so it can be reused; leave `loadChatSession(dir,
  prefix)` itself unchanged (exact ID/prefix only) since
  `sessions_restore_test.go` calls it directly with an exact ID.
- Add `func resolveChatSession(dir, query string) (chatSession, []sessionMatch,
  error)`: run `matchByIDPrefix` first; exactly 1 match returns it (fast path,
  no behavior change for existing ID usage); an ambiguous ID-prefix match
  (>1) keeps today's "session id prefix is ambiguous" error rather than falling
  through to content search; 0 ID/prefix matches falls back to
  `searchChatSessions`; 0 total matches returns "session not found: %s"; 1
  match returns it; >1 returns the matches for the caller to render snippets.
- Add `func formatSessionMatches(matches []sessionMatch) string` beside
  `sessionsText()` (sessions.go:333) that prints `shortID(session.ID)
  updated-at  title -- matched: <snippet>` per line, same style as
  `sessionsText`.
- Update `resumeChat` (sessions.go:193-222) and `forkChat` (sessions.go:224-256)
  to call `resolveChatSession` instead of `loadChatSession`; when multiple
  matches come back, call `m.addInfoBlock("CHATS", formatSessionMatches(matches))`
  and set status to `"N chats match <query>"` instead of a plain error string;
  keep the not-found/ambiguous-ID-prefix error paths exactly as today.
- Update the `/resume` and `/fork` slashArgs hints in `actions.go` (lines 559
  and 573, currently `"[id-prefix]"`) to `"[id-prefix|text]"`.
- Add `internal/tui/sessions_search_test.go`: write 3+ `chatSession` fixtures via
  `saveChatSession` where the distinguishing text lives only in a message body
  (e.g. a tool_result mentioning "gateway auth token"), not the title, then
  assert `resolveChatSession(dir, "gateway auth")` returns exactly that session;
  add a second case where the query matches 2+ sessions and assert the returned
  match slice has len>1 with non-empty snippets.
- Run `go build ./...` and `go vet ./internal/tui/...` to confirm nothing else
  in the package called the old `loadChatSession` matching-loop internals
  directly.

Verification:

```sh
go build ./...
go vet ./internal/tui/...
go test ./internal/tui/... -run TestResumeChatRestoresProjectedUsageSnapshot -v
go test ./internal/tui/... -run TestSearchChatSessions -v
go test ./internal/tui/... -run TestResolveChatSession -v
go test ./internal/tui/...
```

### P1.17 Reply instead of silently dropping voice/audio/video messages in Telegram bot

Finding: `internal/telegrambot/types.go`'s `Message` struct (lines 10-22) has no
Voice/Audio/VideoNote/Video fields. `telegramMessageHasMedia`
(internal/telegrambot/media.go:56) only checks `len(msg.Photo)>0 ||
msg.Document != nil`, and `telegramMessageProcessable` (media.go:60) is
`prompt!="" || hasMedia`. A voice memo, video note, or audio file with no
caption therefore fails `telegramMessageProcessable`, and
`handlePolledUpdate` (poller.go:35-37) silently acks it via
`ackIgnoredUpdate(update, "empty_message")` with zero reply -- confirmed by grep
showing zero occurrences of Voice/Audio/VideoNote anywhere in
`internal/telegrambot/`. The existing `telegramDurableInputError` +
`rejectPolledUpdate` + `ackIgnoredUpdate` machinery (already used for
`vision_unsupported` at media.go:213-227, exercised by
`TestTelegramVisionUnsupportedModelRepliesAndAcks` in poller_test.go) is a
direct, low-effort template to reuse. One nuance: `handlePolledUpdate`'s
`empty_message` check (poller.go:36-37) runs before the `b.allowed(msg)` check
(poller.go:42-46), which already replies "Chat is not allowlisted for this
bot." to non-allowlisted senders of processable messages -- so the
unsupported-media check must slot in *after* the processable/allowed gating,
alongside the existing `not_allowlisted` and `vision_unsupported` paths, not at
the very top, to keep behavior consistent with how images/documents are
already gated for non-allowlisted chats.

Target files:

- `internal/telegrambot/types.go`
- `internal/telegrambot/media.go`
- `internal/telegrambot/poller.go`
- `internal/telegrambot/poller_test.go`

Checklist:

- In `types.go`, add to the `Message` struct (after `Document` at line 21):
  `Voice *Voice`, `Audio *Audio`, `VideoNote *VideoNote`, `Video *Video`, each
  `json:"...,omitempty"`.
- Define new structs `Voice{FileID, FileUniqueID, Duration, MIMEType,
  FileSize}`, `Audio{FileID, FileUniqueID, Duration, Performer, Title,
  MIMEType, FileSize}`, `VideoNote{FileID, FileUniqueID, Length, Duration,
  FileSize}`, `Video{FileID, FileUniqueID, Width, Height, Duration, FileName,
  MIMEType, FileSize}` mirroring the existing Document/PhotoSize field style
  (json tags matching the Telegram Bot API names).
- In `media.go`, add `telegramMessageHasUnsupportedMedia(msg Message) bool`
  checking `msg.Voice != nil || msg.Audio != nil || msg.VideoNote != nil ||
  msg.Video != nil`.
- Update `telegramMessageProcessable` (media.go:60-62) to also return true when
  `telegramMessageHasUnsupportedMedia(msg)` is true, so these messages don't hit
  the `empty_message` ack path at all.
- Add `telegramUnsupportedMediaKind(msg Message) string` returning
  "voice"/"audio"/"video note"/"video" for the matched field, and a helper that
  builds `telegramDurableInput("<kind>_unsupported", "<Kind> messages aren't
  supported yet -- send text or an image.", nil)`, following the
  `telegramVisionUnsupportedMessage`/`telegramDurableInput` pattern at
  media.go:213-227.
- In `poller.go`'s `handlePolledUpdate`, after the `b.allowed(msg)` check
  (poller.go:42-46) and the `/` command check, and before the
  `telegramMessageHasMedia` branch, check `telegramMessageHasUnsupportedMedia(msg)`
  and call `b.rejectPolledUpdate(ctx, update, <the built durable error>)` then
  return -- keeping it downstream of allowlist gating so non-allowlisted senders
  still get the existing "Chat is not allowlisted" reply first.
- Add `TestTelegramVoiceOnlyRepliesAndAcks` to `poller_test.go` modeled on
  `TestTelegramVisionUnsupportedModelRepliesAndAcks`: build an Update with
  `Message.Voice` set and no Text/Caption, call `handlePolledUpdate`, assert
  `sendPlain` was called with a message containing "not supported", offset
  advanced, and the admission record has `Kind=="ignored"` and
  `Reason=="voice_unsupported"`.
- Add a small helper `telegramVoiceUpdate(updateID, chatID, userID int, ...)`
  mirroring `telegramPhotoUpdate`/`telegramTextUpdate` to construct the
  Voice-bearing Update fixture.
- Verify existing Photo/Document flow tests (e.g.
  `TestTelegramUpdateParsesPhotoDocumentCaptionAndThread`) still pass unchanged
  since Voice/Audio/Video are new optional fields with `omitempty` and don't
  affect existing JSON parsing.

Verification:

```sh
go build ./internal/telegrambot/...
go test ./internal/telegrambot/... -run 'Telegram' -v
go test ./internal/telegrambot/...
go vet ./internal/telegrambot/...
```

### P1.18 Add a background poller in internal/telegrambot that pushes a Telegram message when a managed shell process exits

Finding: `shell_process.go`'s managed background processes (`startManagedShell`,
`status()`) are poll-only today -- confirmed no notify/SendMessage/pushover call
anywhere in `internal/tools`. A `notify_owner` tool living in
`internal/tools`, as a first-pass fix might suggest, is wrong on two counts:
(1) there is no Telegram bot token available to the gateway process --
`cmd/fast-agent-harness/service_cmd.go` reads the bot token only inside the
separate `telegram` subcommand (`telegramCmd`), which the `serve` subcommand
hosting `internal/tools.Registry` never sees, so a `notify_owner` tool would
require newly plumbing the bot secret into the gateway process, a new
secret-distribution surface; (2) `docs/architecture.md`'s Package Map lists
`internal/tools`' allowed internal imports as only config, diagnostics,
filesearch, mcpclient, memory, protocol, skills, tooloutput, tools/discovery,
webtools -- `telegrambot` is not among them, and
`internal/architecture/architecture_test.go` (`TestInternalPackageBoundaries`)
fails CI on any disallowed import. The correct fix already has almost all its
plumbing in place on the *bot* side: the gateway already exposes
`GET /v1/processes` (gateway.go, `handleManagedProcesses`) with full
`ManagedProcessList`/`ManagedProcessStatus` (internal/protocol/types.go,
including ExitCode, OutputTailPreview), and
`internal/telegrambot/gateway_client.go`'s `ProcessStatus(ctx)` already calls
it (used by the existing `processes.show` command in commands.go). The bot
already holds the token and an `AllowedChatIDs`/`Client.SendMessage`
(client.go) path. Nothing today periodically diffs process state to detect a
running-to-exited transition and push proactively.

Target files:

- `internal/telegrambot/gateway_client.go`
- `internal/telegrambot/process_watch.go` (new)
- `internal/telegrambot/poller.go`
- `internal/telegrambot/redaction.go`

Checklist:

- Do NOT add `internal/notify`, do NOT add a `notify_owner` tool in
  `internal/tools/tools.go`, and do NOT touch `internal/tools/shell_process.go`
  -- the gateway process has no Telegram secret today and `internal/tools` is
  not allowed to import `internal/telegrambot` per `docs/architecture.md`'s
  Package Map, enforced by `internal/architecture/architecture_test.go`.
- Extend `GatewayClient` (gateway_client.go) with a typed call returning the
  full `gatewayapi.ManagedProcessResponse` (reuse the existing
  `/v1/processes?include_exited=true` request already used by `ProcessStatus`)
  instead of only formatted text, so a poller can inspect
  `protocol.ManagedProcessStatus.Running`/`ExitCode`/`ID`/`OutputTailPreview`
  per process.
- Add a small poller loop in a new `internal/telegrambot/process_watch.go`,
  started alongside `Bot.Run` (poller.go) on a fixed interval (e.g. 5-10s), that
  keeps an in-memory map of last-seen process IDs -> `Running bool`.
- On each tick, diff the fetched list against the map: any process whose state
  flips from `Running=true` to `Running=false` (or that first appears already
  exited and unseen) is "newly finished"; update the map and prune IDs no
  longer returned by the gateway.
- For each newly-finished process, build a short message (id, exit code,
  elapsed, `OutputTailPreview` already computed server-side) and send it via
  the existing `b.client.SendMessage` (client.go) to the configured owner
  chat(s) drawn from `opts.AllowedChatIDs`/`AllowedOperatorUserIDs` -- do not
  build a new HTTP client or new config surface.
- Route the outgoing text through the existing `redactTelegramText`/
  `secrets.Redact` helper (redaction.go) before sending, matching every other
  outbound bot message.
- Guard the poller so it only runs when `SendEnabled` is true and
  `AllowedChatIDs` is non-empty, and make the interval/on-off configurable via a
  `telegramCmd` flag with a sane default, so TUI-only/no-bot setups pay zero
  cost.
- Add `internal/telegrambot/process_watch_test.go` using a fake
  Harness/GatewayClient implementation (pattern already used in
  gateway_client_test.go) returning a running-then-exited process across two
  polls, asserting exactly one `SendMessage` call with the expected chat id and
  text.
- Update `docs/architecture/telegram-and-operator-surfaces.md` to document this
  new outbound-without-inbound-trigger message pattern.

Verification:

```sh
go build ./...
go test -count=1 ./internal/telegrambot/...
go test -count=1 ./internal/architecture
go test -count=1 ./internal/gateway -run TestManagedProcesses
go vet ./internal/telegrambot/...
```

### P1.19 Add prune + doctor visibility for the unbounded attachments store

Finding: `internal/attachments/store.go`'s `Store.StoreImageBytes`
(line 102) permanently writes every vision image under
`$BILLYHARNESS_HOME/attachments` with 0600 perms via `writePrivateFile` (line
301), with no TTL, size cap, or delete path anywhere in the package -- grep
confirms the only callers are `internal/telegrambot/media.go`,
`internal/tui/attachments.go`, and `internal/gateway/gateway.go`, none of which
ever remove files. This is inconsistent with the rest of the codebase:
`internal/tools/webcache.go` already implements TTL+MaxBytes oldest-first
auto-eviction (its cleanup pass sorts kept entries by ModTime and evicts until
under `WebCacheMaxBytes`, ~line 199-224), and
`cmd/fast-agent-harness/doctor.go` already wires `doctorPathUsageFor(path)` for
`GatewaySessionStore` and `ToolOutputStore` (`doctorRuntimeStatus` fields at
lines 67-68, populated at lines 245-246) but not attachments.

Target files:

- `internal/attachments/store.go`
- `internal/attachments/store_test.go`
- `cmd/fast-agent-harness/doctor.go`
- `cmd/fast-agent-harness/main.go`
- `ops/doctor-and-diagnostics.md`

Checklist:

- Add `func (s Store) Prune(maxAge time.Duration, maxTotalBytes int64) (removed
  int, removedBytes int64, err error)` that `filepath.WalkDir`'s `s.Root`,
  collects regular files with mtime+size (skip any `.tmp-attachment-*` temp
  files `writePrivateFile` creates), deletes anything older than `maxAge`, then
  if remaining total exceeds `maxTotalBytes` deletes oldest-first (mirror the
  exact sort+evict loop already used in `internal/tools/webcache.go`) until
  under budget.
- Add `func (s Store) Usage() (fileCount int, totalBytes int64, err error)` for
  reuse by both the CLI gc summary and the doctor check.
- Add `TestPruneRemovesOldFiles` and `TestPruneEnforcesMaxBytesOldestFirst` to
  `store_test.go` covering both trigger paths and confirming files under
  budget/age are untouched.
- Add a new `cmd/fast-agent-harness/attachments_cmd.go` with an `attachments`
  subcommand routed from `main.go`'s switch (alongside the existing `case
  "tools":`, `case "memory":` entries at main.go:45-49) supporting
  `attachments gc [-max-age=720h] [-max-bytes=...] [-dry-run]` that calls
  `attachments.DefaultStore().Prune` and prints removed count/bytes (or a
  dry-run preview using `Usage()`).
- In `doctor.go`, add an `AttachmentsStore doctorPathUsage` field to
  `doctorRuntimeStatus` (next to `ToolOutputStore` at line 68) and populate it
  in the same spot as line 246 via
  `doctorPathUsageFor(attachments.DefaultStoreRoot())`.
- Add a `doctorCheck` (near `doctorSessionStoreAccessCheck`, line 331) that sets
  `Status="warn"` when `AttachmentsStore.SizeBytes` exceeds a conservative
  default threshold (e.g. `1<<30`), and `"ok"` otherwise -- never `"fail"`, so
  `-strict` never trips on it.
- Extend the human-readable runtime summary (`doctorPathUsageSummary` call
  sites near line 1013-1014) to include the new attachments usage alongside
  sessions/tool_output.
- Update `ops/doctor-and-diagnostics.md` to document the `attachments gc`
  command and the new doctor field, including the default max-age/max-bytes and
  that it only warns, never fails, in `-strict` mode.

Verification:

```sh
go build ./...
go test ./internal/attachments/... -run TestPrune -v
go test ./internal/attachments/...
go test ./cmd/fast-agent-harness/... -run TestDoctor -v
go vet ./internal/attachments/... ./cmd/fast-agent-harness/...
go run ./cmd/fast-agent-harness doctor -strict
go run ./cmd/fast-agent-harness attachments gc -dry-run
```

### P1.20 Collapse the 14-16 duplicated clone/unlock/publish-status/publish-catalog call sites in mcpclient/server.go into two helpers

Finding: the clone-status -> unlock -> `publishStatus` -> (if catalog changed)
`publishCatalogChanged` sequence appears at roughly 16 `publishStatus` call
sites and 14 `publishCatalogChanged` call sites in
`internal/mcpclient/server.go` (`start` ~74-99, `ensureConnected` ~103-115,
`startLocked` ~153-249, `callTool` ~251-266, `markCatalogStale` ~279-290,
`refreshCatalog` ~303-373, `snapshot` ~377-388, `recordStaticErrorState`
~442-450). The wiring is real: `newManagedServer(settings, server,
manager.emitStatus, ...)` in manager.go feeds `AddStatusListener` consumers
(TUI/Telegram status), so a forgotten publish on a future early-return would
silently go stale exactly as this pattern risks. The pattern is not uniform
enough for one universal helper: ~9 sites do a direct in-lock mutation then
clone+unlock+publish+conditional-catalog-publish; ~5 sites (`ensureConnected`,
`callTool`, the two `refreshCatalog` branches, `snapshot`) derive status via
`absorbClientLocked` and conditionally publish status itself, not just
catalog -- a genuinely different shape needing a second, smaller helper.

Target files:

- `internal/mcpclient/server.go`
- `internal/mcpclient/client_test.go`

Checklist:

- Add `func (s *managedServer) commitLocked(catalogChanged bool)` for the
  direct-mutation shape: called while `s.mu` is held, it clones `s.status`,
  calls `s.mu.Unlock()`, then `s.publishStatus(status)`, then
  `s.publishCatalogChanged()` iff `catalogChanged` is true. Document in a
  comment that the caller must not touch `s.mu` again after calling it.
- Replace `start()`'s early-return branches (~lines 78-98) with
  `recordFailureLocked(...); catalogChanged := s.clearCatalogLocked();
  s.commitLocked(catalogChanged); return ...`.
- Replace `startLocked`'s restarting-status publish (~line 178) with
  `s.commitLocked(catalogChanged)` using the value already computed earlier in
  the function, and its closed-after-connect/error branches (~195-216) the same
  way after the existing `recordFailureLocked`/`clearCatalogLocked`/
  `recordCatalogFetchFailureLocked` calls.
- Replace `startLocked`'s success path (~246-247), `markCatalogStale` (~289-290),
  and `refreshCatalog`'s end (~372-373) with `s.commitLocked(true)` since these
  always change the catalog.
- Replace `recordStaticErrorState` (~450) with `s.commitLocked(false)` since it
  never touches the catalog.
- Add a second helper `func (s *managedServer) publishAbsorbed(status
  ServerStatus, statusChanged, catalogChanged bool)` (no locking inside --
  caller has already unlocked) for the `absorbClientLocked`-derived shape where
  `publishStatus` itself is conditional, and use it at `ensureConnected`
  (~109-112), `callTool` (~262-266), `refreshCatalog`'s two `absorbClientLocked`
  branches (~332-336, ~346-349), and `snapshot` (~385-388).
- Leave `refreshCatalog`'s listTools-error branch (~323), which only ever calls
  `publishStatus` with no catalog check, as-is, or fold it into
  `commitLocked(false)` only if the surrounding control flow allows it cleanly
  -- do not force a fit that changes behavior.
- Run the full `internal/mcpclient` test suite and specifically re-check
  `TestManagerStatusListenersObserveLifecycleChanges`,
  `TestMCPStdioReconnectRefreshesCatalogAndEmitsChange`, and
  `TestMCPStdioToolsListChangedNotificationRefreshesCatalog` since they assert
  exact publish ordering/counts.
- Add one regression test (in `client_test.go`) that introduces an early-return
  branch in a state transition and asserts a status event is still observed via
  the existing status-listener test harness pattern, guarding against the exact
  silent-staleness failure mode this refactor targets.

Verification:

```sh
go build ./...
go vet ./internal/mcpclient/...
go test ./internal/mcpclient/... -run TestManagerStatusListenersObserveLifecycleChanges -v
go test ./internal/mcpclient/... -run TestMCPStdioReconnectRefreshesCatalogAndEmitsChange -v
go test ./internal/mcpclient/... -run TestMCPStdioToolsListChangedNotificationRefreshesCatalog -v
go test ./internal/mcpclient/... -v
```

## P2 Milestone 5 - Delete Unread Provenance Ceremony And Tighten Process Docs

Goal: remove artifacts and routes that nothing in the codebase ever reads back
(a per-import metadata file, a dead benchmark HTTP route), and shrink process
documentation that has drifted into unenforced duplication or fragile
doc-scraping (architecture.md's Guarded Rules prose, hygiene's markdown-parsed
allowlist, the ADR numbering gap, and TODO-file scope creep). None of this
touches real security surfaces — gateway auth, Telegram operator checks, and
redaction stay untouched.

### P2.1 Stop writing the unread billyharness.skill.json provenance file on skill import

Finding: `internal/skills/skills.go`'s `importSkill()` (lines 584-629) builds an
`importMetadata` struct (`schema_version`, `imported_at`, `name`, `source`,
`source_path`, `source_dir`, `sha256`; struct defined at lines 111-119) and
writes it via `json.MarshalIndent` + `os.WriteFile` to
`destDir/billyharness.skill.json` (line 617, mode `0o600`) on every skill
import. Nothing reads it back: `Discover()` (skills.go:133, walk loop at
line ~352) only matches `SKILL.md` files. Grepping the repo for
`importMetadata`/`billyharness.skill.json` turns up only the write site and two
tests that assert the file's mere existence/content —
`internal/skills/skills_test.go`'s `TestImportCopiesSelectedCompatibilitySkillWithMetadata`
(lines 105-147, metadata read/assert at 132-143) and
`internal/tools/tools_test.go`'s `os.Stat` check at line 980.
`ImportResult` (lines 101-109) already returns
`Name`/`Source`/`Destination`/`SourcePath`/`SHA256`/`FilesCopied`/`BytesCopied`
to the in-memory caller, and the `skill_import` tool response already renders
`source`/`destination` (asserted at tools_test.go:976-977) — making the on-disk
file pure redundant audit ceremony for a solo skills directory.

Target files:

- `internal/skills/skills.go`
- `internal/skills/skills_test.go`
- `internal/tools/tools_test.go`

Checklist:

- Delete the `importMetadata` struct in `internal/skills/skills.go` (lines
  111-119).
- In `importSkill()` (lines 584-629), keep the `sha, err := fileSHA256(skill.Path)`
  call (line 600) — `ImportResult.SHA256` (line 625) still needs it.
- Delete the `meta := importMetadata{...}` block (lines 604-612).
- Delete the `metaBody, err := json.MarshalIndent(meta, "", "  ")` block and its
  error check (lines 613-616).
- Delete the `os.WriteFile(filepath.Join(destDir, "billyharness.skill.json"), ...)`
  block (lines 617-619).
- Leave the final `return ImportResult{...}` (lines 620-628) unchanged.
- Check whether `encoding/json` and `time` imports in skills.go are still used
  elsewhere in the file after removal (e.g. by frontmatter parsing); drop any
  import that becomes unused. `hex`/`sha256` stay because `fileSHA256` still
  needs them.
- In `internal/skills/skills_test.go`, rewrite
  `TestImportCopiesSelectedCompatibilitySkillWithMetadata` (lines 105-147):
  delete the `var meta map[string]any` / `os.ReadFile(...billyharness.skill.json)`
  / `json.Unmarshal` / assertion block (lines 132-143) and instead assert
  `result.Source == SourceHermesRuntime` and
  `result.SHA256 == hex.EncodeToString(sum[:])` directly against the
  already-computed `sha256.Sum256([]byte(skillBody))` (line 140).
- Rename the test if "WithMetadata" now misdescribes it (e.g.
  `TestImportCopiesSelectedCompatibilitySkill`); update any references.
- In `internal/tools/tools_test.go`, delete the
  `os.Stat(filepath.Join(home, "skills", "legacy", "billyharness.skill.json"))`
  check (lines 980-982) — the preceding `imported.Content` assertions (lines
  976-977) already cover source/destination reporting.
- Grep the repo once more for `billyharness.skill.json` and `importMetadata`
  after edits to confirm no leftover references in docs, fixtures, or tests.

Verification:

```sh
go build ./internal/skills/...
go test ./internal/skills/... -run TestImport -v
go test ./internal/tools/... -run TestSkill -v
go vet ./internal/skills/... ./internal/tools/...
grep -rn "billyharness.skill.json\|importMetadata" --include=*.go .
```

### P2.2 Delete dead GET /v1/benchmarks gateway route (keep /v1/tools — it's a live operator diagnostic)

Finding: `internal/gateway/benchmark_routes.go` (161 lines) defines
`handleBenchmarks`, `defaultBenchmarkRunsDir`, `listBenchmarkRuns`,
`readBenchmarkRunSummary`, `resolveBenchmarkArtifactPath`, `absBenchmarkPath`,
`fileExists`, `dirExists` — all serving `GET /v1/benchmarks`, registered at
`internal/gateway/gateway.go:301` (`s.mux.HandleFunc("GET /v1/benchmarks", s.handleBenchmarks)`,
immediately above the `GET /v1/tools` registration on line 302, which must stay).
The route's DTOs, `gatewayapi.BenchmarkListResponse`/`BenchmarkRunSummary`
(`internal/gatewayapi/types.go` lines 409-423), are aliased in
`internal/gateway/gateway.go` at lines 116-117. Grep confirms zero callers in
`tui`/`telegrambot`/`gatewayclient`/`cmd`; `cmd/fast-agent-harness/bench_cmd.go`
writes benchmark runs directly under `bench-runs/...` on disk (its `-out`
flags default to `bench-runs`, `bench-runs/local-loop`,
`bench-runs/provider-compare`) and never reads them back through this gateway
route. The route is also absent from `ops/doctor-and-diagnostics.md`'s curated
diagnostics curl list, unlike `GET /v1/tools`, which IS in that list (line 136:
`curl http://127.0.0.1:8765/v1/tools`) and confirmed live in
`ops/production-inventory-2026-07-04.md` — so `/v1/tools` must be left
untouched.

Target files:

- `internal/gateway/gateway.go`
- `internal/gateway/benchmark_routes.go`
- `internal/gatewayapi/types.go`
- `internal/gateway/session_events_status_test.go`
- `docs/architecture/gateway-and-sessions.md`

Checklist:

- Remove the `s.mux.HandleFunc("GET /v1/benchmarks", s.handleBenchmarks)`
  registration at `internal/gateway/gateway.go:301` — leave the
  `GET /v1/tools` registration on line 302 untouched.
- Delete `internal/gateway/benchmark_routes.go` entirely (`handleBenchmarks`,
  `defaultBenchmarkRunsDir`, `listBenchmarkRuns`, `readBenchmarkRunSummary`,
  `resolveBenchmarkArtifactPath`, `absBenchmarkPath`, `fileExists`, `dirExists`).
- Remove the `type BenchmarkListResponse = gatewayapi.BenchmarkListResponse` and
  `type BenchmarkRunSummary = gatewayapi.BenchmarkRunSummary` aliases at
  `internal/gateway/gateway.go:116-117`.
- Remove the `BenchmarkListResponse` and `BenchmarkRunSummary` struct
  definitions from `internal/gatewayapi/types.go` (lines 409-423).
- Delete `TestGatewayBenchmarksEndpointListsManifestSummaries` in
  `internal/gateway/session_events_status_test.go` (lines 754-812), including
  its manifest/results/events fixture setup that becomes orphaned.
- Update `docs/architecture/gateway-and-sessions.md` to drop the
  "`GET /v1/benchmarks`" row (line 69) from the route table — do not touch the
  "`GET /v1/tools`" row (line 70).
- Run `go build ./...` and `go vet ./...` to catch any remaining references
  (e.g. confirm `config.BillyHomeDir` stays used elsewhere in gateway.go, since
  other handlers reference it too).
- Run the gateway and gatewayapi test suites to confirm the remaining routes
  and DTOs still compile and pass.
- Grep the repo for `v1/benchmarks`, `BenchmarkListResponse`,
  `BenchmarkRunSummary`, and `handleBenchmarks` to confirm zero leftover
  references anywhere (code, docs, fixtures).

Verification:

```sh
go build ./...
go vet ./...
go test ./internal/gateway/...
go test ./internal/gatewayapi/...
grep -rn 'v1/benchmarks\|BenchmarkListResponse\|BenchmarkRunSummary\|handleBenchmarks' --include='*.go' .
```

### P2.3 Delete docs/architecture.md's unenforced "Guarded Rules" section; fold its 2 non-duplicate facts into the table

Note: overlaps P1.6. If P1.6 already removed the "Guarded Rules" prose, apply
only this task's `architecture_test.go` delta and skip the doc edits.

Finding: `docs/architecture.md`'s `## Guarded Rules` section (lines 98-114, the
file's final section) duplicates the Package Map table for ~13 of 15 bullets
almost verbatim against the table's "Forbidden imports and owner notes" column
(e.g. the `eventlog`, `clientux`, `gatewayapi`, `gatewayclient`, `gatewaybase`,
`tui/transcript`, `tools`, `telegrambot`, `tui` bullets are word-for-word
restatements of the same row's notes cell). `internal/architecture/architecture_test.go`'s
package-rule parser only reads rows between the `## Package Map` heading
(line 12) and the next `## ` heading (`## Runtime Event Delivery`, line 69), so
`Guarded Rules` is never parsed or enforced and can drift from the table with
zero test failure. Two bullets carry a slightly sharper mandate than the table
today — "`internal/trace` and `internal/gateway` must directly import
`eventlog`" and "TUI itself must not [import `runtimehost`]" — and should be
folded into the `internal/gateway` row (line 31) and `internal/tui` row
(line 60) respectively before the section is deleted.

Target files:

- `docs/architecture.md`
- `internal/architecture/architecture_test.go`

Checklist:

- Read `docs/architecture.md` lines 98-114 (`## Guarded Rules`) alongside the
  Package Map table rows for `internal/gateway` (line 31) and `internal/tui`
  (line 60).
- In the `internal/gateway` row's "Forbidden imports and owner notes" cell
  (line 31), append the mandate from the bullet "`internal/trace` and
  `internal/gateway` must directly import `eventlog`" — e.g. append "; must
  directly import `eventlog` for session/replay lifecycle, not reintroduce
  separate validation."
- In the `internal/tui` row's notes cell (line 60), append the mandate from
  the bullet "`internal/tui/runtimeclient` ... TUI itself must not [import
  `runtimehost`]" — e.g. append "; must not import `runtimehost` directly —
  only `tui/runtimeclient` may."
- Re-check the remaining 13 Guarded Rules bullets (`protocol`, `clientux`,
  `clientux/projector`, `gatewayapi`, `gatewayclient`, `gatewaybase`,
  `tui/render`, `tui/selection`, `tui/transcript`, `tools`, `telegrambot`
  bullets) against their table rows and confirm each fact is already present
  verbatim or in substance in the "Allowed internal imports" or notes cell —
  no other fold-ins needed.
- Delete the entire `## Guarded Rules` section, lines 98-114, from
  `docs/architecture.md`.
- Verify `docs/architecture.md` still ends cleanly after the
  `## File Size Budget Exceptions` section (lines 78-96) with no dangling
  heading or stray blank lines.
- Do not modify `internal/architecture/architecture_test.go` — it already
  ignores everything outside `## Package Map` and needs no change.
- Run `go test -count=1 ./internal/architecture/...` to confirm the guard
  still passes unchanged after the doc edit.

Verification:

```sh
go test -count=1 ./internal/architecture/...
git diff docs/architecture.md
grep -n '## Guarded Rules' docs/architecture.md
```

### P2.4 Replace hygiene's markdown-scraped large-file allowlist with a literal Go map

Finding: `hygieneLargeFileAllowlist` (`cmd/fast-agent-harness/hygiene.go`
lines 218-253) opens `docs/architecture.md`, matches the literal string
`## File Size Budget Exceptions`, and regex-scans backtick spans for `.go`
paths until the next `## ` heading. It is called once, at line 167
(`largeFileAllowlist := hygieneLargeFileAllowlist(repoDir)`), feeding
`collectHygieneSource` (lines 162-207). Renaming/reformatting that section
silently returns an empty map from the function itself. `-strict` hygiene
already runs in CI (`.github/workflows/ci.yml` "Strict hygiene" step), so an
emptied allowlist would move real files (e.g. `internal/gateway/gateway.go`,
listed in the table at architecture.md line ~90) from "allowed large files" to
"large source files" and fail CI loudly, not silently — but the design is
still fragile, unnecessary coupling of runtime behavior to prose formatting.
`cmd/fast-agent-harness/hygiene_test.go`'s
`TestHygieneStrictAllowsDocumentedLargeFiles` (lines 82-107) exercises this by
writing a fake `docs/architecture.md` into a temp repo — that test must be
rewritten, not just supplemented, once the mechanism changes. The five current
exception paths (architecture.md's File Size Budget Exceptions table) are:
`internal/tools/tools.go`, `internal/gateway/gateway.go`,
`internal/telegrambot/commands_flow_test.go`, `internal/gateway/gateway_test.go`,
`internal/mcpclient/client_test.go` — all still live-tracked P1.x tickets in
`loop-develop/current-todo/004-todo.md`, so dropping the owner/plan columns
loses no unique tracking data.

Target files:

- `cmd/fast-agent-harness/hygiene.go`
- `cmd/fast-agent-harness/hygiene_test.go`
- `docs/architecture.md`

Checklist:

- In `cmd/fast-agent-harness/hygiene.go`, add
  `var hygieneLargeFileExceptions = map[string]bool{...}` near
  `hygieneRuntimeArtifactPaths` (line 23), populated with the 5 current paths:
  `internal/tools/tools.go`, `internal/gateway/gateway.go`,
  `internal/telegrambot/commands_flow_test.go`,
  `internal/gateway/gateway_test.go`, `internal/mcpclient/client_test.go`.
- Delete `hygieneLargeFileAllowlist()` (lines 218-253) entirely.
- In `collectHygieneSource` (lines 162-207), replace
  `largeFileAllowlist := hygieneLargeFileAllowlist(repoDir)` (line 167) with a
  direct reference to `hygieneLargeFileExceptions`, and update the lookup at
  line 196 (`if largeFileAllowlist[path] {`) accordingly.
- Rewrite `cmd/fast-agent-harness/hygiene_test.go`'s
  `TestHygieneStrictAllowsDocumentedLargeFiles` (lines 82-107): it currently
  fabricates a `docs/architecture.md` in a temp repo dir to prove the parser
  works — since the allowlist is no longer repo-doc-derived, change the
  fixture path to one already in `hygieneLargeFileExceptions` (or add a small
  dedicated test-only path to the map for the test) and drop the
  `architecture.md` fixture write.
- Add a new small test (e.g. `TestHygieneExceptionsIncludeKnownLargeFiles`)
  asserting `hygieneLargeFileExceptions["internal/gateway/gateway.go"]` is
  true, so an accidental future deletion/edit of an entry fails a unit test
  instead of only surfacing via CI drift.
- Simplify `docs/architecture.md`'s `## File Size Budget Exceptions` section
  (lines 78-96): replace the 3-column table (File | Current exception owner |
  Split plan) with a 2-column table (Path | Reason), and add a one-line note
  that the enforced list lives in `hygiene.go`'s `hygieneLargeFileExceptions`,
  with this doc table kept only as human-readable context.
- Run `go build ./cmd/fast-agent-harness/...` and
  `go test ./cmd/fast-agent-harness/...` to confirm compilation and the
  rewritten/new tests pass.
- Run `go run ./cmd/fast-agent-harness hygiene -repo . -strict` locally to
  confirm the same 5 files still appear under "allowed large source files"
  and strict mode still exits 0.

Verification:

```sh
go build ./cmd/fast-agent-harness/...
go test ./cmd/fast-agent-harness/...
go run ./cmd/fast-agent-harness hygiene -repo . -strict
```

### P2.5 Acknowledge the ADR 0004/0005 numbering gap and drop unused Status/Supersedes template fields

Finding: `docs/adr/README.md` lists ADRs 0001-0003 (lines 23-29) then jumps
straight to 0006 (lines 30-37, followed by 0007, 0008) with zero
acknowledgment of the gap, even though the same file's own Numbering section
(line 19: "Never reuse a number") and `docs/README.md`'s "`adr/`: append-only
architecture decision records" bullet (line ~45) independently call the
sequence append-only. `git log --diff-filter=A -- 'docs/adr/*.md'` and
`--diff-filter=D` show 0004/0005 were never created or deleted — the gap is an
unexplained skip, not a mistake to cover up. Separately, all six existing ADR
files (0001, 0002, 0003, 0006, 0007, 0008) and the README's own Template block
(lines 41-65) carry byte-identical boilerplate `Status: accepted` /
`Supersedes: none` / `Superseded by: none` — fields that have never once
varied across records written within a 2-day span (2026-07-03 to 2026-07-04),
exactly the kind of unused metadata ceremony this repo is hunting.

Target files:

- `docs/adr/README.md`
- `docs/README.md`

Checklist:

- Confirm via `git log --diff-filter=A -- 'docs/adr/*.md'` and
  `--diff-filter=D` that 0004/0005 never existed (already verified: no add or
  delete commits for those numbers).
- In `docs/adr/README.md`, under the existing `## Numbering` section
  (lines 10-19), add a short note, e.g. "0004 and 0005 were never assigned;
  the sequence resumes at 0006 by design. The next new ADR is 0009."
- In the same file's `## Template` code block (lines 41-65), remove the
  `Status:`, `Supersedes:`, and `Superseded by:` lines (lines 44, 47, 48),
  keeping `Date:` and `Owners:`.
- Update the `## Rules` bullet (line 71: "create a new ADR and mark the old
  one superseded") to instead say something like "create a new ADR and add a
  Status/Supersedes note to both files only when that first supersede
  actually happens," so the rule doesn't reference fields the template no
  longer carries by default.
- Do not touch `docs/adr/0001-*.md` through `0008-*.md` — leave their existing
  Status/Supersedes/Superseded-by lines as-is; this is forward-only.
- Add the same one-line numbering-gap note to `docs/README.md` near its
  "`adr/`: append-only architecture decision records" bullet (line ~45),
  since it makes the identical append-only claim and shows the same 0003 ->
  0006 jump.
- Re-read `docs/adr/README.md` end to end to confirm the Numbering, Template,
  and Rules sections stay internally consistent (no leftover reference to
  fields removed from the template).

Verification:

```sh
git diff --stat docs/adr/README.md docs/README.md
grep -n 'Status:\|Supersedes:\|Superseded by:' docs/adr/README.md
grep -n '0004\|0005\|0009' docs/adr/README.md
go build ./...
```

### P2.6 Add a TODO-scope guardrail to AGENTS.md's Loop Development Workflow section

Finding: `AGENTS.md`'s `## Loop Development Workflow` section (lines 39-81)
lists what a current TODO must *include* (source summary, checklist, target
files, verification, goal prompt — lines 56-62) but has zero guardrail on size
or on sourcing checklist items from external frameworks.
`loop-develop/current-todo/004-todo.md` is the live proof of the failure mode:
2,084 lines, line 16 literally says "This TODO is intentionally large," and it
spans P0-P2 milestones (lines 143, 335, 506, 670, 854, 977, 1086) with items
explicitly benchmarked against OWASP LLM Top 10 (lines 91-95), OpenTelemetry
GenAI observability (lines 100-103), and an "incident-grade artifact"
deliverable (line 568, P0.15) — the exact enterprise-ceremony shape the
owner's stated north star says to hunt. The proposed AGENTS.md addition is
doc-only, doesn't touch 004-todo.md's in-flight real security fixes, and
doesn't weaken any auth/redaction/trust-boundary code.

Target files:

- `AGENTS.md`

Checklist:

- Open `AGENTS.md` and locate the `## Loop Development Workflow` section
  (lines 39-81).
- Immediately after the bulleted "A current TODO should include" list (ends
  line 62), insert one new paragraph stating: prefer several small,
  single-slice `NNN-todo.md` files over one large multi-milestone document;
  when a research pass would otherwise produce more than roughly 10 checklist
  items or spans multiple P0-P2 milestones, split it into separate numbered
  TODOs (e.g. `005-todo.md`, `006-todo.md`) by milestone or theme instead of
  concatenating into one file.
- In the same paragraph or a follow-on sentence, add: before adding a
  checklist item sourced from external framework/compliance research (OWASP
  LLM Top 10, OpenTelemetry GenAI posts, vendor SDK docs, etc.), state the
  concrete Billy-facing failure or daily-use problem in this solo harness
  that it fixes, or drop the item.
- Do not edit `loop-develop/current-todo/004-todo.md` itself — its P0
  security items are real and already in progress (see its 2026-07-04
  completion entries).
- Keep the new text to one short paragraph (3-5 sentences) so `AGENTS.md`
  itself does not grow into a policy manual.
- Re-read the full `## Loop Development Workflow` section after editing to
  confirm it still reads as a short, actionable contract.
- Run `git diff --check` to catch whitespace/formatting issues in the edit.
- Run the repo's hygiene command to confirm doc/file conventions still pass.

Verification:

```sh
git diff --check
go run ./cmd/fast-agent-harness hygiene -repo . -strict
go build ./...
```

## P2 Milestone 6 - Collapse Duplicated Docs, Metadata, And Code Paths

Goal: collapse six single-source-of-truth violations spread across docs
navigation, web-tool telemetry, config resolution, and the TUI/Telegram
surfaces. Each currently forces two copies of the same list, metric, disk
read, debug view, or parsing switch to be hand-kept in sync, inviting drift
or wasting cycles/bytes on the daily loop. None touches gateway auth,
Telegram operator checks, or secret redaction.

### P2.7 Collapse llms.txt's duplicated Architecture Canon list into a pointer to docs/README.md

Finding: llms.txt's `## Architecture Canon` (lines 23-51) already has a
`Docs index` bullet at line 25 pointing to `docs/README.md`, but it undersells
itself ("architecture docs only") while lines 26-51 re-list all 9
architecture docs individually plus the ADR index -- duplicating
`docs/README.md`'s own `## Canon` (14-41) and `## Decisions` (43-60) almost
verbatim. The evidence log at `loop-develop/current-todo/004-todo.md:1273-1277`
shows both files were edited together for ADR 0008, confirming they drift
unless hand-kept in sync on every architecture-doc change.

Target files:

- `llms.txt`
- `docs/README.md`

Checklist:

- Delete the 9 per-file bullets at lines 26-51 (Architecture map, Gateway and
  sessions, Security and trust model, Runtime event system, TUI and client UX
  architecture, Config/provider/context architecture, Tools/MCP/webtools,
  Telegram and operator surfaces, Documentation system, ADR index).
- Rewrite the `Docs index` bullet (line 25) to state it is the sole
  enumerated list, e.g.: `- [Docs index](docs/README.md): architecture canon
  and ADR index -- the sole enumerated list; do not re-list docs here.`
- Do not touch `docs/README.md`'s `## Canon` (14-41) or `## Decisions`
  (43-60) -- it stays the one enumeration.
- Confirm non-docs navigation is unchanged: Start Here, Agent Rules, Machine
  Indexes, Coverage Note, Operations Runbooks, Optional.
- Grep one distinctive filename (`tools-mcp-and-policy.md`) and confirm only
  `docs/README.md` and `docs/architecture/security-model.md` (a genuine
  cross-reference) still list it descriptively.
- Re-read llms.txt top-to-bottom to confirm no dangling links/punctuation
  remain from the deleted bullets. Do not touch `agent-index/`.

Verification:

```sh
grep -n 'Architecture Canon' -A 5 llms.txt
grep -c 'architecture/' llms.txt   # should drop from ~9 to 0-1
diff <(grep -oE 'architecture/[a-z-]+\.md' docs/README.md | sort -u) <(grep -oE 'architecture/[a-z-]+\.md' llms.txt | sort -u)
git diff --stat -- llms.txt docs/README.md   # docs/README.md: zero diff
```

### P2.8 Web tool metadata duplicates ~15 metrics under legacy+new key names, bloating every tool_result event and its on-disk JSONL record

Finding: `webPageMetadata` (`internal/tools/web_metadata.go:147-202`) and
`crawlMetadata` (11-57) each emit `websum_input_tokens`/`websum_output_tokens`/
`websum_cost` (lines 21-23/157-159) carrying the same value as
`tool_summary_input_tokens`/`_output_tokens`/`_estimated_cost_usd`
(44-52/189-197), and `tool_summary_api_cache_hit_tokens`/`_miss_tokens`
(via `websumAPICacheTokens()`, 215-220) duplicate `websum_cache_hit`/`_miss`.
Grepping `internal/tui`, `internal/telegrambot`, `internal/clientux` shows
only `tool_summary_*` is ever read
(`internal/clientux/context.go:249,266`, `projector.go:523,535`) -- the
`websum_*` map keys are legacy dead weight (the underlying struct fields
stay). `webPageMetadata` also always emits 7 unread per-phase timing keys
(`web_cache_lookup_ms`...`web_total_ms`, 169-175). This map becomes
`protocol.ToolResult.Metadata` at `internal/agent/tool_attempt.go:177,203`,
embedded in every `tool_result` event streamed over gateway NDJSON and
persisted into `events.jsonl`.

Target files:

- `internal/tools/web_metadata.go`
- `internal/tools/web_compact.go`
- `internal/agent/tool_attempt.go`

Checklist:

- Delete the redundant `websum_input_tokens`/`websum_output_tokens`/
  `websum_cost`/`websum_cache_hit`/`websum_cache_miss` map entries from both
  `crawlMetadata` and `webPageMetadata`, keeping only `tool_summary_*`. Do NOT
  delete the underlying `WebsumX` struct fields in `web_compact.go` -- only
  the map key is duplicated; `websum_model`/`websum_error` are not duplicates
  and stay.
- Do not touch `web_cache_hit`/`web_cache_miss` -- a distinct HTTP-fetch-level
  cache concept, asserted directly in
  `internal/tui/transcript_render_test.go:238` and
  `internal/telegrambot/commands_flow_test.go:505`.
- Delete the 7 timing keys from `webPageMetadata`'s map (169-175); keep the
  backing `compactPage` fields and their writers in `web_backend.go`/
  `web_core.go` intact (real instrumentation, just unsurfaced).
- Re-grep tui/telegrambot/clientux for removed key names after editing.
- Measure one representative `web_fetch` `tool_result` JSON size before/after
  to record the reduction in the commit message.

Verification:

```sh
go build ./...
go test ./internal/tools/... -v
go test ./internal/tui/... ./internal/telegrambot/... ./internal/clientux/...
go vet ./internal/tools/... ./internal/agent/...
grep -rn "websum_input_tokens\|websum_cache_hit\|web_cache_lookup_ms\|web_total_ms" internal/tui internal/telegrambot internal/clientux
```

### P2.9 Split config.Resolve() into load-layers + apply-overrides so handleConfigStatus/TUI stop double-reading disk

Finding: `handleConfigStatus` (`internal/gateway/gateway.go:348-359`) calls
`config.Resolve()` twice -- once for `base` (349), again with override args
for `resolved` (354). `Resolve()` (`internal/config/resolved.go:69-100`) does
real synchronous I/O every call: home/project TOML decode, billy settings
read, and `applyDotenv()` (86 -> 477-488), which for every `configSpecs()`
entry's env keys re-reads/re-parses dotenv files -- an O(numSpecs) fan-out
that runs twice per request. The identical pattern is duplicated at
`internal/tui/tui.go:1114-1118`. `RuntimeDiffOverridesFromSettings`
(`internal/config/runtime_diff.go:112`) only diffs in-memory state against
`base.Config` -- no disk access -- so the second `Resolve()` call's only real
job is applying overrides via `applyProfileMetadata()`/`finalizeDerivedValues()`
(91/94).

Target files:

- `internal/gateway/gateway.go`
- `internal/config/resolved.go`

Checklist:

- Split `Resolve()` (line 69) into phase A (through `applyEnvironment()`,
  line 87 -- the disk I/O) and phase B (overrides, profile metadata, derived
  values).
- Add `ResolveEffective(overrides ...ResolveOverride) (base, effective
  ResolvedConfig, err error)` that runs phase A once and phase B twice on
  independent copies of the loaded state (no overrides for `base`, given
  overrides for `effective`). Keep `Resolve()` itself unchanged for existing
  single-call sites.
- Clone `resolveState`'s `cfg`/`values`/`warnings` before each phase-B call so
  overrides never leak from `effective` into `base`.
- Update `handleConfigStatus` (gateway.go:348) to call `ResolveEffective`
  with an override-builder `func(base Config) []ResolveOverride` (since
  `RuntimeDiffOverridesFromSettings` needs `base.Config`). Apply the same fix
  to `tui.go:1114-1118`.
- Do NOT add a TTL cache with mutation-invalidation hooks -- unnecessary
  complexity for a rarely-hit, on-demand endpoint on a solo tool.
- Add a test near `TestResolveConfigRecordsPrecedenceAndDoesNotLeakSecrets`
  (`config_test.go:765`) asserting `base`/`effective` independence.
- Manually hit `GET /v1/config` after changing a `settings.json` value to
  confirm the response reflects it (proves phase A isn't cached stale).

Verification:

```sh
go build ./...
go test ./internal/config/... -run Resolve -v
go test ./internal/config/... ./internal/gateway/... ./internal/tui/...
go vet ./internal/config/... ./internal/gateway/... ./internal/tui/...
```

### P2.10 Collapse duplicate debug views: make /status debug alias /debug instead of its own wrapper

Finding: `internal/tui/transcript_runtime.go:272-274` defines
`func (m Model) debugStatusText() string { return m.debugFullText() }`, a
byte-identical wrapper. `internal/tui/actions.go` wires two slash commands to
the same content: `/status debug` (registered 95-120, `case "debug"` at 108
calls `m.debugStatusText()` at 109) and `/debug` (registered 173-184, line
180 calls `m.debugFullText()` directly). `/help` lists two commands that
produce byte-identical output, confusing during actual debugging.

Target files:

- `internal/tui/transcript_runtime.go`
- `internal/tui/debug_snapshot.go`
- `internal/tui/actions.go`

Checklist:

- Delete `debugStatusText()` (transcript_runtime.go:272-274).
- In `actions.go` line 109, change `m.debugStatusText()` to
  `m.debugFullText()`.
- Grep (`grep -rn debugStatusText .`) to confirm no other references remain;
  `interaction_selection_test.go:382` already calls `debugFullText()`
  directly, so no test change is expected, but re-check after the delete.
- Tighten `/status`'s `slashArgs` help entry (`actions.go:103`) to note it
  renders the same view as `/debug`, so `/help` stops implying two tools.
- Confirm `debugFullText()` (`debug_snapshot.go:142`) needs no changes -- it
  stays the single canonical implementation.
- Run `go build ./internal/tui/...` to confirm no other callers, then run the
  TUI test suite to confirm both commands still behave identically.

Verification:

```sh
go build ./internal/tui/...
go vet ./internal/tui/...
go test ./internal/tui/... -run TestInteractionSelection -v
go test ./internal/tui/...
grep -rn debugStatusText .
```

### P2.11 Cache reflow()'s per-block loop to avoid re-hashing/re-rendering unchanged blocks on every streaming flush

Finding: `reflow()` (`internal/tui/transcript_runtime.go:972-1013`) loops
every `m.blocks` and `strings.Join`s the whole transcript (1004) on every
call; streaming flushes call it every ~25ms
(`tui.go:270 streamEventBatchInterval`). Benchmarking with the existing
`BenchmarkTUIReflowLongTranscriptCached` (`tui_test.go:1036`): 2.57ms @300
blocks, 4.93ms @600, 9.26ms @1200 (M5 Pro), scaling linearly. ~41% of that is
`charm.land/bubbles/v2` `viewport.Model.SetContent`'s unconditional
full-rebuild (no incremental API -- architecturally unavoidable). The
remaining ~5.4ms is `render.BlockCacheKey` (`internal/tui/render/cache.go:38-56`)
SHA1-hashing every block's `Content`/`RawCopy` on every call, even untouched
ones -- that hash cost, not rendering (already skipped on cache hit via
`renderBlockCached`, line 999), is the real tax. A fast path can cut
`reflow()` cost ~55-60% for long transcripts, not eliminate it.

Target files:

- `internal/tui/transcript_runtime.go`

Checklist:

- Baseline first: `go test ./internal/tui/ -run '^$' -bench
  BenchmarkTUIReflowLongTranscriptCached -benchtime=200x -benchmem`, plus one
  more benchmark at 300 blocks via `newBenchmarkLongTranscript`
  (`tui_test.go:1059`).
- Add `Model` fields (`tui.go`) to detect the append-only streaming case:
  `lastReflowBlockCount int` plus a filter-signature snapshot (`toolView`,
  `thinkView`, `currentToolTurnID`, per-block `Collapsed` state).
- In `reflow()`, add a fast path taken only when the filter signature is
  unchanged AND either only the last block's cache key changed, or exactly
  one block was appended -- re-render just that block via `renderBlockCached`
  and splice into the cached parts instead of rebuilding from scratch.
- Keep the full-rebuild loop as fallback for every other trigger (resize,
  toggle, view-mode switch, selection change, non-append edits).
- Do not claim `viewport.SetContent`'s cost is eliminated -- scope this to
  the per-block loop/hash cost only, and say so in the commit message.
- Re-run the baseline benchmarks after the change; record before/after ns/op.
  If the 300-block case shows no clear win, reconsider the added complexity.
- Manually verify with `/run` on a long tool-heavy session that toggling
  `toolView`/`thinkView` mid-stream still forces full rebuild and renders
  correctly (highest regression risk).

Verification:

```sh
go build ./internal/tui/...
go test ./internal/tui/... -run TestReflow -v
go test ./internal/tui/ -run '^$' -bench BenchmarkTUIReflowLongTranscriptCached -benchtime=200x -benchmem
go vet ./internal/tui/...
go test ./internal/tui/...
```

### P2.12 Make Telegram /mode call config.ParseAccessMode instead of duplicating its alias switch

Finding: `internal/telegrambot/commands.go`'s `handleModeCommand` (357-378)
hand-rolls its own alias switch at lines 367-373 (`case
config.AccessModeBuild, config.AccessModeGuarded, config.AccessModePlan,
"safe", "readonly", "read-only", "read_only", "analysis":`), then separately
calls `config.NormalizeAccessMode(mode)` (374). `internal/config/access_mode.go:18-29`
already defines `ParseAccessMode(value string) (string, bool)` with exactly
this alias set as the single source of truth -- `NormalizeAccessMode`
(11-16) itself just wraps it. A new alias added to `ParseAccessMode` would
keep getting rejected by `/mode` until this second copy is updated too.

Target files:

- `internal/telegrambot/commands.go`
- `internal/config/access_mode.go`

Checklist:

- Confirm `ParseAccessMode` signature (`access_mode.go:18`); no change needed
  there, it stays the source of truth.
- In `handleModeCommand`, replace the `mode := strings.ToLower(...)` +
  `switch mode {...}` block (367-373) with `normalized, ok :=
  config.ParseAccessMode(arg)`.
- On `!ok`, keep the existing `"Unknown access mode. Use build, guarded, or
  plan."` reply and return -- no user-facing string changes.
- On `ok`, replace `state.AccessMode = config.NormalizeAccessMode(mode)`
  (374) with `state.AccessMode = normalized`.
- Remove the now-unused `mode` local and any dead
  `strings.ToLower`/`TrimSpace` calls left in the function.
- Confirm `commands_flow_test.go:445-485`
  (`TestTelegramModeCommandSetsPlanModeRunRequest`) still passes unchanged.
- Add a test case for an alias only reachable via the old switch (e.g.
  `"safe"` or `"read_only"`) and one for an unknown mode string, to confirm
  `ParseAccessMode` and the rejection message both still fire correctly.

Verification:

```sh
go build ./internal/telegrambot/... ./internal/config/...
go test ./internal/telegrambot/... -run TestMode -v
go test ./internal/telegrambot/... ./internal/config/... -v
go vet ./internal/telegrambot/...
```

## P2 Milestone 7 - Telegram Command Discoverability And Production Deploy Safety

Goal: make the two Telegram surfaces that owners actually reach for from a
phone -- `/status` and `/model` -- as fast and discoverable as their TUI
equivalents, and replace the production rollback runbook's hand-typed
`git checkout` + rebuild + restart sequence with one command that cannot leave
the box on a build that failed its own health checks.

### P2.13 Give Telegram /status a short default view and push ACL/debug internals behind /status debug

Finding: `StatusHTMLWithRuntime` (`internal/telegrambot/status_html.go:37-76`)
always renders all 15 lines in one shot -- session, selected model, active
runtime model, profile, access mode, reasoning, and context/compact fields sit
mixed in with `agent turns`, `tools`, `event cursor`, `pending input`, and the
full `allowed chats` / `allowed users` / `allowed user scope` ID dumps built
from `opts.AllowedChatIDs` / `opts.AllowedUserIDs` (lines 41-58). Every
Telegram user who can reach `/status` in an allowed chat sees the raw allowlist
IDs. `handleStatusCommand` (`internal/telegrambot/commands.go:281-285`) has the
signature `func(ctx, msg, scope ChatScope, _ string)` -- it already receives a
parsed `arg` (see the dispatch `spec.handler(b, ctx, msg, scope, arg)` at
`commands.go:187`, and `arg` is used by neighboring handlers such as
`handleModelCommand` at `commands.go:302`) but discards it, so there is no way
to ask for less or more detail. The TUI already solved this exact problem for
its own `/status`: `internal/tui/actions.go:96-120` declares
`slashArgs: "[debug]"` and dispatches `""` to `m.statusText()` and `"debug"` to
`m.debugStatusText()` (`internal/tui/transcript_runtime.go:219` and `:272`),
replying `"unknown status view " + arg` on anything else. The shared registry
entry for this action, `status.show` in `internal/clientux/actions.go:90-98`,
has an empty `SlashArgs`, which is why Telegram's `/help` line for `/status`
currently shows no argument hint at all (`TelegramCommandUsage()` at
`internal/clientux/actions.go:43-54` only appends `SlashArgs` when non-empty).
No test in `commands_flow_test.go` currently asserts on the allowed-chats,
allowed-users, or event-cursor fields specifically, so splitting the renderer
is low-risk to existing coverage.

Target files:

- `internal/telegrambot/status_html.go`
- `internal/telegrambot/commands.go`
- `internal/clientux/actions.go`
- `internal/telegrambot/commands_flow_test.go`

Checklist:

- In `status_html.go`, rename the current body of `StatusHTMLWithRuntime` to
  an unexported `statusHTMLFull(state ChatState, opts Options, runtime
  gatewayapi.SessionStatus) string` that keeps all 15 lines verbatim.
- Add a new `StatusHTML(state, opts) string` / `StatusHTMLWithRuntime(state,
  opts, runtime) string` pair (same exported names, same signatures, so no
  call site outside this package needs to change) that renders only: session,
  selected model, active runtime model, profile, access mode, reasoning,
  selected context window, selected compact threshold, and send.
- Add `StatusDebugHTMLWithRuntime(state, opts, runtime) string` that renders
  the remaining fields -- agent turns, tools, event cursor, pending input,
  allowed chats, allowed users, allowed user scope -- by slicing/reusing
  `statusHTMLFull`'s field-building logic (the `allowedChats`/`allowedUsers`
  sort-and-format block at lines 41-58) rather than duplicating it.
- In `commands.go`, change `handleStatusCommand`'s signature from `(ctx, msg,
  scope, _ string)` to `(ctx, msg, scope, arg string)` and switch on
  `strings.TrimSpace(arg)`: `""` -> `StatusHTMLWithRuntime`, `"debug"` ->
  `StatusDebugHTMLWithRuntime`, anything else -> `sendPlain(ctx, msg, "Unknown
  status view "+arg)`, mirroring `tui/actions.go:105-119`.
- In `clientux/actions.go`, set `SlashArgs: "[debug]"` on the `status.show`
  entry (lines 90-98) so `TelegramCommandUsage()` and `telegramCommandHelpHTML()`
  (`commands.go:211-233`) pick up the new usage hint for free.
- Update every existing `StatusHTML(...)` / `StatusHTMLWithRuntime(...)`
  assertion in `commands_flow_test.go` (e.g. `TestTelegramModelCommandAnd
  StatusShowInputCapability` at line 156, `TestStatusHTMLContextWindowFollows
  ModelInfo` at line 210, `TestStatusHTMLSeparatesSelectedAndRuntimeModel` at
  line 253) so they still pass against the new short view -- confirm
  session/model/context-window/compact-threshold lines are present and that
  `allowed chats` / `event cursor` are absent from the default view.
- Add a new test exercising `/status debug` through `bot.handleMessage` (same
  fixture pattern as `TestTelegramStatusCommandFetchesRuntimeModel` at line
  287) asserting `allowed chats`, `allowed users`, and `event cursor` appear
  only in the debug reply, never in the plain `/status` reply.
- Add a test for `/status bogus` asserting the "Unknown status view" reply and
  that no HTML status body is sent.
- Grep the repo (`rg -n "StatusHTMLWithRuntime|StatusHTML\("`) for call sites
  outside `internal/telegrambot` (e.g. TUI gateway-mode status glue) and
  update them to the new split functions if any exist.
- Run `gofmt -l` on touched files before verification.

Verification:

```sh
go build ./...
go test ./internal/telegrambot/... -run TestStatusHTML -v
go test ./internal/telegrambot/... -run TestTelegramStatusCommand -v
go test ./internal/telegrambot/...
go vet ./internal/telegrambot/...
```

### P2.14 Wire up the already-defined /models action for Telegram using modelinfo.Providers()

Finding: `internal/clientux/actions.go:204-209` already declares a shared
`"models.list"` `ActionDefinition` (`Slash: "/models"`) used by the TUI, but
its `TelegramAliases` field is empty and `telegramCommands()` in
`internal/telegrambot/commands.go:62-159` never registers a `"models.list"`
entry, so Telegram has no listing command at all -- `handleModelCommand`
(`commands.go:302-313`) only echoes the current model when called with no
argument, and there is no way to see the full alias/provider set from a phone.
The TUI's own `modelsText()` (`internal/tui/transcript_runtime.go:330-344`) is
not a fixed catalog either: it walks `m.models`, a dynamic per-session slice
built from config/toggles/history in `internal/tui/runtime_config.go`, so
copying that function would duplicate TUI session state rather than reuse a
shared source of truth. The actual shared catalog is
`internal/modelinfo.Providers()` (`internal/modelinfo/modelinfo.go:229-235`),
which returns `ProviderInfo{ID, Name, Models []string}` for DeepSeek, Codex,
and custom, and `modelinfo.Lookup(model)` (`modelinfo.go:73-99`) already gives
per-model `VisionInput`/`Known` capability -- both already imported
transitively via `internal/telegrambot/util.go`'s `modelAlias` /
`modelWithCapability` helpers, so this task only needs to call the package
directly, not add a new dependency.

Target files:

- `internal/telegrambot/commands.go`
- `internal/clientux/actions.go`
- `internal/telegrambot/commands_flow_test.go`

Checklist:

- In `clientux/actions.go`, add `TelegramAliases: []string{"/models"}` to the
  existing `"models.list"` entry (lines 204-209); reuse this action ID, do not
  create a new one. Optionally set `TelegramUsage`/`TelegramSummary` if the
  bare `Summary: "list known models"` doesn't read well as a Telegram help
  line.
- In `commands.go`'s `telegramCommands()` slice (lines 62-159), add
  `telegramActionCommand("models.list", telegramCommandSpec{bypassRunLock:
  true, handler: (*Bot).handleModelsCommand})`; leave `class` at its zero value
  (`telegramCommandPublic`) like `help.show`/`commands.search` since listing
  models needs no session or owner check.
- Add `func (b *Bot) handleModelsCommand(ctx context.Context, msg Message,
  scope ChatScope, arg string)` in `commands.go`: import
  `internal/modelinfo`, iterate `modelinfo.Providers()` -> `Provider.Models`,
  and for each model call `modelinfo.Lookup(model)` to format one line as
  `<model> (<provider>, <vision-capable|text-only>)`.
- Mark the chat's currently-selected model with a leading `*`, mirroring
  `modelsText()`'s marker: compare each listed model against `fallback
  (state.Model, b.opts.Model)` where `state := b.chatStateWithLegacy
  (scope.Key(), scope.LegacyKey())`.
- Ignore `arg` for now (no filtering in v1) but keep the parameter so the
  handler signature matches `telegramCommandHandler`; note in a comment that a
  future revision could filter by provider substring.
- Leave `telegramCommandHelpHTML()` (`commands.go:211-233`) untouched -- it
  already includes any spec with a non-empty `usage` string via its existing
  loop, so `/models` appears in `/help` automatically once the spec has a
  `usage` (derived from the action's `Slash`/`SlashArgs`/`TelegramUsage`).
- Add a test in `commands_flow_test.go` (follow the fixture pattern of
  `TestTelegramModelCommandAndStatusShowInputCapability` at line 156) sending
  `/models` and asserting the reply is non-empty, contains
  `deepseek-v4-flash` and `gpt-5.5`, and marks whichever model is current for
  that chat state with `*`.
- Add a second assertion in the same or a new test that an empty-provider
  edge case (no configured chat state) still returns all catalog entries
  rather than erroring.
- Run `gofmt -l` on touched files before verification.

Verification:

```sh
go build ./internal/clientux/... ./internal/telegrambot/... ./internal/modelinfo/...
go test ./internal/telegrambot/... -run TestTelegram -v
go test ./internal/clientux/... ./internal/modelinfo/... -v
go vet ./internal/telegrambot/... ./internal/clientux/...
```

### P2.15 Add scripts/deploy.sh and scripts/rollback.sh for SHA-symlinked production deploys

Finding: `ops/production-services.md`'s Rollback Pattern section (lines
108-138) states outright: "This repository does not define the production
deploy mechanism or a release archive layout," and the only documented
rollback path is a hand-typed `git checkout PREVIOUS_GOOD_COMMIT`, a manual
`go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`,
two separate `systemctl restart` calls, and then `doctor -mode=production`
plus curl checks against `/health` and `/ready` -- five manual steps with no
guard against skipping the last one. `scripts/` today holds only
`verify-deps.sh`, `verify-local.sh`, `bench-smoke.sh`, and `bench-compare.sh`;
no `deploy.sh`/`rollback.sh` exist. `ops/production-inventory-2026-07-04.md`
(the systemd unit table, line 41) confirms both `billyharness-gateway.service`
and `billyharness-telegram.service` point `ExecStart=` at the fixed path
`/root/billyharness/bin/fast-agent-harness <subcommand>`, not a symlink, so
introducing a symlink-swap model is a real host-facing change, not already in
place. This exact idea is already on this project's own roadmap as
`loop-develop/current-todo/004-todo.md` P2.6, "Add production deploy/rollback
scripts only after doctor is trustworthy" (line 1186), gated on the doctor/
readiness split landing first -- that split appears to have just landed
(`P0.17`, commit `c4e9ece`, "Split gateway liveness and readiness checks",
matching the recent git log head). Treat this task as unblocked but keep its
safety contract tied to `doctor -mode=production` and `/ready` being real
signals, per that plan.

Target files:

- `ops/production-services.md`
- `scripts/deploy.sh`
- `scripts/rollback.sh`
- `scripts/lib/deploy-verify.sh`

Checklist:

- Add `scripts/deploy.sh`: resolve `repo_root` the same way
  `scripts/verify-local.sh:4-6` does (`CDPATH= cd -- "$(dirname --
  "${BASH_SOURCE[0]}")"`), then run `go build -buildvcs=false -o
  bin/fast-agent-harness-$(git rev-parse --short HEAD)
  ./cmd/fast-agent-harness`, following the `set -uo pipefail` and
  `run_step`-style timestamped logging already used in `verify-local.sh:56-77`.
- Before repointing the `bin/fast-agent-harness-current` symlink, have
  `deploy.sh` capture the prior symlink target via `readlink
  bin/fast-agent-harness-current` (if it exists) into `bin/.previous-release`.
- Factor the shared "restart both services, then run doctor + health + ready"
  logic into `scripts/lib/deploy-verify.sh`, sourced by both `deploy.sh` and
  `rollback.sh`: `systemctl restart billyharness-gateway.service
  billyharness-telegram.service`, then `./bin/fast-agent-harness-current
  doctor -mode=production`, `curl -sf http://127.0.0.1:8765/health`, and
  `curl -sf http://127.0.0.1:8765/ready`.
- In `deploy.sh`, on any verification failure, restore
  `bin/fast-agent-harness-current` to the `bin/.previous-release` target,
  restart both services again, and exit non-zero -- never leave the symlink
  pointed at a build that failed its own checks.
- Add `scripts/rollback.sh`: read `bin/.previous-release`, re-symlink
  `bin/fast-agent-harness-current` to it, then call the same shared
  `deploy-verify.sh` restart/check helper `deploy.sh` uses so the two scripts
  cannot drift.
- Add release pruning to `deploy.sh`: after a verified-good deploy, append the
  new SHA to `bin/.release-history` and delete `bin/fast-agent-harness-<sha>`
  binaries older than the last N (e.g. 5) entries in that file.
- Update `ops/production-services.md`'s Rollback Pattern section (currently
  lines 108-138) to lead with `scripts/deploy.sh` / `scripts/rollback.sh`,
  keep the existing manual `git checkout PREVIOUS_GOOD_COMMIT` recipe only as
  a documented fallback for a from-scratch checkout, and add a note that on
  the host, `ExecStart=` for both units must change from
  `/root/billyharness/bin/fast-agent-harness <subcommand>` to
  `/root/billyharness/bin/fast-agent-harness-current <subcommand>` (per
  `ops/README.md`'s rule that unit file contents live on the host, not in this
  repo).
- Do not run this against production until the doctor/readiness work is
  confirmed stable there, per the existing gate noted in
  `loop-develop/current-todo/004-todo.md` P2.6; note that dependency
  explicitly in both new scripts' top-of-file comments.
- Run `shellcheck` on both new scripts and the shared helper before
  verification; fix any warnings rather than suppressing them.

Verification:

```sh
shellcheck scripts/deploy.sh scripts/rollback.sh scripts/lib/deploy-verify.sh
go build -buildvcs=false -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness
go test -count=1 ./cmd/fast-agent-harness/...
git diff --check
scripts/verify-deps.sh
```

## Backlog And Watch Items (from completeness review)

Spot-checked against the actual tree (not just the map): `internal/agent` (~8.1k lines),
`internal/bench` (~3.9k lines), `internal/clientux/projector` + `internal/tui/transcript`
(~6.4k lines combined), and package cross-references via `grep`.

- **Audit internal/agent's core turn loop.** Zero tasks touch the ~8,100-line module every
  session actually runs through (retry/backoff, compaction, context-threshold trimming,
  parallel tool-call handling). P0.3 and P2.8 only nick `tool_attempt.go` in passing. Why: this
  is the highest-blast-radius package in the repo and it's the one place the TODO never looks.
  Files: `internal/agent/runtime_loop.go`, `compaction.go`, `model_call.go`, `tool_attempt.go`.
- **Trim or delete the Terminal-Bench eval harness.** `internal/bench` is 3,919 lines;
  `terminalbench.go` alone generates Dockerfiles, docker-compose YAML, pytest stubs, and an
  evaluator script. Only P2.2 removes one HTTP route. Why: a full Docker/pytest dataset-export
  pipeline for benchmark comparison is exactly the "incident-grade artifact pipeline" flavor
  the north star calls out, in a repo used by one person. Files: `internal/bench/terminalbench.go`,
  `internal/bench/bench.go`, `cmd/fast-agent-harness/bench_cmd.go`.
- **Consolidate the two parallel TUI projection layers.** `clientux/projector` (1,621 lines,
  Snapshot) and `tui/transcript` (4,771 lines, Cells) both derive from the same
  `protocol.Event` stream and likely duplicate token/tool-call accounting. P1.11 only moves
  shared decoders into `protocol`; it doesn't merge the projections. Files:
  `internal/clientux/projector/projector.go`, `internal/tui/transcript/*.go`.
- **Question the import-boundary guard itself, not just its data.** `internal/architecture`
  parses `docs/architecture.md`'s 44-row markdown table at test time — the same
  hand-maintained-ceremony flavor as the docguard Stop hook deleted in P0.1. Three tasks
  (P1.10, P2.3, P2.4) patch around it; none asks whether a Go-native check (or none) suits a
  1-person repo better. Files: `internal/architecture/architecture_test.go`, `docs/architecture.md`.
- **Fold internal/filesearch into internal/tools.** 834-line fuzzy file resolver consumed only
  by `internal/tools/fs_find_files.go` and the TUI's file-mention picker — same shape as the
  tooloutput→tools fold already planned in P1.10, just not proposed. Files:
  `internal/filesearch/filesearch.go`, `internal/tools/fs_find_files.go`.
- **Retention/prune sweep for other unbounded on-disk stores.** P1.19 adds prune+doctor
  visibility only for `internal/attachments`. The same "grows forever on one VPS" failure mode
  applies to session JSONL, checkpoint before/after snapshots, and the web fetch/search disk
  cache. Files: `internal/gateway/session_store.go`, `internal/checkpoint/checkpoint.go`,
  `internal/tools/webcache.go`.
- **Clarify credentials/secrets package boundaries.** config-secrets' stated purpose
  ("resolves DeepSeek/Codex credentials... redacts secrets") is actually split across three
  packages — `internal/config`, `internal/credentials` (944 lines), `internal/secrets`
  (redaction, 354 lines) — with no task confirming there's one fallback-chain implementation
  rather than two that can drift apart. Files: `internal/credentials/credentials.go`,
  `internal/config/auth_paths.go`, `internal/secrets/redact.go`.
- **Fold internal/webtools' retry/URL helpers into the helper P1.8 is building.**
  `webtools/backends.go` (1,145 lines, Tavily/Exa clients) reimplements retry-with-backoff and
  base-URL normalization separately from `internal/provider/retry.go` and the gatewaybase
  helpers P1.8 collapses. Left alone, the repo ends up with two "the" retry helpers instead of
  one. Files: `internal/webtools/backends.go`, `internal/gatewaybase/gatewaybase.go`,
  `internal/provider/retry.go`.
- **Decide the fate of untracked `loop-research/` and `.agents/skills/`.** `git status` shows
  both as new/untracked while docs-system is undergoing heavy deletion (P0.1, P0.2, P1.12); no
  task says whether they're in scope for the cleanup or explicitly exempt. Why: otherwise new
  doc ceremony grows back in the same review cycle that deletes the old ceremony. Files:
  `loop-research/`, `.agents/skills/`, `AGENTS.md`.
- **Audit config's 9-layer resolution for layer count, not just function shape.** P2.9 splits
  `config.Resolve()` into load+apply, but nothing asks whether 9 layered sources (defaults,
  home config, project config, settings.json, .env, env vars, CLI/gateway overrides,
  profile.toml, derived) is more indirection than a solo operator needs. Files:
  `internal/config/resolved.go`.
- **Check Telegram/TUI slash-command surface for drifted duplicates.** P2.14 wires the
  already-defined `/models` Telegram action, but nothing verifies `commandregistry` is
  actually deduping the ~18 Telegram commands against TUI actions rather than each surface
  growing its own copy of model/profile/mode switching. Files: `internal/commandregistry`,
  `internal/telegrambot/commands.go`, `internal/tui/actions.go`.

### Task interaction warnings

- **P1.5 vs P1.14 directly conflict.** P1.5 deletes `admission_store.go` entirely; P1.14
  optimizes the same file's replay-on-every-update behavior by caching the sequence number in
  memory. Pick one — P1.14 is moot once P1.5 ships.
- **P1.6 and P2.3 are duplicate tasks.** Both delete `docs/architecture.md`'s "Guarded Rules"
  section and fold its facts into the Package Map table. Merge into one task (P2.3's extra
  edit to `architecture_test.go` is the only real delta).
- **`internal/gateway/gateway.go` (1,505 lines) is edited by four milestones** — P1.9 (session
  dissolution), P1.13 (startup replay deferral), P2.2 (route deletion), P2.9 (Resolve split).
  Land these serially with rebases between them, not in parallel worktrees.
- **`docs/architecture.md` is edited by five milestones** — P1.6, P2.3, P1.12, P2.4, P2.7.
  Sequence P2.4 ("replace the markdown-scraped allowlist with a literal Go map") *after*
  P1.6/P2.3 restructure the table, or it scrapes a doc shape that's about to change.
- **P1.10 and P2.8 both touch `internal/tools/web_metadata.go`.** Land P1.10's tooloutput fold
  first so P2.8's key-dedupe isn't editing a file mid-move.

## Global Verification For The Implementation Loop

Run after every milestone, and again before declaring the loop done:

```sh
go build ./...
go vet ./...
go test -count=1 ./...
git diff --check
go test -count=1 ./internal/architecture
./bin/fast-agent-harness doctor -build=false -services=false -gateway=false
```

Notes:

- Rebuild the binary whenever CLI/gateway/TUI/Telegram/provider/tool/agent code
  changes: `go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.
- Several tasks delete packages or fold them into others (P1.8, P1.9, P1.10,
  and the mcpstatus work referenced in the backlog). Each such task must update
  `docs/architecture.md`'s package table in the SAME change so
  `./internal/architecture` stays green — never skip or weaken that test to
  make a merge pass.
- P2.4 changes how the hygiene allowlist is sourced; after it lands, re-run
  `go run ./cmd/fast-agent-harness hygiene -repo . -strict`.
- Docs rule for every task: if public behavior, CLI flags, gateway APIs, config,
  or package ownership changed, update the matching doc in the same change; if
  not, state which docs were checked and why they stayed unchanged.

## Evidence Log

### 2026-07-05 - P0 Milestone 1 complete

- P0.1/P0.2: deleted the docsgen/docguard/agent-index ceremony layer:
  `.codex/hooks/docguard_stop.py`, `.agents/rules/stop-hook-docguard.md`,
  `docs/documentation-system.md`, and all tracked `agent-index/` files. Removed
  the docguard `Stop` hook entry while preserving `.codex/hooks/loop_research_stop.py`.
- P0.1/P0.2 docs cleanup: updated `AGENTS.md`, `llms.txt`,
  `.agents/rules/documentation.md`, `.agents/rules/README.md`, `docs/README.md`,
  `docs/architecture/security-model.md`, `docs/architecture/tui-and-clientux.md`,
  `docs/architecture/gateway-and-sessions.md`, and `docs/research/README.md` so
  live routing no longer names the deleted manifest/hook layer. `004-todo.md`
  already had P1.17/P1.18/P1.20 struck and an evidence entry saying 005 would
  delete this layer, so no additional 004 edit was needed.
- P0.1/P0.2 code cleanup: removed the deleted manifest path from gateway
  context-epoch hashing. The docs-index hash now covers the live lightweight
  orientation files, `llms.txt` and `docs/README.md`, and the gateway drift test
  writes those files instead of `agent-index/docs-manifest.json`.
- P0.3: `checkpoint.Begin` still takes the full pre-image snapshot. `Tracker`
  now keeps Lstat fingerprints for the before snapshot, and `Complete` walks
  with a fingerprint-first path that reuses unchanged `FileState` values and
  calls the full reader only for new or fingerprint-changed paths.
- P0.3 test coverage: added
  `TestCheckpointShellCompleteReadsOnlyChangedFiles`, which creates 2000
  unchanged files plus one modified, one added, and one deleted file; compares
  fast `Complete` output with a full post-snapshot diff; and asserts complete
  full-content reads stay <= 2.
- Verification passed:
  `rg -n "docs-manifest\\.json|reference-plan\\.md|documentation-system\\.md|docguard|agent-index" . --glob '*.md' --glob '*.go' --glob '*.json' --glob '*.py' --glob '*.txt' --glob '!loop-develop/history/**' --glob '!loop-develop/current-todo/004-todo.md' --glob '!loop-develop/current-todo/005-todo.md' --glob '!loop-develop/current-todo/006-todo.md' --glob '!.git/**'`
  returned no matches.
- Verification passed: `go test ./internal/checkpoint/... -v`.
- Verification passed: `go test ./internal/agent/... -run TestToolAttempt -v`
  (no matching tests in current package; command exited 0).
- Verification passed: `go test ./internal/gateway/... -run Undo -v`.
- Verification passed: `go test ./internal/gateway/... -run Redo -v`.
- Verification passed: `go vet ./internal/checkpoint/... ./internal/agent/...`.
- P0 milestone global verification passed:
  `go build ./... && go vet ./... && go test -count=1 ./... && git diff --check && go test -count=1 ./internal/architecture && go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.

### 2026-07-05 - P1 Milestone 2 complete

- P1.1: deleted `ops/production-inventory-2026-07-04.md` and replaced live
  runbook references with current `doctor -json` plus live SSH inspection. The
  004 P0.16 evidence entry now says the dated inventory was later deleted by
  005 P1.1, without linking to a removed file.
- P1.2: deleted the `incident collect` CLI command and bundle implementation.
  Moved the useful sessions-export redaction coverage into
  `cmd/fast-agent-harness/main_test.go` as
  `TestSessionsExportRedactsTranscriptSurfaces`; removed front-door mentions
  from `README.md`, `llms.txt`, `ops/README.md`, and
  `ops/doctor-and-diagnostics.md`.
- P1.3: deleted the one-time MCP config migration scanner and its CLI/test
  surfaces: `internal/config/mcp_migration.go`, `config mcp-migrate`, and the
  MCP migration tests in `cmd/fast-agent-harness/main_test.go` and
  `internal/config/mcp_hooks_config_test.go`.
- P1.4: deleted `internal/config/hygiene_test.go`. Confirmed the model context
  window literals naturally live in `internal/modelinfo/modelinfo.go`; no
  replacement grep guard was added.
- P1.5: deleted `internal/telegrambot/admission_store.go` and removed the
  sidecar `*.admissions.jsonl` writes/read-back assertions. Gateway session
  input admission, Telegram owner/private-chat checks, retryable download
  behavior, pending-state persistence, and startup abandonment completion remain
  intact. `docs/architecture/telegram-and-operator-surfaces.md` now describes
  log-based ignored/admitted/abandoned outcomes instead of a sidecar ledger.
- P1.6: deleted `docs/architecture.md`'s `## Guarded Rules` prose section after
  folding the one unique direct-import fact into the `internal/gateway` and
  `internal/trace` package table rows. The file-size exception table was left
  untouched. Updated `cmd/fast-agent-harness/hygiene_test.go` to stop requiring
  the deleted heading.
- Sequencing note: P1.5 landed, so P1.14 must be skipped when Milestone 4 is
  reached.
- Verification passed:
  `rg -n "production-inventory|incident collect|incidentCmd|incidentCollectCommand|incidentBundleWriter|mcp-migrate|MCPMigrat|mcp_migration|hygiene: allow|telegramAdmissionStore|RecordIgnored|RecordAdmitted|RecordAbandoned|admissions\\.jsonl|admission_store\\.go|Guarded Rules" . --glob '*.go' --glob '*.md' --glob '!loop-develop/history/**' --glob '!loop-develop/current-todo/004-todo.md' --glob '!loop-develop/current-todo/005-todo.md' --glob '!loop-develop/current-todo/006-todo.md' --glob '!.git/**'`
  returned no matches.
- Verification passed: `go build ./... && go test ./cmd/fast-agent-harness/... && go vet ./cmd/fast-agent-harness/...`.
- Verification passed: `go test ./internal/config/... && go vet ./internal/config/...`.
- Verification passed:
  `gofmt -l internal/telegrambot/ && go build ./internal/telegrambot/... && go vet ./internal/telegrambot/... && go test ./internal/telegrambot/... -run TestTelegram -v && go test ./internal/telegrambot/...`.
- Verification passed: `go test ./internal/architecture/... && go vet ./internal/architecture/...`.
- P1 Milestone 2 global verification passed:
  `go build ./... && go vet ./... && go test -count=1 ./... && git diff --check && go test -count=1 ./internal/architecture && go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.

### 2026-07-05 - P1.7 complete

- Deleted the Codex auth/provider FedRAMP plumbing:
  `codexauth.FedRAMPFromClaims`, `codexAuth.FedRAMP`,
  `codexAuthSnapshot.FedRAMP`, PAT whoami metadata parsing for
  `chatgpt_account_is_fedramp`, and the `X-OpenAI-Fedramp` request header.
- Trimmed tests to cover the remaining account-id, expiration, token refresh,
  PAT hydration, env-token precedence, and Codex request-header behavior without
  artificial FedRAMP payloads or assertions.
- Verification passed: `gofmt -w internal/codexauth/codexauth.go internal/codexauth/codexauth_test.go internal/provider/codex_auth.go internal/provider/codex_auth_test.go internal/provider/codex_provider.go internal/provider/codex_provider_test.go && grep -rni fedramp internal/ || true`
  returned no FedRAMP matches.
- Verification passed: `go build ./...`.
- Verification passed: `go vet ./internal/provider/... ./internal/codexauth/...`.
- Verification passed: `go test ./internal/provider/... ./internal/codexauth/... -run Codex -v`.
- Verification passed: `go test ./internal/provider/... ./internal/codexauth/...`.

### 2026-07-05 - P1.8 complete

- Collapsed the shared gateway URL/auth/readiness helpers into
  `internal/gatewayapi/net.go`: bearer-token env lookup, auth-header insertion,
  base URL normalization, readiness polling, unavailable hints, and
  connection-refused retry wrapping now have one owner.
- Deleted `internal/gatewaybase/`, deleted `internal/gateway/ready.go`, removed
  `gateway`/`gatewayclient` forwarding helpers, and moved the old gatewaybase,
  gateway, and gatewayclient helper tests into `internal/gatewayapi/net_test.go`.
  `gateway.RequiresAuthForAddr` stayed in `internal/gateway` because it is
  server-listen policy.
- Updated command, doctor, service, sessions, Telegram, and gatewayclient call
  sites to use `gatewayapi` helpers directly. Updated `docs/architecture.md`
  and `internal/architecture/architecture_test.go` so the enforced package map
  names `gatewayapi` as the transport-helper owner.
- Verification passed: `go build ./...`.
- Verification passed: `go vet ./...`.
- Verification passed:
  `go test ./internal/architecture/... ./internal/gateway/... ./internal/gatewayclient/... ./internal/gatewayapi/... ./internal/telegrambot/...`.
- Verification passed: `go test ./cmd/fast-agent-harness/...`.
- Verification passed: `grep -rn "gatewaybase" --include="*.go" . docs/architecture.md || true`
  returned no matches.

### 2026-07-05 - P1.9 complete

- Dissolved `internal/session`: moved the in-memory run lock into
  `internal/gateway/run_thread.go` as gateway-local `runThread`, with
  `newRunThread`/`newRunThreadWithOptions`; kept `Runner`, `RunnerFunc`,
  `InputPolicy`, `InputDecision`, `Options`, and `ErrBusy` in gateway for local
  tests and run handling.
- Moved transcript import into the CLI as
  `cmd/fast-agent-harness/session_importer.go` and moved importer coverage to
  `cmd/fast-agent-harness/session_importer_test.go`. `sessions import` now
  calls `ImportTranscript`/`ImportOptions`/`ImportFormatAuto` directly.
- Removed all `internal/session` imports and deleted the package. Updated
  `docs/architecture.md` and the command-adapter allowlist in
  `internal/architecture/architecture_test.go` so the deleted package is no
  longer part of the enforced map.
- Verification passed: `go build ./...`.
- Verification passed: `go vet ./...`.
- Verification passed: `go test ./internal/gateway/...`.
- Verification passed: `go test ./cmd/fast-agent-harness/...`.
- Verification passed:
  `grep -rn "internal/session" --include='*.go' . || echo 'no remaining references'`
  printed `no remaining references`.

### 2026-07-05 - P1.10 complete

- Baseline before the move passed: `go build ./...` and
  `go test ./internal/tools/... ./internal/agent/... ./internal/tooloutput/... ./internal/architecture/...`.
- Folded `internal/tooloutput` into `internal/tools/output_ref.go` and renamed
  the public storage API to the tools-owned names: `OutputRef`,
  `StoreOutput`, `OutputStoreRequest`, and `StatOutputRef`. The existing
  metadata constants, `Exists`, `StatMetadata`, `AddMetadataForPath`, and
  `ArtifactMetadata` now live in `internal/tools`.
- Moved output-ref tests to `internal/tools/output_ref_test.go` and used the
  package's existing `assertMode` helper instead of copying a duplicate. Updated
  `internal/tools` call sites to unqualified names and `internal/agent` call
  sites to the already-imported `tools` package.
- Deleted `internal/tooloutput/`. Updated `docs/architecture.md` and output-ref
  architecture prose in `docs/architecture/tools-mcp-and-policy.md`,
  `docs/architecture/security-model.md`, and
  `docs/architecture/runtime-event-system.md` to point at `internal/tools`.
- Verification passed: `go build ./...`.
- Verification passed: `go vet ./internal/tools/... ./internal/agent/...`.
- Verification passed:
  `go test ./internal/tools/... ./internal/agent/... ./internal/architecture/...`.
- Verification passed: `go test ./...`.

### 2026-07-05 - P1.11 complete

- Added `internal/protocol/metadata.go` with the canonical helpers:
  `EventCallID`, `MetadataString`, `MetadataInt64`, `MetadataBool`,
  `DecodeTodoState`, and `TodoState.Recount`.
- Replaced duplicate `eventCallID` helpers in `internal/tui`,
  `internal/tui/transcript`, and `internal/clientux/projector` with
  `protocol.EventCallID`.
- Replaced duplicate metadata decoders in `internal/toolrender`,
  `internal/clientux/context`, and `internal/clientux/projector` with
  `protocol.Metadata*`. `toolrender.TodoStateFromMetadata` remains as a thin
  wrapper over `protocol.DecodeTodoState`, and todo summaries now use
  `TodoState.Recount`.
- Confirmed relevant metadata producers before merging: compaction metadata keys
  (`compaction_id`, `summary_strategy`, hashes) are emitted as strings, and
  `tool_summary_external_model_used` is emitted as a bool by tools/tests, so
  the broader canonical decoder does not depend on an old narrow coercion.
- Verification passed:
  `rg -n "func eventCallID|\\beventCallID\\(|func metadataString|\\bmetadataString\\(|func metadataInt64|\\bmetadataInt64\\(|func metadataBool|\\bmetadataBool\\(|todoStateFromMetadata|recountTodoState|func protocol\\." internal/toolrender internal/clientux internal/tui internal/protocol --glob '*.go'`
  returned no duplicate target helpers.
- Verification passed: `go build ./...`.
- Verification passed:
  `go test ./internal/protocol/... ./internal/toolrender/... ./internal/clientux/... ./internal/clientux/projector/... ./internal/tui/... ./internal/tui/transcript/...`.
- Verification passed:
  `go vet ./internal/protocol/... ./internal/toolrender/... ./internal/clientux/... ./internal/tui/...`.

### 2026-07-05 - P1.12 complete

- Read both legacy research docs fully before deletion. Their remaining
  architecture-shape, gateway/session, event-log, MCP/tool-policy, TUI/client,
  provider/context, Telegram, and clean-room comparison points were already
  represented in `docs/architecture.md`, `docs/architecture/*.md`, ADRs, or
  this active loop's task list, so no stable rule needed to be rescued first.
- Deleted the stale research lane outright:
  `docs/codex-research-roadmap.md`,
  `docs/competitive-architecture-analysis.md`, and
  `docs/research/README.md`. Removed the `docs/README.md` Research section,
  dropped the `llms.txt` optional research bullets, and replaced root
  `README.md`'s dead research links with the docs index.
- `docs/documentation-system.md`, `agent-index/docs-manifest.json`, and
  `.codex/hooks/docguard_stop.py` were already deleted by P0.1/P0.2 in this
  loop, so the P1.12 checklist bullets for updating those files are satisfied
  by deletion rather than by edits.
- Marked `loop-develop/current-todo/004-todo.md` P1.21 as superseded by this
  deletion pass and rewrote its old evidence so it no longer keeps dangling
  path references to files removed by 005.
- Verification passed: `git diff --check`.
- Verification passed:
  `rg -n "codex-research-roadmap|competitive-architecture-analysis|docs/research/README" --glob '!loop-develop/history/**' --glob '!loop-develop/current-todo/005-todo.md' .`
  returned no matches. The active 005 TODO was excluded because it is the source
  checklist and evidence for this deletion.
- Verification skipped as moot after P0 deletion:
  `jq . agent-index/docs-manifest.json >/dev/null` and
  `python3 -m py_compile .codex/hooks/docguard_stop.py`.
- Verification passed: `go test -count=1 ./internal/architecture/...`.
- Verification passed:
  `go build -buildvcs=false -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.
- P1 Milestone 3 global verification passed:
  `go build ./... && go vet ./... && go test -count=1 ./... && git diff --check && go test -count=1 ./internal/architecture && go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.

### 2026-07-05 - P1.13 complete

- Extended `sessionManifest` with the status/listing fields needed by
  `GET /v1/sessions`: provider/model/profile/reasoning/access mode, run seq,
  last event/time/error, model/tool call counts, dropped events, and
  attachment/image counts. `AppendEvent` and `saveLocked` refresh those fields
  whenever durable event/session state changes.
- Changed gateway startup to load session directories as manifest-only stubs.
  Startup and list no longer replay `history.jsonl` or `events.jsonl`; full
  materialization happens only through the `s.session` choke point used by
  per-session routes.
- Preserved startup input-ledger reconciliation by running only the cheap
  `inputs.jsonl` ambiguity pass during manifest-only load.
- Removed the per-save `replaySessionHistory` cost by using the manifest's
  `HistorySeq`/`HistorySHA256` as the append cursor/hash cache and appending a
  new history snapshot only when the current message hash differs.
- Added tests proving restart leaves sessions as manifest-only stubs, list
  summaries come from manifests, direct `GET /v1/sessions/{id}` lazy-loads the
  full transcript, corrupt history is deferred until that specific session is
  materialized, and lazy-load errors are sanitized.
- Added `BenchmarkGatewaySessionJSONLReplayStartupListManifestOnly`, which the
  existing benchmark regex picks up. On this run, startup/list over five
  sessions took about `281542 ns/op` with 1k events per session and
  `337875 ns/op` with 10k events per session, while allocations stayed flat.
- Updated `docs/architecture/gateway-and-sessions.md` for manifest-only
  startup/lazy materialization and corrected stale `gatewaybase` /
  `internal/session` references.
- Verification passed:
  `go test -count=1 ./internal/gateway/... -run TestSessionStore -v`.
- Verification passed:
  `go test -count=1 ./internal/gateway/... -run TestGatewayReadiness -v`.
- Verification passed: `go test -count=1 ./internal/gateway/...`.
- Verification passed:
  `go test ./internal/gateway/... -bench BenchmarkGatewaySessionJSONLReplay -run '^$' -benchtime=1x`.
- Verification passed: `go build ./...`.
- Verification passed:
  `git diff --check -- docs/architecture/gateway-and-sessions.md internal/gateway/gateway.go internal/gateway/session_authz.go internal/gateway/session_events.go internal/gateway/session_store.go internal/gateway/session_store_test.go internal/gateway/session_store_benchmark_test.go`.

### 2026-07-05 - P1.14 skipped

- P1.14 is moot because P1.5 landed earlier in this loop and deleted
  `internal/telegrambot/admission_store.go` plus the admission-store tests and
  call sites. There is no `telegramAdmissionStore.append()` path left to cache.
- Skip verification: `rg -n "telegramAdmissionStore|admission_store\\.go|admissions\\.jsonl" internal/telegrambot`
  was already covered by the P1.5/P1 Milestone 2 grep with no remaining live
  matches outside the active TODO evidence.

### 2026-07-05 - P1.15 complete

- Added transcript find state to the TUI model (`findQuery`, `findMatches`,
  `findMatchIndex`) and implemented case-insensitive `/find [query]` matching
  against the unhighlighted `viewportContent`.
- Reapplies active find matches after every `Model.reflow`, because
  `viewport.SetContent` clears viewport highlights during streaming/re-render.
- Added `/find`, `ctrl+f`, and `alt+f` action wiring. `ctrl+f` opens the
  palette as `/find ` when no query exists, otherwise advances to the next
  match; `alt+f` moves to the previous match. Bare letter keys remain untouched.
- Added `transcript.find` to the raw-argument slash-command exceptions so query
  case is preserved instead of lowercased.
- Added `internal/tui/find_test.go` covering multi-match navigation, no-match
  status, and active highlight reapplication after a live assistant delta and
  reflow.
- Updated `docs/architecture/tui-and-clientux.md` to record transcript find as
  a viewport/reflow behavior.
- Verification passed:
  `go build ./internal/tui/... ./internal/clientux/...`.
- Verification passed: `go test ./internal/tui/... -run TestFind -v`.
- Verification passed:
  `go test ./internal/tui/... -run TestGoldenStatusTraceMatchesProjectorTelegramTUIAndContext`.
- Verification passed: `go vet ./internal/tui/...`.
- Verification passed:
  `git diff --check -- internal/tui/tui.go internal/tui/transcript_runtime.go internal/tui/actions.go internal/tui/commands.go internal/tui/find_test.go docs/architecture/tui-and-clientux.md`.

### 2026-07-05 - P1.16 complete

- Added saved-chat fallback search for `/resume` and `/fork`: exact/prefix ID
  matching is still tried first and ambiguous ID prefixes still error; only
  zero ID matches fall back to case-insensitive title/message/part text search.
- Added `sessionMatch`, `matchByIDPrefix`, `resolveChatSession`,
  `searchChatSessions`, `snippetAround`, and `formatSessionMatches` helpers.
  Multi-match text queries now render a `CHATS` info block with snippets and set
  status to `N chats match <query>` instead of returning a bare not-found error.
- Updated `/resume` and `/fork` slash argument hints to `[id-prefix|text]` in
  both TUI action wiring and shared `clientux` action metadata.
- Added `internal/tui/sessions_search_test.go` for message-body-only resolution
  and multiple snippet matches.
- Updated `docs/architecture/tui-and-clientux.md` to record the saved-session
  lookup contract.
- Verification passed: `go build ./...`.
- Verification passed: `go vet ./internal/tui/...`.
- Verification passed:
  `go test ./internal/tui/... -run TestResumeChatRestoresProjectedUsageSnapshot -v`.
- Verification passed:
  `go test ./internal/tui/... -run TestSearchChatSessions -v`.
- Verification passed:
  `go test ./internal/tui/... -run TestResolveChatSession -v`.
- Verification passed: `go test ./internal/tui/...`.
- Verification passed:
  `git diff --check -- internal/tui/sessions.go internal/tui/actions.go internal/clientux/actions.go internal/tui/sessions_search_test.go docs/architecture/tui-and-clientux.md`.

### 2026-07-05 - P1.17 complete

- Added Telegram Bot API fields/types for `voice`, `audio`, `video_note`, and
  `video` messages.
- Added unsupported-media detection so voice/audio/video-note/video-only
  messages are processable enough to avoid the silent `empty_message` ack path.
- Added unsupported-media durable rejection helpers. After allowlist and command
  routing, the poller replies with "not supported yet -- send text or an image",
  logs/acks a reason such as `voice_unsupported`, and does not admit a gateway
  input.
- Preserved allowlist behavior: non-allowlisted unsupported media still reaches
  the existing not-allowlisted reply path before media rejection.
- Added `TestTelegramVoiceOnlyRepliesAndAcks` and a `telegramVoiceUpdate`
  fixture. Because P1.5 removed the admission log, this regression verifies the
  remaining durable behavior: user reply, no gateway admission, and offset
  advancement.
- Updated `docs/architecture/telegram-and-operator-surfaces.md` for log-based
  ignored outcomes, unsupported voice/audio/video behavior, and the
  `gatewayapi/net.go` helper owner.
- Verification passed: `go build ./internal/telegrambot/...`.
- Verification passed: `go test ./internal/telegrambot/... -run 'Telegram' -v`.
- Verification passed: `go test ./internal/telegrambot/...`.
- Verification passed: `go vet ./internal/telegrambot/...`.
- Verification passed:
  `git diff --check -- internal/telegrambot/types.go internal/telegrambot/media.go internal/telegrambot/poller.go internal/telegrambot/poller_test.go docs/architecture/telegram-and-operator-surfaces.md`.

### 2026-07-05 - P1.18 complete

- Kept notification ownership on the Telegram side. No `internal/tools`
  changes, no `notify_owner` tool, and no Telegram secret plumbing into the
  gateway process.
- Added typed `GatewayClient.ProcessSnapshot(ctx)` returning
  `gatewayapi.ManagedProcessResponse`; the existing `/processes` command still
  formats the same response through `ProcessStatus`.
- Added `internal/telegrambot/process_watch.go`: a guarded poller that runs
  only when live send is enabled, dry-run is off, a positive watch interval is
  configured, recipients exist, and the harness supports typed process
  snapshots. It tracks process IDs in memory and sends once for running->exited
  or first-seen-exited states.
- Added `-process-watch-interval-sec` plus
  `BILLYHARNESS_TELEGRAM_PROCESS_WATCH_INTERVAL_SEC` /
  `TELEGRAM_PROCESS_WATCH_INTERVAL_SEC`; default is `10`, and `0` disables the
  watcher.
- Process-finished notifications include process id, exit status/error, elapsed
  time, and `OutputTailPreview`, routed through Telegram redaction before send.
  Recipients are drawn from configured allowed chat IDs plus operator user IDs.
- Added `TestManagedProcessWatchSendsFinishedProcessOnce`, covering
  running-then-exited diffing, single-send behavior, chat routing, and redaction.
- Updated `docs/architecture/telegram-and-operator-surfaces.md` for the new
  outbound-without-inbound-trigger process notification pattern.
- Verification passed: `go build ./...`.
- Verification passed: `go test -count=1 ./internal/telegrambot/...`.
- Verification passed: `go test -count=1 ./internal/architecture`.
- Verification note: `go test -count=1 ./internal/gateway -run TestManagedProcesses`
  passed but matched no current tests; the live managed-process endpoint test is
  `TestGatewayManagedProcessesEndpointUsesSharedRegistry`.
- Verification passed:
  `go test -count=1 ./internal/gateway -run TestGatewayManagedProcessesEndpointUsesSharedRegistry -v`.
- Verification passed: `go vet ./internal/telegrambot/...`.
- Verification passed:
  `git diff --check -- internal/telegrambot/gateway_client.go internal/telegrambot/bot.go internal/telegrambot/poller.go internal/telegrambot/process_watch.go internal/telegrambot/process_watch_test.go cmd/fast-agent-harness/service_cmd.go docs/architecture/telegram-and-operator-surfaces.md`.

### 2026-07-05 - P1.19 implementation complete; live doctor pending final hygiene/runtime pass

- Added `Store.Usage()` and `Store.Prune(maxAge, maxTotalBytes)` in
  `internal/attachments/store.go`. The prune pass walks regular files under
  the store root, skips `.tmp-attachment-*` write temp files, removes files
  older than the age bound first, then evicts oldest remaining files until the
  byte budget is met.
- Added `TestPruneRemovesOldFiles` and `TestPruneEnforcesMaxBytesOldestFirst`.
  Both tests also assert kept files and post-prune usage.
- Added `cmd/fast-agent-harness/attachments_cmd.go` and routed
  `attachments gc [-max-age=720h] [-max-bytes=1073741824] [-dry-run]` from
  `main.go`.
- Added `runtime.attachments_store` to doctor, a text-summary
  `attachments=` field, and a warning-only `attachments store usage` check at
  the same `1 GiB` default budget. Added a doctor unit test proving the warning
  does not make strict mode fail.
- Updated `ops/doctor-and-diagnostics.md` with the `attachments gc` command,
  default age/size policy, doctor JSON field, and strict-mode warning behavior.
- Verification passed: `go build ./...`.
- Verification passed:
  `go test ./internal/attachments/... -run TestPrune -v`.
- Verification passed: `go test ./internal/attachments/...`.
- Verification passed:
  `go test ./cmd/fast-agent-harness/... -run TestDoctor -v`.
- Verification passed:
  `go vet ./internal/attachments/... ./cmd/fast-agent-harness/...`.
- Verification failed, environment/hygiene gated:
  `go run ./cmd/fast-agent-harness doctor -strict` exited with
  `doctor found failing checks` because this local runtime has missing
  DeepSeek credentials, missing MCP allowlist servers, no local gateway on
  `127.0.0.1:8765`, and strict hygiene currently reports deleted tracked files
  plus two oversized files. Revisit after final hygiene/index cleanup; do not
  treat attachments usage as the cause (`attachments store usage` was `ok`).
- Verification passed:
  `go run ./cmd/fast-agent-harness attachments gc -dry-run` reported
  `/Users/billy/billyharness/attachments` with `0 file(s), 0 B`.

### 2026-07-05 - P1.20 complete

- Added `managedServer.commitLocked(catalogChanged)` for direct in-lock MCP
  status/catalog mutations. It clones status, unlocks `s.mu`, publishes status,
  and conditionally publishes catalog change; callers must not touch the lock
  after calling it.
- Added `managedServer.publishAbsorbed(status, statusChanged, catalogChanged)`
  for `absorbClientLocked` paths where status publication remains conditional.
- Replaced duplicated clone/unlock/publish blocks across `start`, `startLocked`,
  `callToolResult`, `markCatalogStale`, `refreshCatalog`, `snapshot`, `close`,
  and `recordStaticErrorState`; left the listTools-error branch as a simple
  status-only publish because it does not imply a catalog change.
- Added `TestManagedServerStartEarlyReturnPublishesStatus`, covering the
  non-reconnectable early-return failure branch with both status and catalog
  listener signals.
- No user-facing docs changed for MCP behavior because this is an internal
  lifecycle-publish refactor. The architecture docs were updated only for the
  P1.19 command adapter exception discovered by the milestone gate.
- Verification passed:
  `go test ./internal/mcpclient/... -run TestManagedServerStartEarlyReturnPublishesStatus -v`.
- Verification passed: `go build ./...`.
- Verification passed: `go vet ./internal/mcpclient/...`.
- Verification passed:
  `go test ./internal/mcpclient/... -run TestManagerStatusListenersObserveLifecycleChanges -v`.
- Verification passed:
  `go test ./internal/mcpclient/... -run TestMCPStdioReconnectRefreshesCatalogAndEmitsChange -v`.
- Verification passed:
  `go test ./internal/mcpclient/... -run TestMCPStdioToolsListChangedNotificationRefreshesCatalog -v`.
- Verification passed: `go test ./internal/mcpclient/... -v`.

### 2026-07-05 - P1 Milestone 4 global verification complete

- First milestone global run failed in `internal/architecture` because the new
  `attachments gc` CLI adapter imported `internal/attachments` directly.
- Reviewed that boundary as a command-front-door exception, added
  `attachments` to `TestCommandPackagesRemainAdapters`, and updated
  `docs/architecture.md` to include CLI-surfaced store usage/prune operations
  in `internal/attachments`'s responsibility.
- Verification passed:
  `go build ./... && go vet ./... && go test -count=1 ./... && git diff --check && go test -count=1 ./internal/architecture && go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.

### 2026-07-05 - P2.1 complete

- Deleted the unused `importMetadata` sidecar struct and stopped writing
  `billyharness.skill.json` during skill import. `ImportResult.SHA256` still
  comes from `fileSHA256(skill.Path)`.
- Renamed the import test to `TestImportCopiesSelectedCompatibilitySkill` and
  assert source/hash directly from the returned `ImportResult`.
- Removed the `skill_import` tool test's sidecar `os.Stat` check; source and
  destination remain asserted through the tool response.
- Verification passed: `go build ./internal/skills/...`.
- Verification passed: `go test ./internal/skills/... -run TestImport -v`.
- Verification passed: `go test ./internal/tools/... -run TestSkill -v`.
- Verification passed: `go vet ./internal/skills/... ./internal/tools/...`.
- Verification passed with no matches:
  `grep -rn "billyharness.skill.json\\|importMetadata" --include='*.go' .`.

### 2026-07-05 - P2.2 complete

- Removed the dead `GET /v1/benchmarks` route from `Server.routes` while
  leaving `GET /v1/tools` intact.
- Deleted `internal/gateway/benchmark_routes.go`, benchmark response aliases,
  `gatewayapi` benchmark DTOs, and the endpoint fixture test.
- Restored only the still-live `fileExists` helper in
  `internal/gateway/file_util.go` because session inspection uses it.
- Updated `docs/architecture/gateway-and-sessions.md` to remove the
  `/v1/benchmarks` route row.
- Verification passed: `go build ./...`.
- Verification passed: `go vet ./...`.
- Verification passed: `go test ./internal/gateway/...`.
- Verification passed: `go test ./internal/gatewayapi/...`.
- Verification passed with no Go matches:
  `grep -rn 'v1/benchmarks\\|BenchmarkListResponse\\|BenchmarkRunSummary\\|handleBenchmarks' --include='*.go' .`.
- Full-tree grep only found task text in this TODO and untracked
  `loop-develop/current-todo/006-todo.md`; excluding `loop-develop/**` returned
  no matches.

### 2026-07-05 - P2.3 skipped as already landed by P1.6

- `docs/architecture.md` no longer has a `## Guarded Rules` section.
- The package table already carries the sharper direct-`eventlog` gateway/trace
  note and the TUI `runtimeclient` boundary note.
- Verification passed: `go test -count=1 ./internal/architecture/...`.
- Verification passed: `grep -n '## Guarded Rules' docs/architecture.md`
  returned no matches.

### 2026-07-05 - P2.4 complete

- Replaced hygiene's markdown-scraped allowlist with literal
  `hygieneLargeFileExceptions` in `cmd/fast-agent-harness/hygiene.go`.
- Rewrote the hygiene test to assert literal exceptions and added
  `TestHygieneExceptionsIncludeKnownLargeFiles`.
- Simplified `docs/architecture.md`'s file-size section to a human-readable
  2-column table that points to `hygieneLargeFileExceptions` as the enforced
  source of truth.
- Split the new TUI find helpers into `internal/tui/transcript_find.go`, taking
  `internal/tui/transcript_runtime.go` down to 1446 LOC.
- Split the MCP tool-test fake stdio server into
  `internal/tools/mcp_fake_test.go`, taking `internal/tools/mcp_test.go` down
  to 1180 LOC.
- Verification passed: `go build ./cmd/fast-agent-harness/...`.
- Verification passed: `go test ./cmd/fast-agent-harness/...`.
- Verification passed:
  `go run ./cmd/fast-agent-harness hygiene -repo . -strict` after staging the
  intended deleted Go files; strict hygiene reports no large source files and
  only the historical allowed large test files.

### 2026-07-05 - P2.5 complete

- Confirmed with `git log --diff-filter=A` and `--diff-filter=D` that
  `docs/adr/0004-*` and `0005-*` were never added or deleted.
- Added the 0004/0005 gap note and next-number note to `docs/adr/README.md`
  and `docs/README.md`.
- Removed default `Status`, `Supersedes`, and `Superseded by` template fields
  from the ADR README, leaving existing ADR files untouched.
- Updated the supersede rule to add Status/Supersedes notes only when the first
  real supersede happens.
- Verification passed:
  `git diff --stat docs/adr/README.md docs/README.md`.
- Verification passed:
  `grep -n 'Status:\\|Supersedes:\\|Superseded by:' docs/adr/README.md`
  returned no matches.
- Verification passed:
  `grep -n '0004\\|0005\\|0009' docs/adr/README.md`.
- Verification passed: `go build ./...`.

### 2026-07-05 - P2.6 complete

- Added a short AGENTS.md loop-development guardrail: split large
  multi-milestone TODOs into smaller numbered slices, and only keep external
  framework/compliance checklist items when they name the concrete
  Billy-facing failure they fix.
- Did not edit `loop-develop/current-todo/004-todo.md`.
- Verification passed: `git diff --check`.
- Verification passed: `go run ./cmd/fast-agent-harness hygiene -repo . -strict`.
- Verification passed: `go build ./...`.

### 2026-07-05 - P2 Milestone 5 global verification complete

- First milestone global run stopped on stale imports left after the MCP test
  helper split; removed unused `bufio` and `fmt` from
  `internal/tools/mcp_test.go`.
- Verification passed:
  `go build ./... && go vet ./... && go test -count=1 ./... && git diff --check && go test -count=1 ./internal/architecture && go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.

### 2026-07-05 - P2.7 complete

- Collapsed `llms.txt`'s duplicated Architecture Canon list to one pointer to
  `docs/README.md`, leaving the README as the sole enumerated architecture
  index.
- Kept the existing `docs/README.md` architecture list intact and did not add a
  second copy anywhere else.
- Verification passed:
  `grep -n 'Architecture Canon' -A 5 llms.txt` showed only the docs-index
  pointer.
- Verification passed: `grep -c 'architecture/' llms.txt` returned `0`
  matches.
- Verification passed:
  `diff <(grep -oE 'architecture/[a-z-]+\\.md' docs/README.md | sort -u) <(grep -oE 'architecture/[a-z-]+\\.md' llms.txt | sort -u) || true`
  showed the expected one-way diff because `llms.txt` no longer enumerates
  architecture docs.
- Verification passed:
  `rg -n 'tools-mcp-and-policy.md' llms.txt docs/README.md docs/architecture/security-model.md`
  found the route only in `docs/README.md` and
  `docs/architecture/security-model.md`, not `llms.txt`.

### 2026-07-05 - P2.8 complete

- Removed duplicate compact-web telemetry keys from user-facing web metadata:
  `websum_input_tokens`, `websum_output_tokens`, `websum_cost`,
  `websum_cache_hit`, `websum_cache_miss`, plus per-page timing keys such as
  `web_cache_lookup_ms` and `web_total_ms`.
- Kept the underlying instrumentation fields in `internal/tools/web_compact.go`
  so internal debugging and tests can still inspect timings directly.
- Updated the web metadata test to assert reset behavior for instrumentation
  fields instead of publishing every timing as model-facing metadata.
- A representative removed-key JSON sample measured `261` bytes saved.
- Verification passed:
  `gofmt -w internal/tools/web_metadata.go internal/tools/web_test.go`.
- Verification passed:
  `go build ./... && go test ./internal/tools/... -v && go test ./internal/tui/... ./internal/telegrambot/... ./internal/clientux/... && go vet ./internal/tools/... ./internal/agent/...`.
- Verification passed with no source matches:
  `grep -rn "websum_input_tokens\\|websum_cache_hit\\|web_cache_lookup_ms\\|web_total_ms" internal/tui internal/telegrambot internal/clientux || true`.

### 2026-07-05 - P2.9 complete

- Split config resolution into `loadResolveState()` plus
  `resolveState.resolve(...)`, so one loaded config state can produce both the
  base and runtime-effective views without re-reading config files.
- Added `ResolveEffectiveFromBase(buildOverrides)` and used it from the
  gateway `/v1/config` route and TUI config summary path. Runtime-diff overrides
  are now derived from the finalized base config, not from a second resolver
  pass.
- Added `TestResolveEffectiveKeepsBaseAndOverridesIndependent` to prove base
  and effective metadata stay independent when overrides are applied.
- Local `/v1/config` probe with live gateway runtime overrides showed the base
  view and effective view remain separate, and the effective view reports the
  running gateway/tui runtime override values. A first probe without the dev
  mutation flag correctly refused to start without gateway auth protection.
- Verification passed:
  `gofmt -w internal/config/resolved.go internal/config/config_test.go internal/gateway/status_routes.go internal/tui/tui.go`.
- Verification passed:
  `go build ./... && go test ./internal/config/... -run Resolve -v && go test ./internal/config/... ./internal/gateway/... ./internal/tui/... && go vet ./internal/config/... ./internal/gateway/... ./internal/tui/...`.

### 2026-07-05 - P2.10 complete

- Deleted `debugStatusText()` and routed `/status debug` through the existing
  `debugFullText()` output path.
- Updated the shared status action metadata with `[debug]` slash arguments so
  help/discovery exposes the optional debug form.
- Verification passed:
  `gofmt -w internal/tui/transcript_runtime.go internal/tui/actions.go internal/clientux/actions.go`.
- Verification passed:
  `go build ./internal/tui/... && go vet ./internal/tui/... && go test ./internal/tui/... -run TestInteractionSelection -v && go test ./internal/tui/...`.
- Verification passed with no source matches:
  `rg -n 'debugStatusText' internal cmd docs ops .agents AGENTS.md llms.txt README.md || true`.

### 2026-07-05 - P2.11 complete

- Added a cached TUI reflow fast path keyed by transcript dimensions,
  visible-block keys, and compact view state. The fast path reuses existing
  rendered block strings when nothing relevant changed, when only the last
  visible block changed, or when one visible block was appended.
- Left fallback full reflow behavior intact for structural changes, selection
  shape changes, or key-order changes.
- Baseline benchmark:
  `BenchmarkTUIReflowLongTranscriptCached-15 200 9134447 ns/op 25526897 B/op 5124 allocs/op`.
- Verification passed:
  `gofmt -w internal/tui/tui.go internal/tui/transcript_runtime.go`.
- Verification passed:
  `go build ./internal/tui/... && go test ./internal/tui/... -run TestReflow -v && go vet ./internal/tui/... && go test ./internal/tui/...`.
- Post-change benchmark:
  `BenchmarkTUIReflowLongTranscriptCached-15 200 5358125 ns/op 911191 B/op 2705 allocs/op`.

### 2026-07-05 - P2.12 complete

- Replaced Telegram `/mode`'s local alias switch with `config.ParseAccessMode`,
  so `safe` and future shared aliases follow the same parser as the rest of the
  harness.
- Extended the Telegram mode-flow test to cover `/mode safe` resolving to
  guarded mode and an unknown mode leaving the current mode unchanged.
- Verification passed:
  `gofmt -w internal/telegrambot/commands.go internal/telegrambot/commands_flow_test.go`.
- Verification passed:
  `go build ./internal/telegrambot/... ./internal/config/... && go test ./internal/telegrambot/... ./internal/config/... -v && go vet ./internal/telegrambot/...`.
- Verification passed:
  `go test ./internal/telegrambot/... -run TestTelegramModeCommandSetsPlanModeRunRequest`.

### 2026-07-05 - P2 Milestone 6 global verification complete

- Verification passed:
  `go build ./... && go vet ./... && go test -count=1 ./... && git diff --check && go test -count=1 ./internal/architecture && go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.

### 2026-07-05 - P2.13 complete

- Split Telegram status HTML into the short default
  `StatusHTMLWithRuntime(...)` view and `StatusDebugHTMLWithRuntime(...)`.
  The default view keeps session/model/profile/mode/reasoning/context/send
  fields and omits allowlist IDs, event cursor, pending input, and counters.
- Kept the old full renderer as `statusHTMLFull(...)` with the original line
  order for internal/debug reuse.
- `/status` now accepts `debug`; unknown status views reply with plain text and
  no HTML status body.
- Added command-level tests proving `/status debug` includes allowed chats,
  allowed users, and event cursor only in the debug reply.
- Verification passed:
  `gofmt -w internal/telegrambot/status_html.go internal/telegrambot/commands.go internal/clientux/actions.go internal/telegrambot/commands_flow_test.go`.
- Verification passed:
  `go build ./... && go test ./internal/telegrambot/... -run TestStatusHTML -v && go test ./internal/telegrambot/... -run TestTelegramStatusCommand -v && go test ./internal/telegrambot/... && go vet ./internal/telegrambot/...`.

### 2026-07-05 - P2.14 complete

- Added Telegram `/models` to the existing shared `models.list` action instead
  of creating a new command surface.
- Wired `telegramCommands()` to `handleModelsCommand`, which lists
  `modelinfo.Providers()` catalog models as `<model> (<provider>,
  <vision-capable|text-only>)`.
- The currently selected chat model is marked with `*`; if the chat has no
  model state, the configured option model is marked instead.
- Added a Telegram flow test covering catalog output, current-marker behavior,
  and the no-chat-model-state fallback.
- Verification passed:
  `go build ./internal/clientux/... ./internal/telegrambot/... ./internal/modelinfo/... && go test ./internal/telegrambot/... -run TestTelegram -v && go test ./internal/clientux/... ./internal/modelinfo/... -v && go vet ./internal/telegrambot/... ./internal/clientux/...`.

### 2026-07-05 - P2.15 complete

- Added `scripts/deploy.sh` for SHA-named binary builds and atomic
  `bin/fast-agent-harness-current` symlink updates.
- Added `scripts/rollback.sh` to restore `bin/.previous-release` through the
  same verification helper.
- Added `scripts/lib/deploy-verify.sh` for the shared restart plus
  `doctor -mode=production`, `/health`, and `/ready` gate.
- `deploy.sh` records the previous symlink target before repointing, restores
  it on verification failure, appends verified SHAs to `bin/.release-history`,
  and prunes binaries outside the last
  `${BILLYHARNESS_DEPLOY_KEEP_RELEASES:-5}` entries.
- Updated `ops/production-services.md` and `README.md` so the symlink deploy
  lane is the primary path when host units point at
  `bin/fast-agent-harness-current`; kept `scripts/production-deploy.sh` as the
  older source-checkout fallback.
- Verification note: initial `shellcheck scripts/deploy.sh scripts/rollback.sh
  scripts/lib/deploy-verify.sh` failed because `shellcheck` was not installed
  on this Mac. Installed `shellcheck` with Homebrew and reran the required
  check.
- Verification note: the first shellcheck rerun flagged the inherited
  `CDPATH= cd` idiom; replaced it with `CDPATH='' cd` in the new scripts.
- Verification passed:
  `shellcheck scripts/deploy.sh scripts/rollback.sh scripts/lib/deploy-verify.sh && go build -buildvcs=false -o /tmp/billyharness-verify/fast-agent-harness ./cmd/fast-agent-harness && go test -count=1 ./cmd/fast-agent-harness/... && git diff --check && scripts/verify-deps.sh`.

### 2026-07-05 - P2 Milestone 7 global verification complete

- Verification passed:
  `go build ./... && go vet ./... && go test -count=1 ./... && git diff --check && go test -count=1 ./internal/architecture && go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.

### 2026-07-05 - Final loop verification complete

- Verification passed:
  `go build ./... && go vet ./... && go test -count=1 ./... && git diff --check && go test -count=1 ./internal/architecture && go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`.

## History Archive Note

Archived: 2026-07-05

Final status: complete and verified. Main-chat verification found the final
loop verification evidence and milestone completion evidence through P2.15.
This file is moved out of current so the next active goal can start from
`006-todo.md`.

Residual risk kept from the implementation evidence: deployment scripts and
local verification passed, but live production deploy/rollback execution should
still be run deliberately when Billy asks for a production rollout.
