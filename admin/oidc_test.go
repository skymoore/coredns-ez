package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skymoore/coredns-ez/admin/store"
)

func TestOIDCProvisionRole(t *testing.T) {
	boot := store.User{Username: "admin", Role: store.RoleAdmin}
	if got := oidcProvisionRole(true, "admin", nil); got != store.RoleAdmin {
		t.Fatalf("empty db: %s", got)
	}
	if got := oidcProvisionRole(true, "admin", []store.User{boot}); got != store.RoleViewer {
		t.Fatalf("password on with bootstrap: %s", got)
	}
	if got := oidcProvisionRole(false, "admin", []store.User{boot}); got != store.RoleAdmin {
		t.Fatalf("password off leftover bootstrap: %s", got)
	}
	fed := store.User{Username: "i@msky.me", Role: store.RoleAdmin}
	if got := oidcProvisionRole(false, "admin", []store.User{boot, fed}); got != store.RoleViewer {
		t.Fatalf("password off already has oidc admin: %s", got)
	}
}

func TestWantsJSON(t *testing.T) {
	browser := httptest.NewRequest(http.MethodGet, "/", nil)
	browser.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if wantsJSON(browser) {
		t.Fatal("browser Accept must redirect, not JSON")
	}
	api := httptest.NewRequest(http.MethodGet, "/", nil)
	api.Header.Set("Accept", "application/json")
	if !wantsJSON(api) {
		t.Fatal("application/json must stay JSON")
	}
}
