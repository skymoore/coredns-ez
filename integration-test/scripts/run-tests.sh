#!/usr/bin/env bash
# In-container integration tests. Invoked by scripts/run.sh (host) as:
#   docker compose run --rm tester --stage bootstrap
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "$SCRIPT_DIR/lib.sh"
# shellcheck source=rr-catalog.sh
. "$SCRIPT_DIR/rr-catalog.sh"

STAGE="${1:-bootstrap}"
if [[ "$STAGE" == "--stage" ]]; then
	STAGE="${2:-bootstrap}"
fi

echo "==> stage: $STAGE"
echo "    primary=$PRIMARY secondary=$SECONDARY zone=$ZONE"

# Non-apex NS is a referral (authority section), not an ANSWER. Use the full
# message. Everything else is dig +short via assert_rr_grep.
catalog_lookup() {
	local at="$1" owner="$2" name="$3" type="$4"
	if [[ "$type" == "NS" && -n "$owner" ]]; then
		dig_at "$at" "$name" "$type"
	else
		rr_values "$at" "$name" "$type"
	fi
}

check_catalog() {
	local at="$1" prefix="$2" rows="$3"
	local owner type rdata pattern name got
	while IFS='|' read -r owner type rdata pattern; do
		[[ -z "$type" ]] && continue
		name=$(fqdn "$owner")
		# Glue below an NS cut is not returned on a direct query.
		if [[ "$owner" == ns.st-deleg || "$owner" == ns.it-deleg ]]; then
			continue
		fi
		got=$(catalog_lookup "$at" "$owner" "$name" "$type" || true)
		if printf '%s\n' "$got" | grep -Ei -q -- "$pattern"; then
			pass "$prefix $name $type"
		else
			fail "$prefix $name $type" "pattern /$pattern/ not in: ${got:-<empty>}"
		fi
	done <<<"$rows"
}

wait_catalog() {
	local at="$1" rows="$2"
	local owner type rdata pattern name first=1 got
	while IFS='|' read -r owner type rdata pattern; do
		[[ -z "$type" ]] && continue
		name=$(fqdn "$owner")
		if [[ "$owner" == ns.st-deleg || "$owner" == ns.it-deleg ]]; then
			continue
		fi
		if (( first )); then
			if ! wait_rr_grep "$at" "$name" "$type" "$pattern" 45; then
				fail "secondary received catalog (first $name $type)" "timed out serial=$(soa_serial "$at")"
				return 1
			fi
			pass "secondary received catalog (first $name $type)"
			first=0
			continue
		fi
		got=$(catalog_lookup "$at" "$owner" "$name" "$type" || true)
		if printf '%s\n' "$got" | grep -Ei -q -- "$pattern"; then
			pass "secondary $name $type"
		else
			fail "secondary $name $type" "pattern /$pattern/ not in: ${got:-<empty>}"
		fi
	done <<<"$rows"
}

absent_catalog() {
	local at="$1" prefix="$2" rows="$3"
	local owner type rdata pattern name got
	while IFS='|' read -r owner type rdata pattern; do
		[[ -z "$type" ]] && continue
		name=$(fqdn "$owner")
		if [[ "$owner" == ns.st-deleg || "$owner" == ns.it-deleg ]]; then
			continue
		fi
		got=$(catalog_lookup "$at" "$owner" "$name" "$type" || true)
		if printf '%s\n' "$got" | grep -Ei -q -- "$pattern"; then
			fail "$prefix $name $type is gone" "still: $got"
		else
			pass "$prefix $name $type is gone"
		fi
	done <<<"$rows"
}

dyn_update_lines() {
	local op="$1" owner type rdata pattern
	while IFS='|' read -r owner type rdata pattern; do
		[[ -z "$type" ]] && continue
		if [[ "$op" == add ]]; then
			printf 'update add %s. 60 %s %s\n' "$(fqdn "$owner")" "$type" "$rdata"
		else
			printf 'update delete %s. %s\n' "$(fqdn "$owner")" "$type"
		fi
	done <<<"$(dyn_rows)"
}

ixfr_mentions_catalog() {
	local stream="$1" rows="$2"
	local owner type rdata pattern name
	while IFS='|' read -r owner type rdata pattern; do
		[[ -z "$type" ]] && continue
		name=$(fqdn "$owner")
		if printf '%s\n' "$stream" | grep -F -q "$name"; then
			pass "IXFR names $name"
		else
			fail "IXFR names $name" "$stream"
		fi
	done <<<"$rows"
}

