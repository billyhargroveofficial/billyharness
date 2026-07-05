#!/usr/bin/env bash
# Roll back the production binary symlink to bin/.previous-release.
# Do not run this lane on production until doctor/readiness from 004 P2.6 is
# confirmed stable on the host.

set -euo pipefail

script_dir="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH='' cd -- "$script_dir/.." && pwd)"
cd "$repo_root"

# shellcheck source=scripts/lib/deploy-verify.sh
source "$repo_root/scripts/lib/deploy-verify.sh"

gateway_url="${BILLYHARNESS_DEPLOY_GATEWAY_URL:-http://127.0.0.1:8765}"
current_link="bin/fast-agent-harness-current"
previous_file="bin/.previous-release"

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

if [[ ! -s "$previous_file" ]]; then
	echo "error: $previous_file is missing or empty; no previous release recorded" >&2
	exit 1
fi

previous_target="$(<"$previous_file")"
current_target=""
if [[ -L "$current_link" ]]; then
	current_target="$(readlink "$current_link")"
elif [[ -e "$current_link" ]]; then
	echo "error: $current_link exists but is not a symlink" >&2
	exit 1
fi

ln -sfn "$previous_target" "$current_link"
if ! run_step "verify rollback to $previous_target" deploy_verify_restart_and_check "./$current_link" "$gateway_url"; then
	if [[ -n "$current_target" ]]; then
		ln -sfn "$current_target" "$current_link"
		echo "restored $current_link -> $current_target" >&2
	fi
	exit 1
fi

if [[ -n "$current_target" ]]; then
	printf '%s\n' "$current_target" >"$previous_file"
fi
echo
echo "rollback completed: $current_link -> $(readlink "$current_link")"
