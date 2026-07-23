# Config, Provider, And Context Architecture

This document is the durable architecture map for runtime configuration,
provider/auth binding, model capability metadata, instruction assembly, memory
injection, skills discovery, and context accounting. It describes current
behavior as implemented in code; it is not an active work checklist.

Status note: this document was reviewed against the dirty current worktree on
2026-07-03. Claims describe this checkout, not necessarily a clean release
commit.

## Package Responsibilities

- `internal/config` owns the `Config` struct, config resolution, provenance,
  projections, runtime diffs, diagnostics, MCP/hook loading hooks, and profile
  metadata.
- `internal/modelinfo` is the local provider/model catalog and capability
  policy checker. It is a leaf package and does not fetch live provider
  metadata.
- `internal/credentials` owns credential lookup, persistence, and auth status
  formatting. `internal/codexauth` is pure JWT/auth-payload parsing.
- `internal/provider` builds DeepSeek, Qwen Cloud Token Plan, Codex, and mock providers from
  `config.ProviderBinding`; it validates capability policy before loading
  provider credentials.
- `internal/instructions`, `internal/projectcontext`, and `internal/memory`
  render bounded prompt fragments from local files and project metadata.
- `internal/skills` discovers, views, and imports local `SKILL.md` bundles. It
  is tool-facing metadata, not automatic prompt injection.
- `internal/runstate` records per-turn runtime snapshots, prompt inventory,
  prompt cache break reasons, and approximate context accounting.

These boundaries are enforced at the package-map level by
`docs/architecture.md` and `internal/architecture`.

## Config Resolution And Provenance

The resolver is `internal/config.Resolve`. It starts from `builtInConfig`, then
applies these layers in order:

1. `$BILLYHARNESS_HOME/config.toml`.
2. The nearest project `.billyharness/config.toml`.
3. `$BILLYHARNESS_HOME/settings.json` remembered UI/runtime choices.
4. dotenv values from `findDotenvFiles`.
5. process environment variables.
6. explicit `ResolveOverride` values from CLI, gateway, or TUI runtime state.
7. profile metadata from `$BILLYHARNESS_HOME/profiles/<profile>/profile.toml`
   or the built-in `billy` profile.

Every tracked key is declared in `configSpecs` in
`internal/config/resolved.go`. That table is the source of truth for config key
names, environment variable names, parsers, and redaction. The resolver records
each effective value as `ResolvedValue` with source, source key, optional source
path, warning, error, and redaction status. Human summaries use
`config.FormatSummary`; JSON status surfaces use `SanitizedValues` and
`SanitizedConfig` so redacted values do not leak.

Dotenv lookup is deliberately ordered. `findDotenvFiles` checks
`$BILLYHARNESS_HOME/.env` first. If `FAST_AGENT_ENV_FILE` is set, that explicit
file replaces the normal home-plus-CWD search unless
`BILLYHARNESS_DOTENV_HOME_ONLY=true` is set, in which case only the
Billyharness home dotenv is considered. Process environment still wins over
dotenv. Credential persistence uses the same effective dotenv target for
DeepSeek API-key saves: `FAST_AGENT_ENV_FILE` when set and allowed, otherwise
`$BILLYHARNESS_HOME/.env`. A read-only, directory, or otherwise unwritable
active dotenv path fails closed instead of writing a different fallback file.

Project config is not fully trusted for auth endpoints and credential paths.
`projectConfigDeniedKey` ignores these keys in project config and records a
warning: `base_url`, `api_key_env`, `credential_file`, `codex_base_url`,
`codex_auth_file`, `codex_refresh_url`, `codex_auth_api_base_url`,
`codex_client_id`, and `codex_originator`. Current behavior still allows
project config to set non-denied runtime keys such as `model`, `provider`,
`profile`, and tool/runtime limits; model/provider normalization may later
derive a different provider and warn.

