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

## Host install

```
curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-ez/main/scripts/install.sh | sudo sh
```

Alpine (OpenRC) or Debian/Ubuntu (systemd). Recursion is on for Alpine, off
on Debian/Ubuntu unless `UNBOUND=1`. Pin and start:

```
curl -fsSL …/scripts/install.sh | sudo START=1 VERSION=v1.14.7 UNBOUND=1 sh
```

First start writes `COREDNS_ADMIN_BOOTSTRAP_PASSWORD` if unset and prints the
admin UI URL, username `admin`, and that password (Alpine: `/etc/conf.d/coredns`
with `export`; systemd: `/etc/default/coredns`).
The Corefile `transfer` block must be multi-line; `transfer { to 127.0.0.1 }`
on one line is parsed as an extra address `}` and CoreDNS exits 1.

Re-run to replace `/var/lib/coredns/coredns` and restore `cap_net_bind_service`.
If CoreDNS is already running it is restarted. Corefile and unbound.conf are
not overwritten. The unit/OpenRC file is rewritten if it still launches
`/usr/local/bin/coredns` or does not supervise a clean exit — that layout is
what Settings → Backup and Settings → Update need (`coredns` owns the data
dir and can replace the binary; systemd `Restart=always` / OpenRC
`supervise-daemon` bring `cap_net_bind_service` back).

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

Settings → Backup (operator+) downloads a zip of sqlite, `zones/`, Corefile,
and `tls/` beside the Corefile. The installer makes `/etc/coredns` and
`/var/lib/coredns` owned by `coredns` so that path is readable at runtime.

Stop CoreDNS, or checkpoint sqlite, then copy **both** trees.

| What | Docker / default install |
|---|---|
| Identity (users, tokens, TSIG, ACLs, filters, cluster, zone inventory) | `/var/lib/coredns/admin.sqlite` and `-wal`/`-shm` if present |
| Zone files and IXFR journals | `/var/lib/coredns/zones/` (host install: Corefile `data`) |
| Corefile and TLS material | `/etc/coredns/Corefile`, `/etc/coredns/tls/` |
| Binary (host install) | `/var/lib/coredns/coredns` (`/usr/local/bin/coredns` is a symlink) |

```
sqlite3 /var/lib/coredns/admin.sqlite 'PRAGMA wal_checkpoint(TRUNCATE);'
```

Restore: stop, replace both trees, start. Copying sqlite without zone files (or
the reverse) splits inventory from masters. Filter lists live in sqlite; record
RRs do not.

## Upgrade

Tags follow **upstream CoreDNS** (`v1.14.7`). Plugins and UI ride along. Sqlite
schema changes are additive.

- Settings → Update (admin, linux): downloads the latest GitHub release,
  checks the sha256, writes `/var/lib/coredns/coredns`, and exits so the
  supervisor restarts with `cap_net_bind_service`. The UI offers a backup
  first. Re-run `install.sh` once on any host that still has the binary in
  `/usr/local/bin` — that directory is not writable by `coredns`.
- Docker: `docker pull ghcr.io/skymoore/coredns-ez:<tag>` and recreate the
  container with the **same volume**. In-UI update rewrites `/usr/bin/coredns`
  in the container layer (root); prefer a pull. Use `--restart unless-stopped`
  (compose already does).
- Host: re-run `scripts/install.sh` (same curl line as install). It replaces
  the binary, restores `cap_net_bind_service`, refreshes the unit if needed,
  and restarts CoreDNS if it is already running. Corefile and unbound.conf
  stay put.

A new CoreDNS minor is released only if `patches/coredns-http-handler.patch`
applies. linux/mips and linux/mips64le are omitted (`modernc.org/sqlite`). Do
not point a new binary at a `db`/`data` pair from a different node.

## AXFR

`transfer { to 127.0.0.1 }` is in the default Corefile so the plugin exists.
Add secondary IPs under Cluster → Zone transfer (IP or IP:port, no CIDR, never
`*`). Cluster join appends the secondary’s DNS address. Notify is sent only to
those IPs.
