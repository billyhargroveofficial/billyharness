# 007 - Gateway-Owned Ingress Foundation

## Source Research Summary

Billy wants Billyharness to become more than a human-operated terminal or
Telegram harness: external projects, webhooks, schedules, and local services
should be able to wake an agent, hand it a bounded task, and leave replayable
evidence.

The current Billyharness shape already has the right core for this: gateway
sessions, durable `inputs.jsonl`, idempotent `input_id` handling, session owner
metadata, JSONL replay, gateway auth, and tool/MCP policy gates. The missing
piece is a small, gateway-owned ingress layer that admits external triggers as
session inputs without letting those triggers directly run shell, tools, MCP, or
provider overrides.

External systems point to the same pattern:

- GitHub Agentic Workflows keeps automations inspectable, Markdown-authored, and
  bounded inside GitHub Actions:
  https://github.blog/ai-and-ml/automate-repository-tasks-with-github-agentic-workflows/
- Optio models persistent agents that wake from webhooks, cron ticks, user
  messages, tickets, or agent messages:
  https://github.com/jonwiggins/optio
- Trigger.dev emphasizes long-running tasks, retries, queues, idempotency, cron,
  waitpoints, and run observability:
  https://github.com/triggerdotdev/trigger.dev
- OpenHands Agent Canvas exposes conversations and automations through cron and
  event-driven workflows, including custom webhooks:
  https://docs.openhands.dev/openhands/usage/agent-canvas/overview
- GitHub webhook guidance requires raw-body HMAC verification and constant-time
  compare for webhook delivery trust:
  https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries

Local project scan found useful future adapters:

- `D:\repos\hh-applicant-tool`: best first real adapter. It has recent commits,
  a Python CLI, SQLite state, cohort cron scripts, recruiter-message watch mode,
  dry-run-ish flows, and scheduled research snapshotter docs. Start with
  read-only/dry-run status and review actions; do not let Billyharness call raw
  `call-api`, raw SQL `query`, browser auth, or mutating apply/reply/buttons
  without a later explicit policy slice.
- `D:\repos\nareshka-mono`: strong candidate for MCP-backed practice, stats,
  and spaced follow-up workflows.
- `D:\repos\mapper`: Billydian/vault notes and mindmap workflows could become a
  project adapter once ingress exists.
- `D:\repos\Perplexica`, `D:\CProjs\flov`, `D:\repos\nsay`,
  `D:\CProjs\interview-analytics`, and media/research tools are plausible
  scheduled job targets after the same admission model exists.
- `D:\CProjs\clawd\repo`, `D:\repos\nanoclaw`, and `D:\repos\AionUi` are useful
  only for clean-room comparison of architecture and UX. Do not copy source.

## Decision

Build one foundation slice first: signed or internal external events become
audited gateway admissions into the existing session input ledger. This TODO
does not add a full cron scheduler, HH adapter, UI, or arbitrary project runner.
Those should become separate numbered TODOs after this contract lands.

## Concrete Checklist

- [ ] Problem: external project triggers currently have no generic owner scope,
      so a webhook or scheduler would either look like Telegram/TUI or be too
      broad. Add a generic `ClientID` field to `gatewayapi.SessionOwner`, with
      `HeaderSessionClientID = "X-Billyharness-Session-Client-ID"`, JSON
      `client_id`, normalization, gatewayclient header propagation, persistence,
      filtering, and cross-owner denial tests.
- [ ] Problem: a webhook must not be treated as authority just because it can
      reach the gateway. Define an `internal/ingress` package with small DTOs
      for `IngressEvent`, `IngressRule`, `AdmissionDecision`, and sanitized
      metadata. The package should transform an allowlisted external event into
      a `gatewayapi.SessionInputRequest`, not execute anything directly.
- [ ] Problem: external retries can duplicate work. Generate deterministic
      `input_id` / idempotency keys from rule id, source, external event id,
      payload hash, and target session, then rely on the existing session input
      duplicate/conflict behavior.
- [ ] Problem: webhook payloads can be spoofed or modified. Add raw-body HMAC
      verification helpers with timestamp/skew support where practical, constant
      time signature comparison, and tests for valid, invalid, missing, stale,
      and body-mutated signatures.
- [ ] Problem: failed or rejected external events would otherwise disappear.
      Add a small append-only ingress audit ledger under the gateway store, with
      private file permissions and replay validation, recording only redacted
      data, hashes, decision reason, target session id, and admitted input id.
- [ ] Problem: future adapters could bypass Billyharness safety knobs. Ensure
      ingress admission strips or rejects provider/model/access-mode/tool/MCP
      overrides from payload metadata unless a rule explicitly maps safe static
      values in code/config.
- [ ] Problem: runtime replay and docs currently do not know ingress exists.
      Add protocol/event allowlist entries for gateway-owned ingress lifecycle
      events such as `ingress.received`, `ingress.rejected`,
      `ingress.admitted`, and `ingress.dispatched` if persisted through the
      normal event stream; otherwise document why ingress audit stays separate.
- [ ] Problem: implementation agents need a clear proof boundary. Cover the new
      contract with focused tests for owner matching, deterministic input ids,
      HMAC, audit replay, duplicate/conflict handling, and no direct tool/shell
      execution path.
