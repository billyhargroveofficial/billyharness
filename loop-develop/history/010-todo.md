# 010 - Agent-Club Registry And Trusted Bindings V0

## Source Research Summary

Billyharness now has a neutral `POST /v1/sessions/{id}/agentclub/events`
admission route. That is the right foundation, but it is still too low-level to
be pleasant or production-like: an external adapter can submit a normalized
event, but Billyharness has no operator-visible registry of known capabilities,
trusted bindings, enabled owners, or rule metadata.

Mature systems solve this by separating what an integration *describes* from
what the operator has *enabled*:

- MCP tools are named capabilities with input/output schemas and metadata;
  clients should show exposed tools and keep humans in the loop for sensitive
  invocations: <https://modelcontextprotocol.io/specification/draft/server/tools>
- OpenAI Apps SDK recommends one focused job per tool, explicit input schema,
  predictable output schema, and separate read/write tools; its annotations
  distinguish read-only, destructive, open-world, and idempotent behavior:
  <https://developers.openai.com/apps-sdk/plan/tools>
  <https://developers.openai.com/apps-sdk/reference>
- MCP Registry uses standardized metadata and installation/configuration
  records instead of making every host discover integrations ad hoc:
  <https://modelcontextprotocol.io/registry/about>
- OpenHands Agent Canvas separates conversations from automations and lets
  automations run on cron or events against GitHub, Linear, Slack, and custom
  webhooks: <https://docs.openhands.dev/openhands/usage/agent-canvas/overview>
- GitHub Agentic Workflows uses safe outputs: the agent can reason with
  read-only/default-limited permissions, while separate validated jobs perform
  writes without giving the agent blanket write authority:
  <https://github.github.com/gh-aw/reference/safe-outputs/>
  <https://github.github.com/gh-aw/reference/custom-safe-outputs/>
- Trigger.dev and Temporal show the durable shape for later slices: retries,
  idempotency, schedules, event history, and human waits. Do not import those
  platforms now; borrow the semantics:
  <https://github.com/triggerdotdev/trigger.dev>
  <https://trigger.dev/product>
  <https://docs.temporal.io/workflows>
- LangGraph/LangChain HITL reinforces that approval is a persisted
  interrupt/decision, not a casual chat message:
  <https://docs.langchain.com/oss/python/langchain/human-in-the-loop>
  <https://docs.langchain.com/oss/python/langgraph/interrupts>
- Workflow automation systems such as Activepieces, n8n, and Windmill show the
  same split of definitions, credentials/resources, triggers/actions, and UI:
  <https://github.com/activepieces/activepieces>
  <https://github.com/n8n-io/n8n-nodes-starter>
  <https://github.com/windmill-labs/windmill>

## Product Direction

The next useful layer is a trusted local registry plus bindings:

- a capability descriptor says what exists;
- a binding says the local operator enabled it for a session owner/profile;
- the event route checks the binding before admission;
- clients can list what is enabled before trying to use it.

Do not load arbitrary project manifests yet. Project-local manifests can come
later after a signed/hash/install story. V0 should be boring and local.

## Checklist

- [ ] Problem: `/agentclub/events` accepts any well-formed source/capability
      from an authorized ingress owner, so the gateway cannot explain which
      agent-club capabilities are actually known or enabled. Add a small
      trusted registry model in `internal/agentclub` for capability descriptors:
      `id`, `title`, `description`, `kind`, `risk`, `input_schema`,
      `output_schema`, `dispatch=admit_only`, `approval`, and version.
- [ ] Problem: operator enablement is implicit in session-owner headers. Add a
      binding model that links a capability to `client_type=ingress`,
      `client_id`, optional source/event type restrictions, and optional
      metadata keys. Keep secrets and executable commands out of this slice.
- [ ] Problem: project files must not grant authority. For v0, load bindings
      only from an explicit trusted gateway setting or `$BILLYHARNESS_HOME`
      agent-club config file. If that is too large for the slice, add an
      in-memory/default registry with tests and document the next config step;
      do not load `.billyharness` project manifests yet.
- [ ] Problem: users and adapters need discovery. Add a read-only route such as
      `GET /v1/agentclub/capabilities` that returns the enabled descriptors and
      safe binding metadata visible to the current actor. Do not include secrets,
      raw prompts, payloads, env vars, or command lines.
- [ ] Problem: event admission should enforce known capability semantics. Update
      `POST /v1/sessions/{id}/agentclub/events` to reject unknown or disabled
      `source/capability/event_type` combinations when a registry is configured,
      while preserving current tests for owner-scope, redaction, idempotency,
      unsafe metadata rejection, and no run dispatch.
