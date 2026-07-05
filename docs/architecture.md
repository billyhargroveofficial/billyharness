# Architecture Boundary Map

This map documents the intended import shape for the current `internal/*`
packages. Standard library and third-party imports are allowed unless a package
note says otherwise; the "Allowed internal imports" column lists direct
billyharness imports that are currently expected.

The guard in `internal/architecture` reads the package map below and enforces
direct internal imports from the "Allowed internal imports" column. Temporary
exceptions are allowed only when they name the phase that removes them.

## Package Map

| Package | Responsibility | Allowed internal imports | Forbidden imports and owner notes |
| --- | --- | --- | --- |
| `internal/agent` | Runtime loop, model calls, tool orchestration, compaction, and event emission. | `checkpoint`, `config`, `hooks`, `instructions`, `mcpclient`, `memory`, `modelinfo`, `projectcontext`, `protocol`, `provider`, `runstate`, `tools` | Should shrink behind runtime/toolexec seams in P1.1. Do not add presentation imports. |
| `internal/architecture` | Test-only import graph guard. | none | Guard package must not become runtime code. |
| `internal/attachments` | Attachment metadata, image validation, hashing, private local storage, ref resolution, and store usage/prune operations surfaced by the CLI adapter. | `config`, `protocol` | Must not import provider, gateway, agent, tools, TUI, or Telegram packages; store refs and metadata only, never raw image bytes in JSONL. |
| `internal/bench` | Benchmark runners, local-loop tasks, provider comparison, replay verification. | `agent`, `config`, `modelinfo`, `protocol`, `provider`, `runstate`, `runtimehost`, `trace` | Bench can compose broad runtime pieces, but should not become a shared runtime dependency. |
| `internal/checkpoint` | Turn-scoped filesystem snapshots, compact diffs, preview, and conflict-safe restore records for mutating tool steps. | none | Must not write user `.git` state or import runtime, gateway, tools, provider, TUI, or Telegram packages. |
| `internal/clientux` | Client-facing context projection helpers shared by TUI, gateway, and future projector code. | `config`, `gatewayapi`, `protocol`, `secrets` | May import `secrets` only for shared redaction of debug/incident payloads. Must not import gateway server, agent, provider, tools, TUI, or Telegram. |
| `internal/clientux/projector` | Presentation-neutral protocol event projector for client run snapshots. | `protocol` | Must not import gateway server, agent, provider, tools, TUI, Telegram, or rendering packages. |
| `internal/codexauth` | Shared Codex auth payload, JWT claim, account, expiry, auth-mode, and refresh-status helpers. | none | Must remain pure parsing/status logic with no HTTP, file writes, provider, credentials, or config imports. |
| `internal/commandregistry` | Shared searchable metadata registry for built-in actions, local prompt commands, profiles, and MCP prompt metadata. | `clientux`, `config`, `mcpclient`, `promptcommands` | Must stay metadata-only; no command execution, provider calls, gateway server, TUI, Telegram, cloud registry, or marketplace behavior. |
| `internal/config` | Runtime configuration, profiles, summaries, MCP/hook config loading. | `modelinfo` | Must not import adapters, tools, provider runtime construction, or UI packages. |
| `internal/credentials` | Credential file discovery, token persistence, and auth payload helpers. | `codexauth`, `config`, `modelinfo` | Must not import provider implementations except through future auth wiring. |
| `internal/diagnostics` | Command-based diagnostics runner, bounded output capture, and compiler-style issue parsing. | none | Must stay command-based; no LSP client, watcher, auto-install, or provider calls. |
| `internal/displayfmt` | Leaf display formatting helpers for compact numbers and percentages shared by client surfaces. | none | Must remain presentation-neutral and must not import clients, gateway, provider, tools, or runtime packages. |
| `internal/eventlog` | Event record validation, lifecycle validation, JSONL helpers, and corruption diagnostics. | `protocol` | Guarded: no `agent`, `gateway`, `trace`, `tui`, or `telegrambot` imports. |
| `internal/filesearch` | On-demand workspace file resolver for fuzzy relative-path lookup using git, ripgrep, and walk fallbacks. | none | Must stay local and rebuildable; no watchers, databases, shell history, or file-content injection. |
| `internal/gateway` | HTTP adapter for sessions, benchmark artifacts, session run locking, session persistence, replay, and inspection. | `agent`, `attachments`, `checkpoint`, `clientux`, `clientux/projector`, `config`, `credentials`, `eventlog`, `gatewayapi`, `mcpstatus`, `modelinfo`, `protocol`, `provider`, `runstate`, `runtimehost`, `secrets`, `tools`, `trace` | Must directly import `eventlog` and keep lifecycle semantics there. Server DTOs and shared client transport helpers belong to `gatewayapi`/`gatewayclient`. Stored-session inspection may use the shared client UX projector, not a gateway-local projector. |
| `internal/gatewayapi` | Shared gateway HTTP request/response DTOs, URL normalization, readiness, auth header, and unavailable-hint helpers. | `config`, `protocol`, `serviceops` | Must not import gateway server, clients, runtime, provider, tools, TUI, or Telegram. |
| `internal/gatewayclient` | Shared gateway HTTP client helpers, typed status errors, session JSON/NDJSON methods, and client-side context formatting. | `displayfmt`, `gatewayapi`, `protocol` | Must not import gateway server, agent, provider, tools, TUI, or Telegram. |
| `internal/hooks` | Hook process execution and hook event payloads. | `config`, `protocol`, `secrets` | Must not import agent, tools, provider, or presentation packages. |
| `internal/instructions` | Instruction file discovery and initial instruction assembly. | `config`, `protocol` | Must stay independent of runtime adapters and provider implementations. |
| `internal/mcpclient` | Managed MCP stdio clients, server lifecycle, tool discovery, and status. | `config`, `protocol`, `secrets` | Must not depend on agent, gateway, TUI, or Telegram. |
| `internal/mcpserver` | Local MCP server adapter exposing harness tools. | `protocol`, `tools` | Tool risk decisions must go through the central tools policy boundary. |
| `internal/mcpstatus` | Presentation-friendly MCP status formatting. | `mcpclient` | Keep status formatting small; do not import runtime adapters. |
| `internal/memory` | File-backed curated memory index loading, topic-path validation, caps, and frozen prompt summary rendering. | `config`, `protocol` | Must not auto-write memories, read topic bodies into the prompt, import provider/tools/UI packages, or add database/vector dependencies. |
| `internal/modelinfo` | Model/provider catalog helpers. | none | Must remain a leaf utility package. |
| `internal/protocol` | Shared protocol events, messages, envelopes, tool specs, and typed payloads. | none | Guarded: no billyharness internal imports. |
| `internal/projectcontext` | Bounded project context snapshot and prompt fragment rendering. | `config`, `instructions`, `protocol` | Must not ingest README prose, `.env` values, shell history, watchers, databases, or provider calls. |
| `internal/provider` | Provider clients, Codex/DeepSeek request building, streaming parsers, auth integration, and provider-backed web summary adapter. | `attachments`, `codexauth`, `config`, `credentials`, `modelinfo`, `protocol`, `secrets`, `webtools` | Must not import tools, gateway, TUI, Telegram, or bench. |
| `internal/promptcommands` | Local Markdown prompt-command loading and deterministic placeholder expansion. | none | Must not add shell interpolation, marketplace behavior, provider calls, or access-policy fields. |
| `internal/runstate` | Runtime snapshot metadata and deterministic state hashes. | `config`, `modelinfo`, `protocol` | Should stay presentation-agnostic. |
| `internal/runtimehost` | Shared runtime assembly host for resolved settings, provider construction, model capability lookup, tool registry assembly, MCP attachment, and agent creation. | `agent`, `config`, `mcpstatus`, `modelinfo`, `protocol`, `provider`, `tools` | Must stay a small composition package; no gateway server, TUI, Telegram, benchmark policy, command parsing, or presentation imports. |
| `internal/secrets` | Secret discovery and redaction helpers. | none | Must remain a leaf utility package. |
| `internal/serviceops` | Managed service names, subcommands, unit paths, and pid-file metadata shared by operator tooling. | none | Must remain a leaf metadata package; no systemd calls, process inspection, config loading, gateway, TUI, Telegram, or runtime imports. |
| `internal/skills` | Local SKILL.md discovery, frontmatter parsing, bounded viewing of support files, and explicit local import metadata for compatibility skills. | `config` | Must not import tools, provider, gateway, TUI, Telegram, remote marketplaces, or network clients. |
| `internal/telegrambot` | Telegram adapter, rendering, command handling, gateway client wrapper, and Telegram media attachment ingestion. | `attachments`, `clientux`, `clientux/projector`, `commandregistry`, `config`, `credentials`, `displayfmt`, `eventlog`, `gatewayapi`, `gatewayclient`, `mcpstatus`, `memory`, `modelinfo`, `protocol`, `secrets`, `toolrender` | Must not import gateway server internals. |
| `internal/testkit` | Shared test helpers for HTTP servers, JWTs, and future cross-package fixtures. | none | Must remain test-support only and must not become a runtime dependency. |
| `internal/testkit/fakeprovider` | Shared scripted provider test helper for replay, retry, malformed-stream, and cancellation regressions. | `provider` | Must remain test-support only and must not become a runtime dependency. |
| `internal/toolrender` | Shared tool display labels, argument summaries, output-ref evidence lines, and compact tool result text for clients. | `displayfmt`, `protocol` | Must not import TUI, Telegram, gateway, or tools. |
| `internal/tools` | Tool registry, schemas, central tool policy, filesystem/shell/MCP/web tools, skills wrappers, output refs, cache. | `config`, `diagnostics`, `filesearch`, `mcpclient`, `memory`, `protocol`, `skills`, `tools/discovery`, `webtools` | Guarded: must not import `provider`; model web summaries are injected through `webtools.Summarizer`. |
| `internal/tools/discovery` | Shared native/MCP tool search, filtering, namespaces, and schema-budget shaping. | `protocol` | Must stay independent of registry execution, provider, gateway, TUI, and Telegram. |
| `internal/trace` | Benchmark event writer, payload refs, replay summaries, and timeline projection. | `eventlog`, `protocol` | Must directly import `eventlog`; replay must use it and must not reintroduce separate lifecycle validation. |
| `internal/tui` | Bubble Tea terminal UI, gateway session mode, rendering, input handling, and persisted chat blocks. | `attachments`, `clientux`, `clientux/projector`, `commandregistry`, `config`, `credentials`, `displayfmt`, `filesearch`, `gatewayapi`, `gatewayclient`, `mcpstatus`, `memory`, `modelinfo`, `promptcommands`, `protocol`, `toolrender`, `tui/render`, `tui/runtimeclient`, `tui/selection`, `tui/transcript` | Must not import gateway server internals, agent, provider, or tools directly. Local runtime mode goes through `tui/runtimeclient`. |
| `internal/tui/render` | TUI render cache keys, cached cell rendering, terminal markdown rendering, and activity/tool/status cell rendering. | none | Must not import billyharness runtime packages. Rendering should remain downstream of transcript cells. |
| `internal/tui/runtimeclient` | Local runtime adapter for TUI normal operation: initial messages, local agent runs, and local MCP status. | `config`, `mcpstatus`, `protocol`, `runtimehost` | This is the only TUI subpackage allowed to ask the shared runtime host for local mode. Keep Bubble Tea state and rendering out. |
| `internal/tui/selection` | ANSI-aware transcript selection coordinates, visible line ranges, selected text, highlight rendering, clipboard, and OSC52 fallback. | none | Must not import billyharness runtime packages. Keep Bubble Tea message adaptation in `internal/tui`. |
| `internal/tui/transcript` | Transcript cells, cell types, persistence DTOs, event identity helpers, and canonical tool/context cell text for the TUI transcript. | `displayfmt`, `protocol`, `toolrender` | May use `toolrender` for shared tool labels and `displayfmt` for shared compact numbers. Must not import Bubble Tea, lipgloss, gateway, provider, tools, agent, TUI rendering, or Telegram. |
| `internal/webtools` | Public-host-safe HTTP client and web fetch primitives. | none | Must not import provider, tools, gateway, TUI, or Telegram. |

