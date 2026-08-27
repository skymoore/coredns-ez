package dnsupdatepersist

import (
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

func TestApplyAddsAndDeletes(t *testing.T) {
	t.Cleanup(zonereg.ResetForTest)
	d := newTestPlugin(t, nil)
	add := rr(t, "added.example.org. 60 IN A 192.0.2.50")
	if err := d.Apply([]dns.RR{add}, nil); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, got := range d.Records() {
		if strings.Contains(got.String(), "192.0.2.50") {
			found = true
		}
	}
	if !found {
		t.Fatal("added A missing")
	}
	del := dns.Copy(add)
	del.Header().Class = dns.ClassNONE
	del.Header().Ttl = 0
	if err := d.Apply(nil, []dns.RR{del}); err != nil {
		t.Fatal(err)
	}
	for _, got := range d.Records() {
		if strings.Contains(got.String(), "192.0.2.50") {
			t.Fatal("delete did not remove A")
		}
	}
}

func nsTargets(rrs []dns.RR) []string {
	var out []string
	for _, rr := range rrs {
		ns, ok := rr.(*dns.NS)
		if !ok || !strings.EqualFold(ns.Hdr.Name, "example.org.") {
			continue
		}
		out = append(out, strings.ToLower(dns.CanonicalName(ns.Ns)))
	}
	return out
}

func TestApplyReplacesApexNS(t *testing.T) {
	t.Cleanup(zonereg.ResetForTest)
	d := newTestPlugin(t, nil)

	want := []dns.RR{
		rr(t, "example.org. 300 IN NS ns3.dns.example.org."),
		rr(t, "example.org. 300 IN NS ns1.dns.example.org."),
	}
	wipe := &dns.ANY{Hdr: dns.RR_Header{Name: "example.org.", Rrtype: dns.TypeNS, Class: dns.ClassANY, Ttl: 0}}
	if err := d.Apply(want, []dns.RR{wipe}); err != nil {
		t.Fatal(err)
	}
	got := nsTargets(d.Records())
	if len(got) != 2 {
		t.Fatalf("apex NS count = %d %v, want 2", len(got), got)
	}
	for _, need := range []string{"ns3.dns.example.org.", "ns1.dns.example.org."} {
		found := false
		for _, n := range got {
			if n == need {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %s in %v", need, got)
		}
	}
	for _, stale := range []string{"ns.example.org.", "ns2.example.org."} {
		for _, n := range got {
			if n == stale {
				t.Fatalf("original NS %s still present: %v", stale, got)
			}
		}
	}
}

func TestApplyReplaceApexNSOverlapAndEmpty(t *testing.T) {
	t.Cleanup(zonereg.ResetForTest)
	d := newTestPlugin(t, nil)

	keep := rr(t, "example.org. 300 IN NS ns.example.org.")
	add := rr(t, "example.org. 300 IN NS ns3.example.org.")
	wipe := &dns.ANY{Hdr: dns.RR_Header{Name: "example.org.", Rrtype: dns.TypeNS, Class: dns.ClassANY, Ttl: 0}}
	if err := d.Apply([]dns.RR{keep, add}, []dns.RR{wipe}); err != nil {
		t.Fatal(err)
	}
	got := nsTargets(d.Records())
	if len(got) != 2 {
		t.Fatalf("overlap replace count = %d %v, want 2", len(got), got)
	}

	if err := d.Apply(nil, []dns.RR{wipe}); err == nil {
		t.Fatal("empty apex NS replace succeeded")
	}
	if n := len(nsTargets(d.Records())); n != 2 {
		t.Fatalf("empty wipe changed NS count to %d", n)
	}
}

func TestApplyReplacesApexSOA(t *testing.T) {
	t.Cleanup(zonereg.ResetForTest)
	d := newTestPlugin(t, nil)
	before := soaOf(d.Records())
	if before == nil {
		t.Fatal("seed has no SOA")
	}

	want := rr(t, "example.org. 300 IN SOA ns1.example.org. hostmaster.example.org. 100 7200 1200 86400 60")
	wipe := &dns.ANY{Hdr: dns.RR_Header{Name: "example.org.", Rrtype: dns.TypeSOA, Class: dns.ClassANY, Ttl: 0}}
	if err := d.Apply([]dns.RR{want}, []dns.RR{wipe}); err != nil {
		t.Fatal(err)
	}

	var soas []*dns.SOA
	for _, r := range d.Records() {
		if s, ok := r.(*dns.SOA); ok {
			soas = append(soas, s)
		}
	}
	if len(soas) != 1 {
		t.Fatalf("SOA count = %d, want 1", len(soas))
	}
	got := soas[0]
	if !strings.EqualFold(got.Ns, "ns1.example.org.") {
		t.Errorf("MNAME = %s, want ns1.example.org.", got.Ns)
	}
	if !strings.EqualFold(got.Mbox, "hostmaster.example.org.") {
		t.Errorf("RNAME = %s, want hostmaster.example.org.", got.Mbox)
	}
	if got.Refresh != 7200 || got.Minttl != 60 {
		t.Errorf("timers refresh=%d minimum=%d", got.Refresh, got.Minttl)
	}
	if got.Serial <= before.Serial {
		t.Errorf("serial did not advance: %d -> %d", before.Serial, got.Serial)
	}

	if err := d.Apply(nil, []dns.RR{wipe}); err == nil {
		t.Fatal("empty SOA replace succeeded")
	}
}

func TestApplyEditsSOAAndNSDespiteMutableAllowlist(t *testing.T) {
	t.Cleanup(zonereg.ResetForTest)
	d := newTestPlugin(t, map[uint16]bool{dns.TypeA: true, dns.TypeAAAA: true, dns.TypeTXT: true})
	nsWipe := &dns.ANY{Hdr: dns.RR_Header{Name: "example.org.", Rrtype: dns.TypeNS, Class: dns.ClassANY, Ttl: 0}}
	ns := rr(t, "example.org. 300 IN NS ns1.k8s.example.org.")
	if err := d.Apply([]dns.RR{ns}, []dns.RR{nsWipe}); err != nil {
		t.Fatalf("HTTP replace apex NS with mutable A/AAAA/TXT: %v", err)
	}
	got := nsTargets(d.Records())
	if len(got) != 1 || got[0] != "ns1.k8s.example.org." {
		t.Fatalf("NS after HTTP replace = %v", got)
	}
	soaWipe := &dns.ANY{Hdr: dns.RR_Header{Name: "example.org.", Rrtype: dns.TypeSOA, Class: dns.ClassANY, Ttl: 0}}
	soa := rr(t, "example.org. 300 IN SOA ns1.k8s.example.org. sky.example.org. 100 3600 900 86400 300")
	if err := d.Apply([]dns.RR{soa}, []dns.RR{soaWipe}); err != nil {
		t.Fatalf("HTTP replace SOA with mutable A/AAAA/TXT: %v", err)
	}
	cur := soaOf(d.Records())
	if cur == nil || !strings.EqualFold(cur.Ns, "ns1.k8s.example.org.") {
		t.Fatalf("SOA MNAME = %v", cur)
	}
}
