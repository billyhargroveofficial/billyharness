#!/usr/bin/env bash
set -uo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd)"
cd "$repo_root"

go_bin="${GO_BIN:-go}"
full_race=0
skip_bench=0

usage() {
	cat <<'USAGE'
Usage: scripts/verify-local.sh [--full-race] [--skip-bench]

Runs the local Billyharness verification gate:
  git diff --check
  scripts/verify-deps.sh (read-only go mod tidy -diff + direct dependency check)
  go vet ./...
  go test -count=1 ./...
  focused race packages
  optional full race with --full-race
  govulncheck
  go build ./cmd/fast-agent-harness
  strict hygiene
  non-mutating benchmark smoke
USAGE
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--full-race)
			full_race=1
			;;
		--skip-bench)
			skip_bench=1
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown argument: $1" >&2
			usage >&2
			exit 2
			;;
	esac
	shift
done

declare -a summary=()
failures=0

run_step() {
	local name="$1"
	shift
	local start end elapsed status
	echo
	echo "==> $name"
	start="$(date +%s)"
	if "$@"; then
		status="ok"
	else
		status="fail"
		failures=$((failures + 1))
	fi
	end="$(date +%s)"
	elapsed=$((end - start))
	summary+=("$status ${elapsed}s $name")
	if [[ "$status" == "fail" ]]; then
		echo "!! $name failed after ${elapsed}s"
	else
		echo "<== $name ok in ${elapsed}s"
	fi
}

run_step "git diff --check" git diff --check
run_step "verify dependency metadata" env GO_BIN="$go_bin" scripts/verify-deps.sh
run_step "go vet" "$go_bin" vet ./...
run_step "go test" "$go_bin" test -count=1 ./...
run_step "focused race tests" "$go_bin" test -race -count=1 ./internal/eventlog ./internal/gateway ./internal/telegrambot ./internal/tools ./internal/tui ./internal/clientux/projector
if [[ "$full_race" -eq 1 ]]; then
	run_step "full race tests" "$go_bin" test -race -count=1 ./...
else
	summary+=("skip 0s full race tests (pass --full-race)")
fi
run_step "govulncheck" "$go_bin" run golang.org/x/vuln/cmd/govulncheck@latest ./...
run_step "binary rebuild" "$go_bin" build ./cmd/fast-agent-harness
run_step "strict hygiene" "$go_bin" run ./cmd/fast-agent-harness hygiene -repo "$repo_root" -strict
if [[ "$skip_bench" -eq 1 ]]; then
	summary+=("skip 0s bench smoke (--skip-bench)")
else
	run_step "bench smoke" env GO_BIN="$go_bin" scripts/bench-smoke.sh
fi

echo
echo "Verification summary:"
for line in "${summary[@]}"; do
	echo "  $line"
done

if [[ "$failures" -gt 0 ]]; then
	echo
	echo "verification failed: $failures step(s) failed" >&2
	exit 1
fi

echo
echo "verification passed"
