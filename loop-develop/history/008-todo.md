# 008 - HH Review Queue Ingress Admission (Read-Only)

## Source Research Summary

`007` landed the shared gateway-owned ingress foundation. The next useful
Billy-facing slice is to connect one real local project without widening the
blast radius.

`D:\repos\hh-applicant-tool` is the best first adapter candidate because it is
recent, heavily used, CLI-driven, and already has a review queue for cases that
need human/agent attention. Its real read-only command is:

```sh
f228jobfckr cohort-review --limit N
```

The same operation has mutating flags:

```sh
f228jobfckr cohort-review --done <id> [<id> ...]
f228jobfckr cohort-review --done-all
```

This TODO must only read pending review queue output and admit it as a queued
gateway session input. It must not mark anything done, apply to vacancies, reply
to recruiters, press bot buttons, start the watch daemon, run browser auth, run
raw API calls, run raw SQL, or dispatch a Billyharness run.

## Decision

Build a narrow HH adapter over `Server.AdmitIngressEvent`:

- fixed argv execution only;
- no shell;
- bounded timeout and output size;
- explicit repo/profile validation;
- existing session target required;
- owner scope `client_type=ingress` and
  `client_id=ingress:hh-applicant-tool:<profile>`;
- deterministic external event identity;
- safe metadata only;
- response returns admission/audit/input information, not a run result.

## Concrete Checklist

- [ ] Problem: Billy needs one real external project to wake the harness, not
      just abstract ingress plumbing. Add a small `internal/agentclub/hhapplicant`
      package that models only the HH review queue read path.
- [ ] Problem: project adapters are dangerous if they become generic command
      runners. Execute only fixed argv for `f228jobfckr cohort-review --limit N`
      with no shell, an injectable runner for tests, a timeout, an output byte
      cap, and validation that the repo root is `D:\repos\hh-applicant-tool` or
      an explicitly configured equivalent.
- [ ] Problem: HH review output can contain recruiter text that should reach the
      agent prompt but not leak into audit metadata. Build the prompt from stdout
      while keeping ingress metadata to safe keys such as project, profile,
      command name, output hash, row count if parsed, and policy label.
- [ ] Problem: repeated manual clicks or retries should not duplicate work.
      Generate a deterministic external event id from profile, command, limit,
      stdout hash, and target session id, then let ingress generate the stable
      input id.
- [ ] Problem: an HH adapter must not impersonate Telegram/TUI or a broad
      ingress actor. Admit with `client_type=ingress` and
      `client_id=ingress:hh-applicant-tool:<profile>`, and cover cross-profile
      denial in tests.
- [ ] Problem: a read-only review queue should not silently start an agent run.
      Add a gateway admission route for an existing session, for example
      `POST /v1/sessions/{id}/agentclub/hh/review-queue`, that returns the
      admitted input id, duplicate flag, audit status, output hash, and no run
      dispatch.
- [ ] Problem: HH has adjacent mutating commands that look tempting. Explicitly
      reject or leave unreachable raw `call-api`, raw SQL `query`, browser auth,
      `cohort-review --done`, `cohort-review --done-all`, `cohort-apply`,
      `cohort-reply`, `cohort-buttons`, `cohort-watch`, scheduler/watch modes,
      and arbitrary command args.
- [ ] Problem: future agents need a stable contract. Add typed gateway API and
      gatewayclient helpers only for this route, with tests for success,
      duplicate admission, command failure, timeout, output cap, invalid limit,
      invalid repo root, and no dispatch.
- [ ] Problem: public behavior changes must be discoverable. Update architecture
      and security docs, and run `docsgen` if CLI/API/generated inventories
      change.

## Target Files And Boundaries

Expected touch points:

- new `internal/agentclub/hhapplicant/` package;
- possible new `cmd/fast-agent-harness/agentclub_cmd.go`;
- `cmd/fast-agent-harness/subcommands.go` if a CLI command is added;
- `internal/clientux/cli_docs.go` if CLI docs are registered;
- `internal/gateway/routes.go`;
- possible new `internal/gateway/agentclub_hh.go`;
- `internal/gatewayapi/types.go`;
- `internal/gatewayclient/client.go`;
- `docs/architecture/gateway-and-sessions.md`;
- `docs/architecture/security-model.md`;
- generated docs only through `go run ./cmd/fast-agent-harness docsgen`.

