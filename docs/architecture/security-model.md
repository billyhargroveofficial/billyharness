# Security And Trust Model

This document is the durable architecture map for Billyharness security and
trust boundaries. It consolidates the gateway, Telegram, tools, MCP, secrets,
filesystem, and public-web rules that are spread across the implementation.
It is not a checklist or runbook.

Status note: reviewed against current code paths on 2026-07-07. This document
describes current behavior, not an implementation checklist.

Primary code anchors:

- [internal/gateway/gateway.go](../../internal/gateway/gateway.go): HTTP
  route ownership, session handlers, runtime override projection.
- [internal/gateway/http_security.go](../../internal/gateway/http_security.go):
  bearer, browser, mutation, host, content-type, and privilege clamp checks.
- [internal/gateway/session_authz.go](../../internal/gateway/session_authz.go):
  session owner-header authorization.
- [internal/gateway/ingress.go](../../internal/gateway/ingress.go) and
  [internal/ingress](../../internal/ingress): external ingress admission,
  HMAC verification helpers, unsafe metadata rejection, and redacted audit.
- [internal/gateway/agentclub_events.go](../../internal/gateway/agentclub_events.go)
  and [internal/agentclub](../../internal/agentclub): neutral agent-club event
  contract validation, trusted binding policy, ingress owner scoping,
  read-only discovery, and admission-only route behavior.
- [internal/gateway/agentclub_triggers.go](../../internal/gateway/agentclub_triggers.go):
  verified trigger delivery, raw-body HMAC handling, body caps, deterministic
  event identity, and redacted trigger audit.
- [internal/gateway/response.go](../../internal/gateway/response.go):
  redacted JSON and NDJSON gateway responses.
- [internal/gatewayapi/types.go](../../internal/gatewayapi/types.go) and
  [internal/gatewayclient/client.go](../../internal/gatewayclient/client.go):
  shared session-owner DTOs and request headers.
- [internal/tools](../../internal/tools): native tool registry, policy,
  filesystem, shell, MCP gateway, and web tool handlers.
- [internal/mcpclient](../../internal/mcpclient): MCP process lifecycle,
  catalog handling, env handling, and untrusted server metadata.
- [internal/telegrambot](../../internal/telegrambot): Telegram allowlists,
  gateway scoping, send safety, and auth-command safety.
- [internal/secrets](../../internal/secrets): process-wide redaction helpers.
- [internal/webtools](../../internal/webtools): public-host-safe HTTP client.
- [internal/config](../../internal/config): security-relevant defaults,
  projections, and sanitized config status.

The route/session details live in
[Gateway and sessions](gateway-and-sessions.md). Tool/MCP/web details live in
[Tools, MCP, webtools, and rendering boundaries](tools-mcp-and-policy.md).
Telegram details live in
[Telegram and operator surfaces](telegram-and-operator-surfaces.md). Runtime
override and auth-binding details live in
[Config, provider, and context architecture](config-provider-context.md).

## Trust Boundary Summary

Billyharness is a local-first harness. The default runtime is powerful:
`internal/config/defaults.go` defaults `AutoApproveDangerous=true`,
`AccessMode=build`, workspace roots to the current working directory, gateway
address to `127.0.0.1:8765`, and MCP enabled for the configured allowlist.
That is appropriate for Billy's solo-owner local workflow, but it means the
security boundary is explicit policy plus process/network locality, not a
multi-tenant sandbox.

The major boundaries are:

- Local gateway: HTTP requests can create sessions, run agents, mutate auth,
  cancel runs, and undo/redo workspace changes. Mutating routes therefore need
  an explicit trust decision.
- Browser/loopback: loopback is convenient for TUI/browser/local tools, but a
  browser can send requests to local services. Current worktree hardening
  treats browser-originated loopback mutation as a security-relevant path.
- Bearer auth: `BILLYHARNESS_GATEWAY_AUTH_TOKEN` and legacy
  `FAST_AGENT_GATEWAY_AUTH_TOKEN` are gateway transport credentials, not
  provider credentials.