Profile metadata is applied late but has special precedence. It does not
override dotenv, environment, CLI, or gateway sources. It does not override
remembered `settings.json` values unless the profile itself was selected from
an explicit source such as home config, project config, dotenv, environment,
CLI, or gateway override. Profile metadata can set provider/model/thinking,
reasoning effort, `disable_spark`, context window, web summary mode, MCP
allowlist, instruction fragments, and cost hints as defined by
`ProfileMetadata`. Instruction fragments are active prompt inputs: they are
loaded in declared order from the profile directory, must be relative paths
inside that directory, and missing, directory, symlink, absolute, or traversal
fragments are skipped.

`ResolveStrict` wraps `Resolve` and fails when any resolved value has a typed
parse error. CLI runtime entry points use it through
`cmd/fast-agent-harness/runtime_config.go`, and the local TUI startup uses it in
`internal/tui/tui.go`. Plain `Resolve` is still used for status/reporting paths
such as `/v1/config` and TUI config summaries so invalid values can be shown
with warnings instead of hiding the diagnostics. `Default` calls `MustResolve`;
on resolver failure it returns defaults plus a warning rather than failing the
process.

After resolution, config is projected into smaller contracts:

- `ProviderBinding` combines provider selection, model selection, auth settings,
  and runtime limits for provider construction.
- `ToolPolicySettings`, `DiagnosticsSettings`, `MCPSettings`, `HookSettings`,
  and `InstructionSettings` expose only the fields needed by downstream owners.
- `RuntimeDiffSettings` and `RuntimeDiffOverridesFromSettings` round-trip
  gateway/TUI runtime state back through the resolver with
  `SourceGateway` provenance for status display.

## Runtime Overrides

CLI flags are converted to `ResolveOverride` values with `SourceCLI`. Gateway
and TUI runtime state use `SourceGateway` when reconstructing current config
status. A full runtime diff can carry provider/auth/runtime/tool/MCP/hook state,
but per-run request overrides are intentionally narrower.

`RunOverrideSettings` currently supports provider, model, profile, thinking,
reasoning effort, max tool rounds, and access mode. In the gateway,
`runOverrideSettingsForRequest` drops provider/model/thinking/reasoning
overrides unless mutation auth is disabled or the request was authenticated
with the mutation bearer token. Max tool rounds default to `0`, which means no
round cap for normal runtime loops. When a positive configured cap exists,
`clampMaxToolRoundsOverride` prevents a run from raising max tool rounds above
that configured limit, and
`clampAccessModeOverride` prevents a request from escalating above the
configured access mode.

Per-session snapshots use the same projection machinery. The gateway combines
the server's runtime settings with session status, builds a
`runstate.Snapshot`, and persists sanitized config/model/MCP state for replay
and inspection.

## Provider And Auth Binding

`provider.NewFromBinding` is the provider factory. It first resolves the
effective provider with `modelinfo.ProviderForModel`, then calls
`modelinfo.ValidateCapabilityPolicy` with streaming required, tool calls
required for non-mock providers, and parallel tool calls required when
`MaxParallelTools > 1`. This validation happens before credential lookup, so
unsupported model/provider/capability combinations fail without probing secrets.

Known OpenAI-compatible providers carry their official base URL and API-key
environment name in `internal/modelinfo`. `ProviderSelection` and
`AuthSettings` derive those values when a model change crosses a known provider
family, while an explicit trusted transport override normally remains
authoritative. Qwen Token Plan is the exception: both config projection and
provider construction force its official endpoint and
`QWEN_TOKEN_PLAN_API_KEY`, so that subscription credential cannot be redirected
by a stale or custom transport override. This also prevents a runtime switch
from keeping DeepSeek's endpoint or credential name when
`qwen3.8-max-preview` is selected.

DeepSeek uses the OpenAI-compatible chat completions transport in
`internal/provider/provider.go`. Its API key is resolved by
`credentials.Manager.ResolveDeepSeekAPIKey` from the configured env var through
the shared environment/dotenv lookup, then the configured credential JSON file.
The shared lookup honors `FAST_AGENT_ENV_FILE` and
`BILLYHARNESS_DOTENV_HOME_ONLY` as described above. The request includes
streaming, optional tools, `max_tokens`, and DeepSeek thinking/reasoning fields
when configured. Provider errors redact secrets and classify transport, auth,
rate limit, bad request, context overflow, server, stream closed, and unknown
failures.

