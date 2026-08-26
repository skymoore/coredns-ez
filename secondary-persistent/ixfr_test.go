package secondarypersist

import (
	"testing"

	"github.com/coredns/coredns/plugin/test"

	"github.com/miekg/dns"
)

func TestClassifyXFR(t *testing.T) {
	soa1 := test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 1 7200 3600 1209600 3600")
	soa2 := test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 2 7200 3600 1209600 3600")
	a := test.A("www.example.org. 3600 IN A 192.0.2.1")

	kind, err := classifyXFR([]dns.RR{soa2})
	if err != nil || kind != xfrUptodate {
		t.Fatalf("single SOA: kind=%v err=%v", kind, err)
	}

	kind, err = classifyXFR([]dns.RR{soa2, a, soa2})
	if err != nil || kind != xfrAXFR {
		t.Fatalf("axfr: kind=%v err=%v", kind, err)
	}

	kind, err = classifyXFR([]dns.RR{soa2, soa1, a, soa2, soa2})
	if err != nil || kind != xfrIXFR {
		t.Fatalf("ixfr: kind=%v err=%v", kind, err)
	}

	if _, err := classifyXFR(nil); err == nil {
		t.Fatal("expected empty transfer error")
	}
}

func TestParseAndApplyIXFR(t *testing.T) {
	soa1 := test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 1 7200 3600 1209600 3600")
	soa2 := test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 2 7200 3600 1209600 3600")
	oldA := test.A("www.example.org. 3600 IN A 192.0.2.1")
	newA := test.A("www.example.org. 3600 IN A 192.0.2.9")
	ns := test.NS("example.org. 3600 IN NS ns.example.org.")

	current := []dns.RR{soa1, ns, oldA}
	ixfr := []dns.RR{soa2, soa1, oldA, soa2, newA, soa2}

	incs, err := parseIXFR(ixfr)
	if err != nil {
		t.Fatalf("parseIXFR: %v", err)
	}
	if len(incs) != 1 {
		t.Fatalf("expected 1 increment, got %d", len(incs))
	}
	updated, err := applyIncrements(current, incs, soa2)
	if err != nil {
		t.Fatalf("applyIncrements: %v", err)
	}

	var gotA string
	var gotSerial uint32
	for _, rr := range updated {
		switch x := rr.(type) {
		case *dns.SOA:
			gotSerial = x.Serial
		case *dns.A:
			gotA = x.A.String()
		}
	}
	if gotSerial != 2 {
		t.Fatalf("expected serial 2, got %d", gotSerial)
	}
	if gotA != "192.0.2.9" {
		t.Fatalf("expected 192.0.2.9, got %s", gotA)
	}
	for _, rr := range updated {
		if a, ok := rr.(*dns.A); ok && a.A.String() == "192.0.2.1" {
			t.Fatal("deleted A still present")
		}
	}
}

func TestParseIXFRRejectsEqualSerialIncrement(t *testing.T) {
	soa2 := test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 2 7200 3600 1209600 3600")
	a := test.A("www.example.org. 3600 IN A 192.0.2.1")
	_, err := parseIXFR([]dns.RR{soa2, soa2, a, soa2, soa2})
	if err == nil {
		t.Fatal("expected equal-serial increment to fail")
	}
}

func TestLessRFC1982(t *testing.T) {
	if !less(1, 2) {
		t.Fatal("1 < 2")
	}
	if less(2, 2) {
		t.Fatal("2 < 2")
	}
	if !less(^uint32(0), 1) {
		t.Fatal("wrap: max < 1")
	}
	if less(1, ^uint32(0)) {
		t.Fatal("wrap: 1 is newer than max")
	}
}
