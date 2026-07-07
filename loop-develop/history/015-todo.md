# 015 - Agent-Club Trusted Config And Enable UX

## Goal

Make agent-club usable as a general Billyharness ingress surface that can be
enabled by operator-owned local config, not only by tests or custom Go wiring.

This is the missing layer between the current generic contract and future
project adapters:

- Billy can describe which external capabilities are trusted.
- Billy can bind those capabilities to concrete ingress owners.
- Billy can register webhook/schedule/manual trigger bindings.
- Billy can validate, list, enable, and disable the bindings from CLI.
- The gateway can load the same policy at startup.
- Secrets stay out of committed config, status responses, audit, and logs.

Do not implement an executor in this TODO. This slice still admits events and
trigger deliveries as queued session inputs only. Approving proposals still
does not apply anything.

## Current Local Finding

The core contract is already present:

- `internal/agentclub/contract.go` has `EventRequest`,
  `CapabilityDescriptor`, risk classes, `dispatch=admit_only`, and mapping to
  gateway ingress.
- `internal/agentclub/registry.go` has `Registry`, `TrustedBinding`,
  `CapabilityListResponse`, and match/list logic.
- `internal/agentclub/triggers.go` has `TriggerBinding`, webhook/manual/schedule
  delivery mapping, HMAC auth, body caps, and redacted response fields.
- `internal/gateway/agentclub_events.go`,
  `internal/gateway/agentclub_triggers.go`, and
  `internal/gateway/agentclub_proposals.go` expose the HTTP surfaces.
- `cmd/fast-agent-harness/agentclub_cmd.go` can inspect capabilities and manage
  proposal decisions through the gateway.

The missing production seam is startup/local policy wiring:

- `internal/gateway/gateway.go` only receives `AgentClubRegistry` through
  `ServerOptions`.
- `cmd/fast-agent-harness/service_cmd.go` starts the gateway without loading any
  agent-club config.
- Current registries mostly exist in tests and caller-provided options, so a
  real operator cannot persist trusted capabilities or triggers in the
  Billyharness home directory.
- Existing config precedents live in `internal/config/mcp.go` and
  `internal/config/hooks.go`: default files under `$BILLYHARNESS_HOME`, explicit
  config-file overrides, env-var secret references, validation before runtime
  use, and redacted status.

## Source Research Summary

The design below intentionally borrows the stable patterns, not the code, from
larger systems.

- MCP Registry separates metadata from package code. Its `server.json` metadata
  records unique names, location, execution instructions, env vars, descriptions,
  capabilities, and standardized installation/configuration information, while
  trust comes from namespace verification and downstream scanning/curation.
  Billy-facing failure fixed: without metadata/config separation, every project
  adapter turns into a one-off hardcoded integration instead of a reusable local
  policy entry.
  Source: https://modelcontextprotocol.io/registry/about

- MCP tools are named, schema-described capabilities that hosts may expose
  through different UX patterns; the protocol does not force one UI model.
  Billy-facing failure fixed: Billyharness should describe capability contracts
  and schemas without assuming TUI, Telegram, webhook, or future scheduler is
  the only entrypoint.
  Source: https://modelcontextprotocol.io/specification/2025-06-18/server/tools

- OpenAI Apps SDK requires tool descriptors to carry input/output schemas,
  security schemes, UI/resource metadata, and annotations such as read-only,
  destructive, idempotent, and open-world hints; those hints guide clients but
  servers must still enforce authorization.
  Billy-facing failure fixed: model-visible metadata cannot be the authority
  boundary; Billyharness needs local gateway enforcement around descriptors.
  Source: https://developers.openai.com/apps-sdk/reference
  Source: https://developers.openai.com/apps-sdk/plan/tools

- MCPB desktop-extension manifests put extension metadata, server launch config,
  static tools/prompts, dynamic capability flags, compatibility, and
  `user_config` in a manifest. Sensitive user config is marked sensitive and
  injected as env at runtime instead of being copied into ordinary args/status.
  Billy-facing failure fixed: webhook HMAC secrets and future adapter tokens
  must be referenced by name, not stored inside agent-club manifests or shown by
  `agentclub capabilities`.
  Source: https://github.com/modelcontextprotocol/mcpb/blob/main/MANIFEST.md