Qwen Cloud Token Plan reuses the OpenAI-compatible chat-completions transport
with a distinct provider identity. The only catalogued Qwen model is
`qwen3.8-max-preview`; arbitrary Qwen-looking IDs remain unknown and fail
capability validation. Its binding is fixed to
`https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1` and
`QWEN_TOKEN_PLAN_API_KEY`, and its cost mode is `subscription`.

For that exact model the request always sends `enable_thinking=true` and
`preserve_thinking=true`. Shared reasoning values map onto the provider's
`low`, `medium`, and `xhigh` values; an off/minimal setting becomes `low`, and
high/max becomes `xhigh`. Qwen uses `max_completion_tokens` so the configured
limit covers reasoning plus final output. It sends
`parallel_tool_calls=true` when tools are present and
`MaxParallelTools > 1`.
Agent snapshots and `model.call_started`/`model.call_finished` provenance use
the same effective values (`enabled/low`, `enabled/medium`, or
`enabled/xhigh`) rather than the pre-normalization shared setting.

Qwen requires historical assistant `reasoning_content` to be replayed
verbatim when thinking is preserved. The agent therefore retains it in memory
between tool rounds. If `StoreReasoningContent` is false, the returned
transcript is scrubbed before gateway/session persistence; if true, the normal
explicit persistence behavior applies. This preserves tool-loop correctness
without silently changing the persistence setting.

Codex/OpenAI subscription models use the Responses transport in
`internal/provider/codex_provider.go`. Auth is loaded by `loadCodexAuth` from
`CODEX_ACCESS_TOKEN`/`CODEX_CHATGPT_ACCOUNT_ID` or the configured Codex auth
file. The auth parser accepts OAuth token payloads and personal access tokens;
it rejects API-key, Bedrock API-key, and agent-identity auth modes because this
provider path does not implement those modes. OAuth access tokens are refreshed
with the configured refresh URL and client ID when needed, and successful
refreshes are written back atomically to the auth file. Personal access tokens
are hydrated through the configured auth API base URL to discover account
metadata.

Codex request building collects all system messages into the Responses
`instructions` field. User messages become `input_text` and, for image
attachments, `input_image` parts loaded from the attachment store. Assistant
tool calls and tool outputs are converted to Responses function-call records.
Tools are sent with `strict=false`; `parallel_tool_calls` is disabled when no
tools are present. Reasoning is included only when the configured reasoning
effort is not off.

Auth status is separate from provider construction. `credentials.Status`
reports DeepSeek, Qwen, and Codex configured/missing state, redacted
credentials, account ID, expiry, refresh status, active runtime provider/model,
and cost mode. It is used by gateway/TUI auth and diagnostics surfaces. Qwen
has no credential-write HTTP route; its Token Plan key is supplied through the
fixed environment name or effective dotenv/credential lookup.

## Model Catalog And Capabilities

`internal/modelinfo` is a local catalog. It knows DeepSeek V4 flash/pro,
Qwen Cloud Token Plan `qwen3.8-max-preview`, Codex/OpenAI subscription model
families such as `gpt-5.5`, `gpt-5.4-mini`, and `gpt-5.3-codex-spark`, plus
mock models. Unknown `gpt-`, `o1`, `o3`, and `o4` model IDs are treated as
Codex-family with inferred Codex defaults.
Unknown custom OpenAI-compatible models are allowed only when the provider is
custom or mock and the caller explicitly allows unknown models.

The catalog owns aliases, provider routing, cost mode, subscription hints,
context window size, max output tokens, input modalities, vision support,
reasoning modes, tool-call support, parallel tool-call support, streaming
support, token/cache accounting field names, and helper-model defaults for web
summary and memory. It is used by config defaults, diagnostics, provider
construction, and context-window derivation.

