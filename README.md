# coredns-plugins

Out-of-tree [CoreDNS](https://coredns.io) plugins, plus **GitHub Releases that are full CoreDNS binaries** with those plugins compiled in.

The interesting one is **admin**: a management-plane plugin that multiplexes onto CoreDNS's HTTPS / DoH listener. `/dns-query` stays DoH. `/` is an embedded operator UI (CoreDNS branding, zones, records, ACLs, cluster, users). `/api/v1` is the JSON API. Auth is local users, API tokens, and optional OIDC. Identity lives in SQLite; zone data stays in RFC 1035 master files.

## Releases

[Releases](https://github.com/skymoore/coredns-plugins/releases) are not plugin tarballs. Each tag matching an upstream CoreDNS version (for example `v1.14.7`) is CoreDNS rebuilt with:

- every default CoreDNS plugin
- `admin` (UI embedded at build time, OIDC capable)
- `dns-update-persistent`
- `ixfr`
- `secondary-persistent`
- `patches/coredns-http-handler.patch` so the DoH listener can serve the UI and API

Archives use the same names and platform matrix as [coredns/coredns](https://github.com/coredns/coredns/releases) (`coredns_<version>_<os>_<arch>.tgz`, plus `.zip` on Windows). Swap the binary in and add an `admin` block to the Corefile; see [admin/README.md](admin/README.md).

The Release workflow (`.github/workflows/release.yml`) builds the SPA, injects the plugins, and publishes on `workflow_dispatch` or when a new upstream CoreDNS release appears.

## admin

HTTPS management plane on the DoH listener. Runtime primary/secondary zones, split-horizon ACLs, OIDC + password + bearer auth, SQLite identity, and cluster join so a secondary accepts the same credentials as the primary. See [admin/README.md](admin/README.md).

Requires the HTTPHandler patch on the CoreDNS tree (the release script and the integration image apply it). Build the UI with `make ui` before compiling CoreDNS yourself.

```
file:file
dns-update-persistent:github.com/skymoore/coredns-plugins/dns-update-persistent
ixfr:github.com/skymoore/coredns-plugins/ixfr
admin:github.com/skymoore/coredns-plugins/admin
secondary:secondary
secondary-persistent:github.com/skymoore/coredns-plugins/secondary-persistent
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