- Activepieces pieces split the integration definition into auth, actions, and
  triggers; triggers explicitly choose polling or webhook and expose user-facing
  names/descriptions separately from implementation.
  Billy-facing failure fixed: a trigger is not the same thing as a capability;
  Billy needs both a trusted capability binding and separate trigger binding.
  Source: https://www.activepieces.com/docs/build-pieces/building-pieces/create-trigger
  Source: https://www.activepieces.com/docs/build-pieces/piece-reference/authentication

- n8n custom-node starter keeps credentials, nodes, examples, linting, and
  runtime development workflow separate. Its GitHub Issues example models
  multiple resources/operations plus separate credential methods.
  Billy-facing failure fixed: the CLI should validate and inspect config before
  the gateway is started, instead of making every adapter debug cycle require
  a running webhook.
  Source: https://github.com/n8n-io/n8n-nodes-starter

- Windmill resources are JSON objects for configuration and credentials, typed
  by resource types that use JSON Schema. Variables/resources can be referenced
  rather than inlined.
  Billy-facing failure fixed: config needs schema validation and reference-style
  secret handling so operator mistakes fail before session admission.
  Source: https://www.windmill.dev/docs/core_concepts/resources_and_types

- GitHub Actions secrets are injected into jobs through the secret context or
  environment, and docs warn to mask sensitive values and avoid passing secrets
  through command-line arguments when possible. Environment secrets can be held
  behind environment-level rules.
  Billy-facing failure fixed: Billyharness status, audit, command output, and
  process args must never echo HMAC secrets; config should store env names only.
  Source: https://docs.github.com/actions/security-guides/using-secrets-in-github-actions
  Source: https://docs.github.com/actions/deployment/targeting-different-environments/using-environments-for-deployment

## Product Shape

Add an operator-owned persisted config file, defaulting to:

```text
$BILLYHARNESS_HOME/agentclub.config.json
```

Also support explicit config files from config/env, matching the local style of
MCP and hooks config:

```text
BILLYHARNESS_AGENTCLUB_CONFIG_FILES
FAST_AGENT_AGENTCLUB_CONFIG_FILES
```

Use JSON for this file because agent-club descriptors already carry JSON Schema
fragments (`input_schema`, `output_schema`) and the HTTP contract is JSON.

Suggested first persisted shape:

```json
{
  "schema_version": 1,
  "capabilities": [
    {
      "id": "hh.review_queue",
      "title": "HH Review Queue",
      "description": "Admit a read-only review queue digest from an external adapter.",
      "kind": "review",
      "risk": "read_only",
      "dispatch": "admit_only",
      "approval": "required",
      "version": "v1",
      "input_schema": {
        "type": "object",
        "additionalProperties": true
      }
    }
  ],
  "trusted_bindings": [
    {
      "id": "hh.review_queue.prod",
      "capability": "hh.review_queue",
      "client_type": "ingress",
      "client_id": "hh-applicant-tool:prod",
      "sources": ["hh-applicant-tool"],
      "event_types": ["review_queue"],
      "metadata_keys": ["project", "queue"],
      "enabled": false
    }
  ],
  "triggers": [
    {
      "id": "hh.review_queue.webhook",
      "kind": "webhook",
      "source": "hh-applicant-tool",
      "capability": "hh.review_queue",
      "event_type": "review_queue",
      "owner": {
        "client_type": "ingress",
        "client_id": "hh-applicant-tool:prod"
      },
      "target_session_id": "session-id-goes-here",
      "prompt": "Review the HH applicant queue digest and propose safe next steps.",
      "auth_method": "hmac_sha256",
      "hmac_secret_env": "BILLY_AGENTCLUB_HH_WEBHOOK_SECRET",
      "max_body_bytes": 65536,
      "enabled": false
    }
  ]
}
```

Important:

- `hmac_secret_env` is a config-only field. It resolves to
  `agentclub.TriggerBinding.HMACSecret` at load time and must never appear in
  public capability responses, trigger audit, config status values, or errors
  except as a redacted env var name.
- `trusted_bindings[].id` is required in persisted config so the operator can
  enable/disable a binding without spelling a long tuple. Add a runtime/view ID
  if that is the cleanest implementation.
- Missing default config file is not an error and should preserve current
  no-registry behavior for direct authenticated `agentclub/events` submissions.
  If a config file exists, it is authoritative: invalid config fails startup;
  an empty configured registry means no configured capability is enabled.