Config normalization applies `modelinfo.NormalizeAlias`, `ProviderForModel`,
model-derived context windows, and context compaction defaults. The default
runtime is DeepSeek `deepseek-v4-flash` with a model-derived 1,000,000-token
context window and a 60 percent compaction threshold. Codex/GPT subscription
models default compaction to 90 percent of their selected context window unless
`context_compact_tokens` is explicitly overridden. Codex-family models route to
`openai-codex`; DeepSeek-family models route to `deepseek`;
`qwen3.8-max-preview` routes to `qwen`. The Qwen model-derived context window
is 983,616 tokens and its maximum completion budget is 131,072 tokens. If an
explicit provider conflicts with a known model family, the model wins and the
resolver records a warning. When `DisableSpark` is true and the selected
shorthand alias is `spark`, the model is replaced with `gpt-5.4-mini`.

## Instruction Assembly

Initial runtime messages are built by `agent.InitialMessagesFromSettings` in
this order:

1. The built-in Billyharness system prompt.
2. Optional profile `SOUL.md` as a system message.
3. Optional memory summary as a user message.
4. Optional project context as a user message.
5. Optional AGENTS-style instructions as a user message.

Operator-promoted MCP server instructions are injected later by
`Agent.withMCPInstructions` when `MCPPromoteServerInstructions` is enabled,
after the protected prefix and before ordinary conversation messages.
Unpromoted MCP server instructions remain metadata only. Compaction treats the
system prompt, profile, memory context, project context, AGENTS instructions,
and promoted MCP instructions as a protected prefix.

`internal/instructions` loads global instructions from `$BILLYHARNESS_HOME` or
`$CODEX_HOME`, then project instructions by walking from the git root to the
current workspace directory. It prefers `AGENTS.override.md`, then `AGENTS.md`,
then configured fallback filenames. Project instruction bytes are capped by
`ProjectDocMaxBytes`. Loaded instruction sources record path, scope, byte
count, SHA-256, and capping.

Profile instructions come from the profile metadata `instruction_fragments`
list, defaulting to `SOUL.md`. Fragments are read from
`$BILLYHARNESS_HOME/profiles/<profile>/` in list order and concatenated into
one profile system message. The default `billy` profile and metadata are
created on demand. Profile metadata and profile prompt text stay separate:
metadata changes config projections, while loaded instruction fragments become
the profile system message.

## Project Context

`internal/projectcontext` renders a bounded `<PROJECT_CONTEXT>` fragment when
`ProjectContextMaxBytes > 0`. The snapshot includes cwd, workspace roots, git
root, detected package managers, likely test/build commands, instruction source
metadata, and env-file hints.

Project context does not ingest README prose, `.env` values, shell history,
watcher state, databases, or provider calls. Env hints expose variable names
from files like `.env.example`, `.env.sample`, `.env.template`,
`.env.local.example`, and `.env`; they do not include values. The rendered
fragment is capped, UTF-8 safe, and includes cap flags when roots, commands,
instruction sources, env files, env vars, or the rendered body were capped.

Before a run starts, `projectcontext.ReconcileMessages` refreshes the existing
project-context message if the rendered body hash changed. It replaces the
message in place with a `# Project context updated` marker rather than adding
another context message.

## Memory Injection And Memory Tools

Memory is file-backed and summary-first. `internal/memory.Roots` loads a home
root at `$BILLYHARNESS_HOME/memory` and, when a profile is active, a profile
root at `$BILLYHARNESS_HOME/profiles/<profile>/memory`. Each root is indexed by
`MEMORY.md` lines with `type`, `topic`, `summary`, and relative `path` fields.

The prompt fragment is only a summary index. It renders at most
`MaxPromptEntries` entries, defaults to a 2 KiB summary cap, and includes the
policy line `summary_only; read topic files explicitly when exact details are
needed`. Topic bodies are not read into the initial prompt. Index reads, topic
body reads, and rendered summaries are bounded by `MemoryIndexMaxBytes`,
`MemoryTopicMaxBytes`, and `MemorySummaryMaxBytes`.

