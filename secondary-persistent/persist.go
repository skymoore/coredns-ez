package secondarypersist

import (
	"time"

	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/file/tree"

	"github.com/miekg/dns"
)

// RecordStore is the SQLite persist backend.
type RecordStore interface {
	Save(origin string, rrs []dns.RR) error
	Load(origin string) ([]dns.RR, error)
	Remove(origin string) error
}

type zoneSnapshot struct {
	origin string
	apex   file.Apex
	tree   *tree.Tree
	serial uint32
}

func (s *SecondaryPersist) loadIfPresent(origin string, z *file.Zone) {
	if s.records == nil {
		loadCount.WithLabelValues(origin, "missing").Inc()
		return
	}
	rrs, err := s.records.Load(origin)
	if err != nil {
		loadCount.WithLabelValues(origin, "error").Inc()
		log.Warningf("Failed to load sqlite zone %s: %s", origin, err)
		return
	}
	if soaOfRRs(rrs) == nil {
		loadCount.WithLabelValues(origin, "missing").Inc()
		return
	}
	loaded, err := zoneFromRRs(origin, rrs)
	if err != nil {
		loadCount.WithLabelValues(origin, "error").Inc()
		log.Warningf("Failed to install sqlite zone %s: %s", origin, err)
		return
	}
	installZoneData(z, loaded)
	if soa := zoneSOA(loaded); soa != nil {
		s.markWritten(origin, soa.Serial)
		log.Infof("Loaded persisted zone %s from sqlite serial=%d", origin, soa.Serial)
	}
	loadCount.WithLabelValues(origin, "ok").Inc()
}

func zoneFromRRs(origin string, rrs []dns.RR) (*file.Zone, error) {
	z := file.NewZone(origin, "sqlite")
	for _, rr := range rrs {
		if err := z.Insert(dns.Copy(rr)); err != nil {
			return nil, err
		}
	}
	return z, nil
}

func soaOfRRs(rrs []dns.RR) *dns.SOA {
	for _, rr := range rrs {
		if soa, ok := rr.(*dns.SOA); ok {
			return soa
		}
	}
	return nil
}

func installZoneData(dst, src *file.Zone) {
	src.RLock()
	ap, tr := src.Apex, src.Tree
	src.RUnlock()

	dst.Lock()
	dst.Apex = ap
	dst.Tree = tr
	dst.Expired = false
	dst.Unlock()
}

func zoneSOA(z *file.Zone) *dns.SOA {
	if z == nil {
		return nil
	}
	z.RLock()
	defer z.RUnlock()
	return z.SOA
}

func dumpRRs(z *file.Zone) []dns.RR {
	z.RLock()
	defer z.RUnlock()
	return dumpRRsLocked(z.Apex, z.Tree)
}

func dumpRRsLocked(ap file.Apex, tr *tree.Tree) []dns.RR {
	n := 0
	if ap.SOA != nil {
		n++
	}
	n += len(ap.SIGSOA) + len(ap.NS) + len(ap.SIGNS)
	rrs := make([]dns.RR, 0, n)
	if ap.SOA != nil {
		rrs = append(rrs, ap.SOA)
	}
	rrs = append(rrs, ap.SIGSOA...)
	rrs = append(rrs, ap.NS...)
	rrs = append(rrs, ap.SIGNS...)
	if tr != nil {
		_ = tr.Walk(func(e *tree.Elem, _ map[uint16][]dns.RR) error {
			rrs = append(rrs, e.All()...)
			return nil
		})
	}
	return rrs
}

func snapshotZone(origin string, z *file.Zone) (zoneSnapshot, bool) {
	z.RLock()
	defer z.RUnlock()
	if z.SOA == nil {
		return zoneSnapshot{}, false
	}
	return zoneSnapshot{
		origin: origin,
		apex:   z.Apex,
		tree:   z.Tree,
		serial: z.SOA.Serial,
	}, true
}

func (s *SecondaryPersist) persistAsync(origin string, z *file.Zone) {
	if s.records == nil {
		return
	}
	snap, ok := snapshotZone(origin, z)
	if !ok {
		return
	}
	s.enqueuePersist(snap)
}

func (s *SecondaryPersist) enqueuePersist(snap zoneSnapshot) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	key := snap.origin
	if s.hasWritten[key] && s.lastSerial[key] == snap.serial {
		return
	}
	if s.writing[key] {
		s.pending[key] = snap
		return
	}
	s.writing[key] = true
	s.persistWg.Add(1)
	go func() {
		defer s.persistWg.Done()
		s.runPersist(snap)
	}()
}

func (s *SecondaryPersist) closePersist() {
	s.persistStopOnce.Do(func() { close(s.persistStop) })
	s.persistWg.Wait()
}

func (s *SecondaryPersist) runPersist(snap zoneSnapshot) {
	key := snap.origin
	select {
	case <-s.persistStop:
		s.persistMu.Lock()
		s.writing[key] = false
		delete(s.pending, key)
		s.persistMu.Unlock()
		return
	default:
	}

	s.persistMu.Lock()
	stale := s.hasWritten[key] && !less(s.lastSerial[key], snap.serial)
	s.persistMu.Unlock()
	if stale {
		s.finishPersist(key)
		return
	}

	start := time.Now()
	err := s.records.Save(snap.origin, dumpRRsLocked(snap.apex, snap.tree))
	writeDuration.WithLabelValues(snap.origin).Observe(time.Since(start).Seconds())

	s.persistMu.Lock()
	s.writing[key] = false
	if err != nil {
		writeCount.WithLabelValues(snap.origin, "error").Inc()
		log.Errorf("Failed to persist zone %s: %s", snap.origin, err)
	} else {
		s.hasWritten[key] = true
		s.lastSerial[key] = snap.serial
		serialGauge.WithLabelValues(snap.origin).Set(float64(snap.serial))
		writeCount.WithLabelValues(snap.origin, "ok").Inc()
		log.Infof("Persisted zone %s serial=%d", snap.origin, snap.serial)
	}
	s.persistMu.Unlock()
	s.finishPersist(key)
}

func (s *SecondaryPersist) finishPersist(key string) {
	s.persistMu.Lock()
	s.writing[key] = false
	pending, ok := s.pending[key]
	if ok {
		delete(s.pending, key)
		if !s.hasWritten[key] || less(s.lastSerial[key], pending.serial) {
			s.writing[key] = true
			s.persistWg.Add(1)
			s.persistMu.Unlock()
			go func() {
				defer s.persistWg.Done()
				s.runPersist(pending)
			}()
			return
		}
	}
	s.persistMu.Unlock()
}

func (s *SecondaryPersist) markWritten(origin string, serial uint32) {
	s.persistMu.Lock()
	s.hasWritten[origin] = true
	s.lastSerial[origin] = serial
	s.persistMu.Unlock()
}

func (s *SecondaryPersist) removePersistFile(origin string) {
	if s.records != nil {
		if err := s.records.Remove(origin); err != nil {
			log.Warningf("Failed to remove sqlite zone %s: %s", origin, err)
		}
	}
	s.persistMu.Lock()
	delete(s.hasWritten, origin)
	delete(s.lastSerial, origin)
	delete(s.pending, origin)
	s.persistMu.Unlock()
}
