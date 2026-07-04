#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

usage() {
	cat <<'USAGE'
Usage:
  scripts/bench-compare.sh record [output-file]
  scripts/bench-compare.sh current [output-file]
  scripts/bench-compare.sh compare [baseline-file] [current-file]

Records or compares local benchmark baselines. Baselines default to
bench-runs/bench-baselines/<host-key>/latest.txt, which is ignored by git.

Environment:
  GO_BIN                         Go binary, default go
  BENCHTIME                      Go benchmark benchtime, default 5x
  BENCH_REGEX                    Benchmark regexp
  BENCH_PACKAGES                 Space-separated package list
  BENCH_BASELINE_ROOT            Baseline root, default bench-runs/bench-baselines
  BENCH_HOST_KEY                 Override host key
  BENCH_MAX_NS_REGRESSION_PCT    Default 75
  BENCH_MAX_BYTES_REGRESSION_PCT Default 75
  BENCH_MAX_ALLOCS_REGRESSION_PCT Default 50
  BENCH_MIN_NS_DELTA             Default 1000000
  BENCH_MIN_BYTES_DELTA          Default 65536
  BENCH_MIN_ALLOCS_DELTA         Default 1000
USAGE
}

mode="${1:-compare}"
case "$mode" in
	record|current|compare)
		shift || true
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		echo "unknown mode: $mode" >&2
		usage >&2
		exit 2
		;;
esac

go_bin="${GO_BIN:-go}"
benchtime="${BENCHTIME:-5x}"
bench_regex="${BENCH_REGEX:-Benchmark(SessionJSONL|GatewaySessionJSONL|ReplayAfterSeq|EventJSONL|Projector|ToolSchemaValidation)}"
bench_packages_value="${BENCH_PACKAGES:-./internal/gateway ./internal/eventlog ./internal/clientux/projector ./internal/tools}"
read -r -a bench_packages <<<"$bench_packages_value"
baseline_root="${BENCH_BASELINE_ROOT:-bench-runs/bench-baselines}"
host_key="${BENCH_HOST_KEY:-}"
if [[ -z "$host_key" ]]; then
	host_raw="$("$go_bin" env GOHOSTOS GOHOSTARCH GOVERSION 2>/dev/null | tr '\n' '-')$(hostname -s 2>/dev/null || hostname 2>/dev/null || echo unknown)"
	host_key="$(printf '%s' "$host_raw" | tr -c '[:alnum:]_.-' '_' | sed 's/_*$//')"
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
baseline_dir="$baseline_root/$host_key"
latest_baseline="$baseline_dir/latest.txt"

run_benchmarks() {
	local output_file="$1"
	mkdir -p "$(dirname "$output_file")"
	"$go_bin" test -run '^$' -bench "$bench_regex" -benchmem -benchtime "$benchtime" "${bench_packages[@]}" | tee "$output_file"
}

run_benchstat() {
	if command -v benchstat >/dev/null 2>&1; then
		benchstat "$@"
	else
		"$go_bin" run golang.org/x/perf/cmd/benchstat@latest "$@"
	fi
}

compare_thresholds() {
	local baseline_file="$1"
	local current_file="$2"
	awk \
		-v ns_pct="${BENCH_MAX_NS_REGRESSION_PCT:-75}" \
		-v bytes_pct="${BENCH_MAX_BYTES_REGRESSION_PCT:-75}" \
		-v allocs_pct="${BENCH_MAX_ALLOCS_REGRESSION_PCT:-50}" \
		-v ns_delta="${BENCH_MIN_NS_DELTA:-1000000}" \
		-v bytes_delta="${BENCH_MIN_BYTES_DELTA:-65536}" \
		-v allocs_delta="${BENCH_MIN_ALLOCS_DELTA:-1000}" '
function metric_name(name) {
	sub(/-[0-9]+$/, "", name)
	return name
}
function collect(which,    name, i, key, value) {
	name = metric_name($1)
	for (i = 2; i < NF; i++) {
		value = $i + 0
		if ($(i + 1) == "ns/op") {
			key = name SUBSEP "ns_per_op"
		} else if ($(i + 1) == "B/op") {
			key = name SUBSEP "bytes_per_op"
		} else if ($(i + 1) == "allocs/op") {
			key = name SUBSEP "allocs_per_op"
		} else {
			continue
		}
		if (which == 1) {
			baseline[key] = value
		} else {
			current[key] = value
		}
	}
}
function limit_for(metric) {
	if (metric == "ns_per_op") {
		return ns_pct
	}
	if (metric == "bytes_per_op") {
		return bytes_pct
	}
	return allocs_pct
}
function min_delta_for(metric) {
	if (metric == "ns_per_op") {
		return ns_delta
	}
	if (metric == "bytes_per_op") {
		return bytes_delta
	}
	return allocs_delta
}
FNR == 1 {
	file_index++
}
file_index == 1 && /^Benchmark/ {
	collect(1)
}
file_index == 2 && /^Benchmark/ {
	collect(2)
}
END {
	for (key in current) {
		if (!(key in baseline) || baseline[key] <= 0) {
			continue
		}
		split(key, parts, SUBSEP)
		name = parts[1]
		metric = parts[2]
		limit = limit_for(metric)
		min_delta = min_delta_for(metric)
		allowed = baseline[key] * (1 + limit / 100)
		checked++
		if (current[key] > allowed && current[key] - baseline[key] >= min_delta) {
			printf("REGRESSION %s %s baseline=%g current=%g limit=+%g%% min_delta=%g\n", name, metric, baseline[key], current[key], limit, min_delta) > "/dev/stderr"
			failed = 1
		}
	}
	if (checked == 0) {
		print "warning: no overlapping benchmark metrics were compared" > "/dev/stderr"
	}
	exit failed
}
' "$baseline_file" "$current_file"
}

case "$mode" in
	record)
		output_file="${1:-$baseline_dir/$timestamp.txt}"
		run_benchmarks "$output_file"
		mkdir -p "$baseline_dir"
		if [[ "$output_file" != "$latest_baseline" ]]; then
			cp "$output_file" "$latest_baseline"
		fi
		printf 'recorded benchmark baseline: %s\n' "$output_file"
		printf 'updated latest baseline: %s\n' "$latest_baseline"
		;;
	current)
		output_file="${1:-$baseline_dir/current-$timestamp.txt}"
		run_benchmarks "$output_file"
		printf 'recorded current benchmark run: %s\n' "$output_file"
		;;
	compare)
		baseline_file="${1:-$latest_baseline}"
		current_file="${2:-$baseline_dir/current-$timestamp.txt}"
		if [[ ! -f "$baseline_file" ]]; then
			echo "missing benchmark baseline: $baseline_file" >&2
			echo "create one with: scripts/bench-compare.sh record" >&2
			exit 1
		fi
		if [[ ! -f "$current_file" ]]; then
			run_benchmarks "$current_file"
		fi
		run_benchstat "$baseline_file" "$current_file"
		compare_thresholds "$baseline_file" "$current_file"
		printf 'benchmark comparison passed: baseline=%s current=%s\n' "$baseline_file" "$current_file"
		;;
esac