`internal/store` is currently an empty/reserved directory, not a Go package.

## Runtime Event Delivery

Gateway session JSONL is the durable source of truth. Live `/run` responses and
`/events?follow=true` responses are progress streams only: they may drop live
events under client backpressure, but they must not block active execution.
When a live run stream drops events, the gateway emits a `gateway.stream_gap`
hint and clients should recover by replaying `/v1/sessions/{id}/events` from
their last durable `seq`.

## File Size Budget Exceptions

Targets for handwritten Go files are 1,500 LOC for `.go` files and 1,200 LOC
for `_test.go` files. `fast-agent-harness hygiene` reports tracked source files
over those budgets from `git ls-files` and reports ignored runtime artifacts
separately.

The enforced exception list lives in
`cmd/fast-agent-harness/hygiene.go` as `hygieneLargeFileExceptions`; this table
is human-readable context only.

| Path | Reason |
| --- | --- |
| `internal/tools/tools.go` | Historical registry surface kept close together until tool registration is split by family. |
| `internal/gateway/gateway.go` | Historical gateway surface kept while route handlers continue moving into focused files. |
| `internal/telegrambot/commands_flow_test.go` | Telegram/operator command-flow coverage spans auth policy, session commands, rendering/status commands, and secret-bearing cases. |
| `internal/gateway/gateway_test.go` | Gateway behavior coverage still mixes auth/browser boundary, liveness/readiness, run admission, and owner-scope cases. |
| `internal/mcpclient/client_test.go` | MCP lifecycle, catalog/schema, reconnect/backoff, structured output, and redaction coverage still share fixtures. |
