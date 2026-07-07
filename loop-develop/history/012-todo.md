# 012 - Agent-Club Safe Outputs And Approval Queue V0

## Source Research Summary

The agent-club path should eventually do useful work, but direct external
mutation is the part that must be designed with extra care. Mature systems do
not let an agent casually perform side effects from a chat turn. They separate
proposal, review, decision, and application.

Useful references:

- GitHub Agentic Workflows safe outputs separate agent reasoning from write
  jobs; the agent produces structured output and a constrained job performs the
  write:
  <https://github.github.com/gh-aw/reference/safe-outputs/>
  <https://github.github.com/gh-aw/reference/custom-safe-outputs/>
  <https://github.github.com/gh-aw/reference/safe-outputs-pull-requests/>
- LangChain HITL middleware models one decision per interrupted action with
  `approve`, `edit`, `reject`, and `respond`, while policies define which tools
  interrupt:
  <https://docs.langchain.com/oss/python/langchain/human-in-the-loop>
- LangGraph interrupts/checkpointing show that a paused decision should resume
  the same durable state, and that interrupt payloads must be serializable and
  stable:
  <https://docs.langchain.com/oss/python/langgraph/interrupts>
- OpenAI Agents SDK HITL uses interrupted runs and approval/rejection of pending
  tool calls rather than starting a new conversation:
  <https://openai.github.io/openai-agents-python/human_in_the_loop/>
- Temporal human-in-the-loop patterns use durable waits, signals, timeouts, and
  full workflow history for approvals:
  <https://docs.temporal.io/ai-cookbook/human-in-the-loop-python>
- Terraform plan/apply is the classic non-agent reference: approve a reviewed
  artifact, then apply that exact artifact:
  <https://developer.hashicorp.com/terraform/cli/commands/plan>
  <https://developer.hashicorp.com/terraform/cli/commands/apply>

Billyharness v0 should implement the durable proposal/decision layer before it
implements any executor. This creates a safe landing zone for HH reply drafts,
GitHub comment drafts, Nareshka task changes, and future project mutations.

## Product Direction

Target flow:

```text
agent or adapter proposes action
  -> durable proposal with normalized args/output, risk, preview, hashes
  -> operator approves/rejects/edits
  -> decision is persisted and hash-bound
  -> later slice applies the exact approved artifact
```

In this slice, there is no apply executor. A proposal can become approved or
rejected, but no external mutation is performed by Billyharness.

## Checklist

- [ ] Problem: side effects need a first-class review object. Add an
      agent-club safe-output proposal model with `proposal_id`, session id,
      owner, source, capability, action kind, risk, approval state, preview
      text, normalized payload hash, optional output ref, created/updated time,
      expires_at, and redacted metadata keys.
- [ ] Problem: approvals must be hash-bound. Store a deterministic
      `proposal_hash` over action kind, source/capability, normalized payload,
      preview/output ref hash, target scope, policy version, and session id.
      Approval must apply only to that exact hash.
- [ ] Problem: approval state must be durable. Add a JSONL ledger under the
      session store or a dedicated agent-club approval ledger with records for
      `proposal_created`, `decision_recorded`, `proposal_expired`,
      `proposal_superseded`, and terminal failures. Validate replay order and
      corruption like existing event logs.
- [ ] Problem: gateway clients need review APIs. Add routes such as
      `POST /v1/sessions/{id}/agentclub/proposals`,
      `GET /v1/sessions/{id}/agentclub/proposals`, and
      `POST /v1/sessions/{id}/agentclub/proposals/{proposal_id}/decision`.
      Decisions should support `approve`, `reject`, and maybe `edit` as "create
      a new proposal"; do not apply the action.
- [ ] Problem: permissions must match session ownership. Proposal creation and
      decisions must require the same session-owner/bearer semantics as session
      mutations. A Telegram/TUI/client actor should not approve another owner's
      proposal.
- [ ] Problem: prompt injection can hide in previews. Treat preview/payload as
      untrusted content, cap sizes, store large data through output refs, and
      never copy raw secrets into audit records or list responses.
- [ ] Problem: proposals need policy vocabulary. Reuse descriptor risk enums
      from `010`: `read_only`, `local_read`, `network_read`, `local_write`,
      `network_write`, `external_mutation`, `execute`, `secret_access`,
      `unknown`. Default approval should be required for write/external/execute
      and unknown proposals.
