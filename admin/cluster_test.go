package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

type snapPrimary struct{ origin, path string }

func (s snapPrimary) Origin() string { return s.origin }
func (s snapPrimary) Source() string { return zonereg.SourceCorefile }
func (s snapPrimary) Records() []dns.RR {
	soa, _ := dns.NewRR(s.origin + " 60 IN SOA ns.host. host.host. 1 60 60 60 60")
	return []dns.RR{soa}
}
func (s snapPrimary) Apply(_, _ []dns.RR) error { return nil }
func (s snapPrimary) Path() string              { return s.path }

func TestNormalizeJoinURL(t *testing.T) {
	got, err := normalizeJoinURL("ns1.dns.rwx.dev")
	if err != nil || got != "https://ns1.dns.rwx.dev" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = normalizeJoinURL(" https://ns1.dns.rwx.dev/ ")
	if err != nil || got != "https://ns1.dns.rwx.dev" {
		t.Fatalf("trim %q %v", got, err)
	}
	if _, err := normalizeJoinURL("  "); err == nil {
		t.Fatal("empty url")
	}
}

func TestNormalizeDNSAddr(t *testing.T) {
	got, err := normalizeDNSAddr(" 203.0.113.10 ")
	if err != nil || got != "203.0.113.10:53" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = normalizeDNSAddr("ns1.dns.rwx.dev:53")
	if err != nil || got != "ns1.dns.rwx.dev:53" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := normalizeDNSAddr("127.0.0.1:53"); err == nil {
		t.Fatal("loopback")
	}
	if _, err := normalizeDNSAddr(""); err == nil {
		t.Fatal("empty")
	}
}

func TestPrimaryTransferFromPrefersOverride(t *testing.T) {
	a := testAdmin(t)
	a.cfg.PrimaryDNS = "192.168.8.53:53"
	_ = a.db.SetMeta(store.MetaAdvertise, "192.168.8.53:53")
	if got := a.primaryTransferFrom(); len(got) != 1 || got[0] != "192.168.8.53:53" {
		t.Fatalf("default %+v", got)
	}
	_ = a.db.SetMeta(store.MetaPrimaryDNS, "203.0.113.10:53")
	if got := a.primaryTransferFrom(); len(got) != 1 || got[0] != "203.0.113.10:53" {
		t.Fatalf("override %+v", got)
	}
}

func TestPutPrimaryDNSStaysOnSecondary(t *testing.T) {
	called := false
	prim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(599)
	}))
	t.Cleanup(prim.Close)

	sec := testAdmin(t)
	sec.cfg.Role = roleSecondary
	if err := sec.db.SetMeta(store.MetaPrimaryURL, prim.URL); err != nil {
		t.Fatal(err)
	}
	_ = sec.db.SetMeta(store.MetaAdvertise, "192.168.8.53:53")
	tok := loginToken(t, sec)
	body, _ := json.Marshal(map[string]string{"dns": "203.0.113.10"})
	r := httptest.NewRequest(http.MethodPut, "/api/v1/cluster/primary-dns", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	sec.mux.ServeHTTP(w, r)
	if called {
		t.Fatal("primary dns override was proxied")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.Bytes())
	}
	if got := sec.primaryTransferFrom(); len(got) != 1 || got[0] != "203.0.113.10:53" {
		t.Fatalf("effective %+v", got)
	}
	clear, _ := json.Marshal(map[string]string{"dns": ""})
	r = httptest.NewRequest(http.MethodPut, "/api/v1/cluster/primary-dns", bytes.NewReader(clear))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	sec.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", w.Code, w.Body.Bytes())
	}
	if got := sec.primaryTransferFrom(); len(got) != 1 || got[0] != "192.168.8.53:53" {
		t.Fatalf("cleared %+v", got)
	}
}

func TestNormalizeMemberAPIURL(t *testing.T) {
	got, err := normalizeMemberAPIURL(" https://ns3.dns.rwx.dev/ ")
	if err != nil || got != "https://ns3.dns.rwx.dev" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := normalizeMemberAPIURL("ns3.dns.rwx.dev"); err == nil {
		t.Fatal("scheme required")
	}
	if _, err := normalizeMemberAPIURL("http://127.0.0.1:8080"); err == nil {
		t.Fatal("loopback")
	}
}