- Session owner scope: owner headers are gateway-enforced scoping claims inside
  the HTTP security boundary. They are not cryptographic identity.
- External ingress: webhook/scheduler/project triggers are untrusted until a
  local rule verifies them and the gateway admits them as session inputs.
- Agent-club ingress: external project adapters may submit normalized events
  or verified trigger deliveries, but configured registries can first require
  trusted descriptor/binding/trigger matches, and admitted events must still
  become scoped gateway inputs before any run can happen.
- Telegram: Telegram is a scoped gateway client with its own allowlist and
  send/delete safety; it does not get direct gateway-server imports.
- Tools: tool risk, access mode, workspace path checks, dangerous-tool config,
  and schema validation are the main native execution boundary.
- MCP: MCP server descriptions, schemas, prompts, initialize instructions, and
  outputs are untrusted external-process content unless operator policy
  explicitly promotes them.
- Filesystem: native filesystem tools operate inside configured workspace
  roots, with sensitive-path and symlink policy checks. Billyharness
  tool-output refs are a narrow read exception.
- Secrets: status and response paths redact secret-looking values, but secrets
  still exist in process memory, provider requests, credential files, dotenv
  files, and child processes explicitly configured to receive them.
- Public web: native web fetch/extract/crawl use a public-host-only client to
  reduce SSRF and DNS-rebinding risk.

## Gateway And Browser Trust

`Server.Handler()` always wraps the mux in `httpSecurityMiddleware`.

Current behavior:

- `/health` remains unauthenticated for cheap process liveness.
- `/ready` remains unauthenticated for bounded dependency readiness. It reports
  counts and redacted status for effective config, tool/MCP catalog health, and
  startup session-store diagnostics without raw MCP metadata, prompts, schemas,
  or store paths.
- All `/v1/` routes are treated as browser-reachable protected gateway
  surfaces. Loopback requests must use an allowed loopback host, and any
  `Origin` or `Referer` header must match the gateway host before handlers run.
- When `ServerOptions.AuthToken` is configured, `/v1/` requests require a
  matching bearer token even from loopback remote addresses. This includes
  `GET`, `HEAD`, and `OPTIONS` state-read paths.
- Mutating non-`GET`/`HEAD`/`OPTIONS` `/v1/` requests are additionally
  classified as gateway mutations.
- When `RequireMutationAuth` is true, mutation requests with a body must use
  `application/json`.
- Mutations require a matching bearer token unless
  `DevAllowUnauthenticatedLoopbackMutations` is explicitly enabled and the
  remote address is loopback.
- If mutation auth is required and no bearer token is configured, mutating
  requests receive `503` instead of silently downgrading security.
- The development loopback mutation bypass does not bypass configured-token
  protection for `/v1/` read routes.

The CLI `serve` path in
[cmd/fast-agent-harness/service_cmd.go](../../cmd/fast-agent-harness/service_cmd.go)
is part of the current worktree hardening. It prebinds the listener, requires a
gateway auth token for mutating routes unless the operator explicitly passes
`-dev-allow-unauthenticated-loopback-mutations`, and treats non-loopback or
wildcard listen addresses as requiring auth. This decision is recorded in
[ADR 0007](../adr/0007-local-gateway-mutating-routes-require-explicit-trust.md).
Configured-token protection for state-bearing `/v1/` reads is recorded in
[ADR 0008](../adr/0008-gateway-state-reads-require-bearer-when-token-configured.md).

Bearer token handling is shared through
[internal/gatewaybase/gatewaybase.go](../../internal/gatewaybase/gatewaybase.go)
and surfaced through [internal/gateway/url.go](../../internal/gateway/url.go).
Clients attach `Authorization: Bearer <token>` from
`BILLYHARNESS_GATEWAY_AUTH_TOKEN` first, then
`FAST_AGENT_GATEWAY_AUTH_TOKEN`. The current worktree bearer comparison uses
constant-time comparison in `bearerTokenMatches`.

