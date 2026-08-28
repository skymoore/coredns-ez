package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

// Mirrors the live ns1 layout: origin rwx.dev with public A for ns1 plus an
// internal-ACL A, and a view-only origin k8s.rwx.dev whose apex A lives only
// in the internal view. ACL covers the RFC1918 ranges plus the server's own
// public /32.
func setupLiveLikeNS1(t *testing.T) *Admin {
	t.Helper()
	a := testAdmin(t)
	token := loginToken(t, a)

	postJSON := func(path string, body any) {
		t.Helper()
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("POST %s: %d %s", path, w.Code, w.Body.Bytes())
		}
	}

	postJSON("/api/v1/zones", map[string]string{"origin": "rwx.dev.", "type": "primary"})
	postJSON("/api/v1/zones", map[string]string{"origin": "k8s.rwx.dev.", "type": "primary"})
	postJSON("/api/v1/acls", map[string]any{
		"name":     "internal",
		"networks": []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "149.90.131.89/32"},
	})

	// rwx.dev public apex + ns1 public A
	postJSON("/api/v1/zones/rwx.dev./records", recordJSON{Name: "@", Type: "SOA", TTL: 900, Rdata: "ns1.rwx.dev. sky.rwx.dev. 2026082610 900 300 604800 900"})
	postJSON("/api/v1/zones/rwx.dev./records", recordJSON{Name: "@", Type: "NS", TTL: 900, Rdata: "ns1.rwx.dev."})
	postJSON("/api/v1/zones/rwx.dev./records", recordJSON{Name: "ns1", Type: "A", TTL: 3000, Rdata: "149.90.131.89"})
	// internal view records
	postJSON("/api/v1/zones/rwx.dev./records", recordJSON{Name: "ns1", Type: "A", TTL: 3000, Rdata: "192.168.8.53", ACL: "internal"})
	// k8s.rwx.dev: apex A only in internal view
	postJSON("/api/v1/zones/k8s.rwx.dev./records", recordJSON{Name: "@", Type: "A", TTL: 300, Rdata: "192.168.8.99", ACL: "internal"})
	postJSON("/api/v1/zones/k8s.rwx.dev./records", recordJSON{Name: "api", Type: "A", TTL: 300, Rdata: "192.168.8.245", ACL: "internal"})

	return a
}

func TestLiveLikeNS1ViewServe(t *testing.T) {
	a := setupLiveLikeNS1(t)

	if got := lookupA(t, a, "ns1.rwx.dev.", "192.168.8.7"); got != "192.168.8.53" {
		t.Fatalf("LAN client ns1.rwx.dev A: got %q want 192.168.8.53", got)
	}
	if got := lookupA(t, a, "ns1.rwx.dev.", "8.8.8.8"); got != "149.90.131.89" {
		t.Fatalf("public client ns1.rwx.dev A: got %q want 149.90.131.89", got)
	}
	if got := lookupA(t, a, "k8s.rwx.dev.", "192.168.8.7"); got != "192.168.8.99" {
		t.Fatalf("LAN client k8s.rwx.dev apex A: got %q want 192.168.8.99", got)
	}
	if got := lookupA(t, a, "api.k8s.rwx.dev.", "192.168.8.7"); got != "192.168.8.245" {
		t.Fatalf("LAN client api.k8s.rwx.dev A: got %q want 192.168.8.245", got)
	}
}

// SOA on the view-only apex must validate for LAN clients too (cert-manager
// FindZoneByFqdn walks SOA).
func TestLiveLikeViewOnlyApexSOAFromLAN(t *testing.T) {
	a := setupLiveLikeNS1(t)

	m := new(dns.Msg)
	m.SetQuestion("k8s.rwx.dev.", dns.TypeSOA)
	rw := &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "192.168.8.7"}}
	if _, err := a.ServeDNS(context.Background(), rw, m); err != nil {
		t.Fatal(err)
	}
	if rw.msg == nil {
		t.Fatal("no response")
	}
	if len(rw.msg.Answer) == 0 {
		t.Fatalf("SOA answer empty: rcode=%v ns=%v", rw.msg.Rcode, rw.msg.Ns)
	}
}
