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
| `internal/agent` | Runtime loop, model calls, tool orchestration, compaction, and event emission. | `config`, `hooks`, `instructions`, `mcpclient`, `modelinfo`, `protocol`, `provider`, `runstate`, `tooloutput`, `tools` | Should shrink behind runtime/toolexec seams in P1.1. Do not add presentation imports. |
| `internal/architecture` | Test-only import graph guard. | none | Guard package must not become runtime code. |
| `internal/bench` | Benchmark runners, local-loop tasks, provider comparison, replay verification. | `agent`, `config`, `modelinfo`, `protocol`, `provider`, `runstate`, `tools`, `trace` | Bench can compose broad runtime pieces, but should not become a shared runtime dependency. |
| `internal/clientux` | Client-facing context projection helpers shared by TUI, gateway, and future projector code. | `config`, `gatewayapi`, `protocol` | Must not import gateway server, agent, provider, tools, TUI, or Telegram. |
| `internal/clientux/projector` | Presentation-neutral protocol event projector for client run snapshots. | `protocol` | Must not import gateway server, agent, provider, tools, TUI, Telegram, or rendering packages. |
| `internal/codexauth` | Shared Codex auth payload, JWT claim, account, expiry, auth-mode, and refresh-status helpers. | none | Must remain pure parsing/status logic with no HTTP, file writes, provider, credentials, or config imports. |
| `internal/config` | Runtime configuration, profiles, summaries, MCP/hook config loading. | `modelinfo` | Must not import adapters, tools, provider runtime construction, or UI packages. |
| `internal/credentials` | Credential file discovery, token persistence, and auth payload helpers. | `codexauth`, `config` | Must not import provider implementations except through future auth wiring. |
| `internal/eventlog` | Event record validation, lifecycle validation, JSONL helpers, and corruption diagnostics. | `protocol` | Guarded: no `agent`, `gateway`, `trace`, `tui`, or `telegrambot` imports. |
| `internal/gateway` | HTTP adapter for sessions, benchmark artifacts, session persistence, replay, and inspection. | `agent`, `clientux`, `config`, `credentials`, `eventlog`, `gatewayapi`, `modelinfo`, `protocol`, `provider`, `runstate`, `secrets`, `session`, `tools`, `trace` | Should keep lifecycle semantics in `eventlog`. Server DTOs and client helpers belong to `gatewayapi`/`gatewayclient`. |
| `internal/gatewayapi` | Shared gateway HTTP request/response DTOs. | `config`, `protocol` | Must not import gateway server, clients, runtime, provider, tools, TUI, or Telegram. |
| `internal/gatewayclient` | Shared gateway HTTP client helpers, typed status errors, session JSON/NDJSON methods, and client-side context formatting. | `config`, `gatewayapi`, `protocol` | Must not import gateway server, agent, provider, tools, TUI, or Telegram. |
| `internal/hooks` | Hook process execution and hook event payloads. | `config`, `protocol`, `secrets` | Must not import agent, tools, provider, or presentation packages. |
| `internal/instructions` | Instruction file discovery and initial instruction assembly. | `config`, `protocol` | Must stay independent of runtime adapters and provider implementations. |
| `internal/mcpclient` | Managed MCP stdio clients, server lifecycle, tool discovery, and status. | `config`, `protocol`, `secrets` | Must not depend on agent, gateway, TUI, or Telegram. |
| `internal/mcpserver` | Local MCP server adapter exposing harness tools. | `protocol`, `tools` | Tool risk decisions must go through the central tools policy boundary. |
| `internal/mcpstatus` | Presentation-friendly MCP status formatting. | `mcpclient` | Keep status formatting small; do not import runtime adapters. |
| `internal/modelinfo` | Model/provider catalog helpers. | none | Must remain a leaf utility package. |
| `internal/protocol` | Shared protocol events, messages, envelopes, tool specs, and typed payloads. | none | Guarded: no billyharness internal imports. |
| `internal/provider` | Provider clients, Codex/DeepSeek request building, streaming parsers, auth integration, and provider-backed web summary adapter. | `codexauth`, `config`, `credentials`, `modelinfo`, `protocol`, `secrets`, `webtools` | Must not import tools, gateway, TUI, Telegram, or bench. |
| `internal/runstate` | Runtime snapshot metadata and deterministic state hashes. | `config`, `modelinfo`, `protocol` | Should stay presentation-agnostic. |
| `internal/secrets` | Secret discovery and redaction helpers. | none | Must remain a leaf utility package. |
| `internal/session` | Session message state, run locking, and runner abstraction. | `protocol` | Must not import agent or gateway. |
| `internal/telegrambot` | Telegram adapter, rendering, command handling, and gateway client wrapper. | `clientux`, `clientux/projector`, `config`, `credentials`, `gatewayapi`, `gatewayclient`, `mcpstatus`, `modelinfo`, `protocol`, `toolrender` | Must not import gateway server internals. |
| `internal/testkit` | Shared test helpers for HTTP servers, JWTs, and future cross-package fixtures. | none | Must remain test-support only and must not become a runtime dependency. |
| `internal/tooloutput` | Shared plaintext output-ref storage, metadata, and existence checks. | `config` | Must stay independent of agent, tools, gateway, TUI, and Telegram. |
| `internal/toolrender` | Shared tool display labels and argument summaries for clients. | `protocol` | Must not import TUI, Telegram, gateway, or tools. |
| `internal/tools` | Tool registry, schemas, central tool policy, filesystem/shell/MCP/web tools, output refs, cache. | `config`, `mcpclient`, `protocol`, `tooloutput`, `tools/discovery`, `webtools` | Guarded: must not import `provider`; model web summaries are injected through `webtools.Summarizer`. |
| `internal/tools/discovery` | Shared native/MCP tool search, filtering, namespaces, and schema-budget shaping. | `protocol` | Must stay independent of registry execution, provider, gateway, TUI, and Telegram. |
| `internal/trace` | Benchmark event writer, payload refs, replay summaries, and timeline projection. | `eventlog`, `protocol` | Guarded: replay must use `eventlog`; must not reintroduce separate lifecycle validation. |
| `internal/tui` | Bubble Tea terminal UI, gateway session mode, rendering, input handling, and persisted chat blocks. | `clientux`, `clientux/projector`, `config`, `credentials`, `gatewayapi`, `gatewayclient`, `mcpstatus`, `modelinfo`, `protocol`, `toolrender`, `tui/render`, `tui/runtimeclient`, `tui/selection`, `tui/transcript` | Must not import gateway server internals, agent, provider, or tools directly. Local runtime mode goes through `tui/runtimeclient`. |
| `internal/tui/render` | TUI render cache keys, cached cell rendering, terminal markdown rendering, and activity/tool/status cell rendering. | none | Must not import billyharness runtime packages. Rendering should remain downstream of transcript cells. |
| `internal/tui/runtimeclient` | Local runtime adapter for TUI normal operation: initial messages, local agent runs, and local MCP status. | `agent`, `config`, `mcpstatus`, `protocol`, `provider`, `tools` | This is the only TUI subpackage allowed to compose agent/provider/tools for local mode. Keep Bubble Tea state and rendering out. |
| `internal/tui/selection` | ANSI-aware transcript selection coordinates, visible line ranges, selected text, highlight rendering, clipboard, and OSC52 fallback. | none | Must not import billyharness runtime packages. Keep Bubble Tea message adaptation in `internal/tui`. |
| `internal/tui/transcript` | Transcript cells, cell types, persistence DTOs, event identity helpers, and canonical tool/context cell text for the TUI transcript. | `protocol`, `toolrender` | May use `toolrender` for shared tool labels. Must not import Bubble Tea, lipgloss, gateway, provider, tools, agent, TUI rendering, or Telegram. |
| `internal/webtools` | Public-host-safe HTTP client and web fetch primitives. | none | Must not import provider, tools, gateway, TUI, or Telegram. |

