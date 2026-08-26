# Deploy

This is **skymoore/coredns-ez**, an independent CoreDNS build with an admin
UI. It is not a CNCF or official CoreDNS release. See [NOTICE](../NOTICE) and
[SECURITY.md](../SECURITY.md).

Install paths: [Docker](#docker), [Alpine](#alpine), [Debian/Ubuntu](#debianubuntu).
TLS, backup, and upgrades are below.

## Listen

| Port | Default | Notes |
|---|---|---|
| 53 UDP/TCP | DNS | Authoritative for zones created in the UI. Recursion is off unless you add a private `view` + `forward`. |
| 8080 TCP | Admin UI + `/api/v1` | Plain HTTP. LAN or loopback only. Cookie is not `Secure` on HTTP. |
| 443 TCP | DoH + UI | After you add `tls` (see [TLS](#tls)). `/dns-query` stays DoH. |
| 9153 TCP | Prometheus | Default Corefile binds **127.0.0.1:9153**. Scrape from the host namespace or `docker exec`. |

## Docker

```
docker run --rm \
  -p 53:53/udp -p 53:53/tcp -p 8080:8080 \
  -e COREDNS_ADMIN_BOOTSTRAP_PASSWORD=changeme \
  -v coredns-data:/var/lib/coredns \
  ghcr.io/skymoore/coredns-ez:v1.14.7
```

UI: `http://127.0.0.1:8080` user `admin`. Recursion and public AXFR are off.
If host :53 is taken, map `5353:53`. Persist `/var/lib/coredns`. After the first
GHCR push, set the package visibility to **public** in GitHub. The image is
`ghcr.io/skymoore/coredns-ez` (GitHub repo rename does not alias the old
`coredns-plugins` package).

From a clone: `docker compose up --build`. Metrics are not published to the
host; add `9153:9153` if you need them.

Cluster (fixed IPs, because `transfer { to }` is IP-only): see
[docker-compose.cluster.yml](../docker-compose.cluster.yml). Start the primary,
mint a join key in Cluster, then start the secondary with `COREDNS_JOIN_TOKEN`.

## Alpine

```
curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-ez/main/scripts/install-alpine.sh | sudo sh
```

OpenRC + Unbound on :5353 (private clients only). CoreDNS :53 serves admin
zones to everyone and recurses **only** for private client IPs (`view lan`).
Existing Corefile / unbound.conf / `/etc/conf.d/coredns` are left alone. Re-run
after every binary upgrade so `cap_net_bind_service` is restored.

## Debian/Ubuntu

```
curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-ez/main/scripts/install-systemd.sh | sudo sh
```

Authoritative + UI :8080, no recursion. Optional:

```
curl -fsSL …/scripts/install-systemd.sh | sudo UNBOUND=1 START=1 sh
```

## TLS

Two recipes. Do not put plain `:8080` on a public address.

**CoreDNS `tls` (DoH and UI on 443)**

```
https://.:443 {
	tls /etc/coredns/tls/cert.pem /etc/coredns/tls/key.pem
	prometheus 127.0.0.1:9153
	errors
	log
	admin {
		db /var/lib/coredns/admin.sqlite
		data /var/lib/coredns/zones
		role primary
		bootstrap_admin admin
	}
}
```

See [docker/Corefile.tls](../docker/Corefile.tls). The session cookie becomes
`Secure` because CoreDNS sees TLS.

**Reverse proxy in front of :8080**

Bind CoreDNS HTTP to localhost and terminate TLS on Caddy/nginx. Example:
[docker/Caddyfile](../docker/Caddyfile). The cookie stays non-`Secure` unless
CoreDNS itself is on TLS; keep 8080 on loopback.

This build does not run ACME.

## Backup

Stop CoreDNS, or checkpoint sqlite, then copy **both** trees.

| What | Docker / default install |
|---|---|
| Identity (users, tokens, TSIG, ACLs, filters, cluster, zone inventory) | `/var/lib/coredns/admin.sqlite` and `-wal`/`-shm` if present |
| Zone files and IXFR journals | `/var/lib/coredns/zones/` (host install: Corefile `data`) |

```
sqlite3 /var/lib/coredns/admin.sqlite 'PRAGMA wal_checkpoint(TRUNCATE);'
```

Restore: stop, replace both trees, start. Copying sqlite without zone files (or
the reverse) splits inventory from masters. Filter lists live in sqlite; record
RRs do not.

## Upgrade

Tags follow **upstream CoreDNS** (`v1.14.7`). Plugins and UI ride along. Sqlite
schema changes are additive.

- Docker: `docker pull ghcr.io/skymoore/coredns-ez:<tag>` and recreate the
  container with the **same volume**.
- Host: re-run the installer. It restores `cap_net_bind_service` and does not
  overwrite Corefile, unbound.conf, or the unit file.

A new CoreDNS minor is released only if `patches/coredns-http-handler.patch`
applies. linux/mips and linux/mips64le are omitted (`modernc.org/sqlite`). Do
not point a new binary at a `db`/`data` pair from a different node.

## AXFR

`transfer { to 127.0.0.1 }` is in the default Corefile so the plugin exists.
Add secondary IPs under Cluster → Zone transfer (IP or IP:port, no CIDR, never
`*`). Cluster join appends the secondary’s DNS address. Notify is sent only to
those IPs.
