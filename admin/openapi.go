package admin

import "net/http"

const openAPIJSON = `{
  "openapi": "3.0.3",
  "info": {"title": "CoreDNS Admin", "version": "v1"},
  "paths": {
    "/api/v1/health": {"get": {"summary": "Health"}},
    "/api/v1/auth/login": {"post": {"summary": "Password login"}},
    "/api/v1/auth/oidc/login": {"get": {"summary": "OIDC redirect"}},
    "/api/v1/zones": {"get": {"summary": "List zones"}, "post": {"summary": "Create zone"}},
    "/api/v1/acls": {"get": {"summary": "List ACLs"}, "post": {"summary": "Create ACL"}},
    "/api/v1/acls/{name}": {"patch": {"summary": "Rename ACL or replace networks"}, "delete": {"summary": "Delete ACL and its view zonefiles"}},
    "/api/v1/zones/{origin}/dnssec": {"get": {"summary": "DNSSEC status and DS"}, "post": {"summary": "Enable DNSSEC and generate a CSK"}, "delete": {"summary": "Disable DNSSEC and delete keys"}},
    "/api/v1/zones/{origin}/records": {
      "get": {"summary": "List records"},
      "post": {"summary": "Add record"},
      "put": {"summary": "Replace RRset"},
      "patch": {"summary": "Replace one record"},
      "delete": {"summary": "Delete records"}
    },
    "/api/v1/cluster": {"get": {"summary": "Cluster roster (primary and secondaries)"}},
    "/api/v1/cluster/join-tokens": {"post": {"summary": "Mint a one-time join key (primary)"}},
    "/api/v1/cluster/connect": {"post": {"summary": "Join this node to a primary (url, token; optional name, dns, primary_dns)"}},
    "/api/v1/cluster/primary-dns": {"put": {"summary": "Secondary-local override for the primary DNS address used for AXFR"}},
    "/api/v1/cluster/members/{id}": {"patch": {"summary": "Edit a cluster member name, API URL, or DNS address (primary)"}, "delete": {"summary": "Remove a secondary"}},
    "/api/v1/cluster/join": {"post": {"summary": "Join a secondary to this primary"}},
    "/api/v1/cluster/snapshot": {"get": {"summary": "Auth replica snapshot"}},
    "/api/v1/tsig-keys": {"get": {"summary": "List TSIG keys"}, "post": {"summary": "Create a TSIG key"}},
    "/api/v1/tsig-keys/{id}": {"delete": {"summary": "Delete a TSIG key"}},
    "/api/v1/transfer": {"get": {"summary": "AXFR allow-list"}, "put": {"summary": "Replace extra AXFR IPs (unioned with Corefile to)"}},
    "/api/v1/recursion": {"get": {"summary": "Clients allowed to recurse through Unbound"}, "put": {"summary": "Replace recursion CIDRs (empty denies all)"}},
    "/api/v1/filters": {"get": {"summary": "Block and allow lists plus URL feeds"}},
    "/api/v1/filters/rules": {"post": {"summary": "Add a manual domain pattern"}},
    "/api/v1/filters/rules/{id}": {"delete": {"summary": "Delete a manual domain pattern"}},
    "/api/v1/filters/feeds": {"post": {"summary": "Add a URL list (periodic sync or one-time import)"}},
    "/api/v1/filters/feeds/{id}": {"patch": {"summary": "Update a URL list"}, "delete": {"summary": "Remove a URL list and its entries"}},
    "/api/v1/filters/feeds/{id}/sync": {"post": {"summary": "Fetch a URL list now"}},
    "/api/v1/backup": {"get": {"summary": "Download a zip of sqlite, zone files, and Corefile"}},
    "/api/v1/update": {"get": {"summary": "Latest GitHub release vs this binary"}, "post": {"summary": "Install the latest GitHub release and restart"}},
    "/api/v1/metrics": {"get": {"summary": "Curated in-process Prometheus snapshot"}},
    "/api/v1/queries": {"get": {"summary": "DNS query stats and per-type series. Query range=5m|15m|1h|6h|24h|7d"}},
    "/api/v1/audit": {"get": {"summary": "Recent audit rows"}}
  }
}`

func (a *Admin) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(openAPIJSON))
}
