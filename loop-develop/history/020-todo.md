# 020 - Agent-Club Safe-Output Apply Foundation

## Goal

Add the first safe-output apply foundation for approved agent-club proposals.

Current proposals can be created, listed, approved, rejected, edited, expired,
and superseded. Approval intentionally does not apply anything. This TODO adds
the next layer, but only as a narrow, hash-bound, auditable executor
foundation:

```text
proposal
  -> hash-bound approval
  -> apply request with expected hash
  -> action-specific executor
  -> durable apply record
  -> optional result/output ref
```

The first implementation should support one or two deliberately boring action
kinds that can be tested safely. It must not become a generic shell/API runner.

## Current Local Finding

Existing proposal code:

- `internal/agentclub/proposals.go` defines proposal DTOs, hash policy, states,
  decision requests, and limits.
- `internal/gateway/agentclub_proposals.go` stores proposal and decision events
  in `agentclub-proposals.jsonl`.
- CLI/TUI/Telegram can approve/reject proposals by expected hash.
- Docs explicitly say approval is not execution.

Missing apply foundation:

- no `applied` state;
- no apply route;
- no action executor registry;
- no durable apply ledger;
- no replay of apply results;
- no exact-approved-artifact execution boundary.

Billy-facing failure fixed: agents can ask Billy to approve an exact artifact,
but after approval nothing can safely perform that exact artifact without a
human copying it somewhere manually.

## Source Research Summary

Local sources checked:

- `internal/agentclub/proposals.go`: proposal hash and DTO boundaries.
- `internal/gateway/agentclub_proposals.go`: proposal ledger replay and
  decision semantics.
- `internal/checkpoint`: exact artifact verification, restore conflict
  patterns, and rollback discipline.
- `internal/tools`: central policy/risk taxonomy and dangerous-tool boundaries.
- `docs/architecture/security-model.md`: proposal approval boundary.
- `docs/adr/0009-external-ingress-is-gateway-admission.md`: "future apply"
  placeholder.

Design direction:

- apply must prove the proposal hash matches the approved artifact;
- executor must be action-specific and allowlisted;
- executor output must be durable and redacted;
- apply must not infer authority from chat text;
- no generic shell, raw HTTP API, SQL, browser auth, or MCP call executor in
  this slice.

## First Supported Action Kinds

Pick one narrow action kind to implement first. Recommended:

1. `record_note`
   - Applies by writing an apply result record only, not by mutating external
     systems.
   - Useful as a foundation test executor.
   - Payload example:
     ```json
     {"message":"reviewed queue", "labels":["hh","digest"]}
     ```

2. Optional if the implementation wants a real local side effect:
   `write_output_ref`
   - Writes the exact approved payload/preview to Billyharness private
     `tool-output` or an agent-club apply-output directory.
   - Must not write arbitrary workspace files.

Do not implement external mutation such as HH reply, GitHub issue, email,
browser action, shell command, MCP call, or arbitrary filesystem write in this
TODO.

## Product Shape

Add a route like:

```text
POST /v1/sessions/{id}/agentclub/proposals/{proposal_id}/apply
```

Request:

```json
{
  "schema_version": 1,
  "expected_proposal_hash": "...",
  "idempotency_key": "operator-provided-or-derived",
  "dry_run": false
}
```

Response:

```json
{
  "schema_version": 1,
  "proposal_id": "...",
  "proposal_hash": "...",
  "apply_id": "...",
  "state": "applied",
  "action_kind": "record_note",
  "dry_run": false,
  "output_ref": "...",
  "payload_sha256": "...",
  "run_dispatched": false
}
```

The exact DTO can vary, but it must be redacted and replayable.

## Implementation Checklist

1. Extend proposal model with apply concepts.
   - Add states/events carefully:
     - `applying` only if needed;
     - `applied`;
     - `apply_failed`.
   - Add `ProposalApplyRequest` and `ProposalApplyResponse`.
   - Include expected proposal hash in every apply request.
   - Include an idempotency key or deterministic apply id to prevent double
     application.

2. Add an executor registry with explicit action kinds.
   - Package can be `internal/agentclub/apply` or gateway-local if it needs
     session store access.
   - Registry maps action kind to a small executor.
   - Unsupported action kind returns a clear rejected/failed state.
   - First executor should be safe and boring (`record_note` recommended).
   - Executor must receive normalized proposal data, not raw HTTP body.

3. Add durable apply ledger.
   - Either extend `agentclub-proposals.jsonl` event vocabulary or add a
     separate `agentclub-apply.jsonl`; choose the replay model that stays
     simplest.
   - Record:
     - proposal id/hash;
     - action kind;
     - apply id/idempotency key hash;
     - decision id or approval evidence;
     - state;
     - payload hash;
     - output ref/result summary;
     - redacted reason on failure.
   - Do not store raw secrets, raw external API responses, bearer tokens, or
     metadata values.

4. Enforce exact approval.
   - Apply only pending-approved proposals, not rejected/expired/superseded/
     failed proposals.
   - Require the proposal to be approved before apply.
   - Require expected hash to equal current proposal hash.
   - Stale hash must fail before executor runs.
   - Duplicate apply with same idempotency key returns the existing result.
   - Duplicate apply with different idempotency key after applied must not run
     the executor again.

