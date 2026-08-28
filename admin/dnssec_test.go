package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/response"
	"github.com/coredns/coredns/plugin/test"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

func TestDNSSECEnableSignsAndDNSKEY(t *testing.T) {
	a := testAdmin(t)
	tok := loginToken(t, a)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/zones", strings.NewReader(`{"origin":"example.com.","type":"primary"}`))
	create.Header.Set("Authorization", "Bearer "+tok)
	create.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, create)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create zone: %d %s", w.Code, w.Body.Bytes())
	}

	en := httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./dnssec", nil)
	en.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, en)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body.Bytes())
	}
	var info struct {
		Enabled bool   `json:"enabled"`
		DS      string `json:"ds"`
		DNSKEY  string `json:"dnskey"`
		KeyTag  int    `json:"key_tag"`
		DSData  struct {
			KeyTag     int    `json:"key_tag"`
			Algorithm  int    `json:"algorithm"`
			DigestType int    `json:"digest_type"`
			Digest     string `json:"digest"`
		} `json:"ds_data"`
		KeyData struct {
			Flags     int    `json:"flags"`
			Protocol  int    `json:"protocol"`
			Algorithm int    `json:"algorithm"`
			PublicKey string `json:"public_key"`
		} `json:"key_data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || !info.Enabled || info.DS == "" || info.KeyTag == 0 {
		t.Fatalf("info %+v %v %s", info, err, w.Body.Bytes())
	}
	if info.DSData.KeyTag == 0 || info.DSData.Algorithm != 13 || info.DSData.DigestType != 2 || info.DSData.Digest == "" {
		t.Fatalf("ds_data %+v", info.DSData)
	}
	if info.KeyData.Flags != 257 || info.KeyData.Protocol != 3 || info.KeyData.PublicKey == "" {
		t.Fatalf("key_data %+v", info.KeyData)
	}

	m := new(dns.Msg)
	m.SetQuestion("example.com.", dns.TypeDNSKEY)
	m.SetEdns0(1232, true)
	rw := &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "8.8.8.8"}}
	chain := &adminChain{Admin: a, next: a.Next}
	if _, err := chain.ServeDNS(context.Background(), rw, m); err != nil {
		t.Fatal(err)
	}
	if rw.msg == nil {
		t.Fatal("no DNSKEY response")
	}
	var sawKey, sawSig bool
	for _, rr := range rw.msg.Answer {
		switch rr.(type) {
		case *dns.DNSKEY:
			sawKey = true
		case *dns.RRSIG:
			sawSig = true
		}
	}
	if !sawKey || !sawSig {
		t.Fatalf("DNSKEY DO answer missing key/sig: %v", rw.msg.Answer)
	}

	soa := new(dns.Msg)
	soa.SetQuestion("example.com.", dns.TypeSOA)
	soa.SetEdns0(1232, true)
	rw = &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "8.8.8.8"}}
	if _, err := a.ServeDNS(context.Background(), rw, soa); err != nil {
		t.Fatal(err)
	}
	if rw.msg == nil {
		t.Fatal("no SOA response")
	}
	var sawSOA, sawSOAsig bool
	for _, rr := range rw.msg.Answer {
		switch rr.(type) {
		case *dns.SOA:
			sawSOA = true
		case *dns.RRSIG:
			sawSOAsig = true
		}
	}
	if !sawSOA || !sawSOAsig {
		t.Fatalf("SOA DO answer missing soa/sig: %v", rw.msg.Answer)
	}

	plain := new(dns.Msg)
	plain.SetQuestion("example.com.", dns.TypeSOA)
	rw = &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "8.8.8.8"}}
	if _, err := a.ServeDNS(context.Background(), rw, plain); err != nil {
		t.Fatal(err)
	}
	for _, rr := range rw.msg.Answer {
		if _, ok := rr.(*dns.RRSIG); ok {
			t.Fatal("unsigned query should not get RRSIG")
		}
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/v1/zones/example.com./dnssec", nil)
	del.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, del)
	if w.Code != http.StatusNoContent {
		t.Fatalf("disable: %d %s", w.Code, w.Body.Bytes())
	}
	rw = &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "8.8.8.8"}}
	if _, err := a.ServeDNS(context.Background(), rw, m); err != nil {
		t.Fatal(err)
	}
	for _, rr := range rw.msg.Answer {
		if _, ok := rr.(*dns.DNSKEY); ok {
			t.Fatal("DNSKEY still served after disable")
		}
	}
}

func TestBlackLieNSECOmitsQueriedType(t *testing.T) {
	mustOmit := func(bitmap []uint16, t uint16) bool {
		for _, x := range bitmap {
			if x == t {
				return false
			}
		}
		return true
	}
	has := func(bitmap []uint16, t uint16) bool { return !mustOmit(bitmap, t) }

	soaQ := new(dns.Msg)
	soaQ.SetQuestion("_acme-challenge.test.example.com.", dns.TypeSOA)
	st := request.Request{Req: soaQ, Zone: "example.com."}
	nsec := blackLieNSEC(st, "example.com.", response.NoData)
	if nsec == nil {
		t.Fatal("expected NSEC")
	}
	if !mustOmit(nsec.TypeBitMap, dns.TypeSOA) {
		t.Fatalf("non-apex SOA NODATA must not claim SOA: %v", nsec.TypeBitMap)
	}
	if has(nsec.TypeBitMap, dns.TypeNS) || has(nsec.TypeBitMap, dns.TypeDNSKEY) {
		t.Fatalf("non-apex NSEC must not claim NS/DNSKEY: %v", nsec.TypeBitMap)
	}
	if !has(nsec.TypeBitMap, dns.TypeNSEC) || !has(nsec.TypeBitMap, dns.TypeRRSIG) {
		t.Fatalf("NSEC/RRSIG missing: %v", nsec.TypeBitMap)
	}

	txtQ := new(dns.Msg)
	txtQ.SetQuestion("_acme-challenge.test.example.com.", dns.TypeTXT)
	st = request.Request{Req: txtQ, Zone: "example.com."}
	nsec = blackLieNSEC(st, "example.com.", response.NameError)
	if !mustOmit(nsec.TypeBitMap, dns.TypeTXT) {
		t.Fatalf("TXT NXDOMAIN must not claim TXT: %v", nsec.TypeBitMap)
	}

	apexTXT := new(dns.Msg)
	apexTXT.SetQuestion("example.com.", dns.TypeTXT)
	st = request.Request{Req: apexTXT, Zone: "example.com."}
	nsec = blackLieNSEC(st, "example.com.", response.NoData)
	if !mustOmit(nsec.TypeBitMap, dns.TypeTXT) {
		t.Fatalf("apex TXT NODATA must not claim TXT: %v", nsec.TypeBitMap)
	}
	if !has(nsec.TypeBitMap, dns.TypeSOA) || !has(nsec.TypeBitMap, dns.TypeDNSKEY) {
		t.Fatalf("apex NSEC should still claim SOA/DNSKEY: %v", nsec.TypeBitMap)
	}
}

func TestDNSSECNoDataOmitsQueriedType(t *testing.T) {
	a := testAdmin(t)
	tok := loginToken(t, a)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/zones", strings.NewReader(`{"origin":"example.com.","type":"primary"}`))
	create.Header.Set("Authorization", "Bearer "+tok)
	create.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, create)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create zone: %d %s", w.Code, w.Body.Bytes())
	}
	en := httptest.NewRequest(http.MethodPost, "/api/v1/zones/example.com./dnssec", nil)
	en.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, en)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body.Bytes())
	}

	m := new(dns.Msg)
	m.SetQuestion("_acme-challenge.test.example.com.", dns.TypeSOA)
	m.SetEdns0(1232, true)
	rw := &captureRW{ResponseWriter: test.ResponseWriter{RemoteIP: "8.8.8.8"}}
	chain := &adminChain{Admin: a, next: a.Next}
	if _, err := chain.ServeDNS(context.Background(), rw, m); err != nil {
		t.Fatal(err)
	}
	if rw.msg == nil {
		t.Fatal("no response")
	}
	if rw.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode %d", rw.msg.Rcode)
	}
	var nsec *dns.NSEC
	for _, rr := range rw.msg.Ns {
		if n, ok := rr.(*dns.NSEC); ok {
			nsec = n
		}
	}
	if nsec == nil {
		t.Fatalf("expected NSEC in authority: %v", rw.msg.Ns)
	}
	for _, b := range nsec.TypeBitMap {
		if b == dns.TypeSOA {
			t.Fatalf("SOA present in NSEC bitmap %v", nsec.TypeBitMap)
		}
	}
}

func TestDNSSECSnapshotRoundTrip(t *testing.T) {
	a := testAdmin(t)
	tok := loginToken(t, a)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/zones", strings.NewReader(`{"origin":"sign.test.","type":"primary"}`))
	create.Header.Set("Authorization", "Bearer "+tok)
	create.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, create)
	en := httptest.NewRequest(http.MethodPost, "/api/v1/zones/sign.test./dnssec", nil)
	en.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, en)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body.Bytes())
	}
	snap, err := a.db.Snapshot()
	if err != nil || len(snap.DNSSECKeys) != 1 {
		t.Fatalf("snap keys %d %v", len(snap.DNSSECKeys), err)
	}
	a2 := testAdmin(t)
	if err := a2.db.ApplySnapshot(snap); err != nil {
		t.Fatal(err)
	}
	a2.rebuildSigner()
	got, err := a2.db.GetDNSSECKeys("sign.test.")
	if err != nil || len(got) != 1 || got[0].Private == "" {
		t.Fatalf("replica %+v %v", got, err)
	}
}
