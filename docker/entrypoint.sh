#!/bin/sh
# Seed a bootstrap password on first start if the operator did not set one.
# Optional cluster join: COREDNS_JOIN_URL + COREDNS_JOIN_TOKEN after health.
set -eu

mkdir -p /var/lib/coredns/zones

if [ ! -f /var/lib/coredns/admin.sqlite ] && [ -z "${COREDNS_ADMIN_BOOTSTRAP_PASSWORD:-}" ]; then
	COREDNS_ADMIN_BOOTSTRAP_PASSWORD="$(dd if=/dev/urandom bs=18 count=1 2>/dev/null | base64 | tr -d '\n/+=' | cut -c1-24)"
	export COREDNS_ADMIN_BOOTSTRAP_PASSWORD
	printf 'generated admin password for user "admin": %s\n' "$COREDNS_ADMIN_BOOTSTRAP_PASSWORD" >&2
	printf 'set COREDNS_ADMIN_BOOTSTRAP_PASSWORD on the next start, or keep the /var/lib/coredns volume.\n' >&2
fi

if [ -z "${COREDNS_JOIN_URL:-}" ] || [ -z "${COREDNS_JOIN_TOKEN:-}" ]; then
	exec /usr/bin/coredns "$@"
fi

/usr/bin/coredns "$@" &
pid=$!
trap 'kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null' INT TERM EXIT

i=0
while [ "$i" -lt 30 ]; do
	if wget -qO- http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1; then
		break
	fi
	i=$((i + 1))
	sleep 1
done

dns="${COREDNS_ADVERTISE_DNS:-}"
name="${COREDNS_NODE_NAME:-}"
payload="{\"url\":\"${COREDNS_JOIN_URL}\",\"token\":\"${COREDNS_JOIN_TOKEN}\""
if [ -n "$dns" ]; then
	payload="${payload},\"dns\":\"${dns}\""
fi
if [ -n "$name" ]; then
	payload="${payload},\"name\":\"${name}\""
fi
payload="${payload}}"
if wget -qO- --header='Content-Type: application/json' --post-data="$payload" \
	http://127.0.0.1:8080/api/v1/cluster/connect >/dev/null 2>&1; then
	printf 'cluster connect ok\n' >&2
else
	printf 'cluster connect failed (already joined, or bad token)\n' >&2
fi

trap - INT TERM EXIT
wait "$pid"
