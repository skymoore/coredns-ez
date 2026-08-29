#!/usr/bin/env bash
# Shared helpers for the in-container integration tests.
# shellcheck disable=SC2034

set -euo pipefail

PRIMARY="${PRIMARY:-172.30.53.10}"
SECONDARY="${SECONDARY:-172.30.53.20}"
ZONE="${ZONE:-example.com}"
PRIMARY_ZONEFILE="${PRIMARY_ZONEFILE:-/primary-data/db.example.com}"
SECONDARY_ZONEFILE="${SECONDARY_ZONEFILE:-/secondary-data/db.example.com}"
PRIMARY_SQLITE="${PRIMARY_SQLITE:-/primary-data/admin.sqlite}"
SECONDARY_SQLITE="${SECONDARY_SQLITE:-/secondary-data/admin.sqlite}"
TSIG_NAME="${TSIG_NAME:-updater.example.com.}"
TSIG_SECRET="${TSIG_SECRET:-Y29yZWRucy1pbnRlZ3JhdGlvbi10ZXN0LWtleSEh}"
TSIG_ALG="${TSIG_ALG:-hmac-sha256}"
API_PRIMARY="${API_PRIMARY:-http://172.30.53.10:8443}"
API_SECONDARY="${API_SECONDARY:-http://172.30.53.20:8443}"
API_USER="${API_USER:-admin}"
API_PASS="${API_PASS:-integration-test-password}"
API_ZONE="${API_ZONE:-it-api.example.}"
API_OWNER="${API_OWNER:-www.it-api.example.}"
API_ADDR="${API_ADDR:-192.0.2.99}"
INTERNAL_DNS="${INTERNAL_DNS:-10.53.0.10}"
SPLIT_OWNER="${SPLIT_OWNER:-split.it-api.example.}"
SPLIT_PUBLIC="${SPLIT_PUBLIC:-192.0.2.40}"
SPLIT_INTERNAL="${SPLIT_INTERNAL:-10.8.0.40}"
NAS_OWNER="${NAS_OWNER:-nas.it-api.example.}"
NAS_INTERNAL="${NAS_INTERNAL:-10.8.0.9}"
CSPLIT_ORIGIN="${CSPLIT_ORIGIN:-cache-split.example.}"
CSPLIT_OWNER="${CSPLIT_OWNER:-csplit.cache-split.example.}"
CSPLIT_PUBLIC="${CSPLIT_PUBLIC:-192.0.2.70}"
CSPLIT_INTERNAL="${CSPLIT_INTERNAL:-10.8.0.70}"
CSPLIT_UPDATED="${CSPLIT_UPDATED:-192.0.2.71}"
# Live ns1 regression (pg.db.rwx.dev): public catch-all wildcard, internal
# catch-all, more-specific internal exact, cache+prefetch in front of admin.
HORIZON_ORIGIN="${HORIZON_ORIGIN:-horizon.example.}"
HORIZON_PG="${HORIZON_PG:-pg.db.horizon.example.}"
HORIZON_LATE="${HORIZON_LATE:-late.horizon.example.}"
HORIZON_NS1="${HORIZON_NS1:-ns1.horizon.example.}"
HORIZON_MAIL="${HORIZON_MAIL:-mail.horizon.example.}"
HORIZON_NONE="${HORIZON_NONE:-nosuch.horizon.example.}"
HORIZON_PUB="${HORIZON_PUB:-192.0.2.89}"
HORIZON_INT_WILD="${HORIZON_INT_WILD:-10.8.0.99}"
HORIZON_PG_INT="${HORIZON_PG_INT:-10.8.0.90}"
HORIZON_NS1_PUB="${HORIZON_NS1_PUB:-192.0.2.53}"
HORIZON_NS1_INT="${HORIZON_NS1_INT:-10.8.0.53}"
HORIZON_MAIL_PUB="${HORIZON_MAIL_PUB:-192.0.2.25}"
LAN_SRC="${LAN_SRC:-10.53.0.30}"
PUB_SRC="${PUB_SRC:-172.30.53.30}"
METRICS_PRIMARY="${METRICS_PRIMARY:-http://172.30.53.10:9153/metrics}"
METRICS_SECONDARY="${METRICS_SECONDARY:-http://172.30.53.20:9153/metrics}"

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

wait_sqlite_grep() {
	local db="$1" sql="$2" pattern="$3" timeout="${4:-20}"
	local start now got
	start=$(date +%s)
	while true; do
		if [[ -f "$db" ]]; then
			got=$(sqlite3 "$db" "$sql" 2>/dev/null || true)
			if printf '%s\n' "$got" | grep -E -q -- "$pattern"; then
				return 0
			fi
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 1
	done
}

