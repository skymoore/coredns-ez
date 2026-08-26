package secondarypersist

import (
	"fmt"
	"time"

	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/pkg/catalog"
	"github.com/coredns/coredns/plugin/transfer"

	"github.com/miekg/dns"
)

// MaxSerialIncrement is the maximum difference between two serial numbers. If
// the difference between two serials is greater than this number, the smaller
// one is considered greater. Copied from plugin/file (CoreDNS v1.14.7).
const MaxSerialIncrement uint32 = 2147483647

func (s *SecondaryPersist) transferAndUpdate(origin string, z *file.Zone, x *transfer.Transfer, updateShutdown chan bool) {
	if zoneSOA(z) != nil {
		ok, err := shouldTransfer(origin, z)
		if err != nil {
			log.Warningf("Failed primary check for %s: %s", origin, err)
		}
		if err == nil && !ok {
			s.enterUpdateLoop(origin, z, x, updateShutdown)
			return
		}
		if err := s.transferIn(origin, z, x); err != nil {
			log.Warningf("Transfer of %s failed, serving persisted copy: %s", origin, err)
		}
		if updateStopped(updateShutdown) {
			return
		}
		s.enterUpdateLoop(origin, z, x, updateShutdown)
		return
	}

	dur := time.Millisecond * 250
	max := time.Second * 10
	for {
		err := s.transferIn(origin, z, x)
		if err == nil {
			break
		}
		log.Warningf("All '%s' masters failed to transfer, retrying in %s: %s", origin, dur.String(), err)
		if waitForTransferRetry(updateShutdown, dur) {
			return
		}
		dur <<= 1
		if dur > max {
			dur = max
		}
	}
	if updateStopped(updateShutdown) {
		return
	}
	s.enterUpdateLoop(origin, z, x, updateShutdown)
}

func (s *SecondaryPersist) enterUpdateLoop(origin string, z *file.Zone, x *transfer.Transfer, updateShutdown chan bool) {
	z.UpdateWithTransfer(updateShutdown, x, func(z *file.Zone, t *transfer.Transfer) error {
		return s.transferIn(origin, z, t)
	})
}

func (s *SecondaryPersist) transferIn(origin string, z *file.Zone, t *transfer.Transfer) error {
	_, isCatalog := s.catalogZones[origin]
	soa := zoneSOA(z)

	var (
		err      error
		via      string
		fallback bool
	)
	if soa != nil {
		via = "ixfr"
		err = s.transferIXFR(origin, z, t, isCatalog)
		if err != nil {
			fallback = true
			via = "axfr"
			err = s.transferAXFR(origin, z, t, isCatalog)
		}
	} else {
		via = "axfr"
		err = s.transferAXFR(origin, z, t, isCatalog)
	}
	if err != nil {
		status := "error"
		if fallback {
			status = "fallback"
		}
		transferCount.WithLabelValues(origin, via, status).Inc()
		return err
	}
	if fallback {
		transferCount.WithLabelValues(origin, "ixfr", "fallback").Inc()
		transferCount.WithLabelValues(origin, "axfr", "ok").Inc()
	} else {
		transferCount.WithLabelValues(origin, via, "ok").Inc()
	}

	serial := uint32(0)
	if soa := zoneSOA(z); soa != nil {
		serial = soa.Serial
	}
	log.Infof("Transferred: %s via %s with %d SOA serial", origin, via, serial)
	s.persistAsync(origin, z)
	return nil
}

func (s *SecondaryPersist) transferAXFR(origin string, z *file.Zone, t *transfer.Transfer, isCatalog bool) error {
	if !isCatalog {
		return z.TransferIn(t)
	}

	var parsed *catalog.Catalog
	if err := z.TransferInWithRecords(t, func(rrs []dns.RR) error {
		cat, err := catalog.Parse(origin, rrs)
		if err != nil {
			return err
		}
		parsed = cat
		return nil
	}); err != nil {
		return err
	}
	if parsed == nil {
		return nil
	}
	s.storeCatalog(origin, parsed)
	s.startCatalogMembers(origin, parsed, z, t)
	log.Infof("Parsed catalog zone %s with %d member zones", origin, len(parsed.Members))
	return nil
}

func (s *SecondaryPersist) storeCatalog(origin string, parsed *catalog.Catalog) {
	s.catalogMu.Lock()
	if s.catalogs == nil {
		s.catalogs = make(map[string]*catalog.Catalog)
	}
	s.catalogs[origin] = parsed
	s.catalogMu.Unlock()
}

func (s *SecondaryPersist) startCatalogMembers(origin string, parsed *catalog.Catalog, catalogZone *file.Zone, t *transfer.Transfer) {
	starts := s.applyCatalog(origin, parsed, catalogZone)
	for _, start := range starts {
		if !start.hasData {
			s.loadIfPresent(start.origin, start.zone)
		} else {
			s.persistAsync(start.origin, start.zone)
		}
		go s.transferAndUpdate(start.origin, start.zone, t, start.shutdown)
	}
}

func shouldTransfer(origin string, z *file.Zone) (bool, error) {
	c := new(dns.Client)
	c.Net = "tcp"
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(origin), dns.TypeSOA)

	var Err error
	serial := -1

Transfer:
	for _, tr := range z.TransferFrom {
		Err = nil
		ret, _, err := c.Exchange(m, tr)
		if err != nil || ret == nil || ret.Rcode != dns.RcodeSuccess {
			Err = err
			if Err == nil {
				Err = fmt.Errorf("soa query for %s to %s returned rcode %d", origin, tr, retRcode(ret))
			}
			continue
		}
		for _, a := range ret.Answer {
			if a.Header().Rrtype == dns.TypeSOA {
				serial = int(a.(*dns.SOA).Serial)
				break Transfer
			}
		}
	}
	if serial == -1 {
		return false, Err
	}
	soa := zoneSOA(z)
	if soa == nil {
		return true, Err
	}
	return less(soa.Serial, uint32(serial)), Err // #nosec G115 -- serial fits in uint32 per DNS RFC
}

func retRcode(m *dns.Msg) int {
	if m == nil {
		return -1
	}
	return m.Rcode
}

// less returns true if a is smaller than b when taking RFC 1982 serial arithmetic into account.
func less(a, b uint32) bool {
	if a < b {
		return (b - a) <= MaxSerialIncrement
	}
	return (a - b) > MaxSerialIncrement
}

func waitForTransferRetry(updateShutdown <-chan bool, dur time.Duration) bool {
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-timer.C:
		return false
	case <-updateShutdown:
		return true
	}
}

func updateStopped(updateShutdown <-chan bool) bool {
	select {
	case <-updateShutdown:
		return true
	default:
		return false
	}
}
