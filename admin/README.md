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
are created through the API and stored as RFC 1035 master files under `data`.
SQLite (`db`) holds users, API tokens, OIDC settings, TSIG keys, cluster membership, and
the zone inventory. Record data is **not** in SQLite.

Identity: local users (argon2id), bearer API tokens, and optional OIDC.
Secondaries replicate password/token hashes so the same credentials work on
both nodes. Record mutations on a secondary are proxied to the primary.

Do not stack *file*, *auto*, *dynupdate*, *secondary*, *secondary-persistent*,
or *dns-update-persistent* on an origin the admin plugin owns.

Only zones that register in `zonereg` appear in the UI (`GET /api/v1/zones`):
admin-created zones, *dns-update-persistent*, and *secondary-persistent*.
The in-tree *file* plugin does **not** register. A Corefile `file` zone still
answers DNS queries but will not show up in the dashboard. Convert it to
*dns-update-persistent* (or create it through the API) to manage it here.

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
dns-update-persistent:github.com/skymoore/coredns-plugins/dns-update-persistent
ixfr:github.com/skymoore/coredns-plugins/ixfr
admin:github.com/skymoore/coredns-plugins/admin
secondary:secondary
secondary-persistent:github.com/skymoore/coredns-plugins/secondary-persistent
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
* `POST /api/v1/cluster/connect` on a secondary that has not joined yet (`url` + `token`); the Cluster UI mints the token on the primary and pastes it here

Roles: `admin`, `operator`, `viewer`.

Authenticated JSON for the UI:

* `GET /api/v1/metrics` curated in-process Prometheus snapshot
* `GET /api/v1/audit` recent audit rows
* `GET|POST /api/v1/tsig-keys`, `DELETE /api/v1/tsig-keys/{id}` HMAC keys for nsupdate / signed transfers
* Cluster: on the primary, **Add a secondary** mints a one-time join key. On a new `role secondary` instance, Cluster → paste primary URL + key. Identity (users, tokens, TSIG keys, zone list) replicates; zone data is AXFRed from `advertise`.

Zone transfers (AXFR/IXFR) are **not** gated by the admin login. The CoreDNS `transfer` plugin allows AXFR from every address in `transfer { to ... }`. `to *` means anyone who can reach the DNS port can copy the zone. TSIG is not required for AXFR unless the Corefile `tsig` plugin has `require AXFR`. List secondary IPs in `to` (and optionally require TSIG) before exposing the server.

Build the UI with `make ui` (or `npm --prefix admin/ui ci && npm --prefix admin/ui run build`) before `go build`.

## See Also

RFC 8484 (DoH). *dns-update-persistent*, *ixfr*, *secondary-persistent*.
