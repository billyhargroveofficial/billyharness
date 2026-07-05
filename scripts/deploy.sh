#!/usr/bin/env bash
# Build and atomically repoint the production binary symlink.
# Do not run this lane on production until doctor/readiness from 004 P2.6 is
# confirmed stable on the host.

set -euo pipefail

script_dir="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH='' cd -- "$script_dir/.." && pwd)"
cd "$repo_root"

# shellcheck source=scripts/lib/deploy-verify.sh
source "$repo_root/scripts/lib/deploy-verify.sh"

go_bin="${GO_BIN:-go}"
gateway_url="${BILLYHARNESS_DEPLOY_GATEWAY_URL:-http://127.0.0.1:8765}"
keep_releases="${BILLYHARNESS_DEPLOY_KEEP_RELEASES:-5}"
current_link="bin/fast-agent-harness-current"
previous_file="bin/.previous-release"
history_file="bin/.release-history"
release_sha="$(git rev-parse --short HEAD)"
release_bin="bin/fast-agent-harness-$release_sha"
previous_target=""

run_step() {
	local name="$1"
	shift
	local start end elapsed
	echo
	echo "==> $name"
	start="$(date +%s)"
	if "$@"; then
		end="$(date +%s)"
		elapsed=$((end - start))
		echo "<== $name ok in ${elapsed}s"
		return 0
	fi
	end="$(date +%s)"
	elapsed=$((end - start))
	echo "!! $name failed after ${elapsed}s" >&2
	return 1
}

restore_previous_release() {
	if [[ -n "$previous_target" ]]; then
		ln -sfn "$previous_target" "$current_link"
		echo "restored $current_link -> $previous_target" >&2
	else
		rm -f "$current_link"
		echo "removed $current_link; no previous release was recorded" >&2
	fi
}

append_release_history() {
	local tmp
	tmp="$(mktemp)"
	{
		if [[ -f "$history_file" ]]; then
			grep -E '^[0-9a-f]+$' "$history_file" || true
		fi
		printf '%s\n' "$release_sha"
	} | tail -n "$keep_releases" >"$tmp"
	mv "$tmp" "$history_file"
}

release_is_kept() {
	local sha="$1"
	grep -Fxq "$sha" "$history_file"
}

prune_old_releases() {
	local path sha
	[[ -f "$history_file" ]] || return 0
	for path in bin/fast-agent-harness-*; do
		[[ -e "$path" ]] || continue
		[[ "$path" == "$current_link" ]] && continue
		sha="${path#bin/fast-agent-harness-}"
		if ! release_is_kept "$sha"; then
			rm -f "$path"
		fi
	done
}

mkdir -p bin
if [[ -L "$current_link" ]]; then
	previous_target="$(readlink "$current_link")"
	printf '%s\n' "$previous_target" >"$previous_file"
elif [[ -e "$current_link" ]]; then
	echo "error: $current_link exists but is not a symlink" >&2
	exit 1
else
	: >"$previous_file"
fi

run_step "build $release_bin" "$go_bin" build -buildvcs=false -o "$release_bin" ./cmd/fast-agent-harness
ln -sfn "$(basename "$release_bin")" "$current_link"

if ! run_step "verify release $release_sha" deploy_verify_restart_and_check "./$current_link" "$gateway_url"; then
	restore_previous_release
	if [[ -n "$previous_target" ]]; then
		run_step "verify restored release" deploy_verify_restart_and_check "./$current_link" "$gateway_url" || true
	fi
	exit 1
fi

append_release_history
prune_old_releases
echo
echo "deploy completed: $release_sha"
echo "$current_link -> $(readlink "$current_link")"