- [ ] Problem: operators need exactly-reviewable artifacts. For draft text
      actions, store exact text hash and preview; for filesystem/API actions,
      store a diff or structured payload hash. Do not implement applying those
      artifacts yet.
- [ ] Problem: tests must guard the scary parts. Cover duplicate proposals,
      stale proposal hash, cross-owner rejection, expiration, redaction, list
      filtering by owner, edit-as-new-proposal behavior, no external mutation,
      and no run dispatch.
- [ ] Problem: docs should define the lifecycle. Update architecture/security
      docs with `Proposal -> Decision -> Future Apply` and explain why approval
      is not a chat message.

## Target Files

Likely edit:

- `internal/agentclub/contract.go`
- `internal/agentclub/registry.go`
- `internal/gateway/gateway.go`
- `internal/gateway/session_store.go`
- `internal/gateway/routes.go`
- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`
- `docs/architecture/gateway-and-sessions.md`
- `docs/architecture/security-model.md`
- `docs/adr/0009-external-ingress-is-gateway-admission.md`
- generated docs via `go run ./cmd/fast-agent-harness docsgen`

Likely add:

- `internal/agentclub/proposals.go`
- `internal/agentclub/proposals_test.go`
- `internal/gateway/agentclub_proposals.go`
- `internal/gateway/agentclub_proposals_test.go`

## Architecture Boundaries

- This slice creates proposals and decisions only.
- Do not execute approved actions.
- Do not call external APIs, send HH replies, apply to jobs, modify GitHub, run
  shell commands, call MCP tools, or edit files as part of approval.
- Do not depend on full run checkpoint/resume yet. If a run cannot resume, the
  approved proposal should remain durable for a later apply slice.
- Do not add UI buttons in this slice unless tests already need a minimal client
  helper; UI is `014`.
- No Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots,
  network capture, or browser debug.

## Verification Commands

```sh
go test -count=1 ./internal/agentclub ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewaySessionClientID|TestSessionStore'
go test -count=1 ./internal/gateway ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/012-todo.md end to end. Add agent-club safe-output proposal and approval queue v0. Keep it proposal/decision only: create durable hash-bound proposals, list them by authorized session owner, record approve/reject/edit-as-new decisions, expire or supersede proposals, and never apply or execute approved actions in this slice. Proposals must include risk, source, capability, action kind, preview/output refs, normalized payload hash, proposal hash, owner/session scope, state, timestamps, and redacted metadata keys. Decisions must be owner-scoped, hash-bound, replayable, and audited without raw secrets. Do not call external APIs, send HH replies, apply to jobs, modify GitHub, run shell commands, call MCP tools, edit files, add UI buttons, add auto-run, or implement a safe-output executor. Do not use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, browser network capture, or browser debug. Update architecture/security docs and generated docs if API/package docs change. Verify with the TODO commands, then create a git commit and push the branch after verification passes.
```

## Final Status

Completed on 2026-07-07.

Implemented agent-club safe-output proposal/decision queue v0. Added
hash-bound proposal models in `internal/agentclub`, session-scoped durable
`agentclub-proposals.jsonl` replay in the gateway store, and routes for
creating proposals, listing proposals, and recording approve/reject/edit
decisions. Proposals include source, capability, action kind, risk, owner,
session id, preview/output ref summary, payload hash, proposal hash, policy
version, state, timestamps, optional expiry, supersede links, and metadata
keys. Raw payloads and metadata values are not returned.

Decisions require an explicit expected proposal hash. `edit` creates a new
proposal and supersedes the old one. Expiration is replayed and persisted. This
slice records decisions only: it does not apply approved artifacts, call
external APIs, send HH replies, modify GitHub, run shell commands, call MCP
tools, edit files, dispatch a run, or resume/apply runtime work.

Documentation and generated gateway API docs were updated for the proposal
lifecycle and routes.

Verification passed:

```sh
go test -count=1 ./internal/agentclub ./internal/gatewayclient
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewaySessionClientID|TestSessionStore'
go test -count=1 ./internal/gateway ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
```

`git diff --check` emitted only Windows CRLF normalization warnings and exited
successfully. Remaining unrelated dirty worktree files were left untouched.

Commit/push state: included in the final 012 task commit and pushed after
verification.
