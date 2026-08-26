#!/usr/bin/env bash
# Shared helpers for the in-container integration tests.
# shellcheck disable=SC2034

set -euo pipefail

PRIMARY="${PRIMARY:-172.30.53.10}"
SECONDARY="${SECONDARY:-172.30.53.20}"
ZONE="${ZONE:-example.com}"
PRIMARY_ZONEFILE="${PRIMARY_ZONEFILE:-/primary-data/db.example.com}"
SECONDARY_ZONEFILE="${SECONDARY_ZONEFILE:-/secondary-data/db.example.com}"
TSIG_NAME="${TSIG_NAME:-updater.example.com.}"
TSIG_SECRET="${TSIG_SECRET:-Y29yZWRucy1pbnRlZ3JhdGlvbi10ZXN0LWtleSEh}"
TSIG_ALG="${TSIG_ALG:-hmac-sha256}"

DIG_OPTS=(+time=2 +tries=2 +norecurse)

PASS=0
FAIL=0
failures=()

pass() {
	PASS=$((PASS + 1))
	printf 'ok  - %s\n' "$1"
}

fail() {
	FAIL=$((FAIL + 1))
	failures+=("$1")
	printf 'not ok - %s\n' "$1"
	if [[ -n "${2:-}" ]]; then
		printf '       %s\n' "$2"
	fi
}

dig_at() {
	local at="$1"
	shift
	dig "${DIG_OPTS[@]}" @"$at" "$@"
}

soa_serial() {
	local at="$1"
	dig_at "$at" +short "$ZONE" SOA | awk '{print $3}'
}

rr_values() {
	local at="$1" name="$2" type="$3"
	dig_at "$at" +short "$name" "$type"
}

rcode() {
	local at="$1"
	shift
	dig_at "$at" +noall +comments "$@" | awk '/status:/{gsub(/,/, "", $6); print $6; exit}'
}

wait_soa() {
	local at="$1" timeout="${2:-60}" serial
	local start now
	start=$(date +%s)
	while true; do
		serial=$(soa_serial "$at" || true)
		if [[ -n "$serial" ]]; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 1
	done
}

wait_rr() {
	local at="$1" name="$2" type="$3" expect="$4" timeout="${5:-45}"
	local start now got
	start=$(date +%s)
	while true; do
		got=$(rr_values "$at" "$name" "$type" || true)
		if printf '%s\n' "$got" | grep -Fqx "$expect"; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 1
	done
}

wait_no_rr() {
	local at="$1" name="$2" type="$3" expect="$4" timeout="${5:-45}"
	local start now got
	start=$(date +%s)
	while true; do
		got=$(rr_values "$at" "$name" "$type" || true)
		if ! printf '%s\n' "$got" | grep -Fqx "$expect"; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 1
	done
}

wait_file_grep() {
	local file="$1" pattern="$2" timeout="${3:-20}"
	local start now
	start=$(date +%s)
	while true; do
		if [[ -f "$file" ]] && grep -E -q -- "$pattern" "$file"; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 1
	done
}

wait_file_not_grep() {
	local file="$1" pattern="$2" timeout="${3:-20}"
	local start now
	start=$(date +%s)
	while true; do
		if [[ -f "$file" ]] && ! grep -E -q -- "$pattern" "$file"; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 1
	done
}

assert_serial() {
	local at="$1" name="$2"
	local serial
	serial=$(soa_serial "$at" || true)
	if [[ -n "$serial" && "$serial" =~ ^[0-9]+$ ]]; then
		pass "$name (serial $serial)"
		printf '%s\n' "$serial"
		return 0
	fi
	fail "$name" "got serial='${serial}'"
	printf '\n'
	return 1
}

assert_rr() {
	local at="$1" name="$2" type="$3" expect="$4" label="$5"
	local got
	got=$(rr_values "$at" "$name" "$type" || true)
	if printf '%s\n' "$got" | grep -Fqx "$expect"; then
		pass "$label"
		return 0
	fi
	fail "$label" "expected '$expect', got: ${got:-<empty>}"
	return 1
}

# Substring match on dig +short (presentation of LOC/HTTPS/SVCB/NAPTR is not stable).
assert_rr_grep() {
	local at="$1" name="$2" type="$3" pattern="$4" label="$5"
	local got
	got=$(rr_values "$at" "$name" "$type" || true)
	if printf '%s\n' "$got" | grep -Ei -q -- "$pattern"; then
		pass "$label"
		return 0
	fi
	fail "$label" "pattern /$pattern/ not in: ${got:-<empty>}"
	return 1
}

wait_rr_grep() {
	local at="$1" name="$2" type="$3" pattern="$4" timeout="${5:-45}"
	local start now got
	start=$(date +%s)
	while true; do
		got=$(rr_values "$at" "$name" "$type" || true)
		if printf '%s\n' "$got" | grep -Ei -q -- "$pattern"; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 1
	done
}

assert_no_rr() {
	local at="$1" name="$2" type="$3" expect="$4" label="$5"
	local got
	got=$(rr_values "$at" "$name" "$type" || true)
	if printf '%s\n' "$got" | grep -Fqx "$expect"; then
		fail "$label" "still present: $got"
		return 1
	fi
	pass "$label"
}

assert_rcode() {
	local at="$1" expect="$2" label="$3"
	shift 3
	local got
	got=$(rcode "$at" "$@" || true)
	if [[ "$got" == "$expect" ]]; then
		pass "$label"
		return 0
	fi
	fail "$label" "expected rcode $expect, got ${got:-<empty>}"
	return 1
}

assert_file_grep() {
	local file="$1" pattern="$2" label="$3"
	if [[ ! -f "$file" ]]; then
		fail "$label" "missing file $file"
		return 1
	fi
	if grep -E -q -- "$pattern" "$file"; then
		pass "$label"
		return 0
	fi
	fail "$label" "pattern /$pattern/ not in $file"
	return 1
}

assert_file_not_grep() {
	local file="$1" pattern="$2" label="$3"
	if [[ ! -f "$file" ]]; then
		fail "$label" "missing file $file"
		return 1
	fi
	if grep -E -q -- "$pattern" "$file"; then
		fail "$label" "pattern /$pattern/ still in $file"
		return 1
	fi
	pass "$label"
}

nsupdate_send() {
	# Remaining arguments are UPDATE directives (without send).
	printf '%s\n' "$@" | nsupdate_lines
}

# stdin is UPDATE directives (no server/zone/send).
nsupdate_lines() {
	nsupdate -v -y "${TSIG_ALG}:${TSIG_NAME}:${TSIG_SECRET}" <<EOF
server ${PRIMARY} 53
zone ${ZONE}.
$(cat)
send
EOF
}

nsupdate_unsigned() {
	nsupdate -v <<EOF
server ${PRIMARY} 53
zone ${ZONE}.
$(printf '%s\n' "$@")
send
EOF
}

summary() {
	printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
	if (( FAIL > 0 )); then
		printf 'failures:\n'
		local f
		for f in "${failures[@]}"; do
			printf '  - %s\n' "$f"
		done
		return 1
	fi
	return 0
}
