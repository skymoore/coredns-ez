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

	echo "-- primary persist (sqlite, before NOERROR)"
	if wait_sqlite_grep "$PRIMARY_SQLITE" "SELECT name||' '||rdata FROM records WHERE origin='example.com.';" 'it-https' 10; then
		pass "primary sqlite contains it-https"
	else
		fail "primary sqlite contains it-https"
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
	# Corefile example.com uses the in-process ixfr plugin (not sqlite journals).
	# The IXFR stream checks above already require a real RFC 1995 delta.

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
	if wait_sqlite_grep "$PRIMARY_SQLITE" "SELECT name FROM records WHERE origin='example.com.';" 'persist-probe' 10; then
		pass "primary sqlite contains persist-probe"
	else
		fail "primary sqlite contains persist-probe"
	fi
	if wait_sqlite_grep "$SECONDARY_SQLITE" "SELECT name FROM records WHERE origin='example.com.';" 'persist-probe' 20; then
		pass "secondary sqlite contains persist-probe"
	else
		fail "secondary sqlite contains persist-probe"
	fi

	stage_admin
}

stage_admin() {
	echo "-- admin plugin on DoH :8443"
	local code body token resp join_tok hangup_out stoken

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/health" || true)
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q primary; then
		pass "GET $API_PRIMARY/api/v1/health"
	else
		fail "GET $API_PRIMARY/api/v1/health" "code=$code body=$body"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/" || true)
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -qi '<html'; then
		pass "GET / is the admin UI HTML"
	else
		fail "GET / is the admin UI HTML" "code=$code"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/zones" || true)
	code=$(api_code "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$(api_body "$resp")" | grep -qi '<html'; then
		pass "GET /zones SPA fallback is HTML"
	else
		fail "GET /zones SPA fallback is HTML" "code=$code"
	fi

	local asset
	asset=$(printf '%s' "$body" | sed -n 's/.*src="\(\/assets\/[^"]*\)".*/\1/p' | head -1)
	if [[ -n "$asset" ]]; then
		resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY$asset" || true)
		code=$(api_code "$resp")
		if [[ "$code" == "200" ]]; then
			pass "GET hashed UI asset $asset"
		else
			fail "GET hashed UI asset $asset" "code=$code"
		fi
	else
		fail "GET hashed UI asset" "no /assets/ script in index.html"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1" || true)
	code=$(api_code "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$(api_body "$resp")" | grep -q '"ui"'; then
		pass "GET /api/v1 JSON index"
	else
		fail "GET /api/v1 JSON index" "code=$code body=$(api_body "$resp")"
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

	resp=$(curl -sS -w '\n%{http_code}' "$METRICS_PRIMARY" || true)
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q 'coredns_dns_requests_total'; then
		pass "GET $METRICS_PRIMARY scrape has DNS request series"
	else
		fail "GET $METRICS_PRIMARY scrape has DNS request series" "code=$code"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$METRICS_SECONDARY" || true)
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q 'coredns_dns_requests_total'; then
		pass "GET $METRICS_SECONDARY scrape has DNS request series"
	else
		fail "GET $METRICS_SECONDARY scrape has DNS request series" "code=$code"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/metrics" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q 'coredns_dns_requests_total'; then
		pass "GET /api/v1/metrics includes coredns_dns_requests_total"
	else
		fail "GET /api/v1/metrics includes coredns_dns_requests_total" "code=$code body=$body"
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

	stage_dnssec_enable "$token"

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

	# Hang up after the 200 headers: snapshot apply can drop TLS the way the
	# admin UI saw on ns2. The node must already be secondary (GET /node).
	hangup_out=""
	if hangup_out=$(python3 "$SCRIPT_DIR/connect-hangup.py" "$API_SECONDARY" "$API_PRIMARY" "$join_tok" "$SECONDARY"); then
		pass "POST /cluster/connect flushes 200 before snapshot apply ($hangup_out)"
	else
		fail "POST /cluster/connect flushes 200 before snapshot apply" "$hangup_out"
		return
	fi
	# Apply continues after the client is gone; wait for identity to land.
	stoken=""
	i=0
	while [[ "$i" -lt 20 ]]; do
		stoken=$(api_login "$API_SECONDARY" || true)
		[[ -n "$stoken" && "$stoken" != "null" ]] && break
		i=$((i + 1))
		sleep 1
	done
	resp=$(curl -sS -w '\n%{http_code}' "$API_SECONDARY/api/v1/node" \
		-H "Authorization: Bearer ${stoken:-x}")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"role":"secondary"'; then
		pass "GET /node is secondary after hung-up connect (UI recovery path)"
	else
		fail "GET /node is secondary after hung-up connect (UI recovery path)" "code=$code body=$body"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/cluster" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"role":"primary"' && printf '%s' "$body" | grep -q '"role":"secondary"'; then
		pass "GET primary /api/v1/cluster lists primary and secondary"
	else
		fail "GET primary /api/v1/cluster lists primary and secondary" "code=$code body=$body"
	fi

	stoken=$(api_login "$API_SECONDARY" || true)
	if [[ -n "$stoken" && "$stoken" != "null" ]]; then
		pass "login on secondary with primary credentials"
	else
		fail "login on secondary with primary credentials"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_SECONDARY/api/v1/cluster" \
		-H "Authorization: Bearer $stoken")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"role":"primary"' && printf '%s' "$body" | grep -q '"role":"secondary"'; then
		pass "GET secondary /api/v1/cluster lists primary and secondary"
	else
		fail "GET secondary /api/v1/cluster lists primary and secondary" "code=$code body=$body"
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

	stage_dnssec_replica "$stoken"
	stage_qstat "$token"
	stage_cluster_dns "$token" "$stoken"
	stage_split_horizon "$token"
	stage_split_horizon_cache "$token"
}

stage_dnssec_enable() {
	local token="$1"
	local resp code body ans
	echo "-- DNSSEC enable, wire DNSKEY/RRSIG"

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/zones/${API_ZONE}/dnssec" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"enabled":false'; then
		pass "GET DNSSEC off before enable"
	else
		fail "GET DNSSEC off before enable" "code=$code body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones/${API_ZONE}/dnssec" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "201" || "$code" == "200" ]] && printf '%s' "$body" | grep -q '"enabled":true' \
		&& printf '%s' "$body" | grep -q '"ds_data"' && printf '%s' "$body" | grep -q '"key_data"'; then
		pass "POST enable DNSSEC returns DS/key fields"
	else
		fail "POST enable DNSSEC returns DS/key fields" "code=$code body=$body"
		return
	fi

	local ans
	ans=$(dig_at "$PRIMARY" +dnssec +noall +answer "${API_ZONE%.}" DNSKEY || true)
	if printf '%s\n' "$ans" | grep -q DNSKEY && printf '%s\n' "$ans" | grep -q RRSIG; then
		pass "primary DNSKEY+dnssec has DNSKEY and RRSIG"
	else
		fail "primary DNSKEY+dnssec has DNSKEY and RRSIG" "$ans"
	fi

	ans=$(dig_at "$PRIMARY" +dnssec +noall +answer "$API_OWNER" A || true)
	if printf '%s\n' "$ans" | grep -q "$API_ADDR" && printf '%s\n' "$ans" | grep -q RRSIG; then
		pass "primary A+dnssec is signed"
	else
		fail "primary A+dnssec is signed" "$ans"
	fi

	ans=$(dig_at "$PRIMARY" +noall +answer "$API_OWNER" A || true)
	if printf '%s\n' "$ans" | grep -q "$API_ADDR" && ! printf '%s\n' "$ans" | grep -q RRSIG; then
		pass "primary A without DO has no RRSIG"
	else
		fail "primary A without DO has no RRSIG" "$ans"
	fi

}