Runtime override trust also sits at this boundary. In the current worktree,
`runOverrideSettingsForRequest` drops provider/model/thinking/reasoning
overrides unless mutation auth is disabled or the request authenticated with
the mutation bearer token. It still allows stricter request knobs such as a
lower `max_tool_rounds` or a less-privileged `access_mode`, and clamps requests
that try to raise tool rounds or access privilege above server configuration.

## External Ingress Boundary

External ingress is gateway admission, not execution. There is no public
webhook route and no scheduler. The current project-adapter surface is the
neutral agent-club event route, and future adapters must use the same shape:

- verify raw request bodies before parsing when a shared secret is configured;
- compare HMAC signatures in constant time and reject missing, stale, mutated,
  or invalid signatures;
- translate only allowlisted event sources into `IngressEvent`;
- derive deterministic `input_id` values from rule id, source, external event
  id, payload hash, and target session;
- reject payload metadata that attempts provider, model, access-mode, tool,
  MCP, shell, or command override authority;
- authorize the target session through gateway owner scope before appending the
  normal `inputs.jsonl` admission;
- write the redacted ingress audit ledger even for rejected events when a
  gateway store is configured.

The audit ledger records hashes, decision reason, target session id, admitted
input id, duplicate state, client type/id hash, and metadata keys. It does not
store raw body text, prompt text, external event IDs, metadata values, secrets,
or command payloads.

The agent-club route at `POST /v1/sessions/{id}/agentclub/events` accepts one
normalized event and then uses the same ingress admission bridge. It requires
session-owner headers with a non-empty `client_id` and `client_type=ingress`,
authorizes that actor against the target session before input admission, and
uses that actor as the ingress rule owner. Request payloads cannot set owner,
provider, model, thinking, reasoning effort, access mode, max tool rounds, MCP,
tools, shell, command, environment, browser auth, raw API, raw SQL, or dispatch
authority.

The agent-club response is redacted and admit-only. It returns admitted state,
duplicate state, input id, target session id, source, capability, event type,
payload hash, external event id hash, metadata keys, and
`run_dispatched=false`. It does not include raw prompt, raw payload, external
event id, client id, or metadata values. Agent-club capability descriptors are
metadata only (`id`, title, description, kind, risk, schemas,
`dispatch=admit_only`, approval semantics); Billyharness does not load project
manifests or execute capabilities in this slice.

When an agent-club registry is configured, the route checks descriptor and
trusted binding policy before writing ingress audit or session inputs. Bindings
are local gateway policy only: they link a capability to
`client_type=ingress`, a concrete `client_id`, optional sources, optional
event types, optional safe metadata keys, and an enabled flag. Unknown
capabilities, disabled bindings, source/event mismatches, and disallowed
metadata keys are rejected at admission time. Binding metadata cannot contain
secrets, executable commands, environment variables, raw API calls, SQL, browser
auth material, prompts, or payload values.

`GET /v1/agentclub/capabilities` is the matching read-only discovery surface.
It returns enabled descriptors and safe binding metadata visible to the current
actor, and it does not grant authority to execute anything. Scheduler daemons,
safe outputs, action approvals, project manifest loading, and capability
execution are later slices.

Verified trigger delivery is now a generic gateway admission path at
`POST /v1/agentclub/triggers/{binding_id}/deliveries`. The trusted trigger
binding supplies source, capability, event type, ingress owner, target session,
prompt, auth method, and body cap; request bodies cannot choose those fields.
Webhook bindings can require HMAC-SHA256 over the raw body, using constant-time
signature comparison before parsing. Bodies are capped before verification or
JSON decoding. Webhook event identity comes from binding id plus the configured
delivery id header; schedule/manual identity comes from binding id plus
`scheduled_at_utc`; the existing input idempotency path also includes payload
hash and target session id.

