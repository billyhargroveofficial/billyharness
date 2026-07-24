# Tools, MCP, Webtools, and Rendering Boundaries

This document is the durable architecture map for Billyharness tool execution,
MCP catalog handling, public web access, output references, and downstream tool
rendering. It documents current behavior verified against code paths on
2026-07-05.

## Source Map

- `internal/protocol/types.go` defines `ToolSpec`, `ToolCall`,
  `ToolResult`, `ToolOutputRefEvent`, `ToolCompact`, and the tool risk enum.
- `internal/tools/` owns the native registry, built-in tool handlers, argument
  schema validation, policy decisions, MCP gateway tools, web tools, skill
  wrappers, and parallel metadata.
- `internal/tools/discovery/` owns searchable native/MCP catalog filtering and
  schema-budget shaping.
- `internal/mcpclient/` owns managed MCP server lifecycle, stdio JSON-RPC,
  catalog building, tool-name sanitization, prompt metadata, status, reconnect,
  and MCP output rendering.
- `internal/mcpstatus/` formats presentation-friendly MCP status responses for
  gateway/TUI/Telegram surfaces.
- `internal/webtools/` owns the public-host-safe HTTP client, web backend
  clients, and model-summary request contracts.
- `internal/tools/output_ref.go` owns plaintext output-ref storage and metadata.
- `internal/toolrender/` owns presentation-neutral tool labels and summaries for
  TUI and Telegram.
- `internal/commandregistry/` owns searchable command metadata, including MCP
  prompt metadata that is not executable today.
- `internal/agent/tool_attempt.go` and `internal/agent/agent.go` attach runtime
  metadata, compact oversized output, emit lifecycle events, and create
  `tool.output_ref_created` events.

## Native Tool Registry

The registry type in `internal/tools/tools.go` is the execution boundary for
native tools. A tool is a `Tool` with a `protocol.ToolSpec`, optional
`ParallelMetadata`, and a handler. `NewRegistryFromSettings` registers the
native tool set: filesystem read/search/write/edit helpers, shell process
helpers, diagnostics, todo/user-input helpers, web tools, memory tools, skill
tools, and `tool_search`.

`protocol.ToolSpec` is the model-facing contract: name, description, JSON
parameters, and risk. `Registry.Specs()` returns the model-visible native
specs sorted by name and filters by access mode. In plan mode, only
`read_only` and `network` specs remain visible; write, execute, and external
specs are hidden.

Tool calls enter through `Registry.Call()` in `internal/tools/tools.go`.
Current order is:

1. Look up a model-visible native tool by name.
2. Normalize empty arguments to `{}`.
3. Run the central policy check in `internal/tools/policy.go`.
4. Validate arguments against the tool schema in `internal/tools/schema.go`.
5. Invoke the handler.

An optional immutable `RunCapabilities` value further reduces a single run.
When present, `Registry.Specs`, lazy discovery, the policy decision, and
`Registry.Call` independently enforce the canonical `allowed_tools` set.
Handlers also receive the run capabilities through call context so cloned tool
snapshots cannot recover the parent registry's broader surface. Isolated
snapshots neither refresh nor copy the MCP catalog. A zero capability value
preserves the legacy registry behavior.

`durable-job-v1` adds separate filesystem read and write roots. The registry
overrides any caller-supplied workspace policy at each handler call: local-read
risks receive only read roots and local-write risks receive only write roots.
Its web policy uses explicit HTTPS origin/path prefixes and rechecks redirects;
an unrestricted public-HTTPS grant must be the sole `*` entry. The constructor
accepts only the small structured durable-tool set and rejects shell/execute,
secret, external/MCP, network-write, memory, skill, helper, and cache tools.

The native schema validator is intentionally small and strict. It checks the
subset used by local tool specs: object properties, required fields,
`additionalProperties:false`, arrays, enums, min/max item counts, and primitive
JSON types. Native tool schemas fail validation when they use unsupported JSON
Schema keywords, so Billyharness does not silently expose native contracts it
cannot validate.

## Policy Boundary

Tool risk values from `internal/protocol/types.go` include the legacy native
values `read_only`, `network`, `write`, `execute`, and `external`, plus the
more explicit classes `local_read`, `local_write`, `network_read`,
`network_write`, `external_mutation`, and `secret_access`. Policy normalizes
legacy values into the explicit classes for decisions while preserving old
native tool specs for compatibility.

`internal/tools/policy.go` is the central allow/deny decision point. Current
policy behavior:

