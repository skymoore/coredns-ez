# coredns-ez

Independent [CoreDNS](https://coredns.io) build with extra plugins and an embedded operator UI. **Not a CNCF or official CoreDNS product.** Logos in the UI are for the CoreDNS binary this repo produces. See [NOTICE](NOTICE) and [LICENSE](LICENSE) (Apache-2.0). GitHub redirects the former `coredns-plugins` repo name; GHCR images are `ghcr.io/skymoore/coredns-ez`.

[Releases](https://github.com/skymoore/coredns-ez/releases) are full CoreDNS binaries (same archive names as upstream) plus an Alpine image on GHCR. How to run, TLS, backup, and upgrades: [docs/deploy.md](docs/deploy.md). Hardening notes: [SECURITY.md](SECURITY.md).

The admin plugin multiplexes onto CoreDNS's HTTPS / DoH listener. `/dns-query` stays DoH. `/` is the operator UI (zones, records, ACLs, filters, cluster, users). `/api/v1` is the JSON API. Auth is local users, API tokens, and optional OIDC. Identity lives in SQLite; zone data stays in RFC 1035 master files.

## Releases

Each tag matching an upstream CoreDNS version (for example `v1.14.7`) is CoreDNS rebuilt with:

- every default CoreDNS plugin
- `admin` (UI embedded at build time, OIDC capable)
- `dns-update-persistent`
- `ixfr`
- `secondary-persistent`
- `patches/coredns-http-handler.patch` so the DoH listener can serve the UI and API

Archives use the same names as [coredns/coredns](https://github.com/coredns/coredns/releases) (`coredns_<version>_<os>_<arch>.tgz`, plus `.zip` on Windows). Platforms match upstream except **linux/mips** and **linux/mips64le**: admin stores identity in `modernc.org/sqlite`, which has no port there.

### Host (Alpine OpenRC or Debian/Ubuntu systemd)

```
curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-ez/main/scripts/install.sh | sudo sh
```

Pin a version and start now:

```
curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-ez/main/scripts/install.sh | sudo START=1 VERSION=v1.14.7 sh
```

Detects Alpine vs Debian/Ubuntu. Recursion (Unbound :5353, private clients only) is on for Alpine and off for Debian/Ubuntu unless `UNBOUND=1`. UI: `http://<host>:8080` user `admin`. Set `COREDNS_ADMIN_BOOTSTRAP_PASSWORD` in `/etc/conf.d/coredns` (Alpine, with `export`) or `/etc/default/coredns` (systemd) before the first start.

Re-run the same command to **update**: replaces `/var/lib/coredns/coredns`, restores `cap_net_bind_service`, and restarts CoreDNS if it is already running. Corefile and unbound.conf stay put. The OpenRC/systemd unit is refreshed if it still points at `/usr/local/bin` so Settings → Backup and Settings → Update can run as the `coredns` user.

### Docker

```
docker run --rm \
  -p 53:53/udp -p 53:53/tcp -p 8080:8080 \
  -e COREDNS_ADMIN_BOOTSTRAP_PASSWORD=changeme \
  -v coredns-data:/var/lib/coredns \
  ghcr.io/skymoore/coredns-ez:v1.14.7
```

From a clone: `docker compose up --build`. Recursion off, AXFR localhost-only, Prometheus on `127.0.0.1:9153` inside the container. UI `http://127.0.0.1:8080`. If host :53 is taken, map `5353:53`.

Primary + secondary (fixed 172.30.80.0/24):

```
docker compose -f docker-compose.cluster.yml up --build
# mint a join key on http://127.0.0.1:8080  Cluster
COREDNS_JOIN_TOKEN=... docker compose -f docker-compose.cluster.yml --profile cluster up -d
```

The Release workflow builds the SPA, publishes binaries, runs the Docker integration suite, and pushes `linux/amd64` + `linux/arm64` images to GHCR. After the first image push, set the GHCR package to **public**.

## admin

HTTPS management plane on the DoH listener. Runtime primary/secondary zones, split-horizon ACLs, block/allow filters, OIDC + password + bearer auth, SQLite identity, and cluster join. See [admin/README.md](admin/README.md).

Requires the HTTPHandler patch (the release script and the integration image apply it). Build the UI with `make ui` before compiling CoreDNS yourself.

```
file:file
dns-update-persistent:github.com/skymoore/coredns-ez/dns-update-persistent
ixfr:github.com/skymoore/coredns-ez/ixfr
admin:github.com/skymoore/coredns-ez/admin
secondary:secondary
secondary-persistent:github.com/skymoore/coredns-ez/secondary-persistent
```

## dns-update-persistent

RFC 2136 dynamic updates for a single zone, with the master file rewritten in place after every mutating UPDATE. See [dns-update-persistent/README.md](dns-update-persistent/README.md). Do not configure `file`, `auto`, `dynupdate`, `secondary`, or `secondary-persistent` for the same origin.

## ixfr

RFC 1995 incremental transfers for a zone owned by *dns-update-persistent*. Without it, that plugin uses CoreDNS's AXFR-style IXFR fallback. See [ixfr/README.md](ixfr/README.md).

## secondary-persistent

Durable secondary zones: AXFR/IXFR from a primary, RFC 1035 master-file persistence, and catalog-member persistence. See [secondary-persistent/README.md](secondary-persistent/README.md). Do not configure `secondary` and `secondary-persistent` for the same origin.

## Integration tests

`integration-test/` builds CoreDNS v1.14.7 with the plugins above, runs a primary and a secondary in Docker Compose, and checks queries, AXFR, IXFR, RFC 2136 updates, the admin API, split-horizon, and persist-across-restart. See [integration-test/README.md](integration-test/README.md).

```
./integration-test/scripts/run.sh
```
