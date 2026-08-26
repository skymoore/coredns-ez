# api

## Name

*api* - HTTPS management plane for this CoreDNS build: users, zones, records, and clustering.

## Description

*api* multiplexes onto CoreDNS’s DNS-over-HTTPS listener. `/dns-query` stays DoH.
Everything else (`/api/v1/...`, `/`) is the JSON API. The same TLS certificate the
*tls* plugin installs on `https://.:443` is used. There is no extra listen address.

This requires the small CoreDNS patch in `patches/coredns-http-handler.patch`
(applied by the release script and the integration-test image). Unpatched
CoreDNS still compiles the plugin; non-DoH paths on 443 remain 404.

A process is a **primary** or a **secondary**. The Corefile starts *api*; zones
are created through the API and stored as RFC 1035 master files under `data`.
SQLite (`db`) holds users, API tokens, OIDC settings, cluster membership, and
the zone inventory. Record data is **not** in SQLite.

Identity: local users (argon2id), bearer API tokens, and optional OIDC.
Secondaries replicate password/token hashes so the same credentials work on
both nodes. Record mutations on a secondary are proxied to the primary.

Do not stack *file*, *auto*, *dynupdate*, *secondary*, *secondary-persistent*,
or *dns-update-persistent* on an origin the API owns. Corefile-static zones
still register and appear in `GET /api/v1/zones`.

## Syntax

~~~
api {
    db PATH
    data DIR
    role primary | secondary
    bootstrap_admin USER
    advertise HOST:PORT
    join URL TOKEN
    dns HOST:PORT
    cors ORIGIN...
    oidc {
        issuer URL
        client_id ID
        client_secret SECRET
        redirect_url URL
    }
}
~~~

* `db` / `data` / `role` are required on the first *api* block.
* `bootstrap_admin` plus env `COREDNS_API_BOOTSTRAP_PASSWORD` seed the first admin on an empty DB.
* `advertise` is the DNS address secondaries transfer from.
* `join` is secondary-only, used when the DB has no cluster membership yet.
* `dns` is the primary’s DNS address on a secondary (`HOST:PORT`).
* Put *api* in both the `https://.:443` block and the `.:53` block so DoH and UDP/TCP share the zone manager.

## Compilation

```
file:file
dns-update-persistent:github.com/skymoore/coredns-plugins/dns-update-persistent
ixfr:github.com/skymoore/coredns-plugins/ixfr
api:github.com/skymoore/coredns-plugins/api
secondary:secondary
secondary-persistent:github.com/skymoore/coredns-plugins/secondary-persistent
```

Apply `patches/coredns-http-handler.patch` to the CoreDNS tree before `go generate && go build`.

## Auth

All `/api/v1` routes require `Authorization: Bearer` (user JWT or API token) or
cookie `coredns_api_session`, except:

* `GET /api/v1/health`
* `GET /api/v1/auth/config`
* `POST /api/v1/auth/login`
* `GET /api/v1/auth/oidc/login`
* `GET /api/v1/auth/oidc/callback`
* `POST /api/v1/cluster/join` (join token)
* `GET /api/v1/cluster/snapshot` (member secret)
* `POST /api/v1/cluster/connect` on a secondary that has not joined yet (`url` + `token`)

Roles: `admin`, `operator`, `viewer`.

## See Also

RFC 8484 (DoH). *dns-update-persistent*, *ixfr*, *secondary-persistent*.
