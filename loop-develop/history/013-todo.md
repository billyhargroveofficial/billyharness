# 013 - HH Applicant Tool Agent-Club Adapter V0

## Source Research Summary

HH Application Tool should not be reintroduced into Billyharness core. The new
agent-club architecture means HH becomes an external adapter that speaks the
generic Billyharness contract.

Local HH findings from the earlier research pass:

- `D:\repos\hh-applicant-tool` has a CLI entrypoint `f228jobfckr`.
- Read-oriented commands include `cohort-review --limit N`, `cohort-status`,
  `negotiations-status`, and related cohort/reporting commands.
- Mutating commands include `cohort-apply`, `cohort-reply`, `cohort-buttons`,
  `cohort-watch`, and `cohort-review --done/--done-all`.
- DeepSeek/OpenAI-compatible code exists in `src/f228jobfckr/ai/openai.py`;
  existing operations log AI outputs through `ai_outputs`.
- `cohort-watch` can chain refresh, LLM classification, replies, and buttons,
  so it is not a safe v0 adapter target.
- Research snapshotter/browser-CDP scripts exist and must remain outside this
  slice because Billy's project rules forbid browser debug/CDP flows unless
  explicitly commanded.

Relevant external patterns:

- MCP and Apps SDK keep capabilities declarative and schema-bound, not hidden
  behind arbitrary shell:
  <https://modelcontextprotocol.io/specification/draft/server/tools>
  <https://developers.openai.com/apps-sdk/plan/tools>
- Trigger.dev/Temporal show that external projects should emit durable events,
  not force the receiving host to run project internals synchronously:
  <https://trigger.dev/docs/idempotency>
  <https://docs.temporal.io/workflows>
- Safe-output systems separate draft/proposal from apply. HH reply/apply
  actions should go through `012` proposals before any future mutating executor:
  <https://github.github.com/gh-aw/reference/safe-outputs/>
  <https://docs.langchain.com/oss/python/langchain/human-in-the-loop>

## Product Direction

Build the first HH adapter in `D:\repos\hh-applicant-tool`, not in Billyharness
core. It should read HH state, produce normalized Billyharness agent-club
events, and optionally create safe-output proposals after `012`.

V0 target flow:

```text
f228jobfckr billy-agentclub review-queue --session-id <id> --profile <profile>
  -> run existing read-only HH command(s)
  -> build agentclub.EventRequest
  -> POST Billy /v1/sessions/{id}/agentclub/events
  -> queued Billy session input, no Billy-side HH execution
```

## Checklist

- [ ] Problem: HH integration must live outside Billyharness core. Implement the
      adapter in `D:\repos\hh-applicant-tool`, likely as new CLI subcommands
      under `f228jobfckr billy-agentclub ...`. Do not add HH imports,
      constants, repo roots, command names, or profiles to Billyharness core.
- [ ] Problem: v0 needs a safe capability set. Support only read-only snapshot
      events first: `hh.review_queue.snapshot`, `hh.cohort.status`, and maybe
      `hh.ai_outputs.digest` if it can be read from existing local DB without
      running new LLM calls or mutating HH state.
- [ ] Problem: adapter identity must match Billy bindings. Send owner headers
      `X-Billyharness-Session-Client-Type: ingress` and
      `X-Billyharness-Session-Client-ID: ingress:hh-applicant-tool:<profile>`
      or the exact binding identity created in `010/011`.
- [ ] Problem: HH output can contain recruiter text and secrets. Build prompts
      that mark HH content as untrusted external content, cap payload/prompt
      size, hash raw snapshots, and avoid putting raw recruiter text in Billy
      metadata or adapter logs.
- [ ] Problem: adapter retries must be idempotent. Derive
      `external_event_id` from profile, command/snapshot kind, normalized args,
      snapshot payload hash, and target session id. Duplicate sends should
      become duplicate Billy inputs, not repeated work.
- [ ] Problem: mutating HH actions must stay out of v0. Do not call
      `cohort-apply`, `cohort-reply`, `cohort-buttons`, `cohort-watch`,
      `cohort-review --done`, `cohort-review --done-all`, raw HH API mutation,
      raw SQL mutation, or browser/CDP auth/snapshotter scripts.
- [ ] Problem: DeepSeek work must be explicit. Reading existing `ai_outputs`
      summaries is allowed if it is read-only. Running new DeepSeek
      classification/reply generation belongs to a later proposal/safe-output
      slice, not this adapter v0.
- [ ] Problem: operator setup should be boring. Add docs in HH repo explaining
      required Billy gateway URL/token, owner headers, binding/capability names,
      dry-run behavior, and example commands. Do not store Billy tokens in the
      repo.
- [ ] Problem: tests must prove the adapter does not mutate HH. Add unit tests
      with fake Billy server and fake HH command/data source covering request
      shape, idempotent event id, owner headers, output caps, dry-run/no-post,
      and refusal of mutating commands.

## Target Files

Primary target repository:

- `D:\repos\hh-applicant-tool`

Likely HH files to inspect/edit:

