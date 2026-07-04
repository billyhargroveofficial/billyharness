#!/usr/bin/env bash
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
default_repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd)"

mode="${1:-}"
if [[ $# -gt 0 ]]; then
	shift
fi

default_go_bin="go"
if [[ -x /root/.local/go/bin/go ]]; then
	default_go_bin="/root/.local/go/bin/go"
fi

repo_dir="${BILLYHARNESS_DEPLOY_REPO:-$default_repo_root}"
target_ref="${BILLYHARNESS_DEPLOY_REF:-origin/main}"
rollback_to=""
go_bin="${GO_BIN:-$default_go_bin}"
log_dir="${BILLYHARNESS_DEPLOY_LOG_DIR:-/var/log/billyharness/deploy}"
gateway_url="${BILLYHARNESS_DEPLOY_GATEWAY_URL:-http://127.0.0.1:8765}"
confirm=0
skip_fetch=0
skip_tests=0

usage() {
	cat <<'USAGE'
Usage:
  scripts/production-deploy.sh deploy --yes [--repo DIR] [--ref REF] [--go-bin PATH]
  scripts/production-deploy.sh rollback --yes --to COMMIT [--repo DIR] [--go-bin PATH]

Deploy model:
  source checkout rebuild in-place, then restart the managed systemd services.

The script captures pre/post facts in ${BILLYHARNESS_DEPLOY_LOG_DIR:-/var/log/billyharness/deploy},
builds ./bin/fast-agent-harness with commit/build-time ldflags, restarts
billyharness-gateway.service and billyharness-telegram.service, and gates on:
  ./bin/fast-agent-harness doctor -mode=production -strict
  curl -fsS http://127.0.0.1:8765/health
  curl -fsS http://127.0.0.1:8765/ready

Options:
  --repo DIR          Production checkout, default current repo.
  --ref REF           Deploy ref, default origin/main.
  --to COMMIT         Rollback target commit.
  --go-bin PATH       Go binary, default /root/.local/go/bin/go when present, else ${GO_BIN:-go}.
  --log-dir DIR       Deploy evidence directory.
  --gateway-url URL   Gateway base URL, default http://127.0.0.1:8765.
  --skip-fetch        Do not fetch origin before resolving refs.
  --skip-tests        Skip go test -count=1 ./... before rebuild.
  --yes              Required for deploy or rollback.
USAGE
}

die() {
	echo "error: $*" >&2
	exit 1
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--repo)
			[[ $# -ge 2 ]] || die "--repo requires a value"
			repo_dir="$2"
			shift 2
			;;
		--ref)
			[[ $# -ge 2 ]] || die "--ref requires a value"
			target_ref="$2"
			shift 2
			;;
		--to)
			[[ $# -ge 2 ]] || die "--to requires a value"
			rollback_to="$2"
			shift 2
			;;
		--go-bin)
			[[ $# -ge 2 ]] || die "--go-bin requires a value"
			go_bin="$2"
			shift 2
			;;
		--log-dir)
			[[ $# -ge 2 ]] || die "--log-dir requires a value"
			log_dir="$2"
			shift 2
			;;
		--gateway-url)
			[[ $# -ge 2 ]] || die "--gateway-url requires a value"
			gateway_url="${2%/}"
			shift 2
			;;
		--skip-fetch)
			skip_fetch=1
			shift
			;;
		--skip-tests)
			skip_tests=1
			shift
			;;
		--yes)
			confirm=1
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			die "unknown argument: $1"
			;;
	esac
done

case "$mode" in
	deploy|rollback)
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		usage >&2
		exit 2
		;;
esac

[[ "$confirm" -eq 1 ]] || die "refusing to change production without --yes"

cd "$repo_dir"
git rev-parse --is-inside-work-tree >/dev/null
repo_dir="$(pwd)"

run_id="$(date -u +%Y%m%dT%H%M%SZ)-$mode"
run_dir="$log_dir/$run_id"
mkdir -p "$run_dir"

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

short_commit() {
	git rev-parse --short "$1"
}

ensure_clean_tracked_worktree() {
	if ! git diff-index --quiet HEAD --; then
		die "tracked worktree changes are present; commit, stash, or revert them before production deploy"
	fi
}

capture_facts() {
	local phase="$1"
	local facts_file="$run_dir/$phase-facts.txt"
	local doctor_json="$run_dir/$phase-doctor.json"
	local doctor_stderr="$run_dir/$phase-doctor.stderr"
	{
		echo "phase=$phase"
		echo "generated_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
		echo "repo_dir=$repo_dir"
		echo "commit=$(git rev-parse HEAD 2>/dev/null || true)"
		echo "branch=$(git symbolic-ref --short -q HEAD 2>/dev/null || echo detached)"
		echo "status:"
		git status --short --branch --untracked-files=no || true
		if [[ -f ./bin/fast-agent-harness ]]; then
			echo "binary=$(ls -l ./bin/fast-agent-harness)"
			echo "binary_sha256=$(sha256_file ./bin/fast-agent-harness)"
		else
			echo "binary=missing"
		fi
		for service in billyharness-gateway.service billyharness-telegram.service; do
			printf 'service_%s=' "$service"
			systemctl is-active "$service" 2>/dev/null || true
		done
	} >"$facts_file"
	if [[ -x ./bin/fast-agent-harness ]]; then
		if ./bin/fast-agent-harness doctor -mode=production -json >"$doctor_json" 2>"$doctor_stderr"; then
			:
		else
			echo "doctor_json_failed=$?" >>"$facts_file"
		fi
	fi
}

fetch_origin() {
	if [[ "$skip_fetch" -eq 0 ]]; then
		git fetch --prune origin
	fi
}

run_tests() {
	if [[ "$skip_tests" -eq 1 ]]; then
		echo "skipping tests (--skip-tests)"
		return
	fi
	"$go_bin" test -count=1 ./...
}

build_binary() {
	local target_commit="$1"
	local target_short build_time build_version tmp_bin ldflags
	target_short="$(short_commit "$target_commit")"
	build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	build_version="${BILLYHARNESS_VERSION:-0.1.0+$target_short}"
	tmp_bin="./bin/fast-agent-harness.$target_short.$run_id"
	ldflags="-X main.version=$build_version -X main.buildCommit=$target_commit -X main.buildTime=$build_time"
	mkdir -p ./bin
	"$go_bin" build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$tmp_bin" ./cmd/fast-agent-harness
	chmod 0755 "$tmp_bin"
	mv -f "$tmp_bin" ./bin/fast-agent-harness
	{
		echo "version=$build_version"
		echo "commit=$target_commit"
		echo "built_at=$build_time"
		echo "binary_sha256=$(sha256_file ./bin/fast-agent-harness)"
		echo "ldflags=$ldflags"
	} >"$run_dir/build-provenance.txt"
}

restart_services() {
	systemctl restart billyharness-gateway.service
	systemctl restart billyharness-telegram.service
}

readiness_gate() {
	./bin/fast-agent-harness doctor -mode=production -strict
	curl -fsS "$gateway_url/health" >/dev/null
	curl -fsS "$gateway_url/ready" >/dev/null
}

write_manifest() {
	local action="$1"
	local previous_commit="$2"
	local target_commit="$3"
	local rollback_hint="$repo_dir/scripts/production-deploy.sh rollback --repo $repo_dir --to $previous_commit --yes"
	{
		echo "run_id=$run_id"
		echo "action=$action"
		echo "repo_dir=$repo_dir"
		echo "previous_commit=$previous_commit"
		echo "target_commit=$target_commit"
		echo "gateway_url=$gateway_url"
		echo "skip_tests=$skip_tests"
		echo "skip_fetch=$skip_fetch"
		echo "rollback_command=$rollback_hint"
		echo "evidence_dir=$run_dir"
	} >"$run_dir/manifest.txt"
}

deploy() {
	local previous_commit target_commit
	previous_commit="$(git rev-parse HEAD)"
	capture_facts predeploy
	ensure_clean_tracked_worktree
	fetch_origin
	target_commit="$(git rev-parse "$target_ref^{commit}")"
	git checkout --detach "$target_commit"
	run_tests
	build_binary "$target_commit"
	restart_services
	readiness_gate
	capture_facts postdeploy
	write_manifest deploy "$previous_commit" "$target_commit"
	echo "deploy completed: $target_commit"
	echo "evidence: $run_dir"
	echo "rollback: $repo_dir/scripts/production-deploy.sh rollback --repo $repo_dir --to $previous_commit --yes"
}

rollback() {
	local previous_commit target_commit
	[[ -n "$rollback_to" ]] || die "rollback requires --to COMMIT"
	previous_commit="$(git rev-parse HEAD)"
	capture_facts prerollback
	ensure_clean_tracked_worktree
	fetch_origin
	target_commit="$(git rev-parse "$rollback_to^{commit}")"
	git checkout --detach "$target_commit"
	run_tests
	build_binary "$target_commit"
	restart_services
	readiness_gate
	capture_facts postrollback
	write_manifest rollback "$previous_commit" "$target_commit"
	echo "rollback completed: $target_commit"
	echo "evidence: $run_dir"
}

case "$mode" in
	deploy)
		deploy
		;;
	rollback)
		rollback
		;;
esac
