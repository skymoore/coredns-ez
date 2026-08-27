# ixfr

## Name

*ixfr* - RFC 1995 incremental zone transfer for a zone owned by *dns-update-persistent*.

## Description

CoreDNS *file* and *auto* accept IXFR queries but, when the serial has moved, they dump the **whole zone** (AXFR fallback). *secondary-persistent* can apply a real delta (`parseIXFR`); it never sees one from those primaries.

*ixfr* keeps a journal of increments produced by *dns-update-persistent* UPDATEs and implements `transfer.Transferer` so the *transfer* plugin serves RFC 1995 streams:

```
SOA(new)
  SOA(old)
    deleted RRs
  SOA(new)
    added RRs
SOA(new)
```

Without this plugin in the server block, *dns-update-persistent* keeps the in-tree fallback so existing Corefiles still boot.

The journal is stored in SQLite (`ixfr_journals`) after each mutating UPDATE. On load, increments that do not chain to the current SOA are dropped. A missing or corrupt journal is not fatal — IXFR history is empty until the next UPDATE (secondaries get AXFR fallback).

`history N` (default 64) bounds retained increments. A secondary older than the oldest retained serial gets AXFR fallback, which RFC 1995 allows.

This plugin does not answer queries. Place it in the same server block as *dns-update-persistent* and *transfer*.

## Syntax

~~~
ixfr [ZONE] {
    history N
    file PATH
}
~~~

* **ZONE** defaults to the server block. Exactly one.
* `history` **N** generations to retain. Default 64. Must be ≥ 1.
* `file` **PATH** accepted and ignored. Journals persist in SQLite.

## Compilation

```
file:file
dns-update-persistent:github.com/skymoore/coredns-ez/dns-update-persistent
ixfr:github.com/skymoore/coredns-ez/ixfr
```

## Examples

~~~ corefile
example.com {
    tsig {
        secret updater.example.com. <base64>
        require_opcode UPDATE
    }
    dns-update-persistent {
        file /var/lib/coredns/db.example.com
        mutable TXT
    }
    ixfr
    transfer {
        to 192.0.2.53
    }
}
~~~

## Metrics

* `coredns_ixfr_transfer_total{zone, type}` — `ixfr`, `axfr`, or `uptodate`
* `coredns_ixfr_serial{zone}` — SOA serial of the journal snapshot

## See Also

RFC 1995. *dns-update-persistent* commits diffs here. *secondary-persistent* consumes the resulting streams.