assert_sqlite_grep() {
	local db="$1" sql="$2" pattern="$3" label="$4"
	local got
	got=$(sqlite3 "$db" "$sql" 2>/dev/null || true)
	if printf '%s\n' "$got" | grep -E -q -- "$pattern"; then
		pass "$label"
	else
		fail "$label" "pattern /$pattern/ not in sqlite: ${got:-<empty>}"
	fi
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

# Query with an explicit source address (dig -b). LAN checks must target
# INTERNAL_DNS (10.53.0.10): binding 10.53.0.30 and sending to 172.30.53.10
# does not preserve the source address across docker networks, so CoreDNS
# would see 172.30.53.30 and serve the public view.
assert_rr_from() {
	local src="$1" at="$2" name="$3" type="$4" expect="$5" label="$6"
	local got
	got=$(dig "${DIG_OPTS[@]}" -b "$src" @"$at" +short "$name" "$type" || true)
	if printf '%s\n' "$got" | grep -Fqx "$expect"; then
		pass "$label"
		return 0
	fi
	fail "$label" "src=$src expected '$expect', got: ${got:-<empty>}"
	return 1
}

assert_no_rr_from() {
	local src="$1" at="$2" name="$3" type="$4" expect="$5" label="$6"
	local got
	got=$(dig "${DIG_OPTS[@]}" -b "$src" @"$at" +short "$name" "$type" || true)
	if ! printf '%s\n' "$got" | grep -Fqx "$expect"; then
		pass "$label"
		return 0
	fi
	fail "$label" "src=$src still has '$expect' in: $got"
	return 1
}

# Poll for an answer from an explicit source address (dig -b).
wait_rr_from() {
	local src="$1" at="$2" name="$3" type="$4" expect="$5" timeout="${6:-20}"
	local start now got
	start=$(date +%s)
	while true; do
		got=$(dig "${DIG_OPTS[@]}" -b "$src" @"$at" +short "$name" "$type" 2>/dev/null || true)
		if printf '%s\n' "$got" | grep -Fqx "$expect"; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 0.5
	done
}

# Sum of coredns cache hit counters (all servers/zones/views).
cache_hits_total() {
	curl -sS "$METRICS_PRIMARY" 2>/dev/null | awk '/^coredns_cache_hits_total/ {s += $NF} END {print s+0}'
}

rr_from() {
	local src="$1" at="$2" name="$3" type="$4"
	dig "${DIG_OPTS[@]}" -b "$src" @"$at" +short "$name" "$type" 2>/dev/null || true
}

# n queries from src; every answer must be `expect` and must never be `forbidden`.
assert_stable_rr_from() {
	local src="$1" at="$2" name="$3" type="$4" expect="$5" forbidden="$6" n="$7" label="$8"
	local i got
	for i in $(seq 1 "$n"); do
		got=$(rr_from "$src" "$at" "$name" "$type")
		if printf '%s\n' "$got" | grep -Fqx "$forbidden"; then
			fail "$label" "query $i/$n src=$src served forbidden '$forbidden' in: ${got:-<empty>}"
			return 1
		fi
		if ! printf '%s\n' "$got" | grep -Fqx "$expect"; then
			fail "$label" "query $i/$n src=$src expected '$expect', got: ${got:-<empty>}"
			return 1
		fi
	done
	pass "$label ($n queries)"
}

assert_never_rr_from() {
	local src="$1" at="$2" name="$3" type="$4" forbidden="$5" n="$6" label="$7"
	local i got
	for i in $(seq 1 "$n"); do
		got=$(rr_from "$src" "$at" "$name" "$type")
		if printf '%s\n' "$got" | grep -Fqx "$forbidden"; then
			fail "$label" "query $i/$n src=$src served forbidden '$forbidden' in: ${got:-<empty>}"
			return 1
		fi
	done
	pass "$label ($n queries)"
}

wait_origin_soa() {
	local at="$1" origin="$2" timeout="${3:-20}"
	local start now
	start=$(date +%s)
	while true; do
		if [[ -n "$(rr_values "$at" "$origin" SOA || true)" ]]; then
			return 0
		fi
		now=$(date +%s)
		if (( now - start >= timeout )); then
			return 1
		fi
		sleep 0.5
	done
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

api_curl() {
	local base="$1" method="$2" path="$3"
	shift 3
	curl -sS -w '\n%{http_code}' -X "$method" "$base$path" "$@"
}

api_code() {
	printf '%s\n' "$1" | tail -n1
}

api_body() {
	printf '%s\n' "$1" | sed '$d'
}

json_str() {
	local json="$1" key="$2"
	printf '%s' "$json" | sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" | head -n1
}

json_member_id() {
	local json="$1" role="$2"
	python3 -c 'import json,sys
d=json.loads(sys.argv[1])
role=sys.argv[2]
for m in d.get("members") or []:
    if m.get("role")==role:
        print(m.get("id") or "")
        break
' "$json" "$role"
}

json_member_dns() {
	local json="$1" role="$2"
	python3 -c 'import json,sys
d=json.loads(sys.argv[1])
role=sys.argv[2]
for m in d.get("members") or []:
    if m.get("role")==role:
        print(m.get("dns_addr") or "")
        break
' "$json" "$role"
}

api_login() {
	local base="$1"
	local resp body code
	resp=$(curl -sS -w '\n%{http_code}' -X POST "$base/api/v1/auth/login" \
		-H 'Content-Type: application/json' \
		-d "{\"username\":\"$API_USER\",\"password\":\"$API_PASS\"}")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" != "200" ]]; then
		return 1
	fi
	json_str "$body" token
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