- `access_mode=plan` denies and hides anything outside local/network reads.
- `access_mode=guarded` denies local writes, network writes, execute tools,
  external mutations, and secret access, even if dangerous tools are otherwise
  auto-approved.
- `access_mode=build` allows the normal build-mode surface.
- Write/execute/side-effecting classes require `AutoApproveDangerous`; when
  disabled they return a permission-denied result before handler execution.
- Legacy `external` tools are still marked as requiring approval metadata and
  remain allowed by build/guarded policy unless plan mode blocks them.

Parallel metadata is separate from permission policy. `defaultParallelMetadata`
in `internal/tools/tools.go` marks read-only tools as parallel-safe, web tools
as network-rate-limited, write/execute tools as exclusive-workspace, and
`mcp_list_tools`/`mcp_call` as unknown external and not parallel-safe.

## MCP Trust Boundary

MCP servers are external processes or endpoints. Their tool descriptions,
schemas, prompts, outputs, and initialize instructions are untrusted server
content unless Billyharness policy explicitly promotes them. ADR 0003 records
the durable rule: [MCP instructions are untrusted metadata](../adr/0003-mcp-instructions-are-untrusted-metadata.md).

Current behavior in `internal/mcpclient`:

- `config.LoadDefaultMCPServers` reads `$BILLYHARNESS_HOME/mcp.config.toml`
  when MCP is enabled and no explicit server list is already present. The
  default allowlist is `telegram`, `telegram-parilka`, `github`, and `context7`
  in `internal/config/defaults.go`.
- Stdio MCP is implemented. Streamable HTTP MCP config is parsed and reported
  as `unsupported`, but not connected or called.
- Shell commands such as `sh`, `bash`, `zsh`, `cmd.exe`, and PowerShell are
  denied as MCP command launchers. Stdio MCP commands run by direct argv.
- MCP working directories must resolve inside configured workspace roots.
- The child environment contains a small parent allowlist, explicitly configured
  `env_vars`, and literal server `env`. Secret-like configured values are
  redacted from errors and outputs.
- Optional server startup failures stay visible in status and do not poison
  working servers. Required server failures fail manager initialization.
- Server tool allow/deny lists are applied before catalog publication.
- Local MCP config may set `default_tool_risk` or
  `tool_risks = { tool_name = "network_read" }`. Supported MCP risk values are
  `local_read`, `local_write`, `network_read`, `network_write`, `execute`,
  `external_mutation`, and `secret_access`, with legacy aliases accepted during
  config parsing. MCP risk comes from this local operator policy; server tool
  descriptions and schemas do not get to classify themselves.
- Side-effecting MCP tools require both the normal dangerous-tool policy and an
  explicit `enabled_tools` entry for the original MCP tool name before
  `mcp_call` invokes the remote handler. Missing allowlist entries fail closed
  with permission metadata instead of calling the server.
- MCP server status separates process/transport lifecycle from catalog
  lifecycle. `state` remains as a legacy compact lifecycle field, while
  `transport_state` reports connection/retry/failure state and
  `catalog_state` reports `ready`, `connected_no_tools`,
  `tools_fetch_failed`, `catalog_stale`, `disconnected`, `degraded`, or
  `unsupported`. Status diagnostics are structured code/severity/message
  records and are redacted before presentation.
- MCP tool names are normalized to `mcp__<server>__<tool>`. Sanitized-name
  collisions fail catalog initialization instead of picking a winner.
- `initialize` instructions are stored as `ServerInstructions` metadata and
  tagged with `trust=untrusted_mcp_server_metadata`.
  `Manager.Instructions()` is empty by default. Instructions enter model
  context only when `MCPPromoteServerInstructions` is enabled, and promoted text
  is tagged with `trust=operator_promoted_mcp_initialize_instructions`.
- MCP prompt catalogs are metadata only. `internal/commandregistry` exposes
  them as unavailable entries with "prompt invocation is not implemented".

`internal/mcpstatus/status.go` keeps this boundary visible to clients. It
formats server state, prompts, and server instructions as metadata-only status.
Gateway `/v1/mcp`, TUI local MCP status, and Telegram gateway status all use
`mcpstatus.Response`.

## Lazy MCP Catalog

Dynamic MCP tools are not direct model-visible specs. `NewRegistryWithMCP` in
`internal/tools/tools.go` starts an MCP manager, mirrors the manager catalog into
`Registry.mcpTools`, and then registers only the static gateway tools
`mcp_list_tools` and `mcp_call`.

