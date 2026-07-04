# Stop Hook Docguard

Status: installed as a project-local Codex `Stop` hook.

This document defines the Billyharness Codex `Stop` hook for a fast
documentation drift check. The hook is installed in `.codex/hooks.json` and
implemented by `.codex/hooks/docguard_stop.py`.

## Codex Hook Constraints

The hook follows the official Codex Hooks behavior:

- `Stop` hooks run at turn scope.
- `Stop` does not support `matcher`; any configured `matcher` is ignored.
- Only `type: "command"` hook handlers run today.
- Command hooks run with the session `cwd` as their working directory.
- If a `Stop` hook writes to `stdout` with exit code `0`, that output must be
  JSON. Plain text output is invalid for `Stop`.
- For `Stop`, `{"decision":"block","reason":"..."}` does not reject the turn.
  It asks Codex to continue by creating a new continuation prompt from
  `reason`.
- `stop_hook_active` tells the hook whether the current turn was already
  continued by a `Stop` hook. The docguard must use it to avoid loops.

Reference: [Codex Hooks](https://developers.openai.com/codex/hooks).

## Philosophy

The docguard is a soft gate, not CI.

It should be quick, deterministic, and local. Its job is to catch obvious
documentation drift at the end of an agent turn, then give the agent one more
chance to update docs or explain why no docs change is needed. It should not
run the full test suite, inspect production, reach the network, rewrite files,
or replace human review.

Target runtime is under 30 seconds for ordinary turns.

## Intended Checks

The hook reads the JSON object from `stdin` and uses `cwd` as the starting
directory. It resolves the git root with:

```sh
git rev-parse --show-toplevel
```

If the command fails or the root is not the Billyharness checkout, the hook
passes silently.

Collect changed paths with:

```sh
git diff --name-only --diff-filter=ACMRTUXB HEAD --
git ls-files --others --exclude-standard
```

The first command covers staged and unstaged tracked changes relative to `HEAD`.
The second command catches untracked documentation or implementation files that
would otherwise be invisible to the drift check.

### Docs Touched

Treat these as documentation surfaces:

- `AGENTS.md`
- `llms.txt`
- `.agents/rules/**/*.md`
- `docs/**/*.md`
- `agent-index/docs-manifest.json`
- `agent-index/**/*.md`

Treat these as active-work surfaces, not durable docs:

- `loop-develop/current-todo/**/*.md`
- `loop-develop/history/**/*.md`

Active loop evidence may satisfy the soft hook's evidence condition, but it is
not a substitute for updating stable docs when public behavior or architecture
changed.

### Docs-Sensitive Changes

If any non-test implementation path changes and no documentation, index, or
loop evidence changes, the hook blocks once with a continuation prompt asking
Codex to inspect the relevant docs and either update them or record
docs-not-needed evidence.

Docs-sensitive paths include:

- `cmd/fast-agent-harness/**/*.go`
- `internal/**/*.go`, except `*_test.go`-only changes
- `README.md`
- `go.mod`
- `go.sum`
- `ops/**`
- `.agents/rules/**`
- `agent-index/**`

The continuation reason should name the changed paths and say which docs
indexes to check first: `llms.txt`, `.agents/rules/README.md`,
`docs/README.md`, `docs/documentation-system.md`, and
`agent-index/docs-manifest.json`.

If `stop_hook_active` is already `true`, the hook must not block again for this
heuristic. It should otherwise let the turn finish.

### Forbidden Active-Work Language

The hook scans added lines in changed `docs/**/*.md` files for active-work
language that does not belong in durable architecture docs.

Block on newly added lines outside fenced code blocks that match phrases such
as:

- `copy-ready Codex /goal`
- `/goal prompt`
- `implementation checklist`
- `active TODO`
- `current TODO`
- `completion evidence`
- `commands run`
- `remaining blockers`
- markdown task checkboxes like `- [ ]` or `- [x]`

This scan operates on the diff, not the whole existing file, so legacy
research and documentation-policy text do not create unrelated failures.
`docs/documentation-system.md` may describe the policy itself, but it should
not add a real active-work checklist.

### Manifest JSON

Always validate the manifest when the file exists. The hook parses
`agent-index/docs-manifest.json` with Python so it does not depend on `jq`;
verification can still use `jq empty agent-index/docs-manifest.json`.

### Whitespace

Run for tracked changed files:

```sh
git diff --check
```

Any whitespace error blocks once with the failing output.

### Architecture Guard

Run:

```sh
go test -count=1 ./internal/architecture
```

only when one of these is true:

- `docs/architecture.md` changed;
- changed Go diffs under `internal/**` or `cmd/**` add or remove `package` or
  `import` lines;
- untracked Go files under `internal/**` or `cmd/**` contain package/import
  declarations or `billyharness/internal` imports.

If `go` is missing, pass with a JSON `systemMessage`. If the guard runs and
fails, block once with the failure summary.

## Block Behavior

The hook should block by exiting `0` and writing JSON to `stdout`:

```json
{
  "decision": "block",
  "reason": "Documentation drift check found issues. Fix them or explain why docs stay unchanged, then run the listed verification."
}
```

The `reason` must be concise and actionable. Include:

- changed paths that triggered the check;
- failed command names;
- the first useful error lines;
- the exact command the agent should rerun.

Do not use `continue: false` for docguard failures. For `Stop`, a block
decision is the desired continuation mechanism.

To avoid loops, when `stop_hook_active` is `true` or
`BILLYHARNESS_DOCGUARD_STOP_ACTIVE=1`, the hook passes instead of blocking
again. If a future version chooses to report unresolved issues, it must emit
JSON such as:

```json
{
  "systemMessage": "Docguard still sees documentation drift; final response should mention the remaining risk."
}
```

## Silent Pass Behavior

The hook should exit `0` with no `stdout` when:

- the session is outside the Billyharness git checkout;
- there are no changed paths;
- only active loop files changed;
- docs-sensitive files changed and a documentation, index, or active-loop
  evidence surface also changed;
- docs-sensitive files changed without docs, but `stop_hook_active` is already
  `true`;
- all mechanical checks pass.

Do not print success text. Plain text on `stdout` is invalid for `Stop`.

## Config Skeleton

Installed project-local shape:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "ROOT=$(git rev-parse --show-toplevel 2>/dev/null) && /usr/bin/env python3 \"$ROOT/.codex/hooks/docguard_stop.py\" || true",
            "timeout": 30,
            "statusMessage": "Checking documentation drift"
          }
        ]
      }
    ]
  }
}
```

Notes:

- Do not add `matcher`; `Stop` ignores it.
- Keep the hook project-local, under `.codex/hooks/`.
- Keep pass output empty.
- Keep block output as JSON.
- Keep stderr for diagnostics that do not need to be model-visible.