`internal/store` is currently an empty/reserved directory, not a Go package.

## File Size Budget Exceptions

Targets for handwritten Go files are 1,500 LOC for `.go` files and 1,200 LOC
for `_test.go` files. `fast-agent-harness hygiene` reports tracked source files
over those budgets from `git ls-files` and reports ignored runtime artifacts
separately.

| File | Current exception owner | Split plan |
| --- | --- | --- |
| `internal/mcpclient/client.go` | P2.1 MCP client file split. | Split manager, catalog, server lifecycle, stdio transport, JSON-RPC, content rendering, env/secrets, and reconnect/status fanout into focused files. |
| `internal/tui/tui_test.go` | P1.4 TUI subpackage tests. | Move renderer, transcript, selection, and runtimeclient behavioral coverage beside those packages; keep only Bubble Tea integration tests here. |
| `internal/telegrambot/bot_test.go` | P1.5/P2 Telegram adapter decomposition. | Split command, delivery, runner, session-owner, progress, and rendering tests beside the extracted adapter files. |

## Guarded Rules

- `internal/protocol` has no billyharness internal imports.
- `internal/eventlog` may import `protocol`, but not runtime, replay callers, or presentation adapters.
- `internal/clientux` may import `config`, `gatewayapi`, and `protocol`, but not gateway server, runtime, provider, tools, or presentation adapters.
- `internal/clientux/projector` may import `protocol`, but not gateway server, runtime, provider, tools, presentation adapters, or renderers.
- `internal/gatewayapi` may import `config` and `protocol`, but not gateway server, clients, runtime, provider, or presentation adapters.
- `internal/gatewayclient` may import `config`, `gatewayapi`, and `protocol`, but not gateway server, runtime, provider, tools, or presentation adapters.
- `internal/tui/render` has no billyharness internal imports while it owns render cache keys, cached cell rendering, terminal markdown rendering, and activity/tool/status cell rendering.
- `internal/tui/runtimeclient` may import `agent`, `config`, `mcpstatus`, `protocol`, `provider`, and `tools`; TUI itself must not.
- `internal/tui/selection` has no billyharness internal imports.
- `internal/tui/transcript` may import `protocol` and `toolrender`, but not Bubble Tea, lipgloss, gateway, provider, tools, agent, renderers, or presentation adapters.
- `internal/trace` and `internal/gateway` must directly import `eventlog`.
- `internal/tools` must not import `provider`.
- `internal/telegrambot` must not import `gateway`.
- `internal/tui` must not import `gateway`, `agent`, `provider`, or `tools`.
