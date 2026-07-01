#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

go_bin="${GO_BIN:-/root/.local/go/bin/go}"
benchtime="${BENCHTIME:-1x}"
bench_regex="${BENCH_REGEX:-Benchmark(SessionJSONL|EventJSONL|ReplayAfterSeq)}"

if ! output="$("$go_bin" test -run '^$' -bench "$bench_regex" -benchmem -benchtime "$benchtime" ./internal/gateway ./internal/eventlog 2>&1)"; then
	printf '%s\n' "$output"
	exit 1
fi

printf '%s\n' "$output"
printf '%s\n' "$output" | awk '
function metric_name(name) {
	sub(/-[0-9]+$/, "", name)
	return name
}
/^Benchmark/ {
	name = metric_name($1)
	for (i = 2; i < NF; i++) {
		if ($(i + 1) == "ns/op") {
			print "METRIC", name, $i, "ns_per_op"
		} else if ($(i + 1) == "B/op") {
			print "METRIC", name, $i, "bytes_per_op"
		} else if ($(i + 1) == "allocs/op") {
			print "METRIC", name, $i, "allocs_per_op"
		} else if ($(i + 1) == "initial_events") {
			print "METRIC", name, $i, "initial_events"
		} else if ($(i + 1) == "log_events") {
			print "METRIC", name, $i, "log_events"
		} else if ($(i + 1) == "tail_events") {
			print "METRIC", name, $i, "tail_events"
		}
	}
}
'
