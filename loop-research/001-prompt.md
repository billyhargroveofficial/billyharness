LOOP_RESEARCH_ENABLE
LOOP_RESEARCH_ID: loop-research-001-20260704-stability-debuggability-architecture
LOOP_RESEARCH_TARGET: 15
LOOP_RESEARCH_PROMPT: loop-research/001-prompt.md
LOOP_RESEARCH_RAW: loop-research/001-raw.md
LOOP_RESEARCH_RESULT: loop-research/001-result.md

You are starting a fresh Billyharness guarded loop-research test in /Users/billy/repos/billyharness.

This is a clean test from zero. Use exactly these files:

- loop-research/001-prompt.md
- loop-research/001-raw.md
- loop-research/001-result.md
- loop-research/iterations
- loop-research/.hook-state.json

Do not use 002 or any later number for this test. If loop-research/001-prompt.md, loop-research/001-raw.md, or loop-research/001-result.md already exists, stop immediately and report that the clean 001 test cannot start without Billy explicitly clearing or archiving those files.

Setup:

1. Read AGENTS.md.
2. Create loop-research/ if needed.
3. Write this exact prompt, including this activation block, to loop-research/001-prompt.md.
4. Reset loop-research/iterations to an empty file before the first research iteration.
5. Do not manually create, truncate, append, or edit loop-research/001-raw.md after arming the hook. The Stop hook owns the raw file.
6. Do not edit loop-research/iterations again after the setup reset. The Stop hook owns the star counter.
7. Initialize loop-research/.hook-state.json exactly for this run:

{
  "enabled": true,
  "status": "armed",
  "loop_id": "loop-research-001-20260704-stability-debuggability-architecture",
  "target": 15,
  "prompt_path": "loop-research/001-prompt.md",
  "raw_path": "loop-research/001-raw.md",
  "result_path": "loop-research/001-result.md",
  "session_id": null,
  "completed_iterations": 0,
  "processed_turn_ids": [],
  "processed_iteration_keys": []
}

After setup, begin iteration 1 immediately.

Mission:

Run a 15-iteration research loop on Billyharness stability, debuggability, and architecture bugs. The primary test is the loop-research Stop hook itself: verify that it counts exactly 15 completed iterations, appends each counted assistant iteration to loop-research/001-raw.md, survives compact context without losing the mission or file paths, stays bound to this session instead of unrelated chats, and forces final compression into loop-research/001-result.md when the target is reached.

Research scope:

- stability risks in gateway, TUI, Telegram adapter, provider paths, tool execution, JSONL replay, lazy MCP, filesystem/checkpoint behavior, and long-running agent loops;
- debuggability gaps: missing logs, weak error messages, hard-to-trace state transitions, unclear recovery paths, brittle verification workflows;
- architecture bugs: unclear ownership boundaries, hidden coupling, unsafe authority boundaries, context/compact assumptions, path confusion, race or TOCTOU risks, state-machine drift;
- missing tests, CI gates, runtime probes, debug commands, or docs that would make failures easier to reproduce;
- clean-room external patterns only when useful conceptually. Do not copy competitor source code.

Research quality rules:

- Prefer concrete evidence from repo files, tests, docs, git history, and local commands over vibes.
- Keep each iteration distinct. If findings saturate, switch angle: adversarial review, failure-mode analysis, state-machine review, test-gap review, logging review, deployment/runtime review, compact-context recovery review, security/authority review, or final synthesis prep.
- Separate bugs, stability risks, security/authority risks, missing tests, debug gaps, and feature opportunities.
- Do not implement fixes during the loop unless Billy explicitly interrupts and asks for implementation.
- Do not move loop-develop TODOs.
- Do not create commits.
- Keep Mac-local path assumptions separate from /root/billyharness production assumptions.

Compact-context recovery rule:

If context compacts or the chat resumes with reduced history, continue the same loop. Before the next iteration, read:

- AGENTS.md
- loop-research/001-prompt.md
- loop-research/.hook-state.json
- loop-research/iterations
- the tail or summary of loop-research/001-raw.md if it exists

Then continue with the next distinct research angle. Do not restart numbering. Do not reinitialize .hook-state.json unless the file is missing or clearly corrupted, and if that happens, report the issue before proceeding.

Iteration output rule:

Each completed iteration must end with exactly one marker line:

ITERATION_DONE: <one concrete finding>

Use one concrete finding in the marker, not a vague summary. The Stop hook appends the full counted assistant response to loop-research/001-raw.md and updates loop-research/iterations. Do not add stars manually.

Hook validation expectations during the loop:

- After early iterations, occasionally inspect loop-research/iterations and loop-research/.hook-state.json to confirm the hook is counting this session.
- Check that loop-research/001-raw.md is growing from hook appends, not manual edits.
- Watch for duplicate counting, skipped counting, wrong-session binding, or lost state after compact context.
- If the hook reports target reached, stop researching and write the final result.

Finalization:

When the Stop hook reports that LOOP_RESEARCH_TARGET 15 is reached, compact loop-research/001-raw.md into loop-research/001-result.md.

The final report must include:

- current iteration count;
- whether the Stop hook reached exactly 15 iterations;
- whether loop-research/001-raw.md contains each counted iteration;
- whether compact-context recovery worked or what evidence exists;
- whether the session guard appears to prevent unrelated chats from advancing this loop;
- sources inspected;
- top P0/P1 Billyharness findings with evidence and affected files;
- stability and debuggability risks;
- security and authority risks;
- missing tests or CI gates;
- feature opportunities for Billyharness as a solo harness;
- external clean-room patterns worth copying conceptually, if any;
- recommended next loop-development TODO;
- things not to change yet.

After writing loop-research/001-result.md, end with exactly this marker line:

LOOP_RESEARCH_RESULT_DONE: loop-research/001-result.md