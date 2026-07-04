# Billyharness Architecture Docs

`docs/` is for durable architecture, contracts, decisions, and clean-room
architecture research only. Active TODOs, goal prompts, implementation evidence,
runbooks, and temporary investigation logs live outside `docs/`.

Read [../llms.txt](../llms.txt) first when an agent needs the broader repo
navigation map.

This is an initial architecture index, not exhaustive reference
documentation. For exact command flags, gateway routes, config keys, protocol
fields, or tool schemas, inspect the source until generated references exist.

## Canon

- [Architecture map](architecture.md): current package ownership, allowed
  imports, file-size budgets, and guarded rules. This file is parsed by
  `internal/architecture`, so keep its `Package Map` shape stable.
- [Gateway and sessions](architecture/gateway-and-sessions.md): gateway
  responsibilities, HTTP/session routes, durable session store,
  replay/catch-up semantics, and gateway-client API/security boundaries.
- [Security and trust model](architecture/security-model.md): gateway/browser
  trust, bearer auth, Telegram allowlists, MCP untrusted input, tool policy,
  workspace boundaries, secret redaction, and public-web SSRF protections.
- [Runtime event system](architecture/runtime-event-system.md): source-of-truth
  event model, JSONL replay, lifecycle validation, runtime IDs, tool/model
  lifecycle, output refs, and compaction/context events.
- [TUI and client UX architecture](architecture/tui-and-clientux.md): terminal
  UI event projection, transcript cells, rendering, selection/copy, saved
  sessions, shared client UX metadata, and local runtime adapter boundaries.
- [Config, provider, and context architecture](architecture/config-provider-context.md):
  config provenance, runtime overrides, providers/auth, model capabilities,
  instruction assembly, memory, skills, and context accounting.
- [Tools, MCP, webtools, and rendering boundaries](architecture/tools-mcp-and-policy.md):
  native tool registry, tool policy, lazy MCP, public web access, output refs,
  and client rendering boundaries.
- [Telegram and operator surfaces](architecture/telegram-and-operator-surfaces.md):
  Telegram adapter role, gateway-client boundary, owner scope, admission,
  rendering, attachments, commands, and operator/security constraints.
- [Documentation system](documentation-system.md): how agents should read,
  write, generate, verify, and route documentation.

## Decisions

- `adr/`: append-only architecture decision records. Use this for durable
  choices and rationale, not for active proposals or TODO checklists.
- [ADR 0001](adr/0001-jsonl-event-log-source-of-truth.md): JSONL event logs are
  the durable source of truth for persisted event streams.
- [ADR 0002](adr/0002-gateway-owns-session-authority.md): the gateway owns
  session ownership, access checks, and client-scope authority.
- [ADR 0003](adr/0003-mcp-instructions-are-untrusted-metadata.md): MCP
  instructions and tool metadata stay untrusted unless local operator policy
  promotes or classifies them.
- [ADR 0006](adr/0006-telegram-is-a-gateway-client.md): Telegram is a scoped
  gateway client, not a gateway/runtime peer.
- [ADR 0007](adr/0007-local-gateway-mutating-routes-require-explicit-trust.md):
  local gateway mutating routes require bearer trust or an explicit development
  bypass.
- [ADR 0008](adr/0008-gateway-state-reads-require-bearer-when-token-configured.md):
  gateway `/v1/` state reads require bearer auth when a token is configured.

## Research

- [Research routing](research/README.md): canonical-vs-historical labels and
  cleanup policy for legacy research files.
- [Codex research roadmap](codex-research-roadmap.md): legacy historical source
  material only. It carries a historical banner and still contains legacy
  `Active Backlog` P0/P1/P2 language; verify against current canon and extract
  work into `loop-develop/current-todo` before using it.
- [Competitive architecture analysis](competitive-architecture-analysis.md):
  clean-room competitor source material and design pressure. It carries a
  historical banner; treat it as input, not current truth or an implementation
  checklist.

## Known Documentation Gaps

- Generated CLI, gateway API, config-key, protocol-event, tool-catalog, and
  package-map references are planned in
  [../agent-index/generated/reference-plan.md](../agent-index/generated/reference-plan.md),
  but are not implemented yet.
- `agent-index/generated/repo-map.md` is a hand-curated seed until docsgen
  replaces it with generated output.
- `ops/` contains seed diagnostics and production-service runbooks. Live
  systemd unit contents, environment files, log routing, host state, and
  deployment history still require production-host inspection before being
  documented as verified operations facts.

## Outside `docs/`

- [../.agents/rules/README.md](../.agents/rules/README.md): detailed agent
  behavior rules and read triggers.
- [../agent-index/docs-manifest.json](../agent-index/docs-manifest.json):
  machine-readable docs metadata and freshness hints.
- [../loop-develop/README.md](../loop-develop/README.md): active/current TODO
  workflow and completed loop history.
