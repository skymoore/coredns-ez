# coredns-plugins

Out-of-tree [CoreDNS](https://coredns.io) plugins.

## secondary-persistent

Durable secondary zones: AXFR/IXFR from a primary, RFC 1035 master-file persistence, and catalog-member persistence. See [secondary-persistent/README.md](secondary-persistent/README.md).

Add it to CoreDNS `plugin.cfg` immediately after `secondary`:

```
secondary:secondary
secondary-persistent:github.com/skymoore/coredns-plugins/secondary-persistent
```

Then rebuild CoreDNS (`go generate && go build`). Do not configure `secondary` and `secondary-persistent` for the same origin.

## dns-update-persistent

RFC 2136 dynamic updates for a single zone, with the master file rewritten in place after every mutating UPDATE. See [dns-update-persistent/README.md](dns-update-persistent/README.md).

Add it to CoreDNS `plugin.cfg` immediately after `file`:

```
file:file
dns-update-persistent:github.com/skymoore/coredns-plugins/dns-update-persistent
ixfr:github.com/skymoore/coredns-plugins/ixfr
```

Do not configure `file`, `auto`, `dynupdate`, `secondary`, or `secondary-persistent` for the same origin.

## ixfr

RFC 1995 incremental transfers for a zone owned by *dns-update-persistent*. Without it, that plugin uses CoreDNS's AXFR-style IXFR fallback. See [ixfr/README.md](ixfr/README.md).

## Integration tests

`integration-test/` builds CoreDNS v1.14.7 with both plugins, runs a primary (`dns-update-persistent`) and a secondary (`secondary-persistent`) in Docker Compose, and checks queries, AXFR, IXFR, RFC 2136 updates, and persist-across-restart. See [integration-test/README.md](integration-test/README.md).

```
./integration-test/scripts/run.sh
```
