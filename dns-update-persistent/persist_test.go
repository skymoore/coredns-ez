package dnsupdatepersist

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestAddThenReload(t *testing.T) {
	d, store := newTestPluginPersist(t, nil)
	before := serialOf(t, d)

	m := newUpdate(nil, []dns.RR{rr(t, `_acme-challenge.example.org. 60 IN TXT "token-value"`)})
	if got := send(t, d, m); got != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[got])
	}
	if store.n != 1 {
		t.Fatalf("persist calls = %d, want 1", store.n)
	}

	reloaded := reloadFromPersist(t, d, store)
	if serialOf(t, reloaded) != before+1 {
		t.Errorf("reloaded serial = %d, want %d", serialOf(t, reloaded), before+1)
	}
	resp := query(t, reloaded, "_acme-challenge.example.org.", dns.TypeTXT)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("query after reload: rcode=%s answers=%d", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	txt, ok := resp.Answer[0].(*dns.TXT)
	if !ok || txt.Txt[0] != "token-value" {
		t.Errorf("answer = %v, want the added TXT", resp.Answer[0])
	}
}

func TestDeleteThenReload(t *testing.T) {
	d, store := newTestPluginPersist(t, nil)

	del := rr(t, "ns2.example.org. 0 IN A 192.0.2.2")
	del.Header().Class = dns.ClassNONE
	del.Header().Ttl = 0
	if got := send(t, d, newUpdate(nil, []dns.RR{del})); got != dns.RcodeSuccess {
		t.Fatalf("delete: %s", dns.RcodeToString[got])
	}

	reloaded := reloadFromPersist(t, d, store)
	if reloaded.rrsetExists("ns2.example.org.", dns.TypeA) {
		t.Error("deleted A survived reload")
	}
	if !reloaded.rrsetExists("ns.example.org.", dns.TypeA) {
		t.Error("unrelated A disappeared on reload")
	}
}

func TestDeleteRRsetPersistsAndApexSurvives(t *testing.T) {
	d, store := newTestPluginPersist(t, nil)

	delset := &dns.ANY{Hdr: dns.RR_Header{
		Name: "www.example.org.", Rrtype: dns.TypeA, Class: dns.ClassANY, Ttl: 0,
	}}
	if got := send(t, d, newUpdate(nil, []dns.RR{delset})); got != dns.RcodeSuccess {
		t.Fatalf("delete www: %s", dns.RcodeToString[got])
	}

	wipe := &dns.ANY{Hdr: dns.RR_Header{
		Name: "example.org.", Rrtype: dns.TypeANY, Class: dns.ClassANY, Ttl: 0,
	}}
	if got := send(t, d, newUpdate(nil, []dns.RR{wipe})); got != dns.RcodeSuccess {
		t.Fatalf("apex wipe: %s", dns.RcodeToString[got])
	}

	reloaded := reloadFromPersist(t, d, store)
	if soaOf(reloaded.rrs) == nil {
		t.Error("reload lost the SOA")
	}
	if n := countRRset(reloaded.rrs, "example.org.", dns.TypeNS); n != 2 {
		t.Errorf("reload apex NS count = %d, want 2", n)
	}
	if reloaded.rrsetExists("www.example.org.", dns.TypeA) {
		t.Error("www A survived delete+reload")
	}
}

func TestNoopDoesNotPersist(t *testing.T) {
	d, store := newTestPluginPersist(t, nil)
	if got := send(t, d, newUpdate(nil, []dns.RR{rr(t, "www.example.org. 300 IN A 192.0.2.10")})); got != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[got])
	}
	if store.n != 0 {
		t.Errorf("no-op UPDATE persisted %d times", store.n)
	}
}

func TestPersistFailureIsServFailAndLeavesMemory(t *testing.T) {
	d, store := newTestPluginPersist(t, nil)
	beforeSerial := serialOf(t, d)
	store.fail = errors.New("injected persist failure")

	m := newUpdate(nil, []dns.RR{rr(t, `_acme-challenge.example.org. 60 IN TXT "should-not-land"`)})
	if got := send(t, d, m); got != dns.RcodeServerFailure {
		t.Fatalf("rcode = %s, want SERVFAIL", dns.RcodeToString[got])
	}

	if d.rrsetExists("_acme-challenge.example.org.", dns.TypeTXT) {
		t.Error("failed persist still applied the TXT in memory")
	}
	if serialOf(t, d) != beforeSerial {
		t.Errorf("serial moved on a failed persist: %d -> %d", beforeSerial, serialOf(t, d))
	}

	resp := query(t, d, "www.example.org.", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("live query after failed persist: rcode=%s answers=%d", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
}

func TestIncludeLoadsIntoPersist(t *testing.T) {
	dir := t.TempDir()
	extra := filepath.Join(dir, "extra.db")
	if err := os.WriteFile(extra, []byte("foo.example.org. 300 IN A 192.0.2.99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(dir, "db.example.org")
	body := `$ORIGIN example.org.
$TTL 300
@   SOA ns.example.org. admin.example.org. 100 3600 900 86400 300
@   NS  ns.example.org.
$INCLUDE ` + extra + `
`
	if err := os.WriteFile(seed, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	rrs, err := readZone(seed, "example.org.")
	if err != nil {
		t.Fatalf("readZone: %v", err)
	}
	d, err := NewFromRecords("example.org.", rrs, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &memPersist{rrs: d.Records()}
	d.SetPersist(store.save)
	if !d.rrsetExists("foo.example.org.", dns.TypeA) {
		t.Fatal("$INCLUDE was not loaded")
	}

	if got := send(t, d, newUpdate(nil, []dns.RR{rr(t, `_acme-challenge.example.org. 60 IN TXT "x"`)})); got != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[got])
	}
	found := false
	for _, rr := range store.rrs {
		if strings.Contains(rr.String(), "192.0.2.99") {
			found = true
		}
	}
	if !found {
		t.Fatal("included A missing from persist snapshot")
	}

	reloaded := reloadFromPersist(t, d, store)
	if !reloaded.rrsetExists("foo.example.org.", dns.TypeA) {
		t.Error("included A did not survive reload")
	}
	if !reloaded.rrsetExists("_acme-challenge.example.org.", dns.TypeTXT) {
		t.Error("added TXT did not survive reload")
	}
}
