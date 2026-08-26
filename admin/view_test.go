package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	dnsupdatepersist "github.com/skymoore/coredns-ez/dns-update-persistent"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

func TestACLAndSplitHorizonServe(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)

	zbody, _ := json.Marshal(map[string]string{"origin": "example.com.", "type": "primary"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/zones", bytes.NewReader(zbody))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create zone: %d %s", w.Code, w.Body.Bytes())
	}

	acl, _ := json.Marshal(map[string]any{"name": "internal", "networks": []string{"10.0.0.0/8"}})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader(acl))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create acl: %d %s", w.Code, w.Body.Bytes())
	}

	pub, _ := json.Marshal(recordJSON{Name: "www", Type: "A", TTL: 60, Rdata: "192.0.2.10"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./records", bytes.NewReader(pub))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("add public: %d %s", w.Code, w.Body.Bytes())
	}

	priv, _ := json.Marshal(recordJSON{Name: "www", Type: "A", TTL: 60, Rdata: "10.1.2.3", ACL: "internal"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./records", bytes.NewReader(priv))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("add view: %d %s", w.Code, w.Body.Bytes())
	}

	gotPub := lookupA(t, a, "www.example.com.", "8.8.8.8")
	if gotPub != "192.0.2.10" {
		t.Fatalf("public client got %q", gotPub)
	}
	gotPriv := lookupA(t, a, "www.example.com.", "10.9.8.7")
	if gotPriv != "10.1.2.3" {
		t.Fatalf("internal client got %q", gotPriv)
	}
}

type captureRW struct {
	test.ResponseWriter
	msg *dns.Msg
}

func (c *captureRW) WriteMsg(m *dns.Msg) error {
	c.msg = m
	return nil
}

func lookupA(t *testing.T, a *Admin, name, remote string) string {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(name, dns.TypeA)
	rw := &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: remote}}
	_, err := a.ServeDNS(context.Background(), rw, m)
	if err != nil {
		t.Fatal(err)
	}
	if rw.msg == nil {
		t.Fatalf("no response")
	}
	for _, rr := range rw.msg.Answer {
		if rec, ok := rr.(*dns.A); ok {
			return rec.A.String()
		}
	}
	return ""
}

func TestPatchACLRenameAndNetworks(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)

	zbody, _ := json.Marshal(map[string]string{"origin": "example.com.", "type": "primary"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/zones", bytes.NewReader(zbody))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create zone: %d %s", w.Code, w.Body.Bytes())
	}

	acl, _ := json.Marshal(map[string]any{"name": "internal", "networks": []string{"10.0.0.0/8"}})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader(acl))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create acl: %d %s", w.Code, w.Body.Bytes())
	}

	priv, _ := json.Marshal(recordJSON{Name: "www", Type: "A", TTL: 60, Rdata: "10.1.2.3", ACL: "internal"})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./records", bytes.NewReader(priv))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("add view: %d %s", w.Code, w.Body.Bytes())
	}

	patch, _ := json.Marshal(map[string]any{"name": "office", "networks": []string{"192.168.8.0/24"}})
	r = httptest.NewRequest(http.MethodPatch, "/api/v1/acls/internal", bytes.NewReader(patch))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("patch acl: %d %s", w.Code, w.Body.Bytes())
	}

	if lookupA(t, a, "www.example.com.", "10.9.8.7") != "" {
		t.Fatal("10/8 should no longer match after network replace")
	}
	if got := lookupA(t, a, "www.example.com.", "192.168.8.53"); got != "10.1.2.3" {
		t.Fatalf("office client got %q", got)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/zones/example.com./records", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list records: %d %s", w.Code, w.Body.Bytes())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"acl":"office"`)) {
		t.Fatalf("records still keyed by old ACL: %s", w.Body.Bytes())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`"acl":"internal"`)) {
		t.Fatalf("old ACL name still on records: %s", w.Body.Bytes())
	}

	views, err := a.db.ListZoneViews()
	if err != nil || len(views) != 1 || views[0].ACL != "office" {
		t.Fatalf("zone_views: %+v %v", views, err)
	}
	if _, err := os.Stat(views[0].Path); err != nil {
		t.Fatalf("view file %s: %v", views[0].Path, err)
	}
}

func TestACLRecordOnCorefilePrimary(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)

	origin := "example.com."
	path := filepath.Join(a.cfg.Data, "db.example.com")
	soa := &dns.SOA{
		Hdr:     dns.RR_Header{Name: origin, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
		Ns:      "ns1.example.com.",
		Mbox:    "hostmaster.example.com.",
		Serial:  1,
		Refresh: 3600, Retry: 600, Expire: 86400, Minttl: 60,
	}
	ns := &dns.NS{Hdr: dns.RR_Header{Name: origin, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 300}, Ns: "ns1.example.com."}
	www := &dns.A{
		Hdr: dns.RR_Header{Name: "www.example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
		A:   net.ParseIP("192.0.2.10").To4(),
	}
	if err := dnsupdatepersist.WriteSeed(path, origin, []dns.RR{soa, ns, www}); err != nil {
		t.Fatal(err)
	}
	d, err := dnsupdatepersist.New(origin, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := zonereg.RegisterPrimary(d); err != nil {
		t.Fatal(err)
	}

	acl, _ := json.Marshal(map[string]any{"name": "internal", "networks": []string{"10.0.0.0/8"}})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader(acl))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create acl: %d %s", w.Code, w.Body.Bytes())
	}

	put, _ := json.Marshal(map[string]any{
		"name": "www", "type": "A", "acl": "internal",
		"records": []recordJSON{{Name: "www", Type: "A", TTL: 60, Rdata: "10.1.2.3", ACL: "internal"}},
	})
	r = httptest.NewRequest(http.MethodPut, "/api/v1/zones/example.com./records", bytes.NewReader(put))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("put ACL rrset on corefile primary: %d %s", w.Code, w.Body.Bytes())
	}
	if a.viewOf(origin, "internal") == nil {
		t.Fatal("expected view zonefile")
	}
	if got := lookupA(t, a, "www.example.com.", "10.9.8.7"); got != "10.1.2.3" {
		t.Fatalf("internal client got %q", got)
	}
}

func TestACLContains(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)
	body, _ := json.Marshal(map[string]any{"name": "office", "networks": []string{"192.168.8.0/24", "10.0.0.1"}})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body.Bytes())
	}
	acl, err := a.db.GetACLByName("office")
	if err != nil {
		t.Fatal(err)
	}
	if !acl.Contains(net.ParseIP("192.168.8.53")) || !acl.Contains(net.ParseIP("10.0.0.1")) {
		t.Fatalf("should contain office nets: %+v", acl.Networks)
	}
	if acl.Contains(net.ParseIP("1.2.3.4")) {
		t.Fatal("public IP matched office")
	}
}
