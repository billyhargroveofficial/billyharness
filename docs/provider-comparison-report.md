# Provider Comparison Report

Date: 2026-06-28

This report records the first live provider-comparison smoke after adding `bench compare-providers`. It is intentionally small to avoid burning quota. Treat it as provider-path and reporting evidence, not a final coding-quality benchmark.

## Live DeepSeek Flash vs Pro Smoke

Command shape:

```sh
./bin/fast-agent-harness bench compare-providers \
  -tasks <generated-local-loop-tasks.jsonl> \
  -out <tmp-live-compare> \
  -models deepseek-v4-flash,deepseek-v4-pro \
  -live \
  -limit 1 \
  -timeout-sec 120 \
  -max-rounds 8
```

Result:

| Model | Outcome | Tool correctness | Elapsed | Input | Output | Cache | Estimated cost | Replay |
| --- | --- | --- | ---: | ---: | ---: | --- | ---: | --- |
| `deepseek-v4-flash` | pass | not exercised | 7.9s | 3.2k | 254 | hit 3.2k / miss 18 | ~$0.00008 | verified |
| `deepseek-v4-pro` | pass | not exercised | 10.3s | 3.2k | 480 | miss 3.2k | ~$0.00182 | verified |

Interpretation: for this simple one-task live smoke, Flash was faster and materially cheaper. Pro did not show enough benefit on this task to justify using it as the default normal-chat model.

## Codex OAuth Smoke

Command shape:

```sh
./bin/fast-agent-harness bench compare-providers \
  -tasks <generated-local-loop-tasks.jsonl> \
  -out <tmp-live-codex> \
  -models deepseek-v4-flash \
  -codex \
  -live \
  -limit 1 \
  -timeout-sec 90 \
  -max-rounds 2
```

Result:

| Model | Outcome | Tool correctness | Elapsed | Input | Output | Cost marker | Replay | Failure mode |
| --- | --- | --- | ---: | ---: | ---: | --- | --- | --- |
| `deepseek-v4-flash` | pass | not exercised | 5.8s | 3.2k | 84 | metered | verified | none |
| `gpt-5.5` via Codex OAuth | fail | unverified | 9.8s | 4.4k | 90 | subscription | verified | exceeded max tool rounds: 2 |

Interpretation: Codex OAuth is reachable and emits replay-verified bundles. The failure is not an auth failure; the tiny `max-rounds=2` smoke was too strict for the tool loop. A real coding comparison should run with a larger tool-round budget and a task set designed to require file edits.

## Trace Bug Found And Fixed

The first Codex smoke exposed a real trace bug before Codex ran: live DeepSeek produced parallel tool calls, and `trace.EventWriter.Record` assigned sequence numbers without a mutex. That caused duplicate/gapped JSONL event sequence numbers under concurrent tool events.

Fix: `EventWriter.Record` now serializes sequence assignment, payload-ref writing, and JSON encoding. `TestEventWriterConcurrentRecordsStayContiguous` covers this regression.

## Current Recommendation

Use `deepseek-v4-flash` as the default normal-chat and cheap coding-smoke model. Keep `deepseek-v4-pro` for deliberate higher-quality experiments until a larger live coding benchmark proves it earns the extra latency/cost. Use Codex OAuth for subscription-backed experiments, but evaluate it with a larger `max-rounds` budget before treating it as the coding default.