Trigger audit is redacted separately from ingress audit so failures before
normalization still leave evidence. It records binding id, trigger kind,
source/capability/event type, decision, reason, payload hash, external event id
hash, target session id, input id, duplicate state, client type, client id hash,
and metadata keys. It does not record raw bodies, prompts, delivery ids,
signatures, HMAC secrets, metadata values, headers, command lines, or adapter
secrets. Schedule/manual delivery has no daemon in this slice: future timestamps
are rejected unless explicitly marked as dry registration, and dry registration
does not admit an input.

## Session Scope

Session owner metadata is a routing and authorization claim, not a credential.
It is represented by `gatewayapi.SessionOwner` and the
`X-Billyharness-Session-*` headers in
[internal/gatewayapi/types.go](../../internal/gatewayapi/types.go). The shared
gateway client attaches those headers from `gatewayclient.WithSessionOwner`.

Committed HEAD stores owner metadata when supplied in create-session bodies and
enforces owner scope on reads and mutations through
[internal/gateway/session_authz.go](../../internal/gateway/session_authz.go):

- create-session body owner must match scoped request headers when both are
  present;
- if scoped headers are present and the create body owner is empty, the gateway
  stores the actor as owner;
- session lists are filtered for scoped actors;
- scoped actors may read their own sessions and legacy unowned sessions;
- scoped actors may not mutate legacy unowned sessions;
- cross-owner reads and mutations are denied with `403`;
- unscoped local callers remain unscoped gateway operators.

`SessionOwner.ClientID` and `X-Billyharness-Session-Client-ID` are the generic
principal for clients that do not have Telegram/TUI-specific IDs. If a stored
session owner has `client_id`, scoped actors must present the same client ID.
This lets ingress rules use a narrow owner such as `client_type=ingress` plus a
project-specific client ID instead of sharing one broad external scope.

The authority decision is recorded in
[ADR 0002](../adr/0002-gateway-owns-session-authority.md). Future clients
should use owner headers only after the transport request is already trusted by
gateway auth, loopback exposure, or another deployment boundary.

## Telegram Boundary

Telegram is a scoped gateway client, not a runtime peer. The package boundary
in [docs/architecture.md](../architecture.md) forbids
`internal/telegrambot` from importing `internal/gateway`; Telegram talks to the
gateway through [internal/telegrambot/gateway_client.go](../../internal/telegrambot/gateway_client.go)
and `internal/gatewayclient`.

Telegram has two separate trust checks:

- Telegram admission/send authorization in
  [internal/telegrambot/authz.go](../../internal/telegrambot/authz.go).
- Gateway transport and session authorization through bearer auth plus
  Telegram owner headers.

Live Telegram sending is fail-closed unless the operator scopes it. When real
sending is enabled and dry-run is false, the CLI requires at least one allowed
chat ID, one allowed user ID, or the explicit allow-all option. Runtime
admission accepts a message only when `AllowAllChats` is set, the chat/user is
allowlisted, or no allowlist is configured and `RequireAllowlist` is false.
Admission allowlists are not operator authority. Operator-only commands require
an identified non-bot sender in `AllowedOperatorUserIDs`, falling back to
`AllowedUserIDs` only when no operator set is configured.

Secret-bearing Telegram auth commands have extra safety in the current
worktree. `/auth deepseek ...` in
[internal/telegrambot/commands.go](../../internal/telegrambot/commands.go)
is accepted only in a private owner chat and must delete the source Telegram
message before persisting the key; if Telegram does not provide a deletable
message ID or deletion fails, the key is not saved. Save errors are redacted
against the submitted key before being sent back to chat. Secret-bearing group
commands are rejected before local persistence, including in dry-run mode.
`/auth codex` imports local Codex OAuth through the gateway and does not accept
a token pasted into Telegram.

Telegram owner scope is attached from
[internal/telegrambot/session_owner.go](../../internal/telegrambot/session_owner.go):
`client_type=telegram`, chat ID, thread ID, user ID, profile, and model. Local
Telegram filtering for `/resume` and `/fork` is defense in depth; gateway
session authorization is the hard boundary in the current worktree.

