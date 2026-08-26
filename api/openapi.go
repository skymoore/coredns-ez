package api

import "net/http"

const openAPIJSON = `{
  "openapi": "3.0.3",
  "info": {"title": "CoreDNS API", "version": "v1"},
  "paths": {
    "/api/v1/health": {"get": {"summary": "Health"}},
    "/api/v1/auth/login": {"post": {"summary": "Password login"}},
    "/api/v1/auth/oidc/login": {"get": {"summary": "OIDC redirect"}},
    "/api/v1/zones": {"get": {"summary": "List zones"}, "post": {"summary": "Create zone"}},
    "/api/v1/zones/{origin}/records": {
      "get": {"summary": "List records"},
      "post": {"summary": "Add record"},
      "put": {"summary": "Replace RRset"},
      "delete": {"summary": "Delete records"}
    },
    "/api/v1/cluster/join": {"post": {"summary": "Join a secondary to this primary"}},
    "/api/v1/cluster/snapshot": {"get": {"summary": "Auth replica snapshot"}}
  }
}`

func (a *API) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(openAPIJSON))
}