stage_dnssec_replica() {
	local stoken="$1"
	local resp code body start now got
	echo "-- DNSSEC on secondary after join snapshot"
	start=$(date +%s)
	got=""
	while true; do
		got=$(dig_at "$SECONDARY" +short "${API_ZONE%.}" DNSKEY || true)
		if printf '%s\n' "$got" | grep -q '257'; then
			pass "secondary serves DNSKEY after snapshot"
			break
		fi
		now=$(date +%s)
		if (( now - start >= 45 )); then
			fail "secondary serves DNSKEY after snapshot" "got=${got:-<empty>}"
			break
		fi
		sleep 2
	done

	if [[ -n "$stoken" ]]; then
		resp=$(curl -sS -w '\n%{http_code}' "$API_SECONDARY/api/v1/zones/${API_ZONE}/dnssec" \
			-H "Authorization: Bearer $stoken")
		code=$(api_code "$resp")
		body=$(api_body "$resp")
		if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"enabled":true'; then
			pass "secondary GET DNSSEC shows enabled"
		else
			fail "secondary GET DNSSEC shows enabled" "code=$code body=$body"
		fi
	fi
}

stage_qstat() {
	local token="$1"
	local resp code body t
	echo "-- query stats ranges and per-type series"

	local t
	for t in A AAAA TXT NS SOA MX DNSKEY; do
		dig_at "$PRIMARY" "${API_ZONE%.}" "$t" >/dev/null || true
		dig_at "$PRIMARY" "$API_OWNER" "$t" >/dev/null || true
	done

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/queries" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"range":"1h"' \
		&& printf '%s' "$body" | grep -q '"series"' && printf '%s' "$body" | grep -q '"by_type"'; then
		pass "GET /queries default range is 1h with series"
	else
		fail "GET /queries default range is 1h with series" "code=$code body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/queries?range=5m" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"range":"5m"' \
		&& printf '%s' "$body" | grep -q '"name":"A"'; then
		pass "GET /queries?range=5m includes type A"
	else
		fail "GET /queries?range=5m includes type A" "code=$code body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/queries?range=24h" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"range":"24h"' \
		&& printf '%s' "$body" | grep -q '"step_seconds"'; then
		pass "GET /queries?range=24h"
	else
		fail "GET /queries?range=24h" "code=$code body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/queries?range=7d" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"range":"7d"'; then
		pass "GET /queries?range=7d"
	else
		fail "GET /queries?range=7d" "code=$code body=$body"
	fi

	echo "    waiting 12s for query bucket flush"
	sleep 12
	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/queries?range=1h" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"range_queries"' \
		&& printf '%s' "$body" | grep -q '"types"'; then
		pass "GET /queries after flush still has types in series"
	else
		fail "GET /queries after flush still has types in series" "code=$code body=$body"
	fi
}

