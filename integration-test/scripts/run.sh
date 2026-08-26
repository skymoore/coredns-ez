#!/usr/bin/env bash
# Host-side driver. Builds CoreDNS with both plugins, starts primary +
# secondary, and runs the in-container test stages (including restarts that
# prove persist).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE=(docker compose --project-directory "$ROOT" -f "$ROOT/docker-compose.yml")

usage() {
	cat <<EOF
usage: $(basename "$0") [up|test|down|logs]

  up     build images and start primary + secondary
  test   up (if needed) then run the full integration suite (default)
  down   stop containers and delete persist volumes
  logs   follow primary + secondary logs
EOF
}

plugins_ok() {
	local out
	out="$("${COMPOSE[@]}" exec -T primary /usr/bin/coredns -plugins 2>/dev/null || true)"
	printf '%s\n' "$out"
	printf '%s\n' "$out" | grep -q 'dns-update-persistent' \
		&& printf '%s\n' "$out" | grep -q 'ixfr' \
		&& printf '%s\n' "$out" | grep -q 'secondary-persistent' \
		&& printf '%s\n' "$out" | grep -q '\badmin\b'
}

wait_healthy() {
	local svc="$1" timeout="${2:-90}"
	local start now
	start=$(date +%s)
	while true; do
		if "${COMPOSE[@]}" exec -T "$svc" curl -fsS http://127.0.0.1:8080/health >/dev/null 2>&1; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			echo "timed out waiting for $svc /health" >&2
			"${COMPOSE[@]}" logs --tail=80 "$svc" >&2 || true
			return 1
		fi
		sleep 1
	done
}

cmd_up() {
	"${COMPOSE[@]}" up -d --build primary secondary
	echo "==> waiting for primary health"
	wait_healthy primary
	echo "==> waiting for secondary health"
	wait_healthy secondary
	echo "==> compiled plugins"
	if plugins_ok; then
		echo "ok  - dns-update-persistent, ixfr, admin, and secondary-persistent are compiled in"
	else
		echo "not ok - expected dns-update-persistent, ixfr, admin, and secondary-persistent in \`coredns -plugins\`" >&2
		return 1
	fi
}

run_stage() {
	local stage="$1"
	echo
	echo "======== stage $stage ========"
	"${COMPOSE[@]}" --profile test run --rm --no-deps --build -T tester --stage "$stage"
}

cmd_test() {
	cmd_up

	run_stage bootstrap

	echo
	echo "======== restart primary (persist-from-disk) ========"
	"${COMPOSE[@]}" restart primary
	wait_healthy primary
	run_stage primary-restart

	echo
	echo "======== stop primary, restart secondary ========"
	"${COMPOSE[@]}" stop primary
	"${COMPOSE[@]}" restart secondary
	wait_healthy secondary
	run_stage secondary-alone

	echo
	echo "======== bring primary back ========"
	"${COMPOSE[@]}" start primary
	wait_healthy primary

	echo
	echo "All integration stages passed."
}

cmd_down() {
	"${COMPOSE[@]}" --profile test down -v --remove-orphans
}

cmd_logs() {
	"${COMPOSE[@]}" logs -f primary secondary
}

case "${1:-test}" in
up) cmd_up ;;
test) cmd_test ;;
down) cmd_down ;;
logs) cmd_logs ;;
-h|--help|help) usage ;;
*)
	usage >&2
	exit 2
	;;
esac