- Trigger delivery already requires a registry. No config means no trigger
  bindings and trigger delivery returns the existing unknown-binding failure.

## Implementation Checklist

1. Add an agent-club config loader.
   - Target: `internal/agentclub/config.go` or `internal/config/agentclub.go`.
   - Define a persisted config DTO with `schema_version`, `capabilities`,
     `trusted_bindings`, and `triggers`.
   - Reuse `NormalizeCapabilityDescriptor`, `NormalizeTrustedBinding`, and
     `NormalizeTriggerBinding`; do not duplicate validation by hand.
   - Validate duplicate capability IDs, trusted binding IDs, trigger IDs,
     unknown capability references, unsupported schema versions, unsafe
     metadata keys, and malformed JSON schemas.
   - Billy-facing failure fixed: a typo in one adapter config must fail before
     Billy spends time debugging a silent gateway admission mismatch.

2. Add secret-reference resolution and redaction.
   - Add config DTO field `hmac_secret_env` for webhook triggers.
   - For `auth_method=hmac_sha256`, require `hmac_secret_env` and resolve it
     through the same env/dotenv path style Billyharness already uses
     (`config.LookupEnvOrDotenv` is acceptable if this lives in `internal/config`;
     otherwise inject a lookup function to avoid an import cycle).
   - Never store raw secret bytes in the persisted DTO after registry creation.
   - Errors may name the missing env var, but must not print a found secret
     value.
   - Billy-facing failure fixed: a leaked webhook secret would let a random
     caller enqueue inputs into Billy sessions while leaving confusing audit.

3. Add config projection and default-file discovery.
   - Extend `config.Config`/resolve specs with `AgentClubConfigFiles []string`
     and env names:
     `BILLYHARNESS_AGENTCLUB_CONFIG_FILES`,
     `FAST_AGENT_AGENTCLUB_CONFIG_FILES`.
   - Add `DefaultAgentClubConfigFile()` and
     `DefaultAgentClubConfigFiles()` returning the home file only when it
     exists.
   - Add sanitized diagnostics/config-status handling for the file list and
     secret env names. Treat paths like MCP/hooks config paths: useful locally,
     redacted on public status surfaces.
   - Billy-facing failure fixed: gateway and CLI must agree on the same config
     source instead of having one behavior in `serve` and another in local CLI.

4. Wire gateway startup to load the registry.
   - In `cmd/fast-agent-harness/service_cmd.go`, load the agent-club registry
     before `gateway.NewServerWithOptionsFromSettings`.
   - Pass it through `gateway.ServerOptions.AgentClubRegistry`.
   - Preserve explicit `ServerOptions.AgentClubRegistry` for tests and
     embedders; do not hide dependency injection behind globals.
   - Startup should fail closed on an explicitly configured invalid file, and
     start normally when no default file exists.
   - Billy-facing failure fixed: after a restart, trusted adapters should still
     exist; Billy should not need a custom Go caller to rehydrate them.

5. Add readiness/config diagnostics.
   - Add a compact redacted agent-club readiness summary:
     configured files, descriptor count, enabled binding count, enabled trigger
     count, missing-secret count, validation status.
   - Add config status/doctor details where they naturally fit, without dumping
     raw prompts, payload schemas beyond safe descriptor metadata, or secret
     values.
   - `GET /ready` may stay unauthenticated, so keep this summary bounded and
     redacted.
   - Billy-facing failure fixed: if webhook delivery fails at 3am, Billy should
     see "secret missing" or "0 enabled triggers" without tailing raw logs.

6. Add local CLI config UX.
   - Extend `fast-agent-harness agentclub` with local config commands:
     `config init`, `config validate`, `config status`, and `config path`.
   - Add list commands that do not require a running gateway:
     `agentclub bindings [-json]` and `agentclub triggers [-json]`.
     If this conflicts with the current `bindings` alias for gateway
     capabilities, keep `capabilities` as the gateway command and make
     `bindings` local-config oriented, or add `registry bindings`; pick the
     least surprising shape and update usage/tests.
   - Add mutation commands:
     `agentclub enable binding <id>`,
     `agentclub disable binding <id>`,
     `agentclub enable trigger <id>`,
     `agentclub disable trigger <id>`.
   - Write config atomically, keep parent dir `0700`, files `0600` on
     platforms where Go can enforce it, and preserve unrelated JSON fields
     only if the chosen parser supports it safely. It is acceptable to reformat
     the Billy-owned config file deterministically.
   - Billy-facing failure fixed: enabling HH or a future project should be one
     local operator command, not "edit JSON and hope gateway starts".

