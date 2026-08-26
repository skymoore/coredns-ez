package secondarypersist

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/file/tree"

	"github.com/miekg/dns"
)

type zoneSnapshot struct {
	origin string
	path   string
	apex   file.Apex
	tree   *tree.Tree
	serial uint32
}

func persistFileName(origin string) string {
	return "db." + strings.TrimSuffix(origin, ".")
}

func (s *SecondaryPersist) pathFor(origin string) string {
	if p, ok := s.persistPaths[origin]; ok {
		return p
	}
	if s.persistDir == "" {
		return ""
	}
	name := persistFileName(origin)
	p := filepath.Join(s.persistDir, name)
	rel, err := filepath.Rel(s.persistDir, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return p
}

func (s *SecondaryPersist) loadIfPresent(origin string, z *file.Zone) {
	path := s.pathFor(origin)
	if path == "" {
		return
	}
	loaded, err := loadZone(path, origin)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			loadCount.WithLabelValues(origin, "missing").Inc()
			log.Debugf("No persist file for %s at %s", origin, path)
			return
		}
		loadCount.WithLabelValues(origin, "error").Inc()
		log.Warningf("Failed to load persist file %q for %s: %s", path, origin, err)
		return
	}
	installZoneData(z, loaded)
	if soa := zoneSOA(loaded); soa != nil {
		s.markWritten(path, soa.Serial)
		log.Infof("Loaded persisted zone %s from %s with %d SOA serial", origin, path, soa.Serial)
	}
	loadCount.WithLabelValues(origin, "ok").Inc()
}

func loadZone(path, origin string) (*file.Zone, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseZone(f, origin, path)
}

func parseZone(r io.Reader, origin, fileName string) (*file.Zone, error) {
	zp := dns.NewZoneParser(r, dns.Fqdn(origin), fileName)
	zp.SetIncludeAllowed(false)
	z := file.NewZone(origin, fileName)
	seenSOA := false
	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		if _, isSOA := rr.(*dns.SOA); isSOA {
			seenSOA = true
		}
		if err := z.Insert(rr); err != nil {
			return nil, err
		}
	}
	if err := zp.Err(); err != nil {
		return nil, fmt.Errorf("failed to parse file %q for origin %s: %w", fileName, origin, err)
	}
	if !seenSOA {
		return nil, fmt.Errorf("file %q has no SOA record for origin %s", fileName, origin)
	}
	return z, nil
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

func snapshotZone(origin, path string, z *file.Zone) (zoneSnapshot, bool) {
	z.RLock()
	defer z.RUnlock()
	if z.SOA == nil {
		return zoneSnapshot{}, false
	}
	return zoneSnapshot{
		origin: origin,
		path:   path,
		apex:   z.Apex,
		tree:   z.Tree,
		serial: z.SOA.Serial,
	}, true
}

func (s *SecondaryPersist) persistAsync(origin string, z *file.Zone) {
	path := s.pathFor(origin)
	if path == "" {
		return
	}
	snap, ok := snapshotZone(origin, path, z)
	if !ok {
		return
	}
	s.enqueuePersist(snap)
}

func (s *SecondaryPersist) enqueuePersist(snap zoneSnapshot) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	if s.hasWritten[snap.path] && s.lastSerial[snap.path] == snap.serial {
		return
	}
	if s.writing[snap.path] {
		s.pending[snap.path] = snap
		return
	}
	s.writing[snap.path] = true
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
	select {
	case <-s.persistStop:
		s.persistMu.Lock()
		s.writing[snap.path] = false
		delete(s.pending, snap.path)
		s.persistMu.Unlock()
		return
	default:
	}

	start := time.Now()
	err := writeAtomic(snap)
	writeDuration.WithLabelValues(snap.origin).Observe(time.Since(start).Seconds())

	s.persistMu.Lock()
	s.writing[snap.path] = false
	if err != nil {
		writeCount.WithLabelValues(snap.origin, "error").Inc()
		log.Errorf("Failed to persist zone %s to %s: %s", snap.origin, snap.path, err)
	} else {
		s.hasWritten[snap.path] = true
		s.lastSerial[snap.path] = snap.serial
		serialGauge.WithLabelValues(snap.origin).Set(float64(snap.serial))
		writeCount.WithLabelValues(snap.origin, "ok").Inc()
		log.Infof("Persisted zone %s to %s with %d SOA serial", snap.origin, snap.path, snap.serial)
	}
	pending, ok := s.pending[snap.path]
	if ok {
		delete(s.pending, snap.path)
		if !s.hasWritten[snap.path] || s.lastSerial[snap.path] != pending.serial {
			s.writing[snap.path] = true
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

func (s *SecondaryPersist) markWritten(path string, serial uint32) {
	s.persistMu.Lock()
	s.hasWritten[path] = true
	s.lastSerial[path] = serial
	s.persistMu.Unlock()
}

func (s *SecondaryPersist) removePersistFile(origin string) {
	path := s.pathFor(origin)
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warningf("Failed to remove persist file %q for %s: %s", path, origin, err)
	}
	s.persistMu.Lock()
	delete(s.hasWritten, path)
	delete(s.lastSerial, path)
	delete(s.pending, path)
	s.persistMu.Unlock()
}

func writeAtomic(snap zoneSnapshot) error {
	if snap.apex.SOA == nil {
		return fmt.Errorf("no SOA")
	}
	dir := filepath.Dir(snap.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".persist-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()

	if err := writeSnapshot(f, snap); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, snap.path); err != nil {
		return err
	}
	cleanup = false
	return syncDir(dir)
}

func writeSnapshot(w io.Writer, snap zoneSnapshot) error {
	header := fmt.Sprintf("; persisted by coredns secondary-persistent origin=%s serial=%d\n", snap.origin, snap.serial)
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	if err := writeRR(w, snap.apex.SOA); err != nil {
		return err
	}
	for _, rr := range snap.apex.SIGSOA {
		if err := writeRR(w, rr); err != nil {
			return err
		}
	}
	for _, rr := range snap.apex.NS {
		if err := writeRR(w, rr); err != nil {
			return err
		}
	}
	for _, rr := range snap.apex.SIGNS {
		if err := writeRR(w, rr); err != nil {
			return err
		}
	}
	if snap.tree == nil {
		return nil
	}
	return snap.tree.Walk(func(e *tree.Elem, _ map[uint16][]dns.RR) error {
		for _, rr := range e.All() {
			if err := writeRR(w, rr); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeRR(w io.Writer, rr dns.RR) error {
	if _, err := io.WriteString(w, rr.String()); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
