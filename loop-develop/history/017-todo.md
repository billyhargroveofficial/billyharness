# 017 - Agent-Club Adapter SDK And Reference Client

## Goal

Give external projects a stable way to talk to Billyharness agent-club without
copying internal structs or shelling out fragile curl snippets.

After this TODO, a separate repo such as HH Applicant Tool should be able to
use a small adapter library or reference CLI flow to:

- construct schema-versioned agent-club events;
- construct trigger deliveries;
- sign webhook bodies consistently with Billyharness;
- attach gateway bearer auth and session-owner headers;
- parse redacted admission responses and useful typed errors;
- avoid importing `internal/agentclub`.

This TODO is about adapter authoring ergonomics and contract stability. It does
not migrate HH Applicant Tool, add a scheduler, auto-run sessions, or apply
proposals.

## Current Local Finding

The canonical models live under `internal/agentclub`, which is correct for
Billyharness core but unusable as an external Go import. The gateway route and
config are stable enough to author adapters, but external projects currently
have three bad choices:

- duplicate DTOs manually;
- import an internal package illegally/impossibly;
- shell out to `fast-agent-harness` and parse human text.

015/016 should leave Billy with config and trigger-delivery UX. This TODO makes
the integration contract consumable by other repos.

Billy-facing failure fixed: every future project adapter should not have to
rediscover HMAC signing, owner headers, payload hashing expectations, and
redacted error handling from scratch.

## Source Research Summary

Local sources checked:

- `internal/agentclub/contract.go`, `triggers.go`, `proposals.go`: canonical
  DTOs and validation rules.
- `internal/gatewayclient/client.go`: gateway transport, auth, and owner header
  conventions.
- `internal/ingress/hmac.go` or equivalent HMAC helper implementation:
  signing/verification details.
- `docs/generated/gateway-api.md`: public HTTP route inventory.
- `docs/architecture.md`: package boundary rules; `internal/agentclub` must not
  become a project adapter or gateway client.

Architecture direction:

- keep Billyharness core policy under `internal/agentclub`;
- expose a small public adapter/client package only for DTO construction,
  signing, and HTTP calls;
- make drift tests compare public DTO JSON shape against internal canonical
  examples.

## Implementation Checklist

1. Add a public adapter package.
   - Suggested package: `pkg/agentclub`.
   - It should be small, dependency-light, and safe for external repos.
   - It may expose public DTOs that mirror the wire contract:
     `EventRequest`, `EventAdmissionResponse`, `TriggerDeliveryRequest`,
     `TriggerDeliveryResponse`, and small error types.
   - Do not expose Billyharness gateway server internals.

2. Add public client helpers.
   - Suggested API:
     ```go
     client := agentclub.NewClient(agentclub.ClientOptions{
       GatewayURL: "...",
       BearerToken: "...",
       Owner: agentclub.Owner{ClientType: "ingress", ClientID: "..."},
     })
     resp, err := client.PostEvent(ctx, sessionID, event)
     resp, err := client.DeliverTrigger(ctx, triggerID, delivery)
     ```
   - Include context support, typed status errors, and redacted `Error()`
     strings.
   - Support bearer auth but do not read Billyharness dotenv files in the public
     package.

3. Add HMAC signing helpers.
   - Expose `SignWebhookHMACSHA256(secret, body []byte, timestamp string,
     includeTimestamp bool) string`.
   - Match the gateway verification implementation exactly.
   - Add tests that verify public signing passes internal verification and
     tampered bodies fail.

4. Add payload/event builder helpers.
   - Helpers should canonicalize JSON payloads or at least validate JSON before
     send.
   - Preserve explicit `external_event_id`; do not invent hidden identity rules
     except for documented convenience helpers.
   - Keep adapters responsible for their own source/event names.

5. Add a tiny reference adapter example.
   - Add an example under `examples/agentclub-adapter` or `docs/examples` if
     the repo already has an examples lane.
   - The example should compile or be documentation-only with tested snippets.
   - It should show:
     - event route;
     - trigger delivery;
     - HMAC signing;
     - owner headers;
     - redacted error handling.
   - Do not add HH-specific behavior.

