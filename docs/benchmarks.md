# Benchmarks

Billyharness benchmark runs write replayable trace bundles: a manifest, result rows, event JSONL, and sanitized payload refs. The default local loop uses the mock provider, so it does not spend API tokens and does not require network access.

## Local Long-Loop

Run a 50-100 turn local agent loop:

```sh
cd /root/billyharness
./bin/fast-agent-harness bench local-loop -out /root/billyharness/bench-runs/local-loop -turns 60
```

The command generates `/root/billyharness/bench-runs/local-loop/local-loop-tasks.jsonl`, creates a small workspace template, runs the tasks with the mock scripted-loop provider, and prints JSON with the generated task summary and run summary.

Use `-turns 50` for the shortest smoke and `-turns 100` for a longer local stress run. The value is clamped to `50..100` and represents expected agent turns across the generated task suite.

To generate tasks without running them:

```sh
./bin/fast-agent-harness bench local-loop -out /root/billyharness/bench-runs/local-loop -turns 60 -run=false
```

The generated suite covers local app/file edits, file search, web/tool discovery output-cap behavior, MCP/tool discovery, and a harmless long-loop cancellation/resume telemetry placeholder.

To exercise the native live `web_search` tool instead of the offline tool-discovery web task, add `-live-web`:

```sh
./bin/fast-agent-harness bench local-loop -out /root/billyharness/bench-runs/local-loop-web -turns 60 -live-web
```

## Generic Runs

Run any billyharness JSONL task file:

```sh
./bin/fast-agent-harness bench run -tasks /path/to/tasks.jsonl -out /root/billyharness/bench-runs/custom -mock
```

Useful flags:

- `-dangerous` enables write and shell tools for benchmark tasks.
- `-max-rounds 100` controls the per-task model/tool round limit.
- `-scripted-rounds N` forces the mock provider to perform N tool rounds when a task does not define its own scripted loop.
- `-context-compact-tokens`, `-context-compact-keep`, and `-context-compact-max-chars` override compaction settings.

## Terminal-Bench Adapter

Export billyharness tasks as a Terminal-Bench-shaped dataset:

```sh
./bin/fast-agent-harness bench terminal-bench export -tasks /path/to/tasks.jsonl -out /root/billyharness/benchmarks/terminal-bench-export -force
```

Import a Terminal-Bench dataset into billyharness JSONL:

```sh
./bin/fast-agent-harness bench terminal-bench import -dataset /path/to/tb-dataset -out /root/billyharness/benchmarks/imported-tasks.jsonl
```

## Replay Verification

Every successful benchmark run attempts to replay-check its own event bundle. The JSON output includes:

- `replay_verified`: true when event aggregates match result rows.
- `manifest_json`: bundle manifest with config, provider/model metadata, profile hash, and MCP snapshot.
- `results_jsonl`: per-task outcomes and counters.
- `events_jsonl`: replayable event stream.
- `payloads_dir`: sanitized payload refs for large or sensitive events.

If `replay_verified` is false or missing, treat the bundle as incomplete and inspect the error from the failed command before comparing performance numbers.

## Provider Comparison

The provider comparison flow is intentionally not automated yet. Use the same task file with explicit provider/model config, then compare elapsed time, tool correctness, token/context growth, cost or subscription marker, and failure modes. Keep DeepSeek Flash, DeepSeek Pro, and Codex OAuth runs in separate output directories.