Current lazy flow:

1. `tool_search` searches both native specs and the dynamic MCP mirror.
2. `mcp_list_tools` lists only MCP catalog entries plus server statuses.
3. MCP search/list responses include catalog metadata showing
   `model_visible_tools.kind = static_gateway_tools`,
   `includes_dynamic_mcp_tools = false`, and
   `mcp_catalog.model_visible = false`. The dynamic catalog projection also
   includes `mcp_catalog.state`, which is `ready`, `empty`, `degraded`, or
   `catalog_stale` depending on mirrored tool state and listener lag. MCP tool
   entries include `risk`, `risk_class`, `risk_source`,
   `metadata_trust`, `description_trust`, and `input_schema_trust` when schema
   text is returned. External MCP schemas are preserved as remote JSON Schema
   metadata; search/list responses also label them with
   `input_schema_validation = external_mcp_json_schema_subset` and list any
   unsupported local-validation keywords.
4. MCP search/list results return `call_tool: "mcp_call"` and the full
   `call_name`/`name` such as `mcp__github__search_repositories`.
5. `mcp_call` looks up the full dynamic name in the current mirror, checks the
   target MCP tool risk policy, validates the target MCP tool arguments against
   Billyharness's supported external-schema subset, and then calls the
   underlying server tool. Valid MCP JSON Schema keywords that Billyharness does
   not enforce locally, such as `pattern`, `minimum`, or `oneOf`, do not block
   the call; the server remains responsible for its full schema semantics.
   Permission denials, validation errors, and successful calls carry MCP target
   metadata such as server, original tool, risk source, metadata trust, schema
   validation mode, and unsupported schema keywords.
6. MCP `tools/call` responses preserve redacted raw `content`,
   `structuredContent`, and response `_meta` in tool-result metadata using
   `mcp_result_content`, `mcp_structured_content`, and `mcp_result_meta`.
   Model-facing text remains compact: text parts are rendered inline, while
   image/resource-like parts become short placeholders that point operators to
   the structured metadata in event/debug JSON.

`ToolSet.Snapshot()` freezes the tool view for a provider turn, including an
MCP status/catalog hash from `internal/tools/toolset.go`. Live catalog changes
can still refresh the registry on later calls. Server reconnect and
`tools/list_changed` notifications rebuild the catalog and clear stale tools;
calling a stale name after the mirror clears returns unknown MCP tool.

## Tool Search

`internal/tools/discovery/discovery.go` owns filtering and output shaping for
lazy discovery. Candidates include source, namespace, server, risk, call tool,
and optional call name. Native namespaces are derived from tool-name prefixes
such as `fs`, `web`, `shell`, `mcp-gateway`, and `tool`. MCP namespaces are
`mcp.<server>`.

The search API supports query, MCP server, namespace, risk, limit, optional
schema inclusion, and a schema token budget. Risk filtering matches both exact
legacy risk values and normalized risk classes. Schemas are omitted once the
budget is exceeded, and the response marks schema truncation in metrics. This
keeps large MCP catalogs searchable without making their whole schema set
model-visible.

## Web Tools

Native web tools are built into `internal/tools`, not MCP. Their transport
safety is delegated to `internal/webtools.Client`.

Current public-host safety:

- Only `http` and `https` URLs are allowed.
- Non-ASCII hostnames, IPv6 zone identifiers, `localhost`, `*.localhost`, and
  non-global/special-use IP ranges are rejected, including private, loopback,
  link-local, CGNAT, benchmarking, documentation, multicast, and reserved
  ranges.
- Hosts are resolved before request validation.
- Redirect targets are validated.
- Dialing re-checks resolved addresses, which blocks public-to-private DNS
  rebinding before the second connection.
- Non-2xx responses fail with bounded body text.

An isolated run may additionally carry canonical `allowed_url_prefixes`. The
wire name is retained for the versioned contract, but each entry authorizes
only its exact HTTPS origin and canonical path; request query parameters do not
affect matching, and descendants require their own entries. Entries reject
userinfo, queries, fragments, encoded path forms, dot segments, empty path
segments, and non-canonical escaped paths.
The initial target and every redirect must also use an unescaped canonical path
and are checked against both this allowlist and the normal
public-host/DNS-rebinding boundary.
`Registry.Call` performs the same check before cache lookup or handler
execution. URL-restricted `web_extract` uses the native transport because a
provider extraction backend cannot attest its redirect chain. URL-restricted
runs disable shared web-cache reads and writes so content fetched under a wider
scope cannot cross the capability boundary.

