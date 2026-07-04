# ADR 0003 - MCP Instructions Are Untrusted Metadata

Status: accepted
Date: 2026-07-03
Owners: billy
Supersedes: none
Superseded by: none

## Context

MCP servers can provide tool descriptions, input schemas, prompt catalogs,
tool output, risk-looking language, and initialize instructions. Those values
originate outside the Billyharness codebase and may come from local third-party
processes or future remote endpoints. Injecting server-provided instructions
into model context by default would let an external server alter agent routing
and safety behavior. Trusting a server description or schema to classify the
server's own authority would create the same boundary problem for tools.

## Decision

Billyharness treats MCP initialize instructions and prompt catalogs as
untrusted metadata by default.

`internal/mcpclient` stores initialize instructions in catalog status as
`ServerInstructions`, but `Manager.Instructions()` remains empty unless
`MCPPromoteServerInstructions` is explicitly enabled. Server instruction
metadata is tagged with `trust=untrusted_mcp_server_metadata`. Promoted
instructions are tagged with
`trust=operator_promoted_mcp_initialize_instructions` so later code and audits
can distinguish operator-promoted content from ordinary MCP server metadata.

Dynamic MCP tool specs are also not exposed directly as model-visible tools.
`internal/tools` mirrors the catalog internally and exposes the static gateway
tools `mcp_list_tools` and `mcp_call`; the model must discover a full
`mcp__<server>__<tool>` name before `mcp_call` validates the target schema and
invokes it.

MCP tool authority is classified by local policy, not by remote descriptions or
schemas. MCP config supports `default_tool_risk` and `tool_risks` with explicit
risk classes such as `network_read`, `external_mutation`, and `secret_access`.
Side-effecting MCP tools require local dangerous-tool policy plus an
`enabled_tools` allowlist entry for the original MCP tool name before
`mcp_call` invokes the remote handler.

## Consequences

MCP status and command metadata can show what a server reported without
granting that content instruction authority or risk-classification authority.
Operators have an explicit switch for promotion when they trust a server's
initialize instructions. Operators also have explicit local config for MCP tool
risk. Prompt metadata can be searched and displayed, but prompt invocation is
not current behavior.

The tradeoff is more deliberate MCP use: large dynamic catalogs are discovered
lazily instead of being handed to the model wholesale. Future MCP transports,
prompt invocation, or richer MCP permissions must preserve this trust boundary
or supersede this ADR.

## Verification

Relevant code paths:

- `internal/mcpclient/catalog.go`
- `internal/mcpclient/manager.go`
- `internal/mcpclient/client_test.go`
- `internal/tools/tools.go`
- `internal/tools/mcp_test.go`
- `internal/mcpstatus/status.go`
- `internal/commandregistry/registry.go`

Focused tests covering this decision include
`TestStdioLifecycleCallEnvAndRedaction`,
`TestBuildCatalogUsesLocalRiskPolicyAndLabelsMCPMetadataUntrusted`,
`TestLazyMCPGatewayHidesRawSpecsAndCanCallTool`,
`TestMCPGatewayLabelsUntrustedMetadataAndGatesSideEffectingTargets`, and
`TestBuildRegistryIncludesActionsPromptCommandsProfilesAndMCPPrompts`.
