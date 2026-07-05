#!/usr/bin/env bash
# Shared production deploy gate for the SHA-symlink deploy lane.
# Do not run this lane on production until doctor/readiness from 004 P2.6 is
# confirmed stable on the host.

deploy_verify_restart_and_check() {
	local binary="${1:-./bin/fast-agent-harness-current}"
	local gateway_url="${2:-${BILLYHARNESS_DEPLOY_GATEWAY_URL:-http://127.0.0.1:8765}}"
	gateway_url="${gateway_url%/}"

	systemctl restart billyharness-gateway.service billyharness-telegram.service
	"$binary" doctor -mode=production
	curl -sf "$gateway_url/health" >/dev/null
	curl -sf "$gateway_url/ready" >/dev/null
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	set -euo pipefail
	deploy_verify_restart_and_check "$@"
fi
