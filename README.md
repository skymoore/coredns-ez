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