7. Keep discovery and admission semantics tight.
   - `GET /v1/agentclub/capabilities` should continue to expose only enabled
     capabilities/bindings visible to the actor.
   - If binding IDs are added to `BindingView`, treat them as safe metadata and
     include tests/docs.
   - Config-only fields such as `hmac_secret_env`, raw secret bytes, config
     file paths, and disabled private triggers must not leak through capability
     discovery.
   - Billy-facing failure fixed: an adapter should discover only what it can
     use, and Telegram/TUI operator surfaces should not become a secret viewer.

8. Add tests around failure modes, not only happy paths.
   - Config load: valid round trip, unsupported schema version, duplicate IDs,
     unknown capability, invalid metadata key, malformed schema, missing HMAC
     env, disabled binding, disabled trigger.
   - Gateway: `serve`/server settings loads a configured registry, rejects
     unknown configured capability, accepts enabled configured binding, keeps
     no-config startup behavior.
   - CLI: validate output, JSON output, enable/disable atomic edit, missing file
     errors, redaction of env secret values, Windows path handling.
   - Status: readiness/config status never includes raw secrets or raw HMAC
     values.
   - Billy-facing failure fixed: the dangerous bugs here are config drift and
     accidental authority leakage, not string formatting.

9. Update docs and generated inventories.
   - Update architecture/security docs only where behavior changes:
     `docs/architecture/security-model.md`,
     `docs/architecture/gateway-and-sessions.md`, and/or ADR 0009 if needed.
   - Add a small example config in prose or an examples directory if one exists;
     keep it disabled by default and do not add real session IDs or secrets.
   - Run docsgen when CLI/API/config generated docs change.
   - Billy-facing failure fixed: future adapters need a stable contract to copy,
     not tribal memory from this thread.

10. Explicitly do not cross into executor/project-manifest work.
    - Do not load `.billyharness` or project-local agent manifests.
    - Do not add a scheduler daemon.
    - Do not dispatch runs from trigger delivery.
    - Do not apply proposals.
    - Do not add raw command/API/SQL/browser actions.
    - Do not hardcode HH Application Tool except as a disabled example.
    - Billy-facing failure fixed: this TODO makes the foundation trustworthy
      before Billy starts granting external systems mutation power.

## Target Files

Likely files:

- `internal/agentclub/config.go`
- `internal/agentclub/config_test.go`
- `internal/agentclub/registry.go`
- `internal/agentclub/registry_test.go`
- `internal/config/config.go`
- `internal/config/resolved.go`
- `internal/config/diagnostics.go`
- `internal/config/projections.go`
- `internal/config/*agentclub*_test.go`
- `internal/gateway/gateway.go`
- `internal/gateway/readiness.go`
- `internal/gateway/config_status.go`
- `internal/gateway/agentclub_events_test.go`
- `internal/gateway/agentclub_triggers_test.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go` only if DTOs/status types move there
- `cmd/fast-agent-harness/agentclub_cmd.go`
- `cmd/fast-agent-harness/agentclub_cmd_test.go`
- `cmd/fast-agent-harness/service_cmd.go`
- `cmd/fast-agent-harness/doctor.go`
- `docs/architecture/security-model.md`
- `docs/architecture/gateway-and-sessions.md`
- `docs/adr/0009-external-ingress-is-gateway-admission.md`
- `docs/generated/*` through docsgen only

Avoid touching unrelated clipboard/TUI worktree changes unless tests force a
compile fix directly caused by this TODO.

## Architecture Boundaries

- `internal/agentclub` owns the neutral contract, normalization, registry, and
  config-to-registry mapping if it can do so without importing high-level app
  packages.
- `internal/config` owns home-dir paths, env overrides, resolved config
  diagnostics, and dotenv/env lookup if needed.
- `internal/gateway` owns HTTP admission and redacted readiness/config status.
- `cmd/fast-agent-harness` owns operator CLI and gateway startup wiring.
- `internal/gatewayclient`, TUI, and Telegram should consume public gateway
  DTOs only; they must not import gateway server internals.
