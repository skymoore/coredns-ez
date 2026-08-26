$ORIGIN example.com.
$TTL 60
@	IN	SOA	ns1.example.com. hostmaster.example.com. (
			1	; serial
			30	; refresh — short so the secondary retries quickly if NOTIFY is missed
			15	; retry
			600	; expire
			30	; minimum
			)
	IN	NS	ns1.example.com.
	IN	NS	ns2.example.com.
	IN	A	172.30.53.10
	IN	AAAA	2001:db8::10
	IN	MX	10 mail.example.com.
	IN	TXT	"seed-apex-txt"
	IN	CAA	0 issue "letsencrypt.org"
ns1	IN	A	172.30.53.10
ns2	IN	A	172.30.53.20
www	IN	A	192.0.2.80
www	IN	AAAA	2001:db8::80
mail	IN	A	192.0.2.25
; Static records that UPDATEs must not put into an IXFR delta.
st-txt		IN	TXT	"static-txt"
st-aaaa		IN	AAAA	2001:db8::aa
st-mx		IN	MX	20 backup.example.com.
st-srv		IN	SRV	0 1 9 st-target.example.com.
st-cname	IN	CNAME	www.example.com.
st-ptr		IN	PTR	www.example.com.
st-caa		IN	CAA	0 iodef "mailto:hostmaster.example.com"
st-sshfp	IN	SSHFP	1 1 0123456789abcdef0123456789abcdef01234567
st-tlsa		IN	TLSA	3 1 1 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
st-ds		IN	DS	12345 13 2 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
st-naptr	IN	NAPTR	10 10 "u" "E2U+sip" "!^.*$!sip:info@example.com!" .
st-uri		IN	URI	10 1 "https://static.example.com/"
st-hinfo	IN	HINFO	"AMD64" "LINUX"
st-rp		IN	RP	hostmaster.example.com. txt.example.com.
st-loc		IN	LOC	37 23 30.000 N 121 59 19.000 W 10.00m
st-https	IN	HTTPS	1 . alpn="h2"
st-svcb		IN	SVCB	1 svc.example.com. alpn="h2"
st-eui48	IN	EUI48	01-23-45-67-89-ab
st-eui64	IN	EUI64	01-23-45-67-89-ab-cd-ef
st-afsdb	IN	AFSDB	1 afs.example.com.
st-kx		IN	KX	10 kx.example.com.
st-deleg	IN	NS	ns.st-deleg.example.com.
ns.st-deleg	IN	A	192.0.2.53