- [ ] Problem: future Billy will forget the safety contract if it only lives in
      code. Update the architecture docs that apply: gateway/session ingress,
      runtime events, security model, and tools/MCP policy. Add a short ADR if
      the implementation introduces a durable "external ingress is gateway
      admission, not direct execution" invariant.

## Target Files And Boundaries

Expected touch points:

- `internal/gatewayapi/types.go`
- `internal/gatewayclient/client.go`
- `internal/gateway/session_authz.go`
- `internal/gateway/session_events_status_test.go`
- new `internal/ingress/` package
- possible gateway store/audit helper near `internal/gateway/`
- `internal/protocol/event_types.go`
- `internal/protocol/envelope.go`
- `internal/eventlog/` only if the generic JSONL helpers need small reuse
- `docs/architecture/gateway-and-sessions.md`
- `docs/architecture/security-model.md`
- `docs/architecture/runtime-event-system.md`
- `docs/architecture/tools-mcp-and-policy.md`
- optional `docs/adr/NNNN-external-ingress-gateway-admission.md`

Do not:

- add HH-specific commands in this slice;
- add a full scheduler implementation in this slice;
- add arbitrary shell/project command execution;
- use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots,
  browser network capture, or browser debug;
- copy code from competitor research checkouts.

## Verification Commands

Run focused tests while developing:

```sh
go test -count=1 ./internal/ingress ./internal/gateway ./internal/gatewayclient ./internal/protocol ./internal/eventlog
```

If protocol, generated docs, gateway API inventories, or config inventories
change:

```sh
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
```

Before calling the task done:

```sh
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
```

## Copy-Ready Codex Goal Prompt

```text
/goal Implement loop-develop/current-todo/007-todo.md end to end. Build the gateway-owned ingress foundation only: generic ClientID session owner scoping, an internal ingress admission contract, deterministic input ids, raw-body HMAC verification helpers, a redacted ingress audit ledger, focused tests, and the required architecture docs. Do not implement the HH adapter, a full scheduler, UI, arbitrary project command execution, or any browser automation. Do not use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, browser network capture, or browser debug. Work with the existing dirty worktree without reverting unrelated user changes. If generated docs are affected, run docsgen and docsgen -check. Verify with the commands in the TODO, then create a git commit and push the branch after verification passes.
```

## Follow-Up Candidates

After this foundation lands, the next small TODO should likely be
`hh-applicant-tool` read-only/dry-run adapter admission:

- project status and config/profile discovery;
- cohort queue/review status;
- research snapshotter status and manual run admission;
- dry-run apply/reply previews only;
- no raw `call-api`, raw SQL `query`, browser auth, or mutating apply/reply
  until a separate allowlist and review policy exists.

## Final Status

Status: completed and verified.

Implementation branch: `codex/gateway-ingress-foundation`.

Implementation evidence:

- Initial implementation commit `2bd72de` added the gateway-owned ingress
  foundation, `internal/ingress`, gateway admission/audit bridge, generic
  `SessionOwner.ClientID`, docs, ADR 0009, and generated docs.
- Six native Codex review subagents were launched after implementation.
- Review found blockers before archival:
  - ownerless ingress rules could act as unscoped local operators;
  - durable input append happened before any ingress audit evidence;
  - HMAC skew checks allowed unsigned timestamps;
  - rejected source audits could drop attempted target session id;
  - security/gateway docs had stale owner-scope and audit-order wording.
- The verification pass fixed those blockers before this TODO moved to history:
  - ingress rules now require explicit `client_id` and `client_type`;
  - gateway writes a redacted `received` audit record before input admission and
    writes final `admitted` or `rejected` records after admission;
  - audit failure before input admission prevents the input write;
  - rejected audits preserve attempted target session id;
  - HMAC skew requires timestamp-bound signatures;
  - docs and generated package summaries were updated.

Commands run:

```sh
go test -count=1 ./internal/ingress ./internal/gateway ./internal/gatewayclient ./internal/protocol ./internal/eventlog
go test -count=1 ./internal/gateway -run 'TestGatewayIngress|TestGatewaySessionClientID'
go test -count=1 ./internal/ingress -run 'TestAdmit|TestVerifyRawBodyHMACSHA256'
go run ./cmd/fast-agent-harness docsgen
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./internal/architecture ./internal/docsgen
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
```

Verification notes:

- The first full `go test -count=1 ./...` before the review fixes failed in
  `TestRunMessagesEmitsProviderRetryHook` and
  `TestTUIFileMentionIgnoresStaleAsyncResults`; both passed in isolated reruns,
  and a later full suite passed after the ingress fixes.
- Final `git diff --check` exited 0 with only LF/CRLF warnings on the existing
  dirty Windows worktree.
- Existing unrelated dirty files were left alone:
  - `internal/clipboard/image_clipboard_windows.go`
  - `internal/tui/actions.go`
  - `internal/tui/tui_test.go`
  - `internal/clipboard/image_clipboard_windows_test.go`

Remaining follow-up:

- `008-todo.md` starts the next single-slice adapter task for
  `hh-applicant-tool` read-only review queue ingress.