func TestEnsureSelfMemberKeepsStoredAPIURL(t *testing.T) {
	a := testAdmin(t)
	id, err := a.db.Meta(store.MetaNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.InsertMember(store.Member{
		ID: id, Name: "ns1", APIURL: "https://ns1.dns.rwx.dev", DNSAddr: "192.168.8.53:53", Role: store.MemberPrimary,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureSelfMember("http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	m, err := a.db.GetMember(id)
	if err != nil || m.APIURL != "https://ns1.dns.rwx.dev" {
		t.Fatalf("clobbered api url: %+v %v", m, err)
	}
	if m.DNSAddr != "192.168.8.53:53" {
		t.Fatalf("clobbered dns addr: %+v", m)
	}
}

func TestPublicAPIURLSkipsLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/connect", nil)
	r.Host = "127.0.0.1:8080"
	if u := publicAPIURL(r, "", "192.168.8.54:53"); u != "http://192.168.8.54:8080" {
		t.Fatalf("got %q", u)
	}
	r.Host = "192.168.8.54:8080"
	if u := publicAPIURL(r, "", ""); u != "http://192.168.8.54:8080" {
		t.Fatalf("host %q", u)
	}
	if u := publicAPIURL(r, "http://127.0.0.1:8080", "192.168.8.54:53"); u != "http://192.168.8.54:8080" {
		t.Fatalf("explicit loopback %q", u)
	}
}

func TestFullSnapshotIncludesZonereg(t *testing.T) {
	a := testAdmin(t)
	if err := zonereg.RegisterPrimary(snapPrimary{origin: "corefile.example.", path: "/etc/coredns/db.corefile.example"}); err != nil {
		t.Fatal(err)
	}
	snap, err := a.fullSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Password == nil || !*snap.Password {
		t.Fatal("password flag")
	}
	found := false
	for _, z := range snap.Zones {
		if z.Origin == "corefile.example." {
			found = true
		}
	}
	if !found {
		t.Fatalf("zones %+v", snap.Zones)
	}
}

func TestClusterRosterIncludesPrimary(t *testing.T) {
	a := testAdmin(t)
	token := loginToken(t, a)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Host = "ns1.example:8443"
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("get cluster: %d %s", w.Code, w.Body.Bytes())
	}
	var got struct {
		Members []clusterMemberJSON `json:"members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 1 || got.Members[0].Role != store.MemberPrimary || !got.Members[0].Self {
		t.Fatalf("want this primary in roster, got %+v", got.Members)
	}
	if got.Members[0].APIURL != "http://ns1.example:8443" {
		t.Fatalf("primary api_url %q", got.Members[0].APIURL)
	}

	mint := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/join-tokens", bytes.NewReader([]byte(`{"ttl":"1h"}`)))
	mint.Header.Set("Authorization", "Bearer "+token)
	mint.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, mint)
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tok); err != nil || tok.Token == "" {
		t.Fatalf("join token: %d %s", w.Code, w.Body.Bytes())
	}

	joinBody, _ := json.Marshal(map[string]string{
		"token": tok.Token, "name": "ns2", "api_url": "http://ns2.example:8443", "dns_addr": "192.0.2.20:53",
	})
	join := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/join", bytes.NewReader(joinBody))
	join.Header.Set("Content-Type", "application/json")
	join.Host = "ns1.example:8443"
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, join)
	if w.Code != http.StatusCreated {
		t.Fatalf("join: %d %s", w.Code, w.Body.Bytes())
	}
	var joined struct {
		MemberID string         `json:"member_id"`
		Snapshot store.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &joined); err != nil {
		t.Fatal(err)
	}
	if joined.MemberID == "" || len(joined.Snapshot.Members) != 2 {
		t.Fatalf("join snapshot roster: %+v", joined)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 2 {
		t.Fatalf("roster after join: %+v", got.Members)
	}
	secID := ""
	for _, m := range got.Members {
		if m.Role == store.MemberSecondary {
			secID = m.ID
			if m.Name != "ns2" {
				t.Fatalf("join name %q", m.Name)
			}
		}
	}
	patch, _ := json.Marshal(map[string]string{"name": "ns3.dns.rwx.dev"})
	pr := httptest.NewRequest(http.MethodPatch, "/api/v1/cluster/members/"+secID, bytes.NewReader(patch))
	pr.Header.Set("Authorization", "Bearer "+token)
	pr.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, pr)
	if w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.Bytes())
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range got.Members {
		if m.ID == secID && m.Name == "ns3.dns.rwx.dev" {
			found = true
		}
	}
	if !found {
		t.Fatalf("renamed roster %+v", got.Members)
	}

	dnsPatch, _ := json.Marshal(map[string]string{"dns_addr": "203.0.113.20"})
	pr = httptest.NewRequest(http.MethodPatch, "/api/v1/cluster/members/"+secID, bytes.NewReader(dnsPatch))
	pr.Header.Set("Authorization", "Bearer "+token)
	pr.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, pr)
	if w.Code != http.StatusOK {
		t.Fatalf("dns addr: %d %s", w.Code, w.Body.Bytes())
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, m := range got.Members {
		if m.ID == secID && m.DNSAddr == "203.0.113.20:53" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dns addr roster %+v", got.Members)
	}
	badDNS, _ := json.Marshal(map[string]string{"dns_addr": "127.0.0.1:53"})
	pr = httptest.NewRequest(http.MethodPatch, "/api/v1/cluster/members/"+secID, bytes.NewReader(badDNS))
	pr.Header.Set("Authorization", "Bearer "+token)
	pr.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, pr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("loopback dns: %d %s", w.Code, w.Body.Bytes())
	}

	apiPatch, _ := json.Marshal(map[string]string{"api_url": "https://ns3.dns.rwx.dev"})
	pr = httptest.NewRequest(http.MethodPatch, "/api/v1/cluster/members/"+secID, bytes.NewReader(apiPatch))
	pr.Header.Set("Authorization", "Bearer "+token)
	pr.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, pr)
	if w.Code != http.StatusOK {
		t.Fatalf("api url: %d %s", w.Code, w.Body.Bytes())
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, m := range got.Members {
		if m.ID == secID && m.APIURL == "https://ns3.dns.rwx.dev" {
			found = true
		}
	}
	if !found {
		t.Fatalf("api url roster %+v", got.Members)
	}
	bad, _ := json.Marshal(map[string]string{"api_url": "http://127.0.0.1:8080"})
	pr = httptest.NewRequest(http.MethodPatch, "/api/v1/cluster/members/"+secID, bytes.NewReader(bad))
	pr.Header.Set("Authorization", "Bearer "+token)
	pr.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, pr)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("loopback api url: %d %s", w.Code, w.Body.Bytes())
	}

	if err := a.db.SetMeta(store.MetaNodeName, "ns1.dns.rwx.dev"); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Members {
		if m.Role == store.MemberPrimary && m.Name != "ns1.dns.rwx.dev" {
			t.Fatalf("primary name %q", m.Name)
		}
	}
	var sawPrimary, sawSecondary bool
	for _, m := range got.Members {
		if m.Role == store.MemberPrimary {
			sawPrimary = true
			if m.Self != true {
				t.Fatalf("primary should be self: %+v", m)
			}
		}
		if m.Role == store.MemberSecondary && m.Name == "ns3.dns.rwx.dev" {
			sawSecondary = true
		}
	}
	if !sawPrimary || !sawSecondary {
		t.Fatalf("missing roles in %+v", got.Members)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/v1/cluster/members/"+got.Members[0].ID, nil)
	del.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, del)
	if w.Code != http.StatusConflict {
		t.Fatalf("delete primary: %d %s", w.Code, w.Body.Bytes())
	}
}

func TestApplySnapshotReloadsSecondaryRecords(t *testing.T) {
	a := testAdmin(t)
	a.cfg.Role = roleSecondary
	a.cfg.PrimaryDNS = "127.0.0.1:1"
	t.Cleanup(func() {
		if a.secondaries == nil {
			return
		}
		for _, origin := range a.secondaries.Origins() {
			_ = a.secondaries.StopOrigin(origin)
		}
	})

	soa1, err := dns.NewRR("example.com. 300 IN SOA ns.example.com. host.example.com. 1 3600 600 86400 60")
	if err != nil {
		t.Fatal(err)
	}
	ns, err := dns.NewRR("example.com. 300 IN NS ns.example.com.")
	if err != nil {
		t.Fatal(err)
	}
	www, err := dns.NewRR("www.example.com. 60 IN A 192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	snap := store.Snapshot{
		Generation: 1,
		Zones:      []store.ZoneRow{{Origin: "example.com.", Kind: zonereg.KindPrimary, Source: zonereg.SourceAdmin}},
		Records:    store.RecordsFromRRs("example.com.", "", []dns.RR{soa1, ns, www}),
	}
	if err := a.applySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if a.secondaries == nil {
		t.Fatal("expected secondary engine")
	}
	if !rrsHaveA(a.secondaries.RecordsFor("example.com."), "192.0.2.10") {
		t.Fatalf("snapshot records not loaded: %v", a.secondaries.RecordsFor("example.com."))
	}

	soa2, err := dns.NewRR("example.com. 300 IN SOA ns.example.com. host.example.com. 2 3600 600 86400 60")
	if err != nil {
		t.Fatal(err)
	}
	snap.Generation = 2
	snap.Records = store.RecordsFromRRs("example.com.", "", []dns.RR{soa2, ns})
	if err := a.applySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	got := a.secondaries.RecordsFor("example.com.")
	if rrsHaveA(got, "192.0.2.10") {
		t.Fatalf("deleted A still served: %v", got)
	}
	if soa := soaOf(got); soa == nil || soa.Serial != 2 {
		t.Fatalf("expected serial 2, got %+v", soa)
	}

	snap.Generation = 3
	snap.Zones = append(snap.Zones, store.ZoneRow{Origin: "empty.test.", Kind: zonereg.KindPrimary, Source: zonereg.SourceAdmin})
	if err := a.applySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	for _, origin := range a.secondaries.Origins() {
		if origin == "empty.test." {
			t.Fatal("empty zone without SOA should not be transferred")
		}
	}
}

func TestClusterConnectFlushBeforeApply(t *testing.T) {
	prim := testAdmin(t)
	sec := testAdmin(t)
	primSrv := httptest.NewServer(prim.mux)
	t.Cleanup(primSrv.Close)
	secSrv := httptest.NewServer(sec.mux)
	t.Cleanup(secSrv.Close)

	ptok := loginToken(t, prim)
	mint := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/join-tokens", bytes.NewReader([]byte(`{"ttl":"1h"}`)))
	mint.Header.Set("Authorization", "Bearer "+ptok)
	mint.Header.Set("Content-Type", "application/json")
	mw := httptest.NewRecorder()
	prim.mux.ServeHTTP(mw, mint)
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(mw.Body.Bytes(), &tok); err != nil || tok.Token == "" {
		t.Fatalf("mint: %d %s", mw.Code, mw.Body.Bytes())
	}

	flushed := make(chan struct{})
	unblock := make(chan struct{})
	t.Cleanup(func() { testAfterConnectFlush = nil })
	testAfterConnectFlush = func() {
		close(flushed)
		<-unblock
	}

	stok := loginToken(t, sec)
	body, _ := json.Marshal(map[string]string{
		"url": primSrv.URL, "token": tok.Token, "dns": "192.0.2.20:53", "name": "ns2",
	})
	type result struct {
		code int
		body []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, secSrv.URL+"/api/v1/cluster/connect", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+stok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- result{code: resp.StatusCode, body: b}
	}()

	select {
	case <-flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("200 was not flushed before applySnapshot")
	}

	nr, _ := http.NewRequest(http.MethodGet, secSrv.URL+"/api/v1/node", nil)
	nr.Header.Set("Authorization", "Bearer "+stok)
	nresp, err := http.DefaultClient.Do(nr)
	if err != nil {
		t.Fatal(err)
	}
	nb, _ := io.ReadAll(nresp.Body)
	_ = nresp.Body.Close()
	if nresp.StatusCode != http.StatusOK || !bytes.Contains(nb, []byte(`"role":"secondary"`)) {
		close(unblock)
		t.Fatalf("GET /node during apply: %d %s", nresp.StatusCode, nb)
	}

	close(unblock)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.code != http.StatusOK || !bytes.Contains(got.body, []byte(`"status":"joined"`)) {
		t.Fatalf("connect after apply: %d %s", got.code, got.body)
	}
}

func rrsHaveA(rrs []dns.RR, ip string) bool {
	for _, rr := range rrs {
		if a, ok := rr.(*dns.A); ok && a.A.String() == ip {
			return true
		}
	}
	return false
}