## Tool Policy Boundary

Native tool execution enters through `Registry.Call()` in
[internal/tools/tools.go](../../internal/tools/tools.go). The order is:
model-visible tool lookup, empty-argument normalization, central policy check,
schema validation, then handler execution.

Tool risk comes from `internal/protocol`: `read_only`, `network`, `write`,
`execute`, and `external`. The central decision point is
[internal/tools/policy.go](../../internal/tools/policy.go):

- `access_mode=plan` hides and denies `write`, `execute`, and `external`.
- `access_mode=guarded` denies `write` and `execute`.
- `access_mode=build` allows the normal build-mode surface.
- `write` and `execute` require `AutoApproveDangerous`; if disabled, the call
  returns a permission-denied result before handler execution.
- `external` tools require approval metadata and are blocked by plan mode, but
  are otherwise allowed by the existing build/guarded policy.

The default config in `internal/config/defaults.go` is powerful:
`AutoApproveDangerous=true` and `AccessMode=build`. This means default local
operation trusts the operator's workspace and agent loop. Safer sessions should
lower access mode or disable dangerous auto-approval through config/runtime
policy.

Shell tools are `execute` risk. `shell_exec` resolves its `cwd` through
`safePath`, so commands run in an allowed workspace directory. It also blocks
selected destructive git shapes such as `git reset --hard`, `git clean -f`,
workspace-wide `git checkout` or `git restore`, stash deletion, and force push.
Background shell processes are Billyharness-owned, capped, listed by opaque
IDs, and polled/killed through shell process tools in
[internal/tools/shell_process.go](../../internal/tools/shell_process.go).

Filesystem write/edit tools are `write` risk. `fs_write_file` writes UTF-8
content under a workspace root. `fs_edit_file` works on an existing regular
UTF-8 file, applies exact replacements, can require `expected_sha256`, writes
atomically, and rejects no-op or ambiguous replacements.

## Filesystem Workspace Boundary

Filesystem and shell path policy is implemented by `safePath` in
[internal/tools/tools.go](../../internal/tools/tools.go). It resolves relative
paths against the first configured workspace root, uses absolute and symlink-
resolved policy paths, rejects sensitive-looking paths, and allows only paths
inside configured workspace roots.

Sensitive path detection is intentionally broad rather than precise. Paths
containing markers such as `.env`, `.ssh`, `id_rsa`, `id_ed25519`,
`auth.json`, `token`, `secret`, `.aws`, `.kube`, or `.docker` are refused by
filesystem tools even when they are under a workspace root.

`fs_read_file` has one explicit exception in
[internal/tools/fs_read.go](../../internal/tools/fs_read.go): it can read
absolute paths under `$BILLYHARNESS_HOME/tool-output` because tool-output refs
are how large web/tool artifacts are stored out of band. Ordinary files under
`$BILLYHARNESS_HOME` remain blocked unless they are also under an allowed
workspace root. [internal/tools/output_ref.go](../../internal/tools/output_ref.go)
writes tool-output directories as `0700` and files as `0600`.

This is a path boundary, not an OS sandbox. A shell command that is allowed to
run can still exercise normal process privileges within its working directory
and whatever the operating system permits. The policy boundary is designed for
local operator control and replayable guardrails, not hostile multi-user code
execution.

Checkpoint undo/redo adds a restore-time boundary on top of ordinary tool
execution. Gateway restore paths verify the recorded patch artifact SHA-256,
reject symlink or non-regular patch artifacts, require configured workspace
roots, and recheck restored file paths plus symlink ancestry before writing
files. Tampered, moved, out-of-root, or symlink-escaping patch records fail
closed before workspace mutation.

## MCP Boundary

MCP servers are external processes or, in future, endpoints. Their tool
descriptions, schemas, prompts, initialize instructions, stdout/stderr, and
tool outputs are untrusted server content.

Current MCP hardening properties:

- MCP config is loaded from `$BILLYHARNESS_HOME/mcp.config.toml` when enabled
  and no explicit server list is already present.
