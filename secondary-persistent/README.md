# secondary-persistent

## Name

*secondary-persistent* - transfers a zone from a primary, serves it, and writes it to disk.

## Description

*secondary-persistent* is an out-of-tree replacement for *secondary* that keeps a durable RFC 1035
master-file copy of each transferred zone. On startup it loads any existing file so CoreDNS can
answer immediately, then SOA-checks the primary (RFC 1982) and refreshes only when the remote
serial is newer.

Inbound transfers use IXFR when a local serial exists, and fall back to AXFR if IXFR fails, is
refused, is up-to-date despite a newer remote serial, or is an AXFR-style response. Outbound
transfers still go through the *transfer* plugin (IXFR fallback there remains a single SOA or a
full zone).

RFC 9432 catalog zones are supported with the same member-zone and `coo` behavior as *secondary*.
Member zones are also written under the configured directory.

When this plugin is omitted, CoreDNS can still run pure in-memory secondaries with *secondary*.
Do not configure both plugins for the same origin.

The expire timer restarts at process start (master files have no transfer timestamp).

## Syntax

~~~
secondary-persistent [ZONES...] {
    transfer from ADDRESS [ADDRESS...]
    persist PATH | directory DIR
    catalog [MEMBER-ZONES...]
    fallthrough [ZONES...]
}
~~~

* `transfer from` specifies from which **ADDRESS** to fetch the zone. It can be specified multiple
  times; if one does not work, another will be tried.
* `persist` **PATH** writes a single origin to that file. Relative paths are joined with *root*.
  Requires exactly one origin.
* `directory` **DIR** writes each origin to `DIR/db.<origin>` (trailing dot stripped). Required
  when `catalog` is used or when more than one origin is configured.
* `catalog` treats the transferred zone as an RFC 9432 catalog zone. Optional **MEMBER-ZONES**
  restrict which member zone names are accepted. Members are persisted as `DIR/db.<member>`.
* `fallthrough` If a query for a record in the zone results in NXDOMAIN, the query is passed to
  the next plugin.

Exactly one of `persist` or `directory` is required.

## Examples

Transfer `example.org` and persist it:

~~~ corefile
example.org {
    secondary-persistent {
        transfer from 10.0.1.1 10.1.2.1
        persist /var/lib/coredns/db.example.org
    }
    transfer {
        to *
    }
}
~~~

Catalog consumer with member zones on disk:

~~~ corefile
catalog.example {
    secondary-persistent {
        transfer from 10.1.2.1
        catalog example.org internal.example
        directory /var/lib/coredns/secondary
    }
}
~~~

## Metrics

If monitoring is enabled (via the *prometheus* plugin) the following metrics are exported:

* `coredns_secondary_persistent_load_total{zone, status}` - persist-file load outcomes (`ok`, `missing`, `error`)
* `coredns_secondary_persistent_transfer_total{zone, type, status}` - inbound transfers (`type`=`axfr`/`ixfr`, `status`=`ok`/`error`/`fallback`)
* `coredns_secondary_persistent_writes_total{zone, status}` - atomic persist writes
* `coredns_secondary_persistent_write_duration_seconds{zone}` - persist write+rename duration
* `coredns_secondary_persistent_serial{zone}` - last persisted SOA serial

## Bugs

Only NSEC DNSSEC is supported (same as *file*). NSEC3 records are dropped.
TSIG is not set on inbound transfers.
UDP IXFR is not used; transfers are TCP.

## See Also

See the *secondary* and *transfer* plugins. RFC 5936 (AXFR), RFC 1995 (IXFR), RFC 1982 (serial
arithmetic), RFC 9432 (catalog zones), RFC 1035 (master files).
