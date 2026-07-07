---
name: loop-research
description: Generate or start Billyharness loop-research runs with guarded Stop-hook iteration counting. Use when Billy asks for loop research, long-running research, a 15-iteration research loop, a Ralph-style Codex loop, a generated loop prompt, or a prompt/result pair under loop-research.
---

# Loop Research

## Overview

Use one skill with two modes instead of separate generate/start skills:

- Generate mode: turn Billy's rough research request into a copy-ready guarded loop prompt.
- Start mode: execute a guarded loop research run in the current chat.

The Stop hook owns `loop-research/iterations`; never add stars manually during an active loop.

## Mode Selection

Use generate mode when Billy asks for a loop prompt, goal prompt, generated prompt, or asks how to phrase a research loop.

Use start mode when Billy explicitly says to start/run/launch the loop in the current chat, or when the pasted prompt already contains `LOOP_RESEARCH_ENABLE`.

## Shared Files

Research files live under `loop-research/`:

- `NNN-prompt.md`: exact prompt used for the loop.
- `NNN-raw.md`: hook-owned append-only raw iteration log.
- `NNN-result.md`: final report after the target is reached.
- `iterations`: hook-owned star counter.
- `.hook-state.json`: untracked hook state that arms one Codex session.

Pick `NNN` by scanning `loop-research/*-prompt.md` and `loop-research/*-result.md`; never reuse a number.

## Generate Mode

Return one copy-ready prompt in a fenced `text` block. Do not create or edit files in generate mode unless Billy explicitly asks.

The generated prompt must contain:

- `LOOP_RESEARCH_ENABLE`
- `LOOP_RESEARCH_ID: <unique-id>`
- `LOOP_RESEARCH_TARGET: <count>`
- `LOOP_RESEARCH_PROMPT: loop-research/NNN-prompt.md`
- `LOOP_RESEARCH_RAW: loop-research/NNN-raw.md`
- `LOOP_RESEARCH_RESULT: loop-research/NNN-result.md`
- instructions to create `loop-research/NNN-prompt.md`;
- instructions to initialize `loop-research/.hook-state.json`;
- instructions not to edit `loop-research/iterations` or `loop-research/NNN-raw.md`;
- the situational research mission synthesized from Billy's request;
- required ending markers:
  - `ITERATION_DONE: <one concrete finding>` after each completed iteration;
  - `LOOP_RESEARCH_RESULT_DONE: loop-research/NNN-result.md` after the final report.

Keep the generated prompt direct and runnable. Include a strong situational mission instead of vague "improve everything" language.

## Start Mode

1. Read `AGENTS.md`.
2. Pick the next `NNN`.
3. Choose a unique `loop_id`, for example `loop-research-001-20260704-stability`.
4. Write `loop-research/NNN-prompt.md` with the exact prompt and activation block.
5. For a fresh loop, reset `loop-research/iterations` to empty before the first iteration. After this setup step, do not edit `loop-research/iterations` again.
6. Arm the Stop hook by writing `loop-research/.hook-state.json`:

```json
{
  "enabled": true,
  "status": "armed",
  "loop_id": "loop-research-001-20260704-stability",
  "target": 15,
  "prompt_path": "loop-research/001-prompt.md",
  "raw_path": "loop-research/001-raw.md",
  "result_path": "loop-research/001-result.md",
  "session_id": null,
  "completed_iterations": 0,
  "processed_turn_ids": [],
  "processed_iteration_keys": []
}
```

7. Begin the loop. Each iteration must choose a distinct angle and produce new evidence-backed value.
8. End each completed iteration with exactly one marker line:

```text
ITERATION_DONE: <one concrete finding>
```

The Stop hook appends the full counted assistant response to `loop-research/NNN-raw.md`. Do not edit that raw file manually during the loop.

9. When the Stop hook says the target is reached, compact `loop-research/NNN-raw.md` into `loop-research/NNN-result.md` and end with:

```text
LOOP_RESEARCH_RESULT_DONE: loop-research/NNN-result.md
```

## Research Quality

Prefer concrete evidence over vibes:

- inspect code, tests, docs, git history, current worktree, and runtime behavior when relevant;
- use primary internet sources for external claims;
- compare external repos clean-room only;
- separate bugs, stability risks, security/authority risks, missing tests, debug gaps, and feature opportunities;
- deduplicate findings before the final report;
- recommend a loop-development TODO only when implementation work is clear.

If findings feel saturated before the target count, switch to adversarial review, failure modes, alternative designs, verification, or synthesis.

## Final Report Shape

Write `loop-research/NNN-result.md` with:

- current iteration count;
- summary compacted from `loop-research/NNN-raw.md`;
- sources inspected;
- top P0/P1 findings with evidence and affected files;
- stability and debuggability risks;
- security and authority risks;
- missing tests or CI gates;
- feature opportunities for Billyharness as a solo harness;
- external clean-room patterns worth copying conceptually;
- recommended next loop-development TODO;
- things not to change yet.
