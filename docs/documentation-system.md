# Documentation System

This document defines how Billyharness documentation is organized for humans,
Codex, and future documentation agents. It is about the documentation system
itself, not a backlog of docs to write.

The current documentation system is an initial routing layer, not an
exhaustive reference set. Canonical docs describe stable architecture contracts;
source code, verified command output, and live-host inspection remain the
authority for details that do not yet have generated or dated runbook coverage.

## Goals

- Make the right document easy for an agent to find before editing.
- Keep root `AGENTS.md` short and durable.
- Keep `docs/` as architecture canon, not implementation scratch space.
- Separate stable decisions from active work and temporary research.
- Make generated or machine-oriented indexes explicit.
- Give future agents clear rules for when documentation must change.

## Layers

| Layer | Path | Purpose | Write policy |
| --- | --- | --- | --- |
| Agent contract | `AGENTS.md` | Short project rules, routing, verification, and source-of-truth order. | Edit only when recurring workflow rules or routing change. |
| Agent deep rules | `.agents/rules/` | Detailed rules for documentation, verification, subagents, security, and other recurring workflows. | Edit when a reusable agent behavior rule changes. |
| Agent index | `llms.txt` | Small LLM-readable map of the repo and docs. | Keep concise; link rather than duplicate. |
| Machine index | `agent-index/` | JSON/Markdown metadata for docs, generated maps, freshness, and read triggers. | Generated or structured; do not use for prose canon. |
| Architecture canon | `docs/` | Stable architecture maps, runtime/security contracts, ADRs, and clean-room research. | No active TODOs, goal prompts, runbooks, or evidence logs. |
| Active work | `loop-develop/current-todo/` | Current implementation checklist, evidence, and copy-ready goal prompt. | Update during implementation; do not copy wholesale into `docs/`. |
| Completed work | `loop-develop/history/` | Verified implementation history and evidence. | Append final evidence when moving a completed TODO. |
| Operations | `ops/` | Dated production runbooks and operator procedures. | Commands must be verified and dated; keep outside `docs/`. |
| Generated reference | `reference/` or `agent-index/generated/` | Future generated CLI/API/config/protocol/tool references. | Mark generated files and regenerate from source. |

## `docs/` Hierarchy

Current stable shape:

```text
docs/
  README.md
  architecture.md
  documentation-system.md
  architecture/
    config-provider-context.md
    gateway-and-sessions.md
    runtime-event-system.md
    security-model.md
    telegram-and-operator-surfaces.md
    tools-mcp-and-policy.md
    tui-and-clientux.md
  adr/
    README.md
    0001-jsonl-event-log-source-of-truth.md
    0002-gateway-owns-session-authority.md
    0003-mcp-instructions-are-untrusted-metadata.md
    0006-telegram-is-a-gateway-client.md
    0007-local-gateway-mutating-routes-require-explicit-trust.md
  research/
    README.md
  codex-research-roadmap.md
  competitive-architecture-analysis.md
```

The top-level research files are legacy or clean-room source material, not
current architecture truth. Move or delete them only with matching link and
manifest updates.

Keep `docs/architecture.md` at its current path until the architecture guard is
changed, because `internal/architecture` parses its `Package Map`.

## Document Types

### Canon

Canonical documents describe current architecture and rules. They must be
verified against code when changed.

Examples:

- package boundary maps;
- runtime event/replay contracts;
- gateway/client authority rules;
- tool/MCP/security boundaries;
- documentation-system rules.

### ADR

Architecture decision records are append-only rationale for significant
decisions. Use `docs/adr/`.

Each ADR should include:

- title and number;
- status: `proposed`, `accepted`, `superseded`, or `deprecated`;
- date;
- context;
- decision;
- consequences;
- verification or affected code;
- supersedes/superseded-by links when relevant.

Do not rewrite an accepted ADR when the decision changes. Create a new ADR and
mark the old one superseded.

### Research

Research documents are source material, not truth. They may contain competitor
analysis, internet research, or abandoned ideas. They must clearly say if they
are historical.

Do not put active implementation checklists in research docs. Extract work into
`loop-develop/current-todo/NNN-todo.md`.

### Generated Reference

Detailed generated-reference and docguard strategy lives in
[`../agent-index/generated/reference-plan.md`](../agent-index/generated/reference-plan.md).
Keep this section as the durable policy summary and put extractor details there.
As of the initial documentation system, those references are planned coverage,
not implemented generated output; the current repo map is a seed.

Generated docs must start with a marker:

```md
<!-- Code generated by fast-agent-harness docsgen; DO NOT EDIT.
Source: path/or/glob
Command: command used to regenerate
-->
```

Generated sections inside handwritten files must use:

```md
<!-- BEGIN GENERATED: name -->
...
<!-- END GENERATED: name -->
```

Prefer temp-output comparison during verification so generation does not dirty
the worktree unexpectedly.

Generated reference files should also record source globs, source commit,
source hash, generation time, and `dirty_at_generation`. The top-level manifest
`dirty_at_generation` describes the whole worktree snapshot; per-document
generated metadata should describe the source globs and generator files for
that specific output.

## Agent Read Rules

Before editing, read:

- `AGENTS.md`;
- the closest active TODO in `loop-develop/current-todo/` when doing loop work;
- `.agents/rules/README.md` to find detailed rule files when the task triggers
  one;
- `docs/README.md` and `docs/architecture.md` before changing package
  boundaries, import rules, durable architecture, replay/session contracts, or
  cross-surface behavior;
- the specific canonical doc, ADR, or generated reference for the code being
  changed.

Do not read every documentation file by default. Use `llms.txt`,
`.agents/rules/README.md`, and `agent-index/docs-manifest.json` to select the
smallest relevant set.

## Agent Write Rules

Update documentation in the same implementation cycle when a code change alters:

- public behavior;
- CLI commands, flags, output, or validation errors;
- gateway APIs or event protocol;
- auth, security, permissions, or owner/scope semantics;
- config keys, env vars, profiles, provider/model behavior;
- deployment, service, or operator workflow;
- package ownership, import boundaries, or architecture invariants;
- tool/MCP schemas, lifecycle, or discovery behavior;
- examples that users or agents copy.

Do not update durable docs for pure internal refactors unless a documented
contract changes.

Do not document unimplemented or unverified behavior as current truth. If a
future direction matters, put it in a TODO or a proposed ADR.

## Drift Prevention

The first drift-prevention layer is already live:

- `internal/architecture` parses `docs/architecture.md` and enforces package
  import boundaries.

Next layers should be added as implementation work:

- a Codex `Stop` hook docguard, designed in
  `../.agents/rules/stop-hook-docguard.md`, that performs a fast turn-end
  documentation drift check before later CI layers;
- a `docguard` test package that checks docs index links, local Markdown links,
  manifest coverage, ADR numbering/status links, stale generated references,
  and forbidden active-work language in canonical `docs/`;
- docs freshness metadata in `agent-index/docs-manifest.json`;
- ADR numbering/status/link checks;
- generated-reference diff checks;
- optional scheduled link checking in CI.

## Migration Notes

Existing large research documents in `docs/` are legacy source material. Before
deleting them, extract stable rules into canonical docs or ADRs, and extract
actionable work into `loop-develop/current-todo`.

Do not move `docs/architecture.md` until the architecture guard is updated to
read the new path.