Do not:

- edit `D:\repos\hh-applicant-tool` except read-only inspection;
- implement a scheduler;
- implement a generic project runner;
- implement mutating HH actions;
- run browser automation or browser debug;
- use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, or
  browser network capture;
- call raw `call-api`, raw SQL `query`, `cohort-watch`, browser auth, or any
  command outside the allowlisted review queue argv.

## Verification Commands

Run focused tests while developing:

```sh
go test -count=1 ./internal/agentclub/hhapplicant ./internal/gateway ./internal/gatewayclient ./cmd/fast-agent-harness
```

If CLI/API/generated docs change:

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
/goal Implement loop-develop/current-todo/008-todo.md end to end. Keep it single-slice: add a read-only HH review queue adapter that captures only D:\repos\hh-applicant-tool `f228jobfckr cohort-review --limit N` output, converts it into a gateway-owned ingress admission for an existing Billyharness session, returns admitted input/audit information, and never dispatches a run. Use fixed argv execution with no shell, timeout, output cap, repo/profile validation, safe metadata, deterministic external event identity, and owner scope `client_type=ingress` plus `client_id=ingress:hh-applicant-tool:<profile>`. Do not implement a scheduler, generic project runner, raw command execution, raw call-api, raw SQL query, browser auth, `cohort-review --done`, `cohort-review --done-all`, `cohort-apply`, `cohort-reply`, `cohort-buttons`, `cohort-watch`, or any mutating HH action. Do not edit D:\repos\hh-applicant-tool except for read-only inspection if needed. Do not use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, browser network capture, or browser debug. Update docs and generated docs if the CLI/API surface changes. Verify with the TODO commands, then create a git commit and push the branch after verification passes.
```

## Final Status

Status: completed and verified.

Implementation branch: `codex/gateway-ingress-foundation`.

Implementation commit:

- `cb5a256 Add HH review queue ingress admission`

Verification summary:

- Added `internal/agentclub/hhapplicant` as a narrow read-only adapter for
  `f228jobfckr cohort-review --limit N`.
- Added gateway route
  `POST /v1/sessions/{id}/agentclub/hh/review-queue`.
- Added typed gateway API and gatewayclient helper.
- Confirmed the adapter uses fixed argv with `exec.CommandContext`, no shell,
  timeout, output cap, repo root allowlist, profile validation, deterministic
  external event identity, and owner scope
  `client_type=ingress` plus `client_id=ingress:hh-applicant-tool:<profile>`.
- Confirmed the route admits a queued input through ingress, returns input/audit
  metadata, and does not dispatch a run.
- Confirmed HH stdout can reach the queued prompt while ingress audit metadata
  stores hashes/keys rather than recruiter text, external event id, or client id.
- Confirmed mutating HH actions remain outside the implemented route: no raw
  `call-api`, raw SQL `query`, browser auth, `cohort-review --done`,
  `cohort-review --done-all`, `cohort-apply`, `cohort-reply`,
  `cohort-buttons`, `cohort-watch`, scheduler, or generic project runner.
- Confirmed docs and generated gateway/package docs were updated in the
  implementation commit.

Commands run:

```sh
go test -count=1 ./internal/agentclub/hhapplicant ./internal/gateway ./internal/gatewayclient ./cmd/fast-agent-harness
go run ./cmd/fast-agent-harness docsgen -check
go test -count=1 ./...
go build -o ./bin/fast-agent-harness ./cmd/fast-agent-harness
git diff --check
```

Verification notes:

- All commands passed.
- `git diff --check` exited 0 with only LF/CRLF warnings on pre-existing dirty
  Windows worktree files.
- Existing unrelated dirty Billyharness files were left alone:
  - `internal/clipboard/image_clipboard_windows.go`
  - `internal/tui/actions.go`
  - `internal/tui/tui_test.go`
  - `internal/clipboard/image_clipboard_windows_test.go`
- `D:\repos\hh-applicant-tool` was inspected read-only for command shape; its
  dirty local changes were not touched.

Remaining follow-up:

- Add a separate TODO for scheduler/jobqueue or for the next HH action only
  after choosing a new narrow read-only or explicit review-gated slice.
