# ADR 0001 - JSONL Event Log Source Of Truth

Status: accepted
Date: 2026-07-03
Owners: billy
Supersedes: none
Superseded by: none

## Context

Billyharness needs one inspectable event stream that can drive live clients,
session replay, benchmark traces, corruption diagnostics, and future runtime
changes without coupling presentation adapters to the agent loop.

The existing runtime already emits typed protocol events from
[internal/protocol](../../internal/protocol/types.go), validates records and
lifecycle order in [internal/eventlog](../../internal/eventlog/eventlog.go), and
replays benchmark traces in [internal/trace](../../internal/trace/trace.go).
The architecture map states that gateway session JSONL is durable source of
truth and live streams are progress streams.

## Decision

Persisted event streams use append-only JSONL records as the durable source of
truth. `protocol.Event` is the canonical event payload shape. Writers may wrap
events in context-specific records, but replay must validate contiguous
sequence numbers, stable scope identity, event-type consistency, and applicable
protocol envelopes before projecting summaries or client state.

Shared validation belongs in `internal/eventlog`. Runtime emission belongs in
`internal/agent`. Benchmark trace persistence and replay summaries belong in
`internal/trace`. Presentation adapters should recover from live-stream gaps by
replaying durable events from their last observed sequence.

## Consequences

- JSONL stays easy to inspect, append, copy, and replay without a database.
- Corrupt records fail with path, line, record number, and cause instead of
  silently producing partial state.
- Event schema changes must update `internal/protocol`, lifecycle/replay
  validation where relevant, and any trace/client projections that consume the
  event.
- Large payloads can be kept out of the main JSONL record via output refs or
  trace payload refs, but the durable event must keep enough identity and hash
  metadata to audit the referenced content.
- Full-scan replay is acceptable for the current system. If sparse indexes or
  databases are introduced later, JSONL remains the canonical log unless a new
  ADR supersedes this decision.

## Verification

The current documentation guard is:

```sh
go test -count=1 ./internal/architecture
```

Runtime/replay behavior is covered by focused package tests in
`internal/protocol`, `internal/eventlog`, `internal/agent`, `internal/trace`,
and `internal/bench`.
