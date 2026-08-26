package dnsupdatepersist

import (
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-plugins/internal/zonereg"
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