- [ ] Problem: future safe outputs need a vocabulary now. Extend descriptor
      validation with conservative risk/approval enums inspired by MCP/OpenAI
      hints: `read_only`, `local_read`, `network_read`, `local_write`,
      `network_write`, `external_mutation`, `execute`, `secret_access`,
      `unknown`; approval `none|required`. Do not implement action execution.
- [ ] Problem: docs should make the system understandable. Update
      architecture/security docs with the three-layer model:
      descriptor -> trusted binding -> admitted event -> later normal run.
      Mention that scheduler, webhooks, safe outputs, and action approvals are
      later slices.
- [ ] Problem: generated docs must stay current. Run docsgen and docsgen check
      if route or package docs change.

## Target Files

Likely edit:

- `internal/agentclub/contract.go`
- `internal/agentclub/contract_test.go`
- `internal/gateway/agentclub_events.go`
- `internal/gateway/agentclub_events_test.go`
- `internal/gateway/routes.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`
- `internal/gatewayclient/client_test.go`
- `docs/architecture/gateway-and-sessions.md`
- `docs/architecture/security-model.md`
- `docs/adr/0009-external-ingress-is-gateway-admission.md`
- generated docs via `go run ./cmd/fast-agent-harness docsgen`

Likely add:

- `internal/agentclub/registry.go`
- `internal/agentclub/registry_test.go`
- possibly `internal/gateway/agentclub_catalog.go`
- possibly `internal/gateway/agentclub_catalog_test.go`

## Architecture Boundaries

- Keep registry/bindings as metadata and admission policy only.
- Do not execute capabilities directly.
- Do not add scheduler, webhook HMAC endpoint, auto-run, safe-output executor,
  raw API caller, raw SQL caller, browser auth/debug, generic command runner, or
  HH-specific behavior.
- Do not load project-local manifests in this slice.
- Keep `internal/agentclub` independent from `gateway`, `tools`, `agent`,
  provider packages, shell runners, browser/CDP code, and external project code.
- Keep event admission redacted and `run_dispatched=false`.
- No Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots,
  network capture, or browser debug.

## Verification Commands

```sh
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewayIngress|TestGatewaySessionClientID'
go test -count=1 ./internal/gateway ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/010-todo.md end to end. Add the first usable agent-club registry and trusted bindings layer on top of the existing generic /agentclub/events admission route. Keep it single-slice: descriptors and bindings are metadata/admission policy only, with no capability execution. Add descriptor validation, trusted binding validation, a read-only capability discovery route such as GET /v1/agentclub/capabilities, and route enforcement so configured registries reject unknown or disabled source/capability/event_type combinations before writing session inputs. Preserve owner-scope enforcement, unsafe metadata rejection, idempotent ingress admission, redacted audit/response behavior, and run_dispatched=false. Do not add scheduler, webhook HMAC endpoint, auto-run, safe-output executor, generic command runner, project-local manifest loading, raw API caller, raw SQL caller, browser auth/debug, or HH-specific behavior. Do not use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, browser network capture, or browser debug. Update architecture/security docs and generated docs if API/package docs change. Verify with the TODO commands, then create a git commit and push the branch after verification passes.
```

## Final Status

Completed on 2026-07-07.

Implemented a metadata-only agent-club registry and trusted binding layer in
`internal/agentclub`, wired optional registry enforcement into
`POST /v1/sessions/{id}/agentclub/events`, added read-only discovery at
`GET /v1/agentclub/capabilities`, and added gateway client support for the
typed discovery response. Configured registries now reject unknown capabilities,
disabled capabilities, source/event-type mismatches, and disallowed metadata
keys before ingress audit or session input admission. The route remains
admit-only and returns `run_dispatched=false`.

Documentation was updated in:

- `docs/architecture.md`
- `docs/architecture/gateway-and-sessions.md`
- `docs/architecture/security-model.md`
- `docs/adr/0009-external-ingress-is-gateway-admission.md`
- `docs/generated/gateway-api.md`
- `docs/generated/packages.md`

Verification passed:

```sh
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./internal/agentclub ./internal/ingress ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewayIngress|TestGatewaySessionClientID'
go test -count=1 ./internal/gateway ./cmd/fast-agent-harness
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
```

`git diff --check` emitted only Windows CRLF normalization warnings and exited
successfully. No scheduler, webhook endpoint, auto-run path, safe-output
executor, generic command runner, project-local manifest loader, raw API/SQL
caller, browser auth/debug path, or HH-specific behavior was added.

Commit/push state: included in the final task commit and pushed after
verification. Remaining unrelated dirty worktree files were left untouched.
