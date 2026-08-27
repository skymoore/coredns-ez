# Security

This is an independent CoreDNS build ([NOTICE](NOTICE)). It is not an official
CoreDNS or CNCF product.

## Defaults that matter

- **AXFR is off to the world.** Product Corefiles use `transfer { to 127.0.0.1 }`.
  Add secondary IPs in the UI (Cluster → Zone transfer) or they cannot AXFR.
  `to *` means anyone who can reach :53 TCP can copy every zone. Never ship that
  on a public server. IPs only, no CIDR.
- **Recursion is off** in Docker and the systemd installer. Alpine (and
  `UNBOUND=1` on systemd) recurses only for private client IPs via a CoreDNS
  `view`; Unbound itself also refuses the public internet. Do not add a bare
  `forward .` on the catch-all `.` block.
- **Admin UI on :8080 is plain HTTP.** The session cookie is `HttpOnly` and
  `SameSite=Lax`. `Secure` is set only when CoreDNS itself sees TLS. Do not
  publish :8080 on the internet; use `https://.:443` with `tls` or a reverse
  proxy on loopback. See [docs/deploy.md](docs/deploy.md).
- **Prometheus** binds `127.0.0.1:9153` in the default image Corefile. Do not
  publish that port unless you mean to.
- **Bootstrap password** is required (or generated and printed) on first start.
  Set `COREDNS_ADMIN_BOOTSTRAP_PASSWORD` and keep the sqlite volume.
- **The Docker image does not run as root.** USER is `coredns` (uid 65532).
  Port 53 uses `cap_net_bind_service`. Do not pass `--user 0` except to chown
  an old volume; the entrypoint then drops privileges.

## Reporting

Open a GitHub issue or contact the repository owner. There is no separate
bounty program.
