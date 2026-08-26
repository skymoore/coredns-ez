#!/usr/bin/env bash
# Seed and dynamic RR catalogs for the integration suite.
# Columns, pipe-separated: owner|TYPE|nsupdate-rdata|dig-grep
# owner is relative to $ZONE (empty owner = apex).

seed_rows() {
	cat <<'EOF'
www|A|192.0.2.80|192.0.2.80
www|AAAA|2001:db8::80|2001:db8::80
mail|A|192.0.2.25|192.0.2.25
ns1|A|172.30.53.10|172.30.53.10
ns2|A|172.30.53.20|172.30.53.20
|A|172.30.53.10|172.30.53.10
|AAAA|2001:db8::10|2001:db8::10
|MX|10 mail.example.com.|10 mail.example.com.
|TXT|"seed-apex-txt"|"seed-apex-txt"
|CAA|0 issue "letsencrypt.org"|letsencrypt
st-txt|TXT|"static-txt"|"static-txt"
st-aaaa|AAAA|2001:db8::aa|2001:db8::aa
st-mx|MX|20 backup.example.com.|20 backup.example.com.
st-srv|SRV|0 1 9 st-target.example.com.|st-target.example.com.
st-cname|CNAME|www.example.com.|www.example.com.
st-ptr|PTR|www.example.com.|www.example.com.
st-caa|CAA|0 iodef "mailto:hostmaster.example.com"|iodef
st-sshfp|SSHFP|1 1 0123456789abcdef0123456789abcdef01234567|0123456789abcdef
st-tlsa|TLSA|3 1 1 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef|0123456789abcdef
st-ds|DS|12345 13 2 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef|12345
st-naptr|NAPTR|10 10 "u" "E2U+sip" "!^.*$!sip:info@example.com!" .|E2U
st-uri|URI|10 1 "https://static.example.com/"|static.example.com
st-hinfo|HINFO|"AMD64" "LINUX"|AMD64
st-rp|RP|hostmaster.example.com. txt.example.com.|hostmaster.example.com.
st-loc|LOC|37 23 30.000 N 121 59 19.000 W 10.00m|37
st-https|HTTPS|1 . alpn="h2"|alpn
st-svcb|SVCB|1 svc.example.com. alpn="h2"|svc.example.com
st-eui48|EUI48|01-23-45-67-89-ab|01-23-45-67-89-ab
st-eui64|EUI64|01-23-45-67-89-ab-cd-ef|01-23-45-67-89-ab-cd-ef
st-afsdb|AFSDB|1 afs.example.com.|afs.example.com.
st-kx|KX|10 kx.example.com.|kx.example.com.
st-deleg|NS|ns.st-deleg.example.com.|ns.st-deleg.example.com.
ns.st-deleg|A|192.0.2.53|192.0.2.53
EOF
}

# One atomic RFC 2136 UPDATE adds every row; a later UPDATE deletes them.
dyn_rows() {
	cat <<'EOF'
it-a|A|192.0.2.50|192.0.2.50
it-aaaa|AAAA|2001:db8::50|2001:db8::50
it-txt|TXT|"dyn-txt"|"dyn-txt"
it-mx|MX|15 mx.it.example.com.|15 mx.it.example.com.
it-cname|CNAME|www.example.com.|www.example.com.
it-ptr|PTR|www.example.com.|www.example.com.
it-srv|SRV|1 2 443 svc.example.com.|443 svc.example.com.
it-caa|CAA|0 issue "letsencrypt.org"|letsencrypt
it-sshfp|SSHFP|1 1 0123456789abcdef0123456789abcdef01234567|0123456789abcdef
it-tlsa|TLSA|3 1 1 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef|0123456789abcdef
it-ds|DS|23456 13 2 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef|23456
it-naptr|NAPTR|20 10 "u" "E2U+sip" "!^.*$!sip:ixfr@example.com!" .|ixfr@example.com
it-uri|URI|10 1 "https://ixfr.example.com/path"|ixfr.example.com
it-hinfo|HINFO|"ARM64" "LINUX"|ARM64
it-rp|RP|hostmaster.example.com. rp.example.com.|rp.example.com.
it-loc|LOC|51 30 0.000 N 0 7 0.000 W 0.00m|51
it-https|HTTPS|1 . alpn=h2|alpn
it-svcb|SVCB|1 svc.example.com. alpn=h2|svc.example.com
it-eui48|EUI48|aa-bb-cc-dd-ee-ff|aa-bb-cc-dd-ee-ff
it-eui64|EUI64|aa-bb-cc-dd-ee-ff-00-11|aa-bb-cc-dd-ee-ff-00-11
it-afsdb|AFSDB|1 afs.it.example.com.|afs.it.example.com.
it-kx|KX|10 kx.it.example.com.|kx.it.example.com.
it-deleg|NS|ns.it-deleg.example.com.|ns.it-deleg.example.com.
ns.it-deleg|A|192.0.2.54|192.0.2.54
it-cds|CDS|34567 13 2 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef|34567
it-smimea|SMIMEA|3 1 1 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef|0123456789abcdef
it-cert|CERT|1 0 0 AA==|AA==
_acme-challenge|TXT|"integration-token"|"integration-token"
EOF
}

fqdn() {
	local owner="$1"
	if [[ -z "$owner" ]]; then
		printf '%s' "$ZONE"
	else
		printf '%s.%s' "$owner" "$ZONE"
	fi
}
