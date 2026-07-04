<!-- Seeded manually for the initial documentation system. Replace with a generated file once docsgen exists. -->

# Billyharness Repo Map

This is a compact orientation map for agents. It is intentionally not a full
source dump.

## Core Runtime

- `internal/agent`: runtime loop, model calls, tool orchestration, compaction,
  and event emission.
- `internal/session`: session message state, run locking, and runner
  abstraction.
- `internal/protocol`: shared protocol events, messages, envelopes, tool specs,
  and typed payloads.
- `internal/eventlog`: event validation, lifecycle validation, JSONL helpers,
  and corruption diagnostics.

## Adapters

- `internal/gateway`: HTTP adapter for sessions, persistence, replay, and
  inspection.
- `internal/tui`: terminal UI, gateway session mode, rendering, input handling,
  and persisted chat blocks.
- `internal/telegrambot`: Telegram adapter, rendering, commands, gateway client
  wrapper, and media attachment ingestion.

## Tools And Providers

- `internal/tools`: local tool registry, schemas, policy, filesystem/shell/MCP
  and web tools.
- `internal/mcpclient`: managed MCP stdio clients, server lifecycle, discovery,
  and status.
- `internal/provider`: Codex/DeepSeek provider clients, streaming parsers, auth,
  and provider-backed web summary adapter.
- `internal/config`: runtime configuration, profiles, summaries, MCP/hook
  loading, and model defaults.

## Shared Client UX

- `internal/clientux`: client-facing context projection helpers.
- `internal/clientux/projector`: presentation-neutral protocol event projector.
- `internal/toolrender`: shared tool display labels and argument summaries.

## Guards And Docs

- `internal/architecture`: Go test guard that parses `docs/architecture.md` and
  enforces package boundaries.
- `docs/architecture.md`: package ownership and guarded import map.
- `docs/documentation-system.md`: documentation hierarchy and agent write rules.
