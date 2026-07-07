# Billyharness Architecture Docs

`docs/` is for durable architecture, contracts, and decisions only. Active
TODOs, goal prompts, implementation evidence, runbooks, temporary investigation
logs, and stale research material live outside `docs/`.

Read [../llms.txt](../llms.txt) first when an agent needs the broader repo
navigation map.

This is an initial architecture index, not exhaustive reference
documentation. For exact command flags or generator gaps, inspect the source
until generated references exist.

## Generated

- [Generated CLI reference](generated/cli.md): top-level commands, aliases, and
  summaries, plus doctor check descriptors.
- [Generated commands reference](generated/commands.md): shared actions, TUI
  slash/keybindings, Telegram aliases, and command registry composition.
- [Generated config reference](generated/config.md): config keys, env aliases,
  defaults, provenance layers, and settings.json fields.
- [Generated protocol events reference](generated/events.md): event envelope
  fields, event types, required IDs, payload names, event sources, and
  table-driven lifecycle rules.
- [Generated gateway API reference](generated/gateway-api.md): HTTP routes,
  auth classes, and DTO names.
- [Generated package index](generated/packages.md): package doc comments,
  direct internal imports, and reverse import edges.
- [Generated tool catalog](generated/tools.md): native tool schemas, risk
  classes, parallel metadata, and MCP policy fields.

Edit the source registry, not generated files. `TestDocsCurrent` enforces
committed freshness; regenerate with
`go run ./cmd/fast-agent-harness docsgen`. `doctor -docs` compares the running
binary's registry fingerprints with generated `source-hash` footers.

## Canon

- [Architecture map](architecture.md): current package ownership, allowed
  imports, and file-size budgets. This file is parsed by
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

## Decisions

- `adr/`: append-only architecture decision records. Use this for durable
  choices and rationale, not for active proposals or TODO checklists. ADR 0004
  and 0005 were never assigned; the next new ADR is 0010.
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
- [ADR 0009](adr/0009-external-ingress-is-gateway-admission.md): external
  triggers become gateway-admitted session inputs, not direct execution.

## Known Documentation Gaps

- Event lifecycle diagrams currently cover only entities that are backed by a
  runtime-consumed table; turn/step/tool/user-input/hook rules remain
  procedural in `internal/eventlog` and described in architecture prose.
- Command-specific flag tables are not generated; each subcommand's `FlagSet`
  remains the source of truth.
- `ops/` contains seed diagnostics and production-service runbooks. Live
  systemd unit contents, environment files, log routing, host state, and
  deployment history still require production-host inspection before being
  documented as verified operations facts.

## Outside `docs/`

- [../.agents/rules/README.md](../.agents/rules/README.md): detailed agent
  behavior rules and read triggers.
- [../loop-develop/README.md](../loop-develop/README.md): active/current TODO
  workflow and completed loop history.
