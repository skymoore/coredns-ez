package secondarypersist

import (
	"sync"
	"testing"

	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/test"

	"github.com/miekg/dns"
)

type memStore struct {
	mu sync.Mutex
	m  map[string][]dns.RR
}

func newMemStore() *memStore { return &memStore{m: map[string][]dns.RR{}} }

func (s *memStore) Save(origin string, rrs []dns.RR) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]dns.RR, len(rrs))
	for i, rr := range rrs {
		out[i] = dns.Copy(rr)
	}
	s.m[origin] = out
	return nil
}

func (s *memStore) Load(origin string) ([]dns.RR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[origin], nil
}

func (s *memStore) Remove(origin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, origin)
	return nil
}

func TestLoadIfPresent(t *testing.T) {
	origin := "example.org."
	z := file.NewZone(origin, "stdin")
	for _, rr := range []dns.RR{
		test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 9 7200 3600 1209600 3600"),
		test.NS("example.org. 3600 IN NS ns.example.org."),
	} {
		if err := z.Insert(rr); err != nil {
			t.Fatal(err)
		}
	}
	store := newMemStore()
	if err := store.Save(origin, dumpRRs(z)); err != nil {
		t.Fatal(err)
	}

	dst := file.NewZone(origin, "stdin")
	s := &SecondaryPersist{
		records:     store,
		persistStop: make(chan struct{}),
		lastSerial:  map[string]uint32{},
		hasWritten:  map[string]bool{},
		writing:     map[string]bool{},
		pending:     map[string]zoneSnapshot{},
	}
	s.loadIfPresent(origin, dst)
	if soa := zoneSOA(dst); soa == nil || soa.Serial != 9 {
		t.Fatalf("expected loaded serial 9, got %+v", soa)
	}
}

func TestReloadFromStoreDropsDeletedRecords(t *testing.T) {
	origin := "example.org."
	store := newMemStore()
	if err := store.Save(origin, []dns.RR{
		test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 1 7200 3600 1209600 3600"),
		test.NS("example.org. 3600 IN NS ns.example.org."),
		test.A("www.example.org. 60 IN A 192.0.2.10"),
	}); err != nil {
		t.Fatal(err)
	}
	z := file.NewZone(origin, "stdin")
	s := newTestSecondary(t, origin, z, false)
	s.SetRecordStore(store)
	s.loadIfPresent(origin, z)
	if n := len(dumpRRs(z)); n != 3 {
		t.Fatalf("loaded %d rrs", n)
	}
	if err := store.Save(origin, []dns.RR{
		test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 2 7200 3600 1209600 3600"),
		test.NS("example.org. 3600 IN NS ns.example.org."),
	}); err != nil {
		t.Fatal(err)
	}
	s.ReloadFromStore(origin)
	for _, rr := range dumpRRs(z) {
		if _, ok := rr.(*dns.A); ok {
			t.Fatalf("deleted A still in memory: %s", rr)
		}
	}
	if soa := zoneSOA(z); soa == nil || soa.Serial != 2 {
		t.Fatalf("expected serial 2, got %+v", soa)
	}
}

func TestLoadIfPresentMissingIsOK(t *testing.T) {
	origin := "example.org."
	z := file.NewZone(origin, "stdin")
	s := &SecondaryPersist{
		records:     newMemStore(),
		persistStop: make(chan struct{}),
		lastSerial:  map[string]uint32{},
		hasWritten:  map[string]bool{},
		writing:     map[string]bool{},
		pending:     map[string]zoneSnapshot{},
	}
	s.loadIfPresent(origin, z)
	if zoneSOA(z) != nil {
		t.Fatal("missing sqlite zone should leave zone empty")
	}
}

func TestRemovePersist(t *testing.T) {
	origin := "example.org."
	store := newMemStore()
	soa := test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 1 7200 3600 1209600 3600")
	if err := store.Save(origin, []dns.RR{soa}); err != nil {
		t.Fatal(err)
	}
	s := &SecondaryPersist{
		records:    store,
		lastSerial: map[string]uint32{origin: 1},
		hasWritten: map[string]bool{origin: true},
		pending:    map[string]zoneSnapshot{},
	}
	s.removePersistFile(origin)
	if rrs, _ := store.Load(origin); len(rrs) != 0 {
		t.Fatalf("expected sqlite zone removed, got %d rrs", len(rrs))
	}
}