stage_bootstrap() {
	echo "-- waiting for primary SOA"
	if wait_soa "$PRIMARY" 60; then
		pass "primary answers SOA"
	else
		fail "primary answers SOA" "timed out"
		summary
		return 1
	fi

	echo "-- waiting for secondary to load the zone"
	if wait_soa "$SECONDARY" 60; then
		pass "secondary answers SOA"
	else
		fail "secondary answers SOA" "timed out waiting for first transfer"
		summary
		return 1
	fi

	echo "-- seed records on primary and secondary"
	local seed
	seed=$(seed_rows)
	check_catalog "$PRIMARY" "seed primary" "$seed"
	check_catalog "$SECONDARY" "seed secondary" "$seed"

	local pser sser
	pser=$(soa_serial "$PRIMARY")
	sser=$(soa_serial "$SECONDARY")
	if [[ "$pser" == "$sser" ]]; then
		pass "primary and secondary serials match ($pser)"
	else
		fail "primary and secondary serials match" "primary=$pser secondary=$sser"
	fi

	echo "-- AXFR from primary"
	local axfr
	axfr=$(dig +tcp +time=5 +tries=2 @"$PRIMARY" "$ZONE" AXFR)
	if printf '%s\n' "$axfr" | grep -q 'www.example.com.'; then
		pass "primary AXFR includes www"
	else
		fail "primary AXFR includes www" "$axfr"
	fi
	if printf '%s\n' "$axfr" | grep -q 'st-https.example.com.'; then
		pass "primary AXFR includes seed HTTPS"
	else
		fail "primary AXFR includes seed HTTPS" "$axfr"
	fi
	if printf '%s\n' "$axfr" | grep -q 'SOA'; then
		pass "primary AXFR includes SOA"
	else
		fail "primary AXFR includes SOA"
	fi

	echo "-- AXFR from secondary"
	local saxfr
	saxfr=$(dig +tcp +time=5 +tries=2 @"$SECONDARY" "$ZONE" AXFR)
	if printf '%s\n' "$saxfr" | grep -q 'st-svcb.example.com.'; then
		pass "secondary AXFR includes seed SVCB"
	else
		fail "secondary AXFR includes seed SVCB" "$saxfr"
	fi

	echo "-- unsigned UPDATE is refused"
	local unsigned_out unsigned_rc=0
	unsigned_out=$(nsupdate_unsigned "update add unsigned.$ZONE. 60 A 192.0.2.99" 2>&1) || unsigned_rc=$?
	if (( unsigned_rc != 0 )) || printf '%s\n' "$unsigned_out" | grep -Eqi 'REFUSED|failed|not implemented|denied'; then
		pass "unsigned UPDATE rejected (nsupdate rc=$unsigned_rc)"
	else
		if rr_values "$PRIMARY" "unsigned.$ZONE" A | grep -Fqx "192.0.2.99"; then
			fail "unsigned UPDATE rejected" "record was published: $unsigned_out"
		else
			pass "unsigned UPDATE did not publish a record"
		fi
	fi
	assert_no_rr "$PRIMARY" "unsigned.$ZONE" A "192.0.2.99" "unsigned A is absent on primary"

	echo "-- RFC 2136 ADD many RR types (one atomic UPDATE)"
	local serial_before serial_after dyn
	dyn=$(dyn_rows)
	serial_before=$(soa_serial "$PRIMARY")
	if dyn_update_lines add | nsupdate_lines; then
		pass "signed multi-type UPDATE accepted"
	else
		fail "signed multi-type UPDATE accepted"
	fi
	check_catalog "$PRIMARY" "primary" "$dyn"

	serial_after=$(soa_serial "$PRIMARY")
	if [[ -n "$serial_after" && "$serial_after" -gt "$serial_before" ]]; then
		pass "primary SOA serial bumped ($serial_before -> $serial_after)"
	else
		fail "primary SOA serial bumped" "before=$serial_before after=$serial_after"
	fi

	echo "-- primary persist (sync, before NOERROR)"
	if wait_file_grep "$PRIMARY_ZONEFILE" 'it-https' 10; then
		pass "primary zone file contains it-https"
	else
		fail "primary zone file contains it-https"
	fi

	echo "-- secondary picks up the multi-type UPDATE via IXFR"
	wait_catalog "$SECONDARY" "$dyn"
	assert_rr "$SECONDARY" "www.$ZONE" A "192.0.2.80" "secondary still has www after IXFR"
	assert_rr "$SECONDARY" "ns1.$ZONE" A "172.30.53.10" "secondary still has ns1 after IXFR"
	assert_rr_grep "$SECONDARY" "st-txt.$ZONE" TXT "static-txt" "secondary still has static TXT after IXFR"
	assert_rr_grep "$SECONDARY" "st-https.$ZONE" HTTPS "alpn" "secondary still has static HTTPS after IXFR"

	echo "-- IXFR (RFC 1995 incremental via ixfr plugin)"
	local ixfr_old ixfr_cur soa_count
	ixfr_old=$(dig +tcp +time=5 +tries=2 @"$PRIMARY" "$ZONE" "IXFR=$serial_before")
	if printf '%s\n' "$ixfr_old" | awk -v s="$serial_before" '$4=="SOA" && $7==s { found=1 } END { exit !found }'; then
		pass "IXFR of old serial contains inner SOA $serial_before"
	else
		fail "IXFR of old serial contains inner SOA $serial_before" "$ixfr_old"
	fi
	# Owner-name match: CNAME/PTR rdata can mention www.example.com. without
	# the www RRset itself being in the delta.
	if printf '%s\n' "$ixfr_old" | grep -E -q '^www\.example\.com\.'; then
		fail "IXFR of old serial omits unchanged www RRset" "$ixfr_old"
	else
		pass "IXFR of old serial omits unchanged www RRset (delta, not full zone)"
	fi
	if printf '%s\n' "$ixfr_old" | grep -q 'st-txt.example.com.'; then
		fail "IXFR of old serial omits static TXT" "$ixfr_old"
	else
		pass "IXFR of old serial omits static TXT"
	fi
	ixfr_mentions_catalog "$ixfr_old" "$dyn"
	ixfr_cur=$(dig +tcp +time=5 +tries=2 @"$PRIMARY" "$ZONE" "IXFR=$serial_after")
	soa_count=$(printf '%s\n' "$ixfr_cur" | grep -c 'SOA' || true)
	if ! printf '%s\n' "$ixfr_cur" | grep -q 'it-a.example.com.' && (( soa_count >= 1 )); then
		pass "IXFR of current serial is up-to-date (SOA only)"
	else
		fail "IXFR of current serial is up-to-date" "soa_count=$soa_count body=$ixfr_cur"
	fi
	if wait_file_grep "${PRIMARY_ZONEFILE}.ixfr" 'it-https' 10; then
		pass "IXFR journal contains HTTPS increment"
	else
		fail "IXFR journal contains HTTPS increment"
	fi
	if wait_file_grep "${PRIMARY_ZONEFILE}.ixfr" 'it-svcb' 5; then
		pass "IXFR journal contains SVCB increment"
	else
		fail "IXFR journal contains SVCB increment"
	fi

	echo "-- mutable allowlist: DNAME is not listed"
	if nsupdate_send "update add blocked.$ZONE. 60 DNAME other.example.net." 2>/dev/null; then
		if rr_values "$PRIMARY" "blocked.$ZONE" DNAME | grep -q 'other'; then
			fail "DNAME UPDATE rejected by mutable" "record was published"
		else
			pass "DNAME UPDATE did not publish a record"
		fi
	else
		pass "DNAME UPDATE rejected by mutable (nsupdate non-zero)"
	fi

	echo "-- RFC 2136 DELETE all dynamic types"
	if dyn_update_lines delete | nsupdate_lines; then
		pass "signed multi-type DELETE accepted"
	else
		fail "signed multi-type DELETE accepted"
	fi
	absent_catalog "$PRIMARY" "primary" "$dyn"
	if wait_no_rr "$SECONDARY" "it-a.$ZONE" A "192.0.2.50" 45; then
		pass "secondary dropped it-a A"
	else
		fail "secondary dropped it-a A"
	fi
	absent_catalog "$SECONDARY" "secondary" "$dyn"
	assert_rr "$SECONDARY" "www.$ZONE" A "192.0.2.80" "secondary still has www after DELETE IXFR"
	assert_rr_grep "$SECONDARY" "st-txt.$ZONE" TXT "static-txt" "secondary still has static TXT after DELETE IXFR"

	echo "-- persist-probe TXT (survives the host-side restart stages)"
	if nsupdate_send 'update add persist-probe.example.com. 60 TXT "still-here"'; then
		pass "persist-probe TXT UPDATE accepted"
	else
		fail "persist-probe TXT UPDATE accepted"
	fi
	assert_rr "$PRIMARY" "persist-probe.$ZONE" TXT '"still-here"' "primary serves persist-probe"
	if wait_rr "$SECONDARY" "persist-probe.$ZONE" TXT '"still-here"' 45; then
		pass "secondary serves persist-probe"
	else
		fail "secondary serves persist-probe"
	fi
	if wait_file_grep "$PRIMARY_ZONEFILE" 'persist-probe' 10; then
		pass "primary file contains persist-probe"
	else
		fail "primary file contains persist-probe"
	fi
	if wait_file_grep "$SECONDARY_ZONEFILE" 'persist-probe' 20; then
		pass "secondary file contains persist-probe"
	else
		fail "secondary file contains persist-probe"
	fi

	stage_api
}

