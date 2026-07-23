# 008 — Durable JSONL job store, replay, recovery, and artifacts

## Goal

Persist the provider-neutral job state machine from slice 007 so a gateway
process can survive restarts without tying job lifetime to one HTTP request or
model call. JSONL is the canonical source of truth; snapshots are rebuildable
caches. This slice implements storage and recovery only—no scheduler, provider
execution, gateway route, or CLI command yet.

## Source research summary

The twelve native Codex research tracks converged on these P0 rules:

- a durable Job is a separate resource from an interactive Session;
- append-only events, not an in-memory goroutine, define job truth;
- the current solo/single-gateway architecture calls for JSONL rather than a
  second database;
- a snapshot may accelerate reads but must always be reproducible from
  `spec.json` plus `events.jsonl`;
- an incomplete final JSONL record can be truncated after a crash, while
  malformed records, sequence gaps, checksum failures, and corruption before
  the tail must fail closed;
- compare-and-append must reject stale revisions so duplicate coordinators
  cannot silently fork state;
- private directory/file modes, path containment, bounded record/artifact
  sizes, content hashes, and one process-owner lock are part of correctness;
- recovery guarantees durable orchestration and safe at-least-once read work;
  a crashed non-idempotent writer remains ambiguous for the later scheduler to
  block or reconcile.

## Scope

Create `internal/jobstore` with a provider-neutral storage boundary and a
filesystem implementation rooted at an explicit jobs directory:

```text
<root>/<job-id>/
  spec.json
  events.jsonl
  snapshot.json
  artifacts/
```

### Store contract

- Create a validated job exactly once with an initial queued state.
- Append one typed `jobs.Event` with expected-revision compare-and-swap,
  validate it through `jobs.Reduce`, fsync the canonical JSONL record, then
  atomically refresh the snapshot.
- Load and replay a job deterministically from canonical files.
- List compact job summaries in stable order.
- Rebuild or ignore a missing/stale/invalid snapshot without changing event
  truth.
- Return typed not-found, already-exists, conflict, corruption, closed, and
  ownership errors suitable for future API mapping.

### On-disk integrity and recovery

- Version every spec, event record, and snapshot envelope.
- Store monotonically contiguous event sequence/revision metadata and a
  SHA-256 hash chain over canonical records.
- Reject job/spec identity changes, unsupported versions, sequence gaps,
  revision mismatches, invalid reducer transitions, hash mismatch, and
  mid-log/trailing-newline corruption.
- Recover only a non-empty unterminated final record by truncating to the last
  complete newline; never guess through completed-line corruption.
- Keep JSONL canonical: snapshot deletion/corruption must be repairable by
  replay and snapshot rewrite.
- Serialize access within a process and hold an exclusive root ownership lock
  until `Close`.

### Artifacts

- Write artifacts through a bounded streaming API to a temporary file, fsync,
  SHA-256 hash, and atomic rename.
- Use portable validated IDs and generated storage paths; never trust a caller
  path or URI.
- Refuse overwrite and path traversal; expose verified reads/metadata.

## Checklist

- [x] Add the storage interface, typed errors, envelopes, and stable summaries.
- [x] Add exclusive store ownership and idempotent `Close`.
- [x] Add create/load/list with private permissions and path containment.
- [x] Add compare-and-append, fsync, reducer validation, and atomic snapshots.
- [x] Add versioned records with contiguous sequence/revision and hash-chain
      verification.
- [x] Add deterministic replay and snapshot rebuild.
- [x] Recover only an unterminated partial tail; fail closed on all completed
      corrupt records.
- [x] Add bounded atomic artifact writes, hashes, no-overwrite semantics, and
      verified reads.
- [x] Add adversarial tests for duplicate creation, stale writers, traversal,
      invalid versions, gaps, corruption, partial tails, stale snapshots,
      permissions, artifact tampering, and concurrent append attempts.
- [x] Add `internal/jobstore` to architecture/package documentation.
- [x] Run focused tests, race tests, full relevant tests, docs generation check,
      and `git diff --check`.
- [x] Create a scoped commit and push without including unrelated pre-existing
      worktree changes.

## Completion evidence

- Added a marked, single-owner Darwin/Linux `FileStore`; other platforms
  compile but fail closed until equivalent owner-only filesystem controls are
  implemented.
- JSONL events are canonical, spec-anchored, hash-chained, CAS-appended, and
  replayed through `jobs.Reduce`. Only an unterminated final record is repaired.
- Snapshots are bounded, disposable, and non-fatal; large valid canonical logs
  remain loadable even when a snapshot is skipped.
- Artifact publication is bounded/no-replace and reads return a private,
  content-verified snapshot rather than a mutable source descriptor.
- Added dedicated-root markers, destructive-root guards, no-follow final-file
  opens, Unix hard-link rejection, crash staging cleanup, and typed committed
  outcomes for post-publication durability warnings.
- The remaining filesystem security invariant is explicit: runtime worker
  authority must never grant writes to the store root or its ancestors. The
  scheduler/runtime slice must enforce this boundary.
- Verification passed on 2026-07-23:

```text
go test -count=1 ./internal/jobs ./internal/jobstore ./internal/architecture ./internal/docsgen
go test -race -count=1 ./internal/jobstore
go vet ./internal/jobstore
GOOS=linux GOARCH=amd64 go test -c ./internal/jobstore
GOOS=windows GOARCH=amd64 go test -c ./internal/jobstore
GOOS=freebsd GOARCH=amd64 go test -c ./internal/jobstore
go run ./cmd/fast-agent-harness docsgen -check
go mod tidy -diff
git diff --check
```

## Verification

```sh
go test -count=1 ./internal/jobs ./internal/jobstore
go test -race -count=1 ./internal/jobstore
go test -count=1 ./internal/architecture ./internal/docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

## Copy-ready Codex goal prompt

```text
/goal Implement loop-develop/current-todo/008-todo.md completely. Preserve all
unrelated existing worktree changes. Keep internal/jobs pure; place persistence
in internal/jobstore. Use JSONL as canonical truth, fail closed on completed
corruption, recover only an unterminated tail, enforce CAS revisions and an
exclusive process-owner lock, and make artifacts bounded and content-verified.
Use apply_patch for edits, run focused/race/docs verification, then create a
scoped commit containing only this slice and push it. Do not add scheduler,
provider calls, gateway routes, or CLI commands in this slice.
```