5. Add gateway route and client/CLI UX.
   - Add route to `internal/gateway/routes.go`.
   - Add gateway client helper.
   - Add CLI:
     ```text
     fast-agent-harness agentclub apply -session SESSION_ID -proposal PROPOSAL_ID -hash HASH [-dry-run] [-json]
     ```
   - CLI must show action kind, state, apply id, output ref/result summary, and
     never raw payload/secrets.

6. Add dry-run behavior.
   - Dry run validates approval/hash/action support and returns what would be
     applied.
   - Dry run must not mark proposal applied.
   - Dry run may write no ledger or write a distinct dry-run audit record; pick
     one and document it.

7. Add tests.
   - Approved proposal applies exactly once.
   - Stale hash rejected.
   - Pending/unapproved rejected.
   - Rejected/expired/superseded rejected.
   - Unsupported action kind fails safely.
   - Duplicate apply idempotent.
   - Dry run does not apply.
   - Output redacts raw payload/secrets.
   - Cross-owner apply denied.
   - Replay reconstructs applied/apply_failed state.

8. Update docs and generated references.
   - Update ADR 0009 because "approval does not apply" becomes "approval alone
     does not apply; explicit apply route does".
   - Update security docs with exact-hash apply boundary.
   - Update gateway docs, generated API docs, CLI docs, and package docs.

## Target Files

Likely files:

- `internal/agentclub/proposals.go`
- `internal/agentclub/proposals_test.go`
- `internal/gateway/agentclub_proposals.go`
- `internal/gateway/agentclub_proposals_test.go`
- `internal/gateway/routes.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`
- `internal/gatewayclient/client_test.go`
- `cmd/fast-agent-harness/agentclub_cmd.go`
- `cmd/fast-agent-harness/agentclub_cmd_test.go`
- `internal/secrets` only if extra redaction is needed
- `docs/adr/0009-external-ingress-is-gateway-admission.md`
- `docs/architecture/security-model.md`
- `docs/architecture/gateway-and-sessions.md`
- `docs/generated/*` through docsgen only

## Boundaries

- No generic shell executor.
- No arbitrary HTTP/API executor.
- No SQL executor.
- No browser/auth action executor.
- No MCP tool executor.
- No HH-specific reply/apply action in this TODO.
- No workspace write executor unless it uses existing checkpoint/safe path
  boundaries and is explicitly scoped; prefer not doing it yet.
- No auto-run coupling.
- No raw payload/secret leakage in responses or ledgers.
- No browser automation/debug tooling.

## Verification Commands

```sh
go test -count=1 ./internal/agentclub ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClubProposal|TestGatewaySessionClientID'
go test -count=1 ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
git status --short
```

## Done Means

- Approved proposals can be explicitly applied through a hash-bound route.
- The first executor kind is safe, boring, tested, and auditable.
- Approval alone still does not execute anything.
- Apply is idempotent and replayable.
- Unsupported/dangerous action kinds fail safely.
- Docs clearly explain the new boundary.
- The branch is committed and pushed.

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/020-todo.md end to end.

Add the first safe-output apply foundation for agent-club proposals. Approved
proposals should be applicable only through an explicit hash-bound apply route
and a narrow allowlisted executor. Start with a safe boring action kind such as
record_note; do not implement external mutations or generic executors.

Stay inside boundaries:
- no generic shell/API/SQL/browser/MCP executor;
- no HH-specific reply/apply;
- no arbitrary workspace writes;
- no auto-run coupling;
- no raw payload/secret leakage;
- no browser automation/debug tooling.

Verification required:
go test -count=1 ./internal/agentclub ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClubProposal|TestGatewaySessionClientID'
go test -count=1 ./cmd/fast-agent-harness
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

Implemented the safe-output apply foundation as an explicit hash-bound gateway
route with a durable replayable apply record. Approval remains inert until
`POST /v1/sessions/{id}/agentclub/proposals/{proposal_id}/apply` is called
with the expected proposal hash and an idempotency key. The v0 executor is
allowlisted to `record_note`, which writes a redacted apply record and returns
a synthetic `agentclub:apply:<apply_id>` output ref. Unsupported approved
action kinds become `proposal_apply_failed`; no generic shell/API/SQL/browser
or MCP executor was added, and apply never dispatches a run.

Evidence:

- `go test -count=1 ./internal/agentclub ./internal/gatewayclient`
- `go test -count=1 ./internal/gateway -run 'TestAgentClubProposal|TestGatewaySessionClientID'`
- `go test -count=1 ./cmd/fast-agent-harness`
- `go run ./cmd/fast-agent-harness docsgen`
- `go run ./cmd/fast-agent-harness docsgen -check`
- `go test -count=1 ./...`
- `go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness`
- `git diff --check`

Generated docs updated:

- `docs/generated/gateway-api.md`

Residual blockers: none for this slice. Existing unrelated dirty clipboard/TUI
files were left untouched and should not be staged with this work.
