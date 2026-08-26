#!/bin/sh
# Seed the live zone file on first start. Restarts keep whatever the last
# successful UPDATE (primary) or transfer (secondary) wrote.
set -eu

SEED="${ZONE_SEED:-}"
LIVE="${ZONE_LIVE:-/var/lib/coredns/db.example.com}"

if [ -n "$SEED" ] && [ -f "$SEED" ]; then
	mkdir -p "$(dirname "$LIVE")"
	if [ ! -f "$LIVE" ]; then
		cp "$SEED" "$LIVE"
	fi
fi

exec /usr/bin/coredns "$@"
