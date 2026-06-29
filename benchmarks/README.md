# Agent Benchmark Notes

This directory contains local smoke tasks plus notes for wiring external agent benchmarks into `fast-agent-harness-go`.

## Selected External Benchmarks

1. Terminal-Bench
   - Repo: https://github.com/harbor-framework/terminal-bench
   - Good fit: real terminal tasks, Docker sandbox, English instructions, test scripts, oracle solutions.
   - Quickstart from README: `uv tool install terminal-bench` or `pip install terminal-bench`, then `tb run --help`.
   - Current adapter: `fast-agent-harness bench terminal-bench export` writes a local Terminal-Bench dataset from Billyharness JSONL tasks, including `task.yaml`, Docker files, workspace copy, and a pytest-compatible evaluator bridge. `fast-agent-harness bench terminal-bench import` converts Terminal-Bench task directories back to Billyharness JSONL tasks.
   - Immediate strategy: use the adapter for local `tb run --dataset-path ...` workflows first, then add direct Harbor/Terminal-Bench 2.0 result ingestion once the upstream format stabilizes.

2. SWE-bench / mini-swe-agent style
   - mini-swe-agent repo: https://github.com/SWE-agent/mini-swe-agent
   - SWE-bench repo: https://github.com/swe-bench/SWE-bench
   - Good fit: real GitHub bugfix tasks, patch output, Docker evaluator.
   - Immediate strategy: run a tiny SWE-bench Lite slice with max workers 1 after implementing patch extraction.

3. Commit0
   - Project: https://github.com/commit-0/commit0
   - Good fit: library-from-spec tasks, simpler than full SWE-bench on a weak VPS.
   - Immediate strategy: run 1 repo at a time and use `commit0 test`/`commit0 evaluate` as evaluator.

4. GitTaskBench
   - Repo: https://github.com/QuantaAlpha/GitTaskBench
   - Good fit: 54 repo-level practical tasks across web/PDF/image/audio/video domains.
   - Quickstart from README: install package, produce outputs, run `gittaskbench grade --taskid <id>`.
   - Immediate strategy: start with text/web/PDF tasks; avoid video/audio/image tasks on this small server.

5. SWE-EVO
   - Repo: https://github.com/SWE-EVO/SWE-EVO
   - Good fit: long-horizon software evolution. This is the closest match for 50-100+ step coding agents.
   - Quickstart from README: `pip install -e .`, generate trajectories via OpenHands/SWE-agent scaffolds, then `python SWE-bench/evaluate_instance.py --trajectories_path ... --max_workers ... --scaffold ...`.
   - Immediate strategy: second-stage benchmark after the harness can export trajectories/patches in a compatible format.

## Metrics To Keep

`fast-agent-harness bench run` writes a small replayable run bundle:

- manifest JSON: schema version, run metadata, and bundle paths
- results JSONL: one summarized object per task
- events JSONL: sequenced agent events for replay and latency analysis
- payloads directory: redacted full event payloads for large/sensitive records

Minimum report fields:

- pass/fail/timeout/crash
- wall time
- model calls
- tool calls by name
- provider input/output tokens when available
- evaluator command, evaluator time, evaluator output

## Fixture Ownership And Generated Output Policy

Tracked benchmark fixtures should be hand-owned source inputs, not outputs from
benchmark runs. Keep generated run bundles under ignored output directories such
as `bench-runs/`, `tb-runs/`, or another explicit scratch path outside
`benchmarks/`.

Each task suite owns its task JSONL and workspace templates:

- `benchmarks/local-code-smoke/` owns small standalone JS/Python workspaces used
  by local code smoke tasks.
- `benchmarks/agent-loop-stress/` owns deterministic mock-agent loop tasks.
- `benchmarks/mcp-smoke/` owns the fake MCP server and its test MCP config.

Workspace templates should stay runnable in isolation. Small duplicate files are
allowed when they make each fixture self-contained and avoid hidden coupling
between tasks. Current allowlisted duplicates:

- `benchmarks/local-code-smoke/workspace/package.json`
- `benchmarks/local-code-smoke/workspace-js-text-index/package.json`

Do not copy large fixture trees to make task variants. Prefer a new task JSONL
entry pointing at an existing `workspace_template`, or add a clearly named
workspace template when the files actually diverge. If a generated adapter output
must be checked in for compatibility testing, put it in a dedicated fixture
directory with a README that names the generator command, source task, and reason
the generated files are versioned.

## DeepSeek V4 Flash Configuration

Use environment variables instead of hard-coding secrets:

```bash
export DEEPSEEK_API_KEY='...'
export FAST_AGENT_MODEL=deepseek-v4-flash
export DEEPSEEK_THINKING=enabled
export DEEPSEEK_REASONING_EFFORT=high
```