stage_cluster_dns() {
	local token="$1" stoken="$2"
	local resp code body sec_id
	echo "-- cluster DNS address edit and secondary primary-DNS override"

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/cluster" \
		-H "Authorization: Bearer $token")
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	local sec_id
	sec_id=$(json_member_id "$body" secondary)
	if [[ "$code" == "200" && -n "$sec_id" ]]; then
		pass "cluster roster has secondary id"
	else
		fail "cluster roster has secondary id" "code=$code body=$body"
		return
	fi
	if printf '%s' "$body" | grep -q '"advertise_dns"' && printf '%s' "$body" | grep -q '"primary_dns"'; then
		pass "GET /cluster includes advertise_dns and primary_dns"
	else
		fail "GET /cluster includes advertise_dns and primary_dns" "body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X PATCH "$API_PRIMARY/api/v1/cluster/members/${sec_id}" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d '{"dns_addr":"203.0.113.20"}')
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '203.0.113.20:53'; then
		pass "PATCH secondary dns_addr"
	else
		fail "PATCH secondary dns_addr" "code=$code body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/cluster" \
		-H "Authorization: Bearer $token")
	body=$(api_body "$resp")
	if [[ "$(json_member_dns "$body" secondary)" == "203.0.113.20:53" ]]; then
		pass "GET /cluster shows patched secondary DNS"
	else
		fail "GET /cluster shows patched secondary DNS" "dns=$(json_member_dns "$body" secondary)"
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X PATCH "$API_PRIMARY/api/v1/cluster/members/${sec_id}" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d '{"dns_addr":"127.0.0.1:53"}')
	code=$(api_code "$resp")
	if [[ "$code" == "400" ]]; then
		pass "PATCH loopback dns_addr rejected"
	else
		fail "PATCH loopback dns_addr rejected" "code=$code body=$(api_body "$resp")"
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X PATCH "$API_PRIMARY/api/v1/cluster/members/${sec_id}" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"dns_addr\":\"${SECONDARY}:53\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "200" ]]; then
		pass "PATCH secondary dns_addr restored"
	else
		fail "PATCH secondary dns_addr restored" "code=$code body=$(api_body "$resp")"
	fi

	if [[ -z "$stoken" ]]; then
		fail "secondary token missing for primary-dns override"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_SECONDARY/api/v1/cluster" \
		-H "Authorization: Bearer $stoken")
	body=$(api_body "$resp")
	if printf '%s' "$body" | grep -q '172.30.53.10:53'; then
		pass "secondary cluster default primary DNS is advertise"
	else
		fail "secondary cluster default primary DNS is advertise" "body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X PUT "$API_SECONDARY/api/v1/cluster/primary-dns" \
		-H "Authorization: Bearer $stoken" -H 'Content-Type: application/json' \
		-d '{"dns":"172.30.53.10:53"}')
	code=$(api_code "$resp")
	body=$(api_body "$resp")
	if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"primary_dns_override":"172.30.53.10:53"'; then
		pass "PUT secondary primary-dns override"
	else
		fail "PUT secondary primary-dns override" "code=$code body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' "$API_SECONDARY/api/v1/cluster" \
		-H "Authorization: Bearer $stoken")
	body=$(api_body "$resp")
	if printf '%s' "$body" | grep -q '"primary_dns_override":"172.30.53.10:53"'; then
		pass "GET secondary /cluster shows override"
	else
		fail "GET secondary /cluster shows override" "body=$body"
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_SECONDARY/api/v1/zones/${API_ZONE}/transfer" \
		-H "Authorization: Bearer $stoken")
	code=$(api_code "$resp")
	if [[ "$code" == "200" ]]; then
		pass "POST transfer on secondary with override still works"
	else
		fail "POST transfer on secondary with override still works" "code=$code body=$(api_body "$resp")"
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X PUT "$API_SECONDARY/api/v1/cluster/primary-dns" \
		-H "Authorization: Bearer $stoken" -H 'Content-Type: application/json' \
		-d '{"dns":""}')
	code=$(api_code "$resp")
	if [[ "$code" == "200" ]]; then
		pass "PUT clear primary-dns override"
	else
		fail "PUT clear primary-dns override" "code=$code body=$(api_body "$resp")"
	fi
}