- `README.md`
- `pyproject.toml`
- `src/f228jobfckr/main.py` or command registration modules
- `src/f228jobfckr/operations/cohort_review.py`
- `src/f228jobfckr/operations/cohort_status*.py` if present
- `src/f228jobfckr/ai/audit.py`
- new `src/f228jobfckr/operations/billy_agentclub.py`
- new tests under HH repo's test layout

Billyharness files should not need code changes if `010/011/012` exist. If a
small docs reference is useful, update only loop/history notes or architecture
docs after confirming it does not reintroduce HH into core.

## Architecture Boundaries

- HH adapter owns reading HH state and posting Billy events.
- Billyharness owns generic registry, bindings, admission, approval, and run
  execution.
- HH adapter v0 is read-only and admit-only.
- No browser/CDP/snapshotter integration in this slice.
- No HH mutating commands or LLM reply generation in this slice.
- No Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots,
  network capture, or browser debug.

## Verification Commands

Adjust commands to HH repo tooling after inspection. Expected shape:

```sh
cd D:\repos\hh-applicant-tool
python -m pytest
python -m f228jobfckr billy-agentclub review-queue --help
python -m f228jobfckr billy-agentclub status --help
git diff --check
```

If Billyharness docs or client contracts are touched:

```sh
cd D:\repos\billyharness
go test -count=1 ./internal/agentclub ./internal/gatewayclient
go run ./cmd/fast-agent-harness docsgen -check
git diff --check
```

## Copy-Ready Goal Prompt

```text
/goal Implement loop-develop/current-todo/013-todo.md end to end. Build the first HH Applicant Tool adapter as an external client of Billyharness agent-club, not as Billyharness core code. Work primarily in D:\repos\hh-applicant-tool. Add read-only f228jobfckr billy-agentclub commands that capture safe HH snapshots such as review queue/status/AI-output digest, construct normalized agentclub.EventRequest payloads, send them to Billyharness /v1/sessions/{id}/agentclub/events with client_type=ingress and client_id=ingress:hh-applicant-tool:<profile>, and preserve idempotency, output caps, redaction, dry-run/no-post behavior, and untrusted-content prompting. Do not add HH-specific imports, constants, routes, repo roots, or command execution to Billyharness core. Do not call cohort-apply, cohort-reply, cohort-buttons, cohort-watch, cohort-review --done/--done-all, raw HH API mutation, raw SQL mutation, browser/CDP scripts, or new DeepSeek classification/reply generation. Do not use Playwright, Puppeteer, Chrome MCP, headless Chrome/Edge, screenshots, browser network capture, or browser debug. Add HH tests with fake Billy and fake HH data sources; update HH docs; run the relevant HH verification commands; if Billyharness is touched, run its focused checks too. Create a git commit and push the branch after verification passes.
```

## Final Status

Completed on 2026-07-07.

Implemented the HH Applicant Tool adapter externally in
`D:\repos\hh-applicant-tool`, not in Billyharness core. Added
`f228jobfckr billy-agentclub` with read-only snapshot subcommands:

- `review-queue` -> `hh.review_queue.snapshot`
- `status` -> `hh.cohort.status`
- `ai-output-digest` -> `hh.ai_outputs.digest`

The adapter opens the profile SQLite DB in read-only URI mode, builds normalized
Billyharness `agentclub.EventRequest` payloads, sends owner headers
`client_type=ingress` and `client_id=ingress:hh-applicant-tool:<profile>`, and
supports dry-run/no-post output. Event ids are deterministic from profile,
snapshot kind, session id, args, and payload hash. Payloads are capped and mark
HH content as untrusted external content. AI prompt/response bodies are hashed
in digest snapshots rather than emitted raw.

No HH-specific imports, constants, routes, repo roots, or command execution were
added to Billyharness core. The adapter does not call `cohort-apply`,
`cohort-reply`, `cohort-buttons`, `cohort-watch`, `cohort-review --done`,
`cohort-review --done-all`, raw HH mutation APIs, raw SQL mutation, browser/CDP
scripts, or new LLM generation.

HH verification passed:

```sh
PYTHONPATH=src python -m unittest tests.test_billy_agentclub
PYTHONPATH=src uv run --with pytest --with requests --with urllib3 --with httpx python -m pytest tests/test_billy_agentclub.py
PYTHONPATH=src uv run --with requests --with urllib3 --with httpx --with prettytable python -m f228jobfckr billy-agentclub review-queue --help
PYTHONPATH=src uv run --with requests --with urllib3 --with httpx --with prettytable python -m f228jobfckr billy-agentclub status --help
PYTHONPATH=src uv run --with requests --with urllib3 --with httpx --with prettytable python -m f228jobfckr billy-agentclub ai-output-digest --help
uv run --with ruff ruff check src/f228jobfckr/operations/billy_agentclub.py tests/test_billy_agentclub.py
git diff --check
```

The ambient Python environment did not have `pytest`/project dependencies, so
the pytest and help checks were run through `uv run --with ...`; `unittest`
also passed directly. HH commit `e0605ac6` was pushed to `origin/main`.

Billyharness code was not touched for this slice. This history record is the
only Billyharness change for 013.
