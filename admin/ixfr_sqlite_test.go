package admin

import (
	"testing"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/ixfr"
)

func TestIXFRJournalSQLiteRoundTrip(t *testing.T) {
	a := testAdmin(t)
	x := ixfr.New("example.org.", 8)
	x.SetBackend(sqliteJournal{db: a.db})

	g1 := sqliteIXFRZone(1)
	if err := x.Register("example.org.", g1); err != nil {
		t.Fatal(err)
	}
	txt, err := dns.NewRR(`_acme-challenge.example.org. 60 IN TXT "tok"`)
	if err != nil {
		t.Fatal(err)
	}
	g2 := append(sqliteIXFRZone(2), txt)
	if err := x.Commit(g1, g2); err != nil {
		t.Fatal(err)
	}

	x2 := ixfr.New("example.org.", 8)
	x2.SetBackend(sqliteJournal{db: a.db})
	if err := x2.Register("example.org.", g2); err != nil {
		t.Fatal(err)
	}
	ch, err := x2.Transfer("example.org.", 1)
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	var rrs []dns.RR
	for batch := range ch {
		rrs = append(rrs, batch...)
	}
	var inner, sawTXT bool
	for _, rr := range rrs {
		if soa, ok := rr.(*dns.SOA); ok && soa.Serial == 1 {
			inner = true
		}
		if _, ok := rr.(*dns.TXT); ok {
			sawTXT = true
		}
	}
	if !inner {
		t.Fatal("sqlite journal lost increment 1→2")
	}
	if !sawTXT {
		t.Fatal("IXFR missing committed TXT")
	}

	ch, err = x2.Transfer("example.org.", 0)
	if err != nil {
		t.Fatalf("AXFR: %v", err)
	}
	rrs = nil
	for batch := range ch {
		rrs = append(rrs, batch...)
	}
	if soaOf(rrs) == nil || soaOf(rrs).Serial != 2 {
		t.Fatalf("AXFR serial %+v", soaOf(rrs))
	}
}

func sqliteIXFRZone(serial uint32) []dns.RR {
	soa, _ := dns.NewRR("example.org. 60 IN SOA ns.example.org. host.example.org. 0 30 15 600 30")
	soa.(*dns.SOA).Serial = serial
	ns, _ := dns.NewRR("example.org. 60 IN NS ns.example.org.")
	a, _ := dns.NewRR("ns.example.org. 60 IN A 192.0.2.1")
	www, _ := dns.NewRR("www.example.org. 60 IN A 192.0.2.80")
	return []dns.RR{soa, ns, a, www}
}