`web_fetch`, `web_extract`, and `web_crawl` fetch public textual pages and
return compact JSON digests. They save the full extracted text to an output ref
instead of dumping raw page text into model context by default. `include_text`
or `full_text` is still hard-capped.

`web_search` can use native DuckDuckGo Lite scraping or configured Tavily/Exa
backends. Provider-backed search and extraction live in `internal/webtools` and
are invoked from `internal/tools/web_backend.go`. Configured backend failures
can fall back to native search when safe; missing backend API keys return an
explicit configuration error rather than silently changing behavior. Search
result metadata reports whether freshness and domain filters were requested,
supported, enforced by the provider, post-filtered locally, or skipped. Native
DuckDuckGo search post-filters domains and marks freshness as skipped; when
available, metadata includes result counts before and after local filtering.

Model web summaries are injected through the `webtools.Summarizer` interface.
`internal/tools` does not import `internal/provider`; the provider adapter is
passed in from runtime assembly. Summary requests set `AllowTools:false`, use
bounded input/output token settings, and record helper usage metadata.

## Output Refs

`internal/tools/output_ref.go` writes plaintext artifacts under
`$BILLYHARNESS_HOME/tool-output/<YYYYMMDD>/`. Directories are `0700`; files are
`0600`. Metadata includes path, basename ID, byte count, SHA-256, permissions,
and plaintext status.

Output refs are used in three places:

- Web fetch/extract/crawl tools store full extracted text as an output ref and
  return compact inline content.
- `Agent.compactToolResult` in `internal/agent/agent.go` enforces
  `MaxToolOutputBytes` after any handler returns. Oversized output is replaced
  by a bounded preview plus an output-ref note; original content is stored if
  the handler did not already provide an output ref.
- Checkpoint change records use `tools.StoreOutput` for patch/change evidence
  and attach patch output-ref metadata to `turn.change_recorded` events.
  Gateway undo/redo verifies the recorded patch output-ref SHA-256 and then
  rechecks workspace-root and symlink constraints before restoring files.

When a tool result has an output ref, `toolOrchestrator.EmitAttemptFinished` in
`internal/agent/tool_attempt.go` settles the artifact before any terminal tool
event. Settlement stats the file, verifies existing byte/hash/permission
metadata, requires a portable relative `output_ref_id`, refreshes metadata, and
then emits `tool.output_ref_created` before the final tool result event. If
settlement fails, the terminal result is downgraded to
`output_ref_unsettled`, the terminal `OutputRef` field is cleared, and no
`tool.output_ref_created` event is emitted. The output-ref event carries the
same ref metadata and a `ToolCompact` summary. `protocol.EnrichEvent` copies
call and attempt IDs from `ToolOutputRefEvent` into the event envelope.

`fs_read_file` can read absolute paths under Billyharness tool-output even when
they are outside workspace roots. Ordinary files under `$BILLYHARNESS_HOME`
remain blocked unless they are also under an allowed workspace root.

## Rendering Boundary

Rendering is downstream of protocol events. Tool execution and policy decisions
must not move into client renderers.

`internal/toolrender` consumes protocol DTOs and returns compact labels for
TUI and Telegram styles. It knows about common tool names, result metadata,
`ToolCompact`, output refs, permission events, and turn-change summaries. It
does not import `internal/tools`, `internal/mcpclient`, gateway, TUI, or
Telegram packages.

The TUI transcript projector in `internal/tui/transcript/projector.go` converts
protocol events into cells and uses `toolrender` for tool lifecycle/result/ref
text. Telegram rendering also uses `toolrender` summaries. Rich copies and raw
copies remain separate in transcript/export code, so compact UI text does not
replace the raw event payload or output-ref metadata.

## Current Behavior vs Future Hardening

Current behavior is intentionally conservative around dynamic external
capabilities: stdio MCP only, metadata-only MCP prompts, untrusted MCP
initialize instructions by default, hidden dynamic MCP specs, public-host-safe
web fetches, bounded inline output, and presentation-only rendering helpers.

The code does not currently implement streamable HTTP MCP calls, direct model
visibility for dynamic MCP schemas, executable MCP prompts, automatic trust of
MCP initialize instructions, or provider imports inside `internal/tools`.
Future hardening or feature work must preserve the trust boundary above or
record a new ADR before documenting changed behavior as current truth.
