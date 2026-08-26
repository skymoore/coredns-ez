package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
