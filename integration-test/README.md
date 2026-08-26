# Integration tests

Docker Compose stack that builds CoreDNS v1.14.7 with every default plugin plus
`dns-update-persistent`, `ixfr`, `admin`, and `secondary-persistent`, then
exercises a real primary/secondary pair (DNS plus the HTTPS admin API on :8443).

```
primary   172.30.53.10   dns-update-persistent   (RFC 2136 + in-place persist)
secondary 172.30.53.20   secondary-persistent    (AXFR/IXFR in + persist)
tester    172.30.53.30   dig / nsupdate
```

## Run

From this directory:

```
./scripts/run.sh
```

That builds the image, starts both servers, runs queries / AXFR / IXFR /
dynamic updates / disk checks, restarts the primary to prove the rewritten
master is the boot source of truth, then stops the primary and restarts the
secondary to prove it serves its persisted copy.

```
./scripts/run.sh up      # build and start only
./scripts/run.sh logs    # follow CoreDNS logs
./scripts/run.sh down    # stop and delete persist volumes
```

Host ports if you want to poke the stack yourself: primary `1053`, secondary
`1153` (TCP and UDP), health `18080` / `18081`, Prometheus scrape `19153` /
`19154`, admin UI/API `18443` / `18444`.

`prometheus :9153` is first in every DNS server block (DoH, catch-all, and
`example.com`) so the admin dashboard's in-process gatherer sees
`coredns_dns_requests_total`.

```
dig @127.0.0.1 -p 1053 www.example.com
nsupdate -y hmac-sha256:updater.example.com.:Y29yZWRucy1pbnRlZ3JhdGlvbi10ZXN0LWtleSEh
```

## Layout

| Path | Role |
|---|---|
| `Dockerfile` | multi-stage: CoreDNS with both plugins, then a tester image |
| `docker-compose.yml` | primary, secondary, tester on a fixed-IP network |
| `test-zone/db.example.com` | seed zone (copied into a volume on first start) |
| `test-zone/Corefile` | primary |
| `test-zone/Corefile.secondary` | secondary |
| `scripts/run.sh` | host driver (restarts, plugin check) |
| `scripts/run-tests.sh` | in-container assertions |

`transfer from` / `transfer to` take IP addresses only, which is why the
compose network pins `172.30.53.10` and `172.30.53.20`. AXFR is allowed from
the tester (`172.30.53.30`) and the secondary (`172.30.53.20`, primary only,
so NOTIFY is sent). Host-side `dig AXFR` against the published ports is
REFUSED unless you add your host IP.

The primary includes the `ixfr` plugin, so a stale-serial IXFR is a real RFC 1995
delta (inner SOA + added/deleted RRs), not a full-zone dump. One atomic UPDATE
adds (and a later UPDATE deletes) many RR types: A, AAAA, TXT, MX, CNAME, PTR,
SRV, CAA, SSHFP, TLSA, DS, NAPTR, URI, HINFO, RP, LOC, HTTPS, SVCB, EUI48,
EUI64, AFSDB, KX, NS+glue, CDS, SMIMEA, CERT. The IXFR stream must name each
of those owners and must omit unchanged seed records (`www`, `st-txt`). After
NOTIFY the secondary must still have the seed types — proof it applied
`installIXFR`. The journal file (`db.example.com.ixfr`) is checked after the
first UPDATE and again after the primary restart. DNAME is left out of
`mutable` so the allowlist is still tested.