Example smoke run:

```bash
./fast-agent-harness bench run \
  -tasks benchmarks/local-code-smoke/tasks.jsonl \
  -out bench-runs/deepseek-v4-flash-high \
  -model deepseek-v4-flash \
  -max-rounds 100 \
  -timeout-sec 900 \
  -dangerous
```

Fast deterministic 75-round agent loop and compaction stress, no API spend:

```bash
./bin/fast-agent-harness bench run \
  -mock \
  -tasks benchmarks/agent-loop-stress/tasks.jsonl \
  -out bench-runs/agent-loop-stress \
  -max-rounds 100 \
  -timeout-sec 120
```

This uses a scripted mock provider that emits real tool calls through the normal agent loop, writes normal events/results JSONL, and forces context compaction with a low task-local threshold.

## Terminal-Bench Adapter

Export Billyharness tasks to a Terminal-Bench-compatible local dataset:

```bash
./bin/fast-agent-harness bench terminal-bench export \
  -tasks benchmarks/local-code-smoke/tasks.jsonl \
  -out benchmarks/terminal-bench-export \
  -force
```

The export creates one task directory per JSONL task:

- `task.yaml` with Terminal-Bench metadata and the Billyharness prompt as `instruction`
- `workspace/` copied from `workspace_template` or `workspace`
- `run-tests.sh` plus `tests/billyharness_evaluator.py`, which runs the original Billyharness `evaluator` argv and emits pytest-like summary lines for Terminal-Bench's default parser
- `Dockerfile` and `docker-compose.yaml` using the standard Terminal-Bench client-container shape
- `.billyharness-task.json` metadata for loss-aware round-trip import

Run the exported dataset with an installed `tb` CLI:

```bash
tb run \
  --dataset-path benchmarks/terminal-bench-export \
  --agent nop \
  --output-path tb-runs/billyharness \
  --no-upload-results \
  --no-livestream
```

Import a Terminal-Bench dataset back to Billyharness JSONL:

```bash
./bin/fast-agent-harness bench terminal-bench import \
  -dataset benchmarks/terminal-bench-export \
  -out /tmp/billyharness-from-tb.jsonl
```

Generic imports map `task.yaml` `instruction` to `prompt`, task directory name to `id`, `max_agent_timeout_sec` to `timeout_seconds`, and `run-tests.sh` to an evaluator command with `TEST_DIR` and `APP_DIR` set. Exports from this adapter round-trip the original `evaluator` argv and use the exported `workspace/` as `workspace_template`.

This first slice intentionally does not vendor or install Terminal-Bench in Go tests. It validates the filesystem format and evaluator bridge locally; full Docker execution remains an external smoke test.

## Local Checks Performed

Terminal-Bench infrastructure smoke:

```bash
uvx --from terminal-bench tb --help
uvx --from terminal-bench tb datasets list
apt-get install -y docker-compose-v2
uvx --from terminal-bench tb run \
  --dataset terminal-bench-core==0.1.1 \
  --agent nop \
  --n-tasks 1 \
  --n-concurrent 1 \
  --global-agent-timeout-sec 60 \
  --global-test-timeout-sec 60 \
  --output-path /tmp/tb-nop-smoke \
  --no-upload-results \
  --no-livestream
```

The smoke reached the official evaluator and wrote:

```text
/tmp/tb-nop-smoke/2026-06-26__12-14-08/results.json
```

It scored 0/1 because the agent was `nop`; this was an infrastructure check, not a model benchmark.

DeepSeek V4 Flash high local smoke results:

```text
bench-runs/deepseek-v4-flash-high-fixed/20260626T102755Z-results.jsonl
  js-utils-001: pass, 15.6s, 7 model calls, 7 tool calls

bench-runs/deepseek-v4-flash-high-suite3/20260626T102923Z-results.jsonl
  3/3 pass, 57.7s, 17 model calls, 18 tool calls, 40,140 input tokens, 5,828 output tokens

bench-runs/deepseek-v4-flash-high-suite3-r2/20260626T103050Z-results.jsonl
  3/3 pass, 67.7s, 23 model calls, 25 tool calls, 70,744 input tokens, 6,412 output tokens
```

Terminal-Bench built-in `terminus` with `deepseek/deepseek-v4-flash` was also tried on `hello-world`; it failed before acting because LiteLLM returned repeated DeepSeek `BadRequestError`s:

```text
/tmp/tb-deepseek-hello-terminus/2026-06-26__12-34-04/results.json
```

That failure is in the external Terminal-Bench/LiteLLM agent path, not in `fast-agent-harness-go`.
