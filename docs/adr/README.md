# Architecture Decision Records

This directory is for durable Billyharness architecture decisions.

Use an ADR when a decision changes the shape, safety, or long-term maintenance
of the project. Examples include persistence format, event/replay semantics,
gateway authority boundaries, MCP trust rules, documentation architecture, and
package-boundary policy.

## Numbering

Use monotonically increasing four-digit numbers:

```text
0001-use-jsonl-as-session-source-of-truth.md
0002-keep-docs-architecture-only.md
```

Never reuse a number.

0004 and 0005 were never assigned; the sequence resumes at 0006 by design. The
next new ADR is 0009.

## Records

- [ADR 0001](0001-jsonl-event-log-source-of-truth.md): accepted; JSONL event
  logs are the durable source of truth for persisted event streams.
- [ADR 0002](0002-gateway-owns-session-authority.md): accepted; the gateway
  owns session ownership, access checks, and client-scope authority.
- [ADR 0003](0003-mcp-instructions-are-untrusted-metadata.md): accepted; MCP
  instructions and tool metadata stay untrusted unless local operator policy
  promotes or classifies them.
- [ADR 0006](0006-telegram-is-a-gateway-client.md): accepted; Telegram is a
  scoped gateway client, not a gateway/runtime peer.
- [ADR 0007](0007-local-gateway-mutating-routes-require-explicit-trust.md):
  accepted; local gateway mutating routes require bearer trust or an explicit
  development bypass.
- [ADR 0008](0008-gateway-state-reads-require-bearer-when-token-configured.md):
  accepted; state-bearing gateway `/v1/` reads require bearer auth when a
  gateway token is configured.

## Template

```md
# ADR NNNN - Title

Date: YYYY-MM-DD
Owners: billy

## Context

What problem or pressure forced this decision?

## Decision

What did we choose?

## Consequences

What improves, what gets worse, and what must future agents remember?

## Verification

Commands, tests, or code paths that prove the decision is reflected in the repo.
```

## Rules

- Keep ADRs short.
- Do not use ADRs as TODO lists.
- Do not rewrite accepted ADRs when a decision changes; create a new ADR and
  add a Status/Supersedes note to both files only when that first supersede
  actually happens.
- Link accepted ADRs from `docs/README.md` and from the canonical architecture
  doc that relies on them.
