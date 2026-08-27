# dns-update-persistent

## Name

*dns-update-persistent* - serves a zone, accepts RFC 2136 dynamic updates, and persists the zone to SQLite.

## Description

*dns-update-persistent* implements [RFC 2136](https://www.rfc-editor.org/rfc/rfc2136) DNS UPDATE for a single zone, then atomically replaces that zone's master file after every mutating update. It is the durable counterpart of the in-memory *dynupdate* example: adds appear on disk, deletes disappear, and a restart serves the last successfully committed generation.

CoreDNS's plugin chain does not filter on opcode: an UPDATE's Zone section occupies the question slot and ZCLASS is `IN`, so an UPDATE is dispatched like any query once it is accepted. miekg/dns's default accept function, however, replies NOTIMP to every opcode other than QUERY and NOTIFY *before* that chain runs. This plugin replaces that default so RFC 2136 messages are accepted (exactly one Zone section; prerequisites and updates may be large). It then dispatches on opcode and hands everything else to a `file` view of the same zone.

### This plugin owns the zone

The tempting design — keep dynamically-added records in a side table, fall through to *file* for the rest — is wrong for the deployment this plugin is for. The *transfer* plugin takes the **first** `Transferer` that does not return `ErrNotAuthoritative`; it does not merge. A side table would therefore be invisible to AXFR, so a challenge record would never reach the secondary that the public NS records point at.

Owning the zone also means reads, wildcards, delegation, NODATA proofs and AXFR all come from CoreDNS's own `file.Zone`.

### Durability

RFC 2136 §3.4.2.1 requires that a client never observe half an update. Every change is applied to a **copy** of the record set. Only once all prerequisites have passed, the entire update section has prescanned clean, **and** SQLite has accepted the new generation is a fresh zone swapped in under a write lock.

NOERROR therefore means the new zone is in SQLite. A write failure returns SERVFAIL and leaves both memory and the store on the previous generation.

A no-op UPDATE (identical RR, ignored apex SOA/NS delete, CNAME exclusivity skip) is still NOERROR and does **not** persist or bump the serial.

Rebuilding is O(zone) per update. That is deliberate: this plugin exists for low-rate mutation (ACME and similar), where atomicity is worth far more than update throughput.

A Corefile `file` path is a **one-time seed** for import. Mutations never write that file. *admin* imports leftover zone files into SQLite and strips those server blocks.

### Authentication is the *tsig* plugin's job

*dns-update-persistent* does not verify TSIG and holds no keys. It checks only that verification **happened**, and refuses the update otherwise — a dynamic-update endpoint that answers an unsigned request is an open zone-mutation API. There is deliberately no insecure mode.

Put *tsig* in the same server block with `require_opcode UPDATE`, so unsigned updates are refused before they reach here as well.

### Why `mutable` exists

TSIG authenticates the sender; it cannot say what the sender is allowed to change. A key issued to an ACME client needs to publish TXT records under one name and nothing else, but a bare RFC 2136 grant lets it repoint your A records. `mutable` narrows that to a type list, checked during the prescan so a disallowed type rejects the whole update rather than letting part of it land.

## Syntax

~~~
dns-update-persistent [ZONE] {
    file PATH
    mutable TYPE...
}
~~~

* **ZONE** the zone to serve and accept updates for. Defaults to the server block's zone. Exactly one — two zones in one block would share a lock and a rebuild, so a bad update to either would stall and endanger both.
* `file` **PATH** one-time seed to load at parse. Relative paths are joined with *root*. Parsed through the same code path as the *file* plugin. Not a persist destination.
* `mutable` **TYPE...** restricts updates to these RR types. Omitted, RFC 2136's own rules are the only limit.

## Compilation

Add these lines to CoreDNS `plugin.cfg` immediately after `file`:

```
file:file
dns-update-persistent:github.com/skymoore/coredns-ez/dns-update-persistent
ixfr:github.com/skymoore/coredns-ez/ixfr
```

Then rebuild (`go generate && go build`). Do not configure *file*, *dynupdate*, *secondary*, or *secondary-persistent* for the same origin.

Put *ixfr* in the same server block to serve RFC 1995 incremental IXFR after UPDATEs. Without it, outgoing IXFR is CoreDNS's full-zone fallback (`file.Zone.Transfer`).

## What is implemented

All five prerequisite forms of §3.2, the prescan/apply split of §3.4.1–2, and the update rules that keep a zone a zone:

| Rule | Behaviour |
|---|---|
| Prerequisites | NXDOMAIN / YXDOMAIN / NXRRSET / YXRRSET, including value-dependent RRset equality |
| Out-of-zone name | NOTZONE — a distinct answer from NXDOMAIN; the name may exist, just not here |
| Wrong zone | NOTAUTH — "wrong server", not "wrong credentials" |
| Apex SOA | never deleted; an added SOA applies only if its serial is greater (RFC 1982 arithmetic) |
| Last apex NS | never deleted |
| CNAME exclusivity | enforced both ways, silently ignored rather than rejected, per §3.4.2.3 |
| Identical record re-added | TTL updated, no serial bump if TTL is unchanged, NOERROR |
| Serial | incremented on any real change, so a secondary's serial comparison sees it |
| NOTIFY | sent after a persisted change when *transfer* is configured in the block |
| Persist | SQLite write before NOERROR |

## Known limitations

* **Seed `file` is import-only.** `$INCLUDE` / `$GENERATE` are honored on that first parse; SQLite stores the flattened RRs.
* **No NSEC3.** `file.Zone` rejects NSEC3 records outright, so an NSEC3-signed seed zone will not load.
* **Online signing is the *dnssec* plugin's job.** Place *dns-update-persistent* after it in the chain (the default slot, after `file`, does) so added records are signed on the way out; a bare record served from a signed zone reads as bogus.

## Metrics

* `coredns_dns_update_persistent_updates_total{zone, rcode}` — UPDATE replies
* `coredns_dns_update_persistent_writes_total{zone, status}` — persist attempts (`ok`, `error`, `skipped`)
* `coredns_dns_update_persistent_write_duration_seconds{zone}` — write+rename histogram
* `coredns_dns_update_persistent_serial{zone}` — SOA serial currently served

## Examples

Accept challenge records from an ACME client, persist them, and nothing else:

~~~ corefile
example.org {
    tsig {
        secret acme-updater.example.org. <base64 key>
        require_opcode UPDATE
    }

    dns-update-persistent {
        file /var/lib/coredns/db.example.org
        mutable TXT
    }

    ixfr

    transfer {
        to 192.0.2.53
    }
}
~~~

Test it with `nsupdate`:

~~~
$ nsupdate -y hmac-sha256:acme-updater.example.org.:<base64 key>
> server 192.0.2.1
> zone example.org.
> update add _acme-challenge.example.org. 60 TXT "token"
> send
~~~

After `send`, `/var/lib/coredns/db.example.org` contains that TXT and a bumped SOA serial. A matching DELETE removes it from the file. Restarting CoreDNS serves the post-update zone without contacting a primary.

## See Also

RFC 2136 for the update protocol, RFC 8945 for TSIG, and CoreDNS's *tsig*, *file* and *transfer* plugins, all three of which this composes with rather than reimplements. *ixfr* is the RFC 1995 journal this plugin commits into. *secondary-persistent* is the durable *secondary* counterpart; this plugin is a durable *primary*.
