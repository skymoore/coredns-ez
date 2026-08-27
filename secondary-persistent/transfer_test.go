package secondarypersist

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	plugintest "github.com/coredns/coredns/plugin/test"

	"github.com/miekg/dns"
)

const transferTestZone = "example.org."

func TestTransferAXFRAndPersist(t *testing.T) {
	serial := uint32(250)
	server := dnstest.NewServer(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		soa := plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. %d 7200 3600 1209600 3600", transferTestZone, serial))
		switch req.Question[0].Qtype {
		case dns.TypeSOA:
			m.Answer = []dns.RR{soa}
		case dns.TypeAXFR, dns.TypeIXFR:
			m.Answer = []dns.RR{
				soa,
				plugintest.NS(transferTestZone + " IN NS ns.example.org."),
				plugintest.A("www.example.org. IN A 192.0.2.10"),
				soa,
			}
		}
		_ = w.WriteMsg(m)
	})
	defer server.Close()

	z := file.NewZone(transferTestZone, "stdin")
	z.TransferFrom = []string{server.Addr}
	s := newTestSecondary(t, transferTestZone, z, false)

	if err := s.transferIn(transferTestZone, z, nil); err != nil {
		t.Fatalf("transferIn: %v", err)
	}
	if zoneSOA(z) == nil || zoneSOA(z).Serial != serial {
		t.Fatalf("expected serial %d", serial)
	}

	waitForPersist(t, s, transferTestZone, serial)

	dst := file.NewZone(transferTestZone, "stdin")
	s.loadIfPresent(transferTestZone, dst)
	if zoneSOA(dst) == nil || zoneSOA(dst).Serial != serial {
		t.Fatal("expected reload from sqlite persist")
	}

	req := new(dns.Msg)
	req.SetQuestion("www.example.org.", dns.TypeA)
	rec := dnstest.NewRecorder(&plugintest.ResponseWriter{})
	if _, err := s.ServeDNS(context.Background(), rec, req); err != nil {
		t.Fatal(err)
	}
	if rec.Msg == nil || len(rec.Msg.Answer) != 1 {
		t.Fatalf("expected A answer, got %+v", rec.Msg)
	}
}

func TestShouldTransferSkipWhenSerialEqual(t *testing.T) {
	serial := uint32(250)
	axfrs := 0
	server := dnstest.NewServer(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		soa := plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. %d 7200 3600 1209600 3600", transferTestZone, serial))
		switch req.Question[0].Qtype {
		case dns.TypeSOA:
			m.Answer = []dns.RR{soa}
		case dns.TypeAXFR, dns.TypeIXFR:
			axfrs++
			m.Answer = []dns.RR{soa, soa}
		}
		_ = w.WriteMsg(m)
	})
	defer server.Close()

	z := file.NewZone(transferTestZone, "stdin")
	z.TransferFrom = []string{server.Addr}
	if err := z.Insert(plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. %d 7200 3600 1209600 3600", transferTestZone, serial))); err != nil {
		t.Fatal(err)
	}
	ok, err := shouldTransfer(transferTestZone, z)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("equal serial should not transfer")
	}

	s := newTestSecondary(t, transferTestZone, z, false)
	done := make(chan struct{})
	shutdown := make(chan bool)
	go func() {
		s.transferAndUpdate(transferTestZone, z, nil, shutdown)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	close(shutdown)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("transferAndUpdate did not stop")
	}
	if axfrs != 0 {
		t.Fatalf("expected no AXFR/IXFR, got %d", axfrs)
	}
}

func TestIXFRFallbackToAXFR(t *testing.T) {
	serial := uint32(3)
	server := dnstest.NewServer(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		soa := plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. %d 7200 3600 1209600 3600", transferTestZone, serial))
		switch req.Question[0].Qtype {
		case dns.TypeSOA:
			m.Answer = []dns.RR{soa}
		case dns.TypeIXFR:
			m.Rcode = dns.RcodeNotImplemented
		case dns.TypeAXFR:
			m.Answer = []dns.RR{
				soa,
				plugintest.NS(transferTestZone + " IN NS ns.example.org."),
				plugintest.A("www.example.org. IN A 192.0.2.33"),
				soa,
			}
		}
		_ = w.WriteMsg(m)
	})
	defer server.Close()

	z := file.NewZone(transferTestZone, "stdin")
	z.TransferFrom = []string{server.Addr}
	if err := z.Insert(plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. 2 7200 3600 1209600 3600", transferTestZone))); err != nil {
		t.Fatal(err)
	}
	s := newTestSecondary(t, transferTestZone, z, false)
	if err := s.transferIn(transferTestZone, z, nil); err != nil {
		t.Fatalf("transferIn: %v", err)
	}
	if zoneSOA(z) == nil || zoneSOA(z).Serial != serial {
		t.Fatalf("expected serial %d after AXFR fallback", serial)
	}
}