- The default allowlist is `telegram`, `telegram-parilka`, `github`, and
  `context7` in `internal/config/defaults.go`.
- Streamable HTTP MCP config is parsed and reported as unsupported; it is not
  connected or called.
- Stdio commands such as shells and PowerShell are rejected as MCP launchers in
  [internal/mcpclient/stdio.go](../../internal/mcpclient/stdio.go).
- MCP `cwd` resolves inside configured workspace roots.
- Child environments contain a small parent allowlist, explicitly requested
  `env_vars`, and literal configured `env`.
- Secret-like configured env values are redacted from MCP errors, stderr tails,
  and tool output.
- Optional startup failures remain visible in status; required failures fail
  manager initialization.
- Enabled/disabled tool filters are applied before catalog publication.
- MCP tool risk is local operator policy from `default_tool_risk` or
  `tool_risks` in MCP config. Remote tool descriptions and schemas are
  untrusted metadata and do not classify their own authority.
- Side-effecting MCP risk classes (`local_write`, `network_write`, `execute`,
  `external_mutation`, and `secret_access`) require the normal dangerous-tool
  policy and an explicit `enabled_tools` allowlist entry for the original MCP
  tool name before `mcp_call` executes the remote handler.
- Sanitized MCP tool-name collisions fail catalog initialization.
- Dynamic MCP tool specs are mirrored internally and reached through static
  gateway tools such as `tool_search`, `mcp_list_tools`, and `mcp_call`.

MCP initialize instructions are metadata-only by default.
[internal/mcpclient/catalog.go](../../internal/mcpclient/catalog.go) separates
`ServerInstructions` metadata from promoted `Instructions`. Server instruction
metadata is tagged with `trust=untrusted_mcp_server_metadata`.
`Manager.Instructions()` is empty by default; initialize instructions enter
model context only when `MCPPromoteServerInstructions` is enabled, and promoted
text is tagged with `trust=operator_promoted_mcp_initialize_instructions`.
That decision is recorded in
[ADR 0003](../adr/0003-mcp-instructions-are-untrusted-metadata.md).

MCP prompt catalogs are metadata only. They may appear in status and command
metadata, but prompt invocation is not current behavior.

## Secret Redaction

Secret handling is layered:

- `internal/credentials` resolves and persists DeepSeek API keys and Codex auth
  payloads. Status values report `credential=redacted` instead of raw tokens.
- `internal/config.Resolve` tracks redacted config keys, and status surfaces
  use `SanitizedValues` and `SanitizedConfig`.
- Gateway JSON and NDJSON responses pass through `marshalRedactedJSON` in
  [internal/gateway/response.go](../../internal/gateway/response.go), which
  delegates recursive JSON redaction to `internal/secrets`.
- Providers redact API tokens and OAuth tokens from HTTP error bodies.
- Hooks, MCP status/errors, Telegram client errors, Telegram auth-command save
  failures, Telegram outbound delivery/rendered run errors, transcript export,
  and incident bundle artifacts use the shared `internal/secrets` redactor.

`internal/secrets.Redact` is pattern and environment based. It handles common
bearer and proxy-auth headers, cookie and API-key headers, credential-bearing
URLs, secret query parameters, token/api-key/password fields, common provider
and GitHub tokens, Telegram bot-token URLs, MCP-style secret argv flags, JWTs,
Yandex tokens, and image data URLs. It also replaces secret-looking environment
values whose variable names contain token, secret, password, api_key, or
apikey. Structured helpers redact JSON strings, JSON object keys, URL
credentials, and secret-looking argv pairs without changing the durable event
log as the replay source of truth.

Redaction is a leak-reduction boundary, not a guarantee that secrets never
exist. Secrets still exist in local files, environment variables, provider
requests, memory, and child processes that are explicitly configured to receive
them. Durable docs and status surfaces should avoid echoing raw config, raw
Telegram messages containing keys, or credential-bearing URLs.

## Public Web Boundary

