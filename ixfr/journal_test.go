package ixfr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/miekg/dns"
)

func mustRR(t *testing.T, s string) dns.RR {
	t.Helper()
	r, err := dns.NewRR(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func zone(serial uint32, extra ...dns.RR) []dns.RR {
	soa, _ := dns.NewRR("example.org. 60 IN SOA ns.example.org. host.example.org. 0 30 15 600 30")
	soa.(*dns.SOA).Serial = serial
	ns, _ := dns.NewRR("example.org. 60 IN NS ns.example.org.")
	a, _ := dns.NewRR("ns.example.org. 60 IN A 192.0.2.1")
	www, _ := dns.NewRR("www.example.org. 60 IN A 192.0.2.80")
	out := []dns.RR{soa, ns, a, www}
	return append(out, extra...)
}

func drain(t *testing.T, x *IXFR, serial uint32) []dns.RR {
	t.Helper()
	ch, err := x.Transfer("example.org.", serial)
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	var out []dns.RR
	for batch := range ch {
		out = append(out, batch...)
	}
	return out
}

func TestDiffAddDeleteTTL(t *testing.T) {
	old := zone(1)
	txt := mustRR(t, `_acme-challenge.example.org. 60 IN TXT "tok"`)
	newer := append(zone(2), txt)
	added := diffMissing(newer, old)
	if len(added) != 1 {
		t.Fatalf("added = %d, want 1", len(added))
	}
	deleted := diffMissing(old, newer)
	if len(deleted) != 0 {
		t.Fatalf("deleted = %d, want 0", len(deleted))
	}

	ttl := zone(1)
	ttl[len(ttl)-1].Header().Ttl = 5
	if n := diffMissing(ttl, zone(1)); len(n) != 1 {
		t.Fatalf("TTL change should count as add, got %d", len(n))
	}
}

func TestIXFRStreamMatchesRFC1995(t *testing.T) {
	dir := t.TempDir()
	x := &IXFR{Zone: "example.org.", history: 8, path: filepath.Join(dir, "j.ixfr")}
	gen1 := zone(1)
	if err := x.Register("example.org.", x.path, gen1); err != nil {
		t.Fatal(err)
	}
	txt := mustRR(t, `_acme-challenge.example.org. 60 IN TXT "tok"`)
	gen2 := append(zone(2), txt)
	if err := x.Commit(gen1, gen2); err != nil {
		t.Fatal(err)
	}

	rrs := drain(t, x, 1)
	if len(rrs) < 4 {
		t.Fatalf("short IXFR: %v", rrs)
	}
	soa0, ok := rrs[0].(*dns.SOA)
	if !ok || soa0.Serial != 2 {
		t.Fatalf("envelope SOA = %v", rrs[0])
	}
	soaOld, ok := rrs[1].(*dns.SOA)
	if !ok || soaOld.Serial != 1 {
		t.Fatalf("delete SOA = %v, want serial 1", rrs[1])
	}
	// empty delete section, then add SOA serial 2, then TXT, then closing SOA
	var sawTXT, sawWWW, sawNS bool
	innerOld := 0
	for _, rr := range rrs {
		switch v := rr.(type) {
		case *dns.SOA:
			if v.Serial == 1 {
				innerOld++
			}
		case *dns.TXT:
			sawTXT = true
		case *dns.A:
			if v.Hdr.Name == "www.example.org." {
				sawWWW = true
			}
			if v.Hdr.Name == "ns.example.org." {
				sawNS = true
			}
		}
	}
	if innerOld != 1 {
		t.Fatalf("want exactly one inner SOA serial 1, got %d", innerOld)
	}
	if !sawTXT {
		t.Fatal("IXFR missing added TXT")
	}
	if sawWWW || sawNS {
		t.Fatal("IXFR included unchanged records (not a delta)")
	}
	last, ok := rrs[len(rrs)-1].(*dns.SOA)
	if !ok || last.Serial != 2 {
		t.Fatalf("closing SOA = %v", rrs[len(rrs)-1])
	}

	up := drain(t, x, 2)
	if len(up) != 1 {
		t.Fatalf("uptodate IXFR len = %d, want 1", len(up))
	}

	full := drain(t, x, 0)
	var fullTXT, fullWWW bool
	for _, rr := range full {
		switch v := rr.(type) {
		case *dns.TXT:
			fullTXT = true
		case *dns.A:
			if v.Hdr.Name == "www.example.org." {
				fullWWW = true
			}
		}
	}
	if !fullTXT || !fullWWW {
		t.Fatal("AXFR missing records")
	}
}

func TestHistoryCapFallsBackToAXFR(t *testing.T) {
	dir := t.TempDir()
	x := &IXFR{Zone: "example.org.", history: 1, path: filepath.Join(dir, "j.ixfr")}
	g := zone(1)
	if err := x.Register("example.org.", x.path, g); err != nil {
		t.Fatal(err)
	}
	for ser := uint32(2); ser <= 4; ser++ {
		next := zone(ser)
		next = append(next, mustRR(t, `_acme-challenge.example.org. 60 IN TXT "n"`))
		next[len(next)-1].(*dns.TXT).Txt = []string{string(rune('a' + ser))}
		if err := x.Commit(g, next); err != nil {
			t.Fatal(err)
		}
		g = next
	}
	// history 1: only 3→4 remains. IXFR from 1 must AXFR-fallback (includes www).
	rrs := drain(t, x, 1)
	var sawWWW bool
	for _, rr := range rrs {
		if a, ok := rr.(*dns.A); ok && a.Hdr.Name == "www.example.org." {
			sawWWW = true
		}
	}
	if !sawWWW {
		t.Fatal("expected AXFR fallback to include www")
	}
}

func TestJournalPersistsAndReconciles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j.ixfr")
	x := &IXFR{Zone: "example.org.", history: 8, path: path}
	g1 := zone(1)
	if err := x.Register("example.org.", path, g1); err != nil {
		t.Fatal(err)
	}
	txt := mustRR(t, `_acme-challenge.example.org. 60 IN TXT "tok"`)
	g2 := append(zone(2), txt)
	if err := x.Commit(g1, g2); err != nil {
		t.Fatal(err)
	}

	x2 := &IXFR{Zone: "example.org.", history: 8, path: path}
	if err := x2.Register("example.org.", path, g2); err != nil {
		t.Fatal(err)
	}
	rrs := drain(t, x2, 1)
	var inner bool
	for _, rr := range rrs {
		if soa, ok := rr.(*dns.SOA); ok && soa.Serial == 1 {
			inner = true
		}
	}
	if !inner {
		t.Fatal("reloaded journal lost increment 1→2")
	}

	// Extra increment after current serial is dropped.
	g3 := append(zone(3), txt)
	if err := x.Commit(g2, g3); err != nil {
		t.Fatal(err)
	}
	x3 := &IXFR{Zone: "example.org.", history: 8, path: path}
	if err := x3.Register("example.org.", path, g2); err != nil { // zone file still serial 2
		t.Fatal(err)
	}
	if n := len(x3.journal.incs); n != 1 {
		t.Fatalf("reconcile kept %d increments, want 1 (drop 2→3)", n)
	}
}

func TestMissingJournalIsEmptyHistory(t *testing.T) {
	x := &IXFR{Zone: "example.org.", history: 8}
	if err := x.Register("example.org.", filepath.Join(t.TempDir(), "nope.ixfr"), zone(2)); err != nil {
		t.Fatal(err)
	}
	rrs := drain(t, x, 1)
	var sawWWW bool
	for _, rr := range rrs {
		if a, ok := rr.(*dns.A); ok && a.Hdr.Name == "www.example.org." {
			sawWWW = true
		}
	}
	if !sawWWW {
		t.Fatal("missing journal should AXFR-fallback")
	}
}

func TestCommitManyRRTypesRoundTrip(t *testing.T) {
	extras := []string{
		`it-aaaa.example.org. 60 IN AAAA 2001:db8::50`,
		`it-txt.example.org. 60 IN TXT "dyn-txt"`,
		`it-mx.example.org. 60 IN MX 15 mx.example.org.`,
		`it-cname.example.org. 60 IN CNAME www.example.org.`,
		`it-srv.example.org. 60 IN SRV 1 2 443 svc.example.org.`,
		`it-caa.example.org. 60 IN CAA 0 issue "letsencrypt.org"`,
		`it-sshfp.example.org. 60 IN SSHFP 1 1 0123456789abcdef0123456789abcdef01234567`,
		`it-tlsa.example.org. 60 IN TLSA 3 1 1 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`,
		`it-ds.example.org. 60 IN DS 23456 13 2 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`,
		`it-naptr.example.org. 60 IN NAPTR 20 10 "u" "E2U+sip" "!^.*$!sip:ixfr@example.org!" .`,
		`it-uri.example.org. 60 IN URI 10 1 "https://ixfr.example.org/path"`,
		`it-hinfo.example.org. 60 IN HINFO "ARM64" "LINUX"`,
		`it-rp.example.org. 60 IN RP hostmaster.example.org. rp.example.org.`,
		`it-https.example.org. 60 IN HTTPS 1 . alpn="h2"`,
		`it-svcb.example.org. 60 IN SVCB 1 svc.example.org. alpn="h2"`,
		`it-eui48.example.org. 60 IN EUI48 01-23-45-67-89-ab`,
		`it-eui64.example.org. 60 IN EUI64 01-23-45-67-89-ab-cd-ef`,
		`it-afsdb.example.org. 60 IN AFSDB 1 afs.example.org.`,
		`it-kx.example.org. 60 IN KX 10 kx.example.org.`,
		`it-ptr.example.org. 60 IN PTR www.example.org.`,
		`it-cert.example.org. 60 IN CERT 1 0 0 AA==`,
		`it-cds.example.org. 60 IN CDS 34567 13 2 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`,
	}
	g1 := zone(1)
	g2 := zone(2)
	wantName := map[string]bool{}
	for _, s := range extras {
		rr := mustRR(t, s)
		g2 = append(g2, rr)
		wantName[rr.Header().Name] = true
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "j.ixfr")
	x := &IXFR{Zone: "example.org.", history: 8, path: path}
	if err := x.Register("example.org.", path, g1); err != nil {
		t.Fatal(err)
	}
	if err := x.Commit(g1, g2); err != nil {
		t.Fatal(err)
	}
	rrs := drain(t, x, 1)
	seen := map[string]bool{}
	for _, rr := range rrs {
		seen[rr.Header().Name] = true
	}
	for name := range wantName {
		if !seen[name] {
			t.Errorf("IXFR missing %s", name)
		}
	}
	if seen["www.example.org."] {
		t.Error("IXFR included unchanged www")
	}

	x2 := &IXFR{Zone: "example.org.", history: 8, path: path}
	if err := x2.Register("example.org.", path, g2); err != nil {
		t.Fatal(err)
	}
	rrs = drain(t, x2, 1)
	seen = map[string]bool{}
	for _, rr := range rrs {
		seen[rr.Header().Name] = true
	}
	for name := range wantName {
		if !seen[name] {
			t.Errorf("reloaded journal IXFR missing %s", name)
		}
	}
}

func TestCorruptJournalIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j.ixfr")
	if err := os.WriteFile(path, []byte("this is not a journal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	x := &IXFR{Zone: "example.org.", history: 8, path: path}
	if err := x.Register("example.org.", path, zone(1)); err != nil {
		t.Fatal(err)
	}
	if x.journal == nil {
		t.Fatal("expected empty journal after corrupt file")
	}
	if len(x.journal.incs) != 0 {
		t.Fatalf("corrupt journal kept %d increments", len(x.journal.incs))
	}
}
