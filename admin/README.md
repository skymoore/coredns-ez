# admin

## Name

*admin* - HTTPS management plane for this CoreDNS build: UI, users, zones, records, and clustering.

## Description

*admin* multiplexes onto CoreDNS’s DNS-over-HTTPS listener. `/dns-query` stays DoH.
`/api/v1/...` is the JSON API. `/` and other non-DoH paths serve the embedded
admin UI (React SPA in `admin/ui`, built into `embed.FS`). The same TLS certificate the
*tls* plugin installs on `https://.:443` is used. There is no extra listen address.

This requires the small CoreDNS patch in `patches/coredns-http-handler.patch`
(applied by the release script and the integration-test image). Unpatched
CoreDNS still compiles the plugin; non-DoH paths on 443 remain 404.

A process is a **primary** or a **secondary**. The Corefile starts *admin*; zones
and records live in SQLite (`db`), along with users, API tokens, OIDC settings,
TSIG keys, and cluster membership. `data` is accepted for compatibility and unused
for record persist. If the Corefile still names zone files (`file`, *dns-update-persistent*,
*secondary-persistent*) or CoreDNS `view`/`acl` policy, they are imported into
SQLite on startup and stripped. Recursion is allowed only for client IPs in an
ACL. The Corefile should only name listeners, ports, and which plugins are enabled.

Identity: local users (argon2id), bearer API tokens, and optional OIDC.
Secondaries replicate password/token hashes so the same credentials work on
both nodes. Record mutations on a secondary are proxied to the primary.

Do not stack *file*, *auto*, *dynupdate*, *secondary*, *secondary-persistent*,
or *dns-update-persistent* on an origin the admin plugin owns. After import,
those plugins are not in the Corefile; admin serves every origin from SQLite.

## Syntax

~~~
admin {
    db PATH
    data DIR
    role primary | secondary
    bootstrap_admin USER
    advertise HOST:PORT
    join URL TOKEN
    dns HOST:PORT
    cors ORIGIN...
    password on | off
    oidc {
        issuer URL
        client_id ID
        client_secret SECRET
        redirect_url URL
        button_text TEXT
        button_image URL
    }
}
~~~

* `db` / `data` / `role` are required on the first *admin* block.
* `password` defaults to `on`. `password off` hides password login (OIDC button only); OIDC is then required, and the first OIDC sign-in becomes admin (no bootstrap user).
* `oidc` `button_text` and `button_image` (http or https URL) customize the login button. Default label is `Continue with OIDC`.
* `bootstrap_admin` plus env `COREDNS_ADMIN_BOOTSTRAP_PASSWORD` seed the first admin on an empty DB when password login is on.
* `advertise` is the DNS address secondaries transfer from.
* `join` is secondary-only, used when the DB has no cluster membership yet.
* `dns` is the primary’s DNS address on a secondary (`HOST:PORT`).
* Put *admin* in both the `https://.:443` block and the `.:53` block so DoH and UDP/TCP share the zone manager.

## Compilation

```
file:file
dns-update-persistent:github.com/skymoore/coredns-ez/dns-update-persistent
ixfr:github.com/skymoore/coredns-ez/ixfr
admin:github.com/skymoore/coredns-ez/admin
secondary:secondary
secondary-persistent:github.com/skymoore/coredns-ez/secondary-persistent
```

Apply `patches/coredns-http-handler.patch` to the CoreDNS tree before `go generate && go build`.

## Auth

All `/api/v1` routes require `Authorization: Bearer` (user JWT or API token) or
cookie `coredns_admin_session`, except:

* `GET /api/v1`
* `GET /api/v1/health`
* `GET /api/v1/auth/config`
* `POST /api/v1/auth/login`
* `GET /api/v1/auth/oidc/login`
* `GET /api/v1/auth/oidc/callback`
* `POST /api/v1/cluster/join` (join token)
* `GET /api/v1/cluster/snapshot` (member secret)
* `POST /api/v1/cluster/connect` (`url` + `token`) on a node that has not joined yet. Empty-db secondaries may call it without a session. A default install is a standalone primary: an admin session can join from Cluster without changing the Corefile `role`. A primary that already has secondaries cannot join.

Roles: `admin`, `operator`, `viewer`.

Authenticated JSON for the UI:

* `GET /api/v1/metrics` curated in-process Prometheus snapshot
* `GET /api/v1/audit` recent audit rows
* `GET|POST /api/v1/tsig-keys`, `DELETE /api/v1/tsig-keys/{id}` HMAC keys for nsupdate / signed transfers
* Filters: `GET /api/v1/filters`; `POST/DELETE /api/v1/filters/rules`; URL lists at `POST /api/v1/filters/feeds` with `sync` `periodic` (refresh on an interval) or `once` (import now, do not refresh). Blocked names that are not in an admin-owned zone are NXDOMAIN. Allow wins. `example.com` matches itself and subdomains; `*.example.com` matches subdomains only.
* `GET|PUT /api/v1/transfer` extra AXFR IPs (unioned with Corefile `transfer { to }`). IPs only; `*` is rejected. Cluster join appends the secondary DNS address.
* `GET /api/v1/backup` zip of sqlite, Corefile, and tls (operator+). Host install puts those trees in `/etc/coredns` and `/var/lib/coredns` owned by `coredns`.
* `GET|POST /api/v1/update` GitHub release check / self-update (POST is admin; linux only). The running binary’s directory must be writable (`install.sh` places it at `/var/lib/coredns/coredns` and supervises a clean exit so bind capability is restored).
* Cluster: on the primary, **Add a secondary** mints a one-time join key. On the new node (including a default `role primary` install), Cluster → **Join an existing cluster** and paste the URL + key. Set **This node name** (for example `ns3.dns.rwx.dev`); on the primary, **Rename** edits it later. `COREDNS_NODE_NAME` is the default when joining from Docker. That node becomes a secondary (stored in sqlite so it survives restart). The primary Corefile is copied and rewritten: `role secondary`, `advertise` becomes `dns` (the primary’s DNS), `bind` uses this node’s IP, `db`/`data` stay local, OIDC `redirect_url` is this node’s callback (register it on the IdP). `dns-update-persistent` and `file` zone blocks become `secondary-persistent` (AXFR from the primary, persist the same paths). Referenced zone and TLS files are seeded, then CoreDNS restarts. Users/tokens/TSIG still replicate in sqlite. Login is local, not proxied. If the primary Corefile uses `{$ENV}` secrets, set the same variables on the secondary.

Zone transfers (AXFR/IXFR) are **not** gated by the admin login. The CoreDNS `transfer` plugin allows AXFR from every address in `transfer { to ... }` plus extra IPs from `PUT /api/v1/transfer`. `to *` means anyone who can reach the DNS port can copy the zone. Product Corefiles use `to 127.0.0.1` only. TSIG is not required for AXFR unless the Corefile `tsig` plugin has `require AXFR`.

Build the UI with `make ui` (or `npm --prefix admin/ui ci && npm --prefix admin/ui run build`) before `go build`.

## See Also

RFC 8484 (DoH). *dns-update-persistent*, *ixfr*, *secondary-persistent*.