6. Add contract drift tests.
   - Verify public DTO JSON field names match the internal HTTP contract.
   - Verify public HMAC signatures match gateway verification.
   - Verify public client sends the correct route, headers, content type, and
     body.
   - Verify typed errors do not include bearer tokens, secrets, raw signatures,
     or credential-bearing URLs.

7. Decide docs placement.
   - Public adapter package docs should explain this is for external project
     adapters, not runtime plugins.
   - Update `docs/architecture.md` package map if a new `pkg/*` rule exists or
     add a short public package docs note elsewhere.
   - Update generated package docs if docsgen includes public packages.

## Target Files

Likely files:

- `pkg/agentclub/doc.go`
- `pkg/agentclub/types.go`
- `pkg/agentclub/client.go`
- `pkg/agentclub/hmac.go`
- `pkg/agentclub/*_test.go`
- `internal/ingress` tests if signing helpers are shared
- `examples/agentclub-adapter/*` or docs example
- `docs/architecture.md`
- `docs/generated/*` through docsgen only
- optionally `scripts/verify-deps.sh` only if new public-package deps are added

## Boundaries

- Do not import `internal/gateway` from the public package.
- Do not make the public package read `$BILLYHARNESS_HOME` or `.env`.
- Do not expose provider/model/access-mode/tool/MCP override knobs.
- Do not add adapter installation, scheduler, auto-run, executor, or apply.
- Do not migrate HH Applicant Tool in this TODO.
- Do not create a marketplace or project-local manifest format.

## Verification Commands

```sh
go test -count=1 ./pkg/agentclub ./internal/ingress ./internal/agentclub ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

If the implementation chooses not to add `pkg/agentclub`, it must explain the
alternative external adapter story in docs and tests.

## Done Means

- External Go repos can import a public adapter/client package or follow a
  tested reference adapter without copying internal Billyharness code.
- Public HMAC signing matches gateway verification.
- Public client helpers attach bearer auth and owner headers correctly.
- DTO JSON shape is covered by drift tests.
- Docs explain what the adapter SDK is and is not.
- No executor/auto-run/scheduler/proposal apply behavior is introduced.
- The branch is committed and pushed.

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/017-todo.md end to end.

Build a small public agent-club adapter SDK/reference client for external
projects. External repos must be able to construct agent-club events, deliver
triggers, sign webhook bodies, attach bearer auth and owner headers, and parse
typed redacted errors without importing internal Billyharness packages.

Stay inside boundaries:
- no HH migration;
- no scheduler;
- no auto-run;
- no executor/apply;
- no project-local manifest format;
- no gateway server imports from public adapter code;
- no browser automation/debug tooling.

Verification required:
go test -count=1 ./pkg/agentclub ./internal/ingress ./internal/agentclub ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short

If broad tests are blocked by unrelated pre-existing worktree changes, record
the exact blocker and still run every focused command that applies.

After verification passes, create a git commit and push the branch. Include the
commit hash, branch name, commands run, and residual blockers in the final
report.
```

## Final Status

Completed on 2026-07-07.

Implementation evidence:

- Added public `pkg/agentclub` with schema-versioned event, trigger,
  capability, owner, response, and trigger-delivery DTOs.
- Added public builders for normalized events, manual/schedule JSON trigger
  deliveries, webhook deliveries, canonical JSON, payload hashing, external
  event id hashing, and raw-body HMAC-SHA256 signing.
- Added a public HTTP client with explicit gateway URL, bearer token, owner
  headers, `PostEvent`, `DeliverTrigger`, `Capabilities`, and typed status
  errors whose `Error()` strings omit raw response bodies.
- Added `examples/agentclub-adapter` as a generic compileable adapter example.
- Added drift/compatibility tests against internal DTO JSON tags, internal
  event normalization, gateway HMAC verification, client route/header/body
  behavior, and redacted error strings.
- Updated architecture docs for the public external-adapter package boundary.

Verification commands run:

```sh
go test -count=1 ./pkg/agentclub ./internal/ingress ./internal/agentclub ./internal/gatewayclient
go test -count=1 ./cmd/fast-agent-harness
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

Commit/push state:

- Pending final chain commit and push after TODOs 018-020 complete.

Remaining blockers:

- None for 017.
- Unrelated pre-existing clipboard/TUI worktree changes remain unstaged and
  untouched.