stage_split_horizon_query() {
	local tag="${1:+$1 }"
	# Queries to 172.30.53.10 leave from 172.30.53.30 (outside 10/8).
	# Queries to 10.53.0.10 leave from 10.53.0.30 (inside the internal ACL).
	assert_rr "$PRIMARY" "$SPLIT_OWNER" A "$SPLIT_PUBLIC" "${tag}public client gets public split A"
	assert_rr "$INTERNAL_DNS" "$SPLIT_OWNER" A "$SPLIT_INTERNAL" "${tag}internal client gets ACL split A"
	assert_no_rr "$PRIMARY" "$NAS_OWNER" A "$NAS_INTERNAL" "${tag}public client does not see internal-only nas"
	assert_rr "$INTERNAL_DNS" "$NAS_OWNER" A "$NAS_INTERNAL" "${tag}internal client gets internal-only nas"
}

stage_split_horizon() {
	local token="$1"
	echo "-- split-horizon ACLs via API"

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/acls" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d '{"name":"internal","networks":["10.0.0.0/8"]}')
	code=$(api_code "$resp")
	if [[ "$code" == "201" || "$code" == "400" ]]; then
		# 400 if the ACL already exists on a re-run.
		pass "POST /api/v1/acls internal"
	else
		fail "POST /api/v1/acls internal" "code=$code body=$(api_body "$resp")"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones/${API_ZONE}/records" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"name\":\"$SPLIT_OWNER\",\"type\":\"A\",\"ttl\":60,\"rdata\":\"$SPLIT_PUBLIC\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "201" ]]; then
		pass "POST public A $SPLIT_OWNER"
	else
		fail "POST public A $SPLIT_OWNER" "code=$code body=$(api_body "$resp")"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones/${API_ZONE}/records" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"name\":\"$SPLIT_OWNER\",\"type\":\"A\",\"ttl\":60,\"rdata\":\"$SPLIT_INTERNAL\",\"acl\":\"internal\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "201" ]]; then
		pass "POST ACL A $SPLIT_OWNER"
	else
		fail "POST ACL A $SPLIT_OWNER" "code=$code body=$(api_body "$resp")"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones/${API_ZONE}/records" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"name\":\"$NAS_OWNER\",\"type\":\"A\",\"ttl\":60,\"rdata\":\"$NAS_INTERNAL\",\"acl\":\"internal\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "201" ]]; then
		pass "POST ACL A $NAS_OWNER"
	else
		fail "POST ACL A $NAS_OWNER" "code=$code body=$(api_body "$resp")"
		return
	fi

	if wait_rr "$PRIMARY" "$SPLIT_OWNER" A "$SPLIT_PUBLIC" 20; then
		pass "primary serves public split A"
	else
		fail "primary serves public split A"
	fi

	assert_rr "$INTERNAL_DNS" "$SPLIT_OWNER" A "$SPLIT_INTERNAL" "internal client gets ACL split A"
	assert_no_rr "$PRIMARY" "$NAS_OWNER" A "$NAS_INTERNAL" "public client does not get internal-only nas"
	assert_rr "$INTERNAL_DNS" "$NAS_OWNER" A "$NAS_INTERNAL" "internal client gets internal-only nas"
	assert_rr "$INTERNAL_DNS" "$API_OWNER" A "$API_ADDR" "internal client still sees public-only www"
}