func TestTransferIXFRAppliesDeltas(t *testing.T) {
	oldSOA := plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. 1 7200 3600 1209600 3600", transferTestZone))
	newSOA := plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. 2 7200 3600 1209600 3600", transferTestZone))
	oldA := plugintest.A("www.example.org. IN A 192.0.2.1")
	newA := plugintest.A("www.example.org. IN A 192.0.2.9")
	ns := plugintest.NS(transferTestZone + " IN NS ns.example.org.")

	server := dnstest.NewServer(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		switch req.Question[0].Qtype {
		case dns.TypeSOA:
			m.Answer = []dns.RR{newSOA}
		case dns.TypeIXFR:
			m.Answer = []dns.RR{newSOA, oldSOA, oldA, newSOA, newA, newSOA}
		case dns.TypeAXFR:
			t.Errorf("unexpected AXFR fallback")
		}
		_ = w.WriteMsg(m)
	})
	defer server.Close()

	z := file.NewZone(transferTestZone, "stdin")
	z.TransferFrom = []string{server.Addr}
	for _, rr := range []dns.RR{oldSOA, ns, oldA} {
		if err := z.Insert(rr); err != nil {
			t.Fatal(err)
		}
	}
	s := newTestSecondary(t, transferTestZone, z, false)
	if err := s.transferIn(transferTestZone, z, nil); err != nil {
		t.Fatalf("transferIn: %v", err)
	}
	if zoneSOA(z) == nil || zoneSOA(z).Serial != 2 {
		t.Fatalf("expected serial 2, got %+v", zoneSOA(z))
	}

	req := new(dns.Msg)
	req.SetQuestion("www.example.org.", dns.TypeA)
	rec := dnstest.NewRecorder(&plugintest.ResponseWriter{})
	if _, err := s.ServeDNS(context.Background(), rec, req); err != nil {
		t.Fatal(err)
	}
	if rec.Msg == nil || len(rec.Msg.Answer) != 1 {
		t.Fatalf("expected A answer, got %+v", rec.Msg)
	}
	a, ok := rec.Msg.Answer[0].(*dns.A)
	if !ok || a.A.String() != "192.0.2.9" {
		t.Fatalf("expected 192.0.2.9, got %+v", rec.Msg.Answer)
	}
}

func TestShouldTransferWraparound(t *testing.T) {
	server := dnstest.NewServer(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		m.Answer = []dns.RR{
			plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. 1 7200 3600 1209600 3600", transferTestZone)),
		}
		_ = w.WriteMsg(m)
	})
	defer server.Close()

	z := file.NewZone(transferTestZone, "stdin")
	z.TransferFrom = []string{server.Addr}
	if err := z.Insert(plugintest.SOA(fmt.Sprintf("%s IN SOA ns.example.org. hostmaster.example.org. %d 7200 3600 1209600 3600", transferTestZone, uint32(4294967295)))); err != nil {
		t.Fatal(err)
	}
	ok, err := shouldTransfer(transferTestZone, z)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("RFC 1982 wrap: remote serial 1 should be newer than 2^32-1")
	}
}

func waitForPersist(t *testing.T, s *SecondaryPersist, origin string, serial uint32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.persistMu.Lock()
		ok := s.hasWritten[origin] && s.lastSerial[origin] == serial
		s.persistMu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for persist of %s serial %d", origin, serial)
}

func newTestSecondary(t *testing.T, origin string, z *file.Zone, catalog bool) *SecondaryPersist {
	t.Helper()
	catalogZones := map[string]plugin.Zones{}
	if catalog {
		catalogZones[origin] = nil
	}
	s := newSecondaryPersist(file.Zones{
		Z:     map[string]*file.Zone{origin: z},
		Names: []string{origin},
	}, fall.F{}, catalogZones, persistConfig{})
	s.SetRecordStore(newMemStore())
	t.Cleanup(s.closePersist)
	t.Cleanup(s.stopDynamicZones)
	return s
}