Memory paths must be relative to the memory root and must not escape it.
Prompt-like summaries containing memory-context markers or instructions such as
"ignore previous instructions" are blocked or rejected. Memory auto extraction
is a config field, but it currently defaults to false; the prompt path injects
only existing indexed summaries.

The memory tool registry exposes read-only list/search/read tools and write
tools for add/replace/remove. Mutating operations are previews unless
`confirm=true`, and writes use 0700 directories plus 0600 files.

## Skills Discovery

`internal/skills` discovers local `SKILL.md` files under
`$BILLYHARNESS_HOME/skills` and project `.billyharness/skills`; optional
compatibility discovery can include `.claude/skills` and Hermes skill
directories. It parses lightweight frontmatter for name, description, and tags,
lists bounded metadata, and can view the main skill file or support files.

Support-file viewing is constrained to relative paths under `references/`,
`templates/`, `scripts/`, or `assets/`. Symlinked skill files and support files
are skipped or rejected. Import copies a discovered non-home skill into the home
skills directory with import metadata. Skills are not injected by
`InitialMessagesFromSettings`; agents use the skills tools explicitly.

## Context Accounting And Prompt Cache State

`internal/runstate.NewSnapshot` records the runtime state that matters for a
model turn: provider ID, model ID, reasoning mode, context budget, tool schema
hash, MCP status/catalog hash, profile/instruction hash, prompt inventory,
permission mode, access mode, config hash, and context epoch. The context epoch
is hash-only: it carries an aggregate epoch hash plus the config, tool catalog,
MCP catalog, AGENTS, memory, project-context, docs-index, MCP-instructions, and
prompt-inventory hashes when those inputs are available. `WithPromptCacheBreak`
compares the current snapshot to the previous turn and reports which fields,
prompt sections, or epoch inputs changed.

Prompt inventory is approximate. It records protected/system/context prompt
sections and tool schemas with byte counts, SHA-256 values, and token estimates
using a simple chars-divided-by-four estimator. Provider usage events later
carry real input/output/cache/reasoning token counts when the provider reports
them.

Context threshold events use the same approximate estimator and fire once per
run at 50, 70, 85, and 95 percent of `ContextWindowTokens`. Compaction triggers
when either estimated message tokens or observed provider prompt tokens reach
`ContextCompactTokens`. Deterministic compaction replaces older unprotected
conversation with a system summary while preserving the protected prefix and
the most recent `ContextCompactKeep` messages. If
`ContextCompactStrategy=model`, Billyharness first performs deterministic
replacement and then asks the configured helper provider/model to rewrite the
summary; helper usage is emitted separately.

The current architecture does not have tokenizer-accurate preflight budgeting
or live provider capability discovery. Those are not current behavior and should
not be documented as such elsewhere.

## Current Hardening Boundaries

Current hardening that is already implemented:

- strict config failure exists through `ResolveStrict` for startup/runtime
  entry points that opt into it;
- non-strict status paths preserve diagnostics and warnings;
- project config cannot override configured auth endpoints and credential
  paths;
- resolved config and auth status redact secret values;
- provider capability policy is checked before credential lookup;
- memory prompt injection is summary-only, capped, path-confined, and sanitizes
  prompt-like summaries;
- project context exposes env variable names, not env values;
- per-run gateway overrides cannot raise positive max-round caps or access
  privilege, and
  provider/model overrides are gated when mutation auth is required;
- skills discovery is local, bounded, and support-file reads are path-confined.

Current behavior that should not be overstated:

- plain `Resolve` and `MustResolve` can continue with warnings; only
  `ResolveStrict` fails closed on typed config errors;
- the model catalog is maintained in code and may be stale relative to provider
  reality;
- project config can still set non-denied runtime routing keys such as
  `provider`, `model`, and `profile`;
- approximate context estimates are not authoritative token counts;
- memory auto extraction is not enabled by default and is not part of the
  initial prompt path.