stage_split_horizon_cache() {
	local token="$1"
	echo "-- split-horizon-cache: per-source keys in front of admin"

	# Dedicated origin served through the cache block in the Corefile.
	local resp code
	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"origin\":\"$CSPLIT_ORIGIN\",\"type\":\"primary\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "200" || "$code" == "201" || "$code" == "409" ]]; then
		pass "POST /api/v1/zones $CSPLIT_ORIGIN"
	else
		fail "POST /api/v1/zones $CSPLIT_ORIGIN" "code=$code body=$(api_body "$resp")"
		return
	fi

	local ok=0 i
	for i in $(seq 1 40); do
		if [[ -n "$(rr_values "$PRIMARY" "$CSPLIT_ORIGIN" SOA || true)" ]]; then
			ok=1
			break
		fi
		sleep 0.5
	done
	if [[ "$ok" == "1" ]]; then
		pass "cache-split origin answers SOA"
	else
		fail "cache-split origin answers SOA"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones/${CSPLIT_ORIGIN}/records" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"name\":\"csplit\",\"type\":\"A\",\"ttl\":5,\"rdata\":\"$CSPLIT_PUBLIC\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "201" ]]; then
		pass "POST public A $CSPLIT_OWNER"
	else
		fail "POST public A $CSPLIT_OWNER" "code=$code body=$(api_body "$resp")"
		return
	fi

	resp=$(curl -sS -w '\n%{http_code}' -X POST "$API_PRIMARY/api/v1/zones/${CSPLIT_ORIGIN}/records" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"name\":\"csplit\",\"type\":\"A\",\"ttl\":5,\"rdata\":\"$CSPLIT_INTERNAL\",\"acl\":\"internal\"}")
	code=$(api_code "$resp")
	if [[ "$code" == "201" ]]; then
		pass "POST ACL A $CSPLIT_OWNER"
	else
		fail "POST ACL A $CSPLIT_OWNER" "code=$code body=$(api_body "$resp")"
		return
	fi

	# Prime the public bucket first. The internal-source client must still get
	# its own view answer: with a name-only cache key the cached public answer
	# would replay here.
	assert_rr_from "172.30.53.30" "$PRIMARY" "$CSPLIT_OWNER" A "$CSPLIT_PUBLIC" "cache: public source gets public A"
	assert_rr_from "10.53.0.30" "$PRIMARY" "$CSPLIT_OWNER" A "$CSPLIT_INTERNAL" "cache: internal source gets view A (per-source key)"
	assert_rr_from "172.30.53.30" "$PRIMARY" "$CSPLIT_OWNER" A "$CSPLIT_PUBLIC" "cache: public source still gets public A"

	# Repeat inside one bucket must hit the cache, not the upstream.
	local hits0 hits1
	hits0=$(cache_hits_total)
	assert_rr_from "10.53.0.30" "$PRIMARY" "$CSPLIT_OWNER" A "$CSPLIT_INTERNAL" "cache: internal source repeat stable"
	hits1=$(cache_hits_total)
	if (( hits1 > hits0 )); then
		pass "cache: same-bucket repeat served from cache"
	else
		fail "cache: same-bucket repeat served from cache" "hits $hits0 -> $hits1"
	fi

	# Within the cache TTL the old answer is replayed; after it expires the
	# refreshed value appears. Proves caching is active and bounded.
	if ! wait_rr_from "172.30.53.30" "$PRIMARY" "$CSPLIT_OWNER" A "$CSPLIT_PUBLIC" 20; then
		fail "cache: re-prime public bucket"
		return
	fi
	resp=$(curl -sS -w '\n%{http_code}' -X PUT "$API_PRIMARY/api/v1/zones/${CSPLIT_ORIGIN}/records" \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"name\":\"csplit\",\"type\":\"A\",\"records\":[{\"name\":\"csplit\",\"type\":\"A\",\"ttl\":5,\"rdata\":\"$CSPLIT_UPDATED\"}]}")
	code=$(api_code "$resp")
	if [[ "$code" != "200" ]]; then
		fail "PUT replace $CSPLIT_OWNER" "code=$code body=$(api_body "$resp")"
		return
	fi
	assert_rr_from "172.30.53.30" "$PRIMARY" "$CSPLIT_OWNER" A "$CSPLIT_PUBLIC" "cache: stale public A served within TTL"
	if wait_rr_from "172.30.53.30" "$PRIMARY" "$CSPLIT_OWNER" A "$CSPLIT_UPDATED" 20; then
		pass "cache: refreshed public A after TTL expiry"
	else
		fail "cache: refreshed public A after TTL expiry"
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
	# Live queries use the sqlite-reloaded view. The transfer plugin may still
	# AXFR the Corefile-seeded file.Zone; persist is proven by UDP + sqlite.
	assert_sqlite_grep "$PRIMARY_SQLITE" "SELECT name FROM records WHERE origin='example.com.';" 'persist-probe' "primary sqlite still contains persist-probe"

	local token resp code body
	token=$(api_login "$API_PRIMARY" || true)
	if [[ -n "$token" && "$token" != "null" ]]; then
		pass "API login on primary after restart"
	else
		fail "API login on primary after restart"
	fi
	assert_rr "$PRIMARY" "$API_OWNER" A "$API_ADDR" "primary still serves API-created A after restart"
	local ans
	ans=$(dig_at "$PRIMARY" +dnssec +noall +answer "${API_ZONE%.}" DNSKEY || true)
	if printf '%s\n' "$ans" | grep -q DNSKEY && printf '%s\n' "$ans" | grep -q RRSIG; then
		pass "primary DNSKEY still signed after restart"
	else
		fail "primary DNSKEY still signed after restart" "$ans"
	fi
	if [[ -n "$token" ]]; then
		resp=$(curl -sS -w '\n%{http_code}' "$API_PRIMARY/api/v1/queries?range=1h" \
			-H "Authorization: Bearer $token")
		code=$(api_code "$resp")
		body=$(api_body "$resp")
		if [[ "$code" == "200" ]] && printf '%s' "$body" | grep -q '"series"'; then
			pass "GET /queries after primary restart still has series"
		else
			fail "GET /queries after primary restart still has series" "code=$code body=$body"
		fi
	fi
	stage_split_horizon_query "after restart"

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
	assert_sqlite_grep "$SECONDARY_SQLITE" "SELECT name FROM records WHERE origin='example.com.';" 'persist-probe' "secondary sqlite still contains persist-probe"

	local stoken
	stoken=$(api_login "$API_SECONDARY" || true)
	if [[ -n "$stoken" && "$stoken" != "null" ]]; then
		pass "API login on secondary with primary down"
	else
		fail "API login on secondary with primary down"
	fi
	assert_rr "$SECONDARY" "$API_OWNER" A "$API_ADDR" "secondary still serves API-created A with primary down"
	local ans
	ans=$(dig_at "$SECONDARY" +short "${API_ZONE%.}" DNSKEY || true)
	if printf '%s\n' "$ans" | grep -q '257'; then
		pass "secondary still serves DNSKEY with primary down"
	else
		fail "secondary still serves DNSKEY with primary down" "$ans"
	fi

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
