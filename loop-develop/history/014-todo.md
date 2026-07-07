# 014 - Agent-Club Operator UX V0

## Source Research Summary

Once Billyharness has registry/bindings, verified triggers, and safe-output
proposals, the system still needs operator ergonomics. Mature systems expose
automation and approval state where the operator already works.

References:

- OpenHands Agent Canvas presents conversations and automations together, with
  cron/event/custom webhook automations tied to agent conversations:
  <https://docs.openhands.dev/openhands/usage/agent-canvas/overview>
- LangChain/LangGraph HITL emphasizes explicit pending actions and decisions
  rather than hidden chat-message approvals:
  <https://docs.langchain.com/oss/python/langchain/human-in-the-loop>
  <https://docs.langchain.com/oss/python/langgraph/interrupts>
- GitHub Agentic Workflows safe outputs make generated changes inspectable and
  validated before write jobs run:
  <https://github.github.com/gh-aw/reference/safe-outputs/>
- n8n, Activepieces, and Windmill are useful UI references: list enabled pieces
  or integrations, show credentials/resources separately, expose trigger/action
  status, and keep approval operations explicit:
  <https://github.com/n8n-io/n8n-nodes-starter>
  <https://github.com/activepieces/activepieces>
  <https://github.com/windmill-labs/windmill>

Billyharness should stay compact. V0 should add operator visibility and
approval controls through existing surfaces: gateway client, CLI, TUI, and
Telegram. Do not build a web dashboard in this slice.

## Product Direction

Target operator loop:

```text
see enabled capabilities
see recent trigger deliveries / queued inputs
see pending safe-output proposals
approve or reject a proposal
resume normal Billy workflow later
```

V0 should make the invisible agent-club plumbing feel real without adding new
execution authority.

## Checklist

- [ ] Problem: operators cannot see what agent-club capabilities are enabled.
      Add gatewayclient support and CLI command(s) to list registry descriptors
      and trusted bindings from `GET /v1/agentclub/capabilities`.
- [ ] Problem: operators cannot see pending safe-output proposals. Add
      gatewayclient and CLI support for listing proposals for a session, showing
      state, risk, source, capability, action kind, preview/output ref summary,
      created/expires timestamps, and proposal hash short form.
- [ ] Problem: approvals need a low-friction command path. Add CLI commands for
      approve/reject proposal decisions with explicit session id, proposal id,
      expected proposal hash, and optional comment. Refuse ambiguous approvals.
- [ ] Problem: TUI should surface agent-club state without becoming a dashboard.
      Add a compact TUI view/panel or command overlay that can show enabled
      capabilities for the current owner/session and pending proposals. Use
      existing TUI patterns; do not create a marketing/landing page or browser
      UI.
- [ ] Problem: Telegram is where off-terminal approvals matter. Add Telegram
      rendering for pending proposals with short risk/preview text and explicit
      approve/reject actions only if `012` decision APIs exist. Callback data
      must include proposal id and expected hash, and the server must recheck
      authorization before recording a decision.
- [ ] Problem: approval UX can leak secrets. All surfaces must use redacted
      proposal summaries and output refs. Do not show raw payloads unless the
      proposal explicitly marks preview text as safe-to-display and size-capped.
- [ ] Problem: decisions need clear state transitions. UI/CLI/TG should handle
      `pending`, `approved`, `rejected`, `expired`, `superseded`, `stale`,
      and `failed` states without crashing or offering impossible buttons.
- [ ] Problem: no new execution authority. Approving in CLI/TUI/TG records a
      decision only; it must not apply the proposal, run tools, call external
      APIs, or dispatch a run in this slice.
- [ ] Problem: tests should cover operator mistakes. Add tests for list
      rendering, redaction, cross-owner filtering, stale expected hash,
      double-approval refusal, Telegram callback authorization, and TUI state
      formatting.
- [ ] Problem: docs should explain daily use. Add operator docs/examples for
      listing capabilities, inspecting proposals, and approve/reject flows from
      CLI/TUI/Telegram.

## Target Files

Likely edit:

- `internal/gatewayclient/client.go`
- `internal/gatewayclient/client_test.go`
- `cmd/fast-agent-harness/gateway_client_cmd.go` or nearby CLI command files
- `internal/tui/*`
- `internal/telegrambot/*`
- `internal/gatewayapi/types.go`
- `docs/architecture/gateway-and-sessions.md`
- `docs/architecture/security-model.md`
- generated docs via `go run ./cmd/fast-agent-harness docsgen`

Likely add:

- CLI tests for agent-club commands if local pattern exists
- TUI formatting tests for proposal/capability display
- Telegram callback/rendering tests for agent-club proposals

## Architecture Boundaries

- UX surfaces call existing gateway APIs; they do not duplicate approval
  storage or policy.
- No web dashboard in this slice.
- No execution/apply executor in this slice.
- Do not add browser automation, screenshots, or network capture.
- Do not add HH-specific UI strings beyond data coming from descriptors.
- Preserve existing session owner and bearer authorization semantics.

## Verification Commands

```sh
go test -count=1 ./internal/gatewayclient ./cmd/fast-agent-harness
go test -count=1 ./internal/tui ./internal/telegrambot
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewaySessionClientID'
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

If TUI/gateway/Telegram behavior changed broadly:

```sh
go test -count=1 ./internal/gateway ./internal/tui ./internal/telegrambot ./cmd/fast-agent-harness
```

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/014-todo.md end to end. Add operator UX v0 for agent-club capabilities and safe-output proposals across existing Billyharness surfaces. Add gatewayclient and CLI support to list enabled capabilities/bindings, list session proposals, and record approve/reject decisions with explicit session id, proposal id, expected proposal hash, and optional comment. Add compact TUI visibility for current-session capabilities/proposals and Telegram rendering/callbacks for pending proposal approve/reject if the proposal APIs from 012 exist. Preserve redaction, owner-scope authorization, stale-hash refusal, duplicate/double-decision refusal, and clear terminal states. Do not add a web dashboard, execution/apply executor, auto-run, generic command runner, project-local manifest loading, raw API/SQL callers, browser auth/debug, or HH-specific UI hardcode. Do not use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, browser network capture, or browser debug. Update docs/generated docs if CLI/API behavior changes. Verify with the TODO commands, then create a git commit and push the branch after verification passes.
```

## Final Status

Completed on 2026-07-07.

Implemented:

- Gatewayclient redacted agent-club capability/proposal formatters plus hash
  short-form helpers for operator surfaces.
- `fast-agent-harness agentclub` CLI commands for capability discovery,
  proposal listing, and explicit approve/reject decisions with `-session`,
  `-proposal`, `-hash`, and optional `-comment`.
- TUI `/agentclub` compact current-session view backed by the existing gateway
  discovery and proposal APIs.
- Telegram `/agentclub` operator-only rendering with pending proposal
  approve/reject callback buttons. Callback handling re-fetches proposals under
  Telegram owner scope, verifies the expected hash prefix, submits the full
  proposal hash to the gateway decision API, and never applies the proposal.
- Architecture/security/operator docs plus generated CLI/command/package docs.

Verification evidence:

```sh
go test -count=1 ./internal/gatewayclient ./cmd/fast-agent-harness
go test -count=1 ./internal/tui ./internal/telegrambot
go test -count=1 ./internal/gateway -run 'TestAgentClub|TestGatewaySessionClientID'
go test -count=1 ./internal/clientux
go test -count=1 ./internal/gateway ./internal/tui ./internal/telegrambot ./cmd/fast-agent-harness
go test -count=1 ./internal/architecture
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
go test -count=1 ./...
go build ./cmd/fast-agent-harness
```

Commit/push state:

- Implementation commit is `c1a2c0a Add agent-club operator UX`.
- Branch `codex/gateway-ingress-foundation` is pushed and aligned with
  `origin/codex/gateway-ingress-foundation`.

Remaining blockers:

- None for this slice. The worktree still contains unrelated pre-existing
  clipboard/TUI changes that were not part of 014 and should not be staged with
  this completion commit.
