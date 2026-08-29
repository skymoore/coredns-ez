package dnsupdatepersist

import (
	"testing"

	"github.com/miekg/dns"
)

func TestHasCoveringWildcard(t *testing.T) {
	soa := &dns.SOA{
		Hdr:     dns.RR_Header{Name: "rwx.dev.", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 900},
		Ns:      "ns1.rwx.dev.",
		Mbox:    "sky.rwx.dev.",
		Serial:  1,
		Refresh: 900, Retry: 300, Expire: 604800, Minttl: 900,
	}
	wild := &dns.A{
		Hdr: dns.RR_Header{Name: "*.rwx.dev.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 3600},
		A:   []byte{192, 168, 8, 99},
	}
	exact := &dns.A{
		Hdr: dns.RR_Header{Name: "pg.db.rwx.dev.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 3600},
		A:   []byte{192, 168, 8, 90},
	}
	d, err := NewFromRecords("rwx.dev.", []dns.RR{soa, wild, exact}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !d.Answers("pg.db.rwx.dev.", dns.TypeA) {
		t.Fatal("exact A must answer")
	}
	if !d.HasCoveringWildcard("random.rwx.dev.", dns.TypeA) {
		t.Fatal("*.rwx.dev must cover random.rwx.dev")
	}
	if !d.HasCoveringWildcard("pg.db.rwx.dev.", dns.TypeA) {
		t.Fatal("*.rwx.dev must also cover pg.db.rwx.dev")
	}
	if d.HasCoveringWildcard("rwx.dev.", dns.TypeA) {
		t.Fatal("apex is not covered by a wildcard at the apex")
	}
	if d.HasCoveringWildcard("other.test.", dns.TypeA) {
		t.Fatal("out of zone")
	}
}