- Public docs describe the stable behavior. Active TODOs and goal prompts stay
  under `loop-develop`.

## Verification Commands

Run focused tests first:

```sh
go test -count=1 ./internal/agentclub ./internal/config
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewayReadiness|TestConfigStatus|TestGatewaySessionClientID'
go test -count=1 ./cmd/fast-agent-harness ./internal/runtimehost
```

Then update and check generated docs if CLI/API/config generated docs changed:

```sh
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
```

Before calling it done:

```sh
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

If full `./...` is blocked by pre-existing unrelated worktree changes, record
the exact failure and still run the focused package set above.

## Done Means

- A fresh gateway start can load agent-club descriptors, trusted bindings, and
  trigger bindings from `$BILLYHARNESS_HOME/agentclub.config.json` or explicit
  config-file env overrides.
- Operator CLI can initialize, validate, list, enable, and disable local
  agent-club bindings/triggers.
- HMAC secrets are referenced by env name and resolved only at runtime.
- Readiness/config/doctor output reports useful redacted agent-club status.
- Existing gateway event, trigger, proposal, TUI, Telegram, and CLI behavior
  still works.
- No raw secrets, raw webhook bodies, raw delivery IDs, raw prompts, project
  commands, or executor authority leak into public status/discovery/audit.
- Docs and generated inventories are current.
- The implementation branch is committed and pushed after verification passes.

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/015-todo.md end to end.

Build the agent-club trusted config and enable UX for Billyharness:

1. Add a persisted operator-owned agent-club config loader for
   $BILLYHARNESS_HOME/agentclub.config.json plus explicit config-file env
   overrides.
2. Support capabilities, stable trusted binding IDs, trigger bindings, runtime
   HMAC secret env refs, validation, and redacted diagnostics.
3. Wire `fast-agent-harness serve` so the gateway loads this registry at startup
   while preserving explicit ServerOptions injection for tests/embedders.
4. Extend `fast-agent-harness agentclub` with local config init/validate/status,
   list bindings/triggers, and enable/disable binding/trigger commands.
5. Add readiness/config/doctor status that is useful and redacted.
6. Update relevant architecture docs and generated docs when public behavior,
   CLI, config, or API DTOs change.

Stay inside the TODO boundaries:
- no executor;
- no scheduler daemon;
- no auto-run dispatch from trigger delivery;
- no proposal apply;
- no project-local manifest loader;
- no raw command/API/SQL/browser actions;
- no HH-specific hardcode except a disabled example.

Do not use Playwright, Puppeteer, Chrome MCP, headless browsers, screenshots,
network capture, or browser debug. Inspect code, routes, imports, tests, built
artifacts, and public files instead.

Verification required before done:
go test -count=1 ./internal/agentclub ./internal/config
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewayReadiness|TestConfigStatus|TestGatewaySessionClientID'
go test -count=1 ./cmd/fast-agent-harness ./internal/runtimehost
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short

If a broad test is blocked by unrelated pre-existing worktree changes, record
the exact blocker and still complete all focused verification that applies.

After verification passes, create a git commit for the implementation and push
the branch. Include the commit hash, branch name, commands run, and any residual
blockers in your final report.
```

## Final Status

Completed on 2026-07-07.

Implemented:

- persisted operator-owned `agentclub.config.json` loader with schema version,
  descriptors, stable trusted binding IDs, trigger bindings, HMAC env refs,
  validation, redacted status, and default `$BILLYHARNESS_HOME` discovery;
- `serve` startup wiring through `ServerOptions.AgentClubRegistry` while
  preserving explicit injection for tests/embedders;
- readiness, `/v1/config`, and `doctor` agent-club status;
- local `agentclub config init|validate|status|path`,
  `agentclub bindings`, `agentclub triggers`, and
  `agentclub enable|disable <binding|trigger> ID` CLI;
- docs and generated docs for the new config/CLI/API status surface.

Verification evidence:

```sh
go test -count=1 ./internal/agentclub ./internal/config
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewayReadiness|TestConfigStatus|TestGatewaySessionClientID|TestGatewayHealthAndReadinessAreSplit'
go test -count=1 ./cmd/fast-agent-harness ./internal/runtimehost
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
```

All verification commands above passed. `git status --short` still showed
unrelated pre-existing clipboard/TUI worktree changes, which were intentionally
left unstaged.
