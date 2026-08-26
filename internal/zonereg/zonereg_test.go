package zonereg

import (
	"testing"

	"github.com/miekg/dns"
)

type fakePrimary struct {
	origin, source, path string
}

func (f fakePrimary) Origin() string            { return f.origin }
func (f fakePrimary) Source() string            { return f.source }
func (f fakePrimary) Path() string              { return f.path }
func (f fakePrimary) Records() []dns.RR         { return nil }
func (f fakePrimary) Apply(_, _ []dns.RR) error { return nil }

func TestRegisterConflictAndLookup(t *testing.T) {
	t.Cleanup(ResetForTest)
	p := fakePrimary{origin: "Example.COM.", source: SourceAPI, path: "/z/db.example.com"}
	if err := RegisterPrimary(p); err != nil {
		t.Fatal(err)
	}
	if err := RegisterPrimary(p); err == nil {
		t.Fatal("expected conflict")
	}
	got, kind := Lookup("www.example.com.")
	if got != "example.com." || kind != KindPrimary {
		t.Fatalf("lookup = %q %q", got, kind)
	}
	all := All()
	if len(all) != 1 || all[0].Origin != "example.com." || all[0].Source != SourceAPI {
		t.Fatalf("all = %+v", all)
	}
	Unregister("example.com.")
	if PrimaryOf("example.com.") != nil {
		t.Fatal("still registered")
	}
}