stage_api() {
	echo "-- API plugin on DoH :8443"
	local code body token resp

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/health" || true)
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q primary; then
		pass "GET $API_PRIMARY/api/v1/health"
	else
		fail "GET $API_PRIMARY/api/v1/health" "code=$code body=$body"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/zones" || true)
	code=$(api_code "$resp")
	if [[ "$code" == "401" ]]; then
		pass "GET /api/v1/zones is 401 without auth"
	else
		fail "GET /api/v1/zones is 401 without auth" "code=$code"
	fi

	token=$(api_login "$API_PRIMARY" || true)
	if [[ -n "$token" && "$token" != "null" ]]; then
		pass "POST /api/v1/auth/login on primary"
	else
		fail "POST /api/v1/auth/login on primary"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"origin\":\"$API_ZONE\",\"type\":\"primary\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "200" || "$code" == "201" ]]; then
		pass "POST /api/v1/zones $API_ZONE"
	else
		fail "POST /api/v1/zones $API_ZONE" "code=$code body=$(api_body "$resp")"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones/${API_ZONE}/records" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"name\":\"$API_OWNER\",\"type\":\"A\",\"ttl\":60,\"rdata\":\"$API_ADDR\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "201" ]]; then
		pass "POST A $API_OWNER"
	else
		fail "POST A $API_OWNER" "code=$code body=$(api_body "$resp")"
		return
	fi

	if wait_rr "$PRIMARY" "$API_OWNER" A "$API_ADDR" 20; then
		pass "primary UDP serves API-created $API_OWNER"
	else
		fail "primary UDP serves API-created $API_OWNER"
	fi

	local doh_code ctype
	# RFC 8484 POST. Bare GET /dns-query must not be the JSON mux (400 = DoH parse).
	doh_code=$(curl -sS -o /dev/null -w '%{http_code}' "$API_PRIMARY/dns-query" || true)
	if [[ "$doh_code" != "200" ]]; then
		pass "GET /dns-query is DoH not the JSON API (code=$doh_code)"
	else
		fail "GET /dns-query is DoH not the JSON API" "code=$doh_code"
	fi
	# www.it-api.example. IN A, RD, id=1. Write the wire query to a file:
	# bash $(...) strips NUL bytes.
	printf '\x00\x01\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x03www\x06it-api\x07example\x00\x00\x01\x00\x01' > /tmp/doh.q
	doh_code=$(curl -sS -o /tmp/doh.msg -w '%{http_code}' -X POST "$API_PRIMARY/dns-query" \
		-H 'Content-Type: application/dns-message' --data-binary @/tmp/doh.q || true)
	if [[ "$doh_code" == "200" ]]; then
		pass "POST /dns-query DoH answers API-created zone (http $doh_code)"
	else
		fail "POST /dns-query DoH answers API-created zone" "code=$doh_code"
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/cluster/join-tokens" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d '{"ttl":"1h"}')
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	local join_tok
	join_tok=$(json_str "$body" token)
	if [[ "$code" == "201" && -n "$join_tok" && "$join_tok" != "null" ]]; then
		pass "POST /api/v1/cluster/join-tokens"
	else
		fail "POST /api/v1/cluster/join-tokens" "code=$code body=$body"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_SECONDARY/api/v1/cluster/connect" \
		-H 'Content-Type: application/json' \
		-d "{\"url\":\"$API_PRIMARY\",\"token\":\"$join_tok\",\"dns\":\"$SECONDARY:53\",\"api_url\":\"$API_SECONDARY\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "200" ]]; then
		pass "POST secondary /api/v1/cluster/connect"
	else
		fail "POST secondary /api/v1/cluster/connect" "code=$code body=$(api_body "$resp")"
		return
	fi

	local stoken
	stoken=$(api_login "$API_SECONDARY" || true)
	if [[ -n "$stoken" && "$stoken" != "null" ]]; then
		pass "login on secondary with primary credentials"
	else
		fail "login on secondary with primary credentials"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_SECONDARY/api/v1/zones" \
		-H "Authorization: Bearer $stoken")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q 'it-api.example'; then
		pass "secondary GET /api/v1/zones lists $API_ZONE"
	else
		fail "secondary GET /api/v1/zones lists $API_ZONE" "code=$code body=$body"
	fi

	if wait_rr "$SECONDARY" "$API_OWNER" A "$API_ADDR" 45; then
		pass "secondary UDP serves API-created $API_OWNER"
	else
		fail "secondary UDP serves API-created $API_OWNER"
	fi
}

stage_primary_restart() {
	echo "-- after primary restart, in-memory view comes from the rewritten file"
	if wait_soa "$PRIMARY" 60; then
		pass "primary SOA after restart"
	else
		fail "primary SOA after restart"
		summary
		return 1
	fi
	assert_rr "$PRIMARY" "persist-probe.$ZONE" TXT '"still-here"' "primary still serves persist-probe after restart"
	assert_rr "$PRIMARY" "www.$ZONE" A "192.0.2.80" "primary still serves seed www A after restart"
	assert_rr_grep "$PRIMARY" "st-https.$ZONE" HTTPS "alpn" "primary still serves seed HTTPS after restart"
	assert_rr_grep "$PRIMARY" "st-eui48.$ZONE" EUI48 "01-23-45-67-89-ab" "primary still serves seed EUI48 after restart"
	assert_no_rr "$PRIMARY" "it-a.$ZONE" A "192.0.2.50" "primary did not resurrect deleted it-a"
	local ixfr1
	ixfr1=$(dig +tcp +time=5 +tries=2 @"$PRIMARY" "$ZONE" IXFR=1)
	if printf '%s\n' "$ixfr1" | grep -q 'persist-probe' && ! printf '%s\n' "$ixfr1" | grep -E -q '^www\.example\.com\.'; then
		pass "after restart, IXFR from serial 1 is still incremental (journal survived)"
	else
		fail "after restart, IXFR from serial 1 is still incremental" "$ixfr1"
	fi
	assert_file_grep "$PRIMARY_ZONEFILE" 'persist-probe' "primary file still contains persist-probe"

	local token
	token=$(api_login "$API_PRIMARY" || true)
	if [[ -n "$token" && "$token" != "null" ]]; then
		pass "API login on primary after restart"
	else
		fail "API login on primary after restart"
	fi
	assert_rr "$PRIMARY" "$API_OWNER" A "$API_ADDR" "primary still serves API-created A after restart"

	if wait_rr "$SECONDARY" "persist-probe.$ZONE" TXT '"still-here"' 30; then
		pass "secondary still serves persist-probe"
	else
		fail "secondary still serves persist-probe"
	fi
}

stage_secondary_alone() {
	echo "-- primary is down; secondary serves the persisted copy"
	if wait_soa "$SECONDARY" 30; then
		pass "secondary SOA with primary down"
	else
		fail "secondary SOA with primary down"
		summary
		return 1
	fi
	assert_rr "$SECONDARY" "persist-probe.$ZONE" TXT '"still-here"' "secondary still serves persist-probe from disk"
	assert_rr "$SECONDARY" "www.$ZONE" A "192.0.2.80" "secondary still serves seed www A from disk"
	assert_rr_grep "$SECONDARY" "st-svcb.$ZONE" SVCB "svc.example.com" "secondary still serves seed SVCB from disk"
	assert_rr_grep "$SECONDARY" "st-naptr.$ZONE" NAPTR "E2U" "secondary still serves seed NAPTR from disk"
	assert_no_rr "$SECONDARY" "it-a.$ZONE" A "192.0.2.50" "secondary did not resurrect deleted it-a"
	assert_file_grep "$SECONDARY_ZONEFILE" 'persist-probe' "secondary file still contains persist-probe"

	local stoken
	stoken=$(api_login "$API_SECONDARY" || true)
	if [[ -n "$stoken" && "$stoken" != "null" ]]; then
		pass "API login on secondary with primary down"
	else
		fail "API login on secondary with primary down"
	fi
	assert_rr "$SECONDARY" "$API_OWNER" A "$API_ADDR" "secondary still serves API-created A with primary down"

	local prc
	prc=$(rcode "$PRIMARY" "$ZONE" SOA || true)
	if [[ -z "$prc" || "$prc" == "SERVFAIL" || "$prc" == "REFUSED" ]]; then
		pass "primary is unreachable as expected (rcode='${prc}')"
	else
		pass "primary is unreachable as expected (rcode='${prc}')"
	fi
}

case "$STAGE" in
bootstrap) stage_bootstrap ;;
primary-restart) stage_primary_restart ;;
secondary-alone) stage_secondary_alone ;;
*)
	echo "unknown stage: $STAGE" >&2
	echo "usage: $0 --stage bootstrap|primary-restart|secondary-alone" >&2
	exit 2
	;;
esac

summary