Native web tools use [internal/webtools.Client](../../internal/webtools/client.go)
for HTTP fetches. It is designed for public web access, not local-network
fetching:

- only `http` and `https` schemes are allowed;
- empty hosts are rejected;
- `localhost`, `*.localhost`, loopback, private, link-local, multicast, and
  unspecified IPs are rejected;
- hostnames are resolved before the request;
- redirects are revalidated before following;
- dialing re-resolves and rechecks public addresses, which blocks public-to-
  private DNS rebinding before the second connection;
- non-2xx responses return bounded body text.

`web_fetch`, `web_extract`, and `web_crawl` store full extracted text in
tool-output refs and return compact inline digests by default. Inline raw text
requires explicit `include_text` or `full_text` and is still capped. Provider
web backends in `internal/webtools/backends.go` use configured API keys and
bounded responses; missing backend keys fail explicitly rather than silently
changing auth behavior.

## Current Truth Boundaries

Current code truth:

- Gateway bearer auth exists only when an auth token is configured.
- `/health` bypasses bearer auth for cheap liveness.
- `/ready` bypasses bearer auth for bounded readiness summaries.
- Configured bearer auth protects `/v1/` reads and mutations, including
  loopback callers.
- Mutating `/v1/` gateway routes require bearer trust or an explicit loopback
  development bypass when `RequireMutationAuth` is true.
- The `serve` command refuses to start mutating routes without a token unless
  that explicit development bypass is set.
- Browser-oriented host, origin/referer, and JSON content-type checks run
  before mutating gateway handlers.
- Provider/model/reasoning per-run overrides are bearer-gated under mutation
  auth; tool-round and access-mode requests are clamped.
- Session owner headers scope list/read/mutation behavior.
- External ingress is admitted through gateway session inputs and redacted audit,
  not through direct tool, MCP, shell, or provider override execution.
- Agent-club ingress admits only normalized external adapter events as queued
  inputs; it does not expose generic project commands, schedulers, webhooks,
  auto-run, browser auth, raw APIs, raw SQL, or project-specific actions.
- Telegram carries owner scope through gateway requests and hardens
  secret-bearing auth commands.
- MCP initialize instructions are metadata-only by default and require explicit
  operator promotion before model-context injection.

## Verification Anchors

Documentation-only verification for this page should include:

```sh
git diff --check
go test -count=1 ./internal/architecture
```

When changing the security behavior itself, use focused package tests in the
owning packages. The most relevant current test names are:

- `TestGatewayAuthMiddlewareProtectsNonLoopbackClients`
- `TestGatewayMutationAuthProtectsLoopbackBrowserRoutes`
- `TestGatewayMutationAuthExplicitDevLoopbackBypass`
- `TestGatewayRunRequestPrivilegeClamps`
- `TestGatewaySessionOwnerMetadataPersistsAndLists`
- `TestGatewaySessionOwnerScopeFiltersAndDeniesCrossOwner`
- `TestGatewaySessionClientIDOwnerScopeFiltersAndDeniesCrossOwner`
- `TestGatewayIngressAdmitsAuditsDuplicatesAndConflictsWithoutDispatch`
- `TestGatewayIngressAuditsRejectedAdmission`
- `TestAgentClubEventRouteAdmitsInputAuditsAndDoesNotDispatch`
- `TestAgentClubEventRouteRequiresIngressOwnerHeaders`
- `TestAgentClubEventRouteDeniesCrossOwnerBeforeInputWrite`
- `TestStdioLifecycleCallEnvAndRedaction`
- `TestRunMessagesDoesNotInjectUntrustedMCPInstructionsByDefault`
- `TestClientRejectsLocalhostAndRFC1918Targets`
- `TestClientRejectsRedirectToPrivateIPBeforeSecondDial`
- `TestClientRejectsPublicThenPrivateRebinding`
- `TestTelegramAuthDeepSeekDeletesSecretMessageAndDoesNotRenderKey`
- `TestTelegramAuthDeepSeekRedactsSaveError`
