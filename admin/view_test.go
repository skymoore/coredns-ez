package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
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

func TestRecursionGatedByAllowListNotACL(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)
	setupSplitHorizon(t, a, token)

	a.Next = plugin.HandlerFunc(func(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{
			&dns.A{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30}, A: net.ParseIP("9.9.9.9").To4()},
		}
		return dns.RcodeSuccess, w.WriteMsg(m)
	})

	if lookupA(t, a, "www.example.com.", "8.8.8.8") != "192.0.2.10" {
		t.Fatal("public auth should still answer")
	}
	if lookupA(t, a, "www.example.com.", "10.1.2.3") != "10.1.2.3" {
		t.Fatal("ACL client should still get the view")
	}
	m := new(dns.Msg)
	m.SetQuestion("outside.test.", dns.TypeA)
	rw := &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "10.1.2.3"}}
	_, err := a.ServeDNS(context.Background(), rw, m)
	if err != nil {
		t.Fatal(err)
	}
	if rw.msg == nil || rw.msg.Rcode != dns.RcodeRefused {
		t.Fatalf("ACL must not grant recursion rcode=%v", rw.msg)
	}

	if err := a.db.SetRecursion([]string{"10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if lookupA(t, a, "outside.test.", "10.1.2.3") != "9.9.9.9" {
		t.Fatal("recursion allow-list client should recurse")
	}
	rw = &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "8.8.8.8"}}
	_, err = a.ServeDNS(context.Background(), rw, m)
	if err != nil {
		t.Fatal(err)
	}
	if rw.msg == nil || rw.msg.Rcode != dns.RcodeRefused {
		t.Fatalf("public recursion rcode=%v", rw.msg)
	}
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
	if !a.db.HasRecords("example.com.", "office") {
		t.Fatal("expected sqlite view records after ACL rename")
	}
}

func TestACLRecordOnCorefilePrimary(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)

	origin := "example.com."
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
	d, err := dnsupdatepersist.NewFromRecords(origin, []dns.RR{soa, ns, www}, nil)
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
		t.Fatal("expected view zone")
	}
	if got := lookupA(t, a, "www.example.com.", "10.9.8.7"); got != "10.1.2.3" {
		t.Fatalf("internal client got %q", got)
	}
}

func setupSplitHorizon(t *testing.T, a *Admin, token string) {
	t.Helper()
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
}

func TestClusterSnapshotKeepsViewsOffPublic(t *testing.T) {
	primary := testAdmin(t)
	token := loginToken(t, primary)
	setupSplitHorizon(t, primary, token)

	snap, err := primary.fullSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Views) != 1 || snap.Views[0].ACL != "internal" {
		t.Fatalf("snapshot views %+v", snap.Views)
	}
	viewRR := false
	for _, rec := range snap.Records {
		if rec.View == "internal" && rec.Type == "A" && strings.Contains(rec.Rdata, "10.1.2.3") {
			viewRR = true
		}
	}
	if !viewRR {
		t.Fatalf("snapshot missing view records: %+v", snap.Records)
	}

	zonereg.ResetForTest()
	replica := testAdmin(t)
	setupSplitHorizon(t, replica, loginToken(t, replica))
	if err := replica.replaceViewsFromSnap(snap.Views); err != nil {
		t.Fatal(err)
	}

	recs, ok := replica.collectRecords("example.com.", "")
	if !ok {
		t.Fatal("replica zone missing")
	}
	var internal, publicView string
	for _, rec := range recs {
		if rec.Type != "A" {
			continue
		}
		if strings.Contains(rec.Rdata, "10.1.2.3") {
			if rec.ACL != "internal" {
				t.Fatalf("view A published as acl=%q: %+v", rec.ACL, rec)
			}
			internal = rec.Rdata
		}
		if rec.ACL == "" || rec.ACL == "public" {
			if strings.Contains(rec.Rdata, "10.1.2.3") {
				t.Fatalf("view A flattened onto public: %+v", rec)
			}
			if strings.Contains(rec.Rdata, "192.0.2.10") {
				publicView = rec.Rdata
			}
		}
	}
	if internal == "" {
		t.Fatalf("internal A missing: %+v", recs)
	}
	if publicView == "" {
		t.Fatalf("public A missing: %+v", recs)
	}
	if got := lookupA(t, replica, "www.example.com.", "10.9.8.7"); got != "10.1.2.3" {
		t.Fatalf("internal client got %q", got)
	}
	if got := lookupA(t, replica, "www.example.com.", "8.8.8.8"); got != "192.0.2.10" {
		t.Fatalf("public client got %q", got)
	}
}

func TestAXFRDoesNotIncludeViewRecords(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)
	setupSplitHorizon(t, a, token)

	ch, err := a.Transfer("example.com.", 0)
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	var sawPub, sawView bool
	for rrs := range ch {
		for _, rr := range rrs {
			s := rr.String()
			if strings.Contains(s, "192.0.2.10") {
				sawPub = true
			}
			if strings.Contains(s, "10.1.2.3") {
				sawView = true
			}
		}
	}
	if !sawPub {
		t.Fatal("AXFR missing public A")
	}
	if sawView {
		t.Fatal("AXFR included the ACL view record")
	}

	m := new(dns.Msg)
	m.SetQuestion("example.com.", dns.TypeAXFR)
	rw := &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "10.9.8.7"}}
	_, err = a.ServeDNS(context.Background(), rw, m)
	if err != nil {
		t.Fatal(err)
	}
	if rw.msg != nil {
		for _, rr := range append(append([]dns.RR{}, rw.msg.Answer...), rw.msg.Ns...) {
			if strings.Contains(rr.String(), "10.1.2.3") {
				t.Fatalf("AXFR query from ACL IP served view record %s", rr)
			}
		}
	}
}

func TestLoadRecoversViewOnlyOrigin(t *testing.T) {
	a := testAdmin(t)
	soa, err := dns.NewRR("k8s.rwx.dev. 300 IN SOA ns.rwx.dev. host.rwx.dev. 1 3600 600 86400 60")
	if err != nil {
		t.Fatal(err)
	}
	ns, err := dns.NewRR("k8s.rwx.dev. 300 IN NS ns.rwx.dev.")
	if err != nil {
		t.Fatal(err)
	}
	aRec, err := dns.NewRR("svc.k8s.rwx.dev. 60 IN A 10.1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.ReplaceRecords("k8s.rwx.dev.", "internal", []dns.RR{soa, ns, aRec}); err != nil {
		t.Fatal(err)
	}
	if err := a.db.UpsertZoneView(store.ZoneView{Origin: "k8s.rwx.dev.", ACL: "internal"}); err != nil {
		t.Fatal(err)
	}
	if err := a.loadPersistedZones(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.GetZone("k8s.rwx.dev."); err != nil {
		t.Fatal("view-only origin was not promoted to a zone")
	}
	if zonereg.PrimaryOf("k8s.rwx.dev.") == nil {
		t.Fatal("k8s.rwx.dev. not registered")
	}
	tok := loginToken(t, a)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("k8s.rwx.dev.")) {
		t.Fatalf("zone list missing k8s.rwx.dev.: %d %s", w.Code, w.Body.Bytes())
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
