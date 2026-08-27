package secondarypersist

import (
	"fmt"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/pkg/upstream"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

const adminCatalog = "admin"

// NewEngine returns an empty secondary that persists transferred zones to
// RecordStore (SQLite when used with admin).
func NewEngine() *SecondaryPersist {
	return newSecondaryPersist(
		file.Zones{Z: map[string]*file.Zone{}, Names: []string{}},
		fall.F{},
		map[string]plugin.Zones{},
		persistConfig{},
	)
}

// SetNext sets the fallthrough handler.
func (s *SecondaryPersist) SetNext(next plugin.Handler) { s.Next = next }

// SetTransfer sets the transfer plugin used for inbound AXFR/IXFR.
func (s *SecondaryPersist) SetTransfer(x *transfer.Transfer) { s.Xfer = x }

// SetRecordStore persists transferred zones to SQLite instead of files.
func (s *SecondaryPersist) SetRecordStore(rs RecordStore) { s.records = rs }

// StartOrigin begins transferring origin from the given masters.
func (s *SecondaryPersist) StartOrigin(origin string, from []string, x *transfer.Transfer) error {
	origin = strings.ToLower(dns.CanonicalName(origin))
	if len(from) == 0 {
		return fmt.Errorf("transfer from is required")
	}

	s.zoneMu.Lock()
	s.ensureZoneStateLocked()
	if _, exists := s.Z[origin]; exists {
		s.zoneMu.Unlock()
		return fmt.Errorf("zone %s already exists", origin)
	}
	z := file.NewZone(origin, "stdin")
	z.TransferFrom = append([]string(nil), from...)
	z.Upstream = upstream.New()
	s.Z[origin] = z
	s.Names = append(s.Names, origin)
	s.zoneNames[z] = origin
	shutdown := make(chan bool)
	s.dynamicZones[origin] = &dynamicZone{catalog: adminCatalog, memberID: origin, shutdown: shutdown}
	s.zoneMu.Unlock()

	s.loadIfPresent(origin, z)
	go s.transferAndUpdate(origin, z, x, shutdown)
	if err := zonereg.RegisterSecondary(&originView{s: s, origin: origin}); err != nil {
		log.Warningf("zonereg %s: %v", origin, err)
	}
	return nil
}

// StopOrigin ends the transfer loop and unregisters origin.
func (s *SecondaryPersist) StopOrigin(origin string) error {
	origin = strings.ToLower(dns.CanonicalName(origin))
	s.zoneMu.Lock()
	ok := s.removeDynamicZoneLocked(origin, adminCatalog)
	s.zoneMu.Unlock()
	if !ok {
		return fmt.Errorf("zone %s is not an API secondary", origin)
	}
	zonereg.Unregister(origin)
	return nil
}

// Origins returns a copy of currently served zone names.
func (s *SecondaryPersist) Origins() []string {
	s.zoneMu.RLock()
	defer s.zoneMu.RUnlock()
	out := make([]string, len(s.Names))
	copy(out, s.Names)
	return out
}

// ReloadFromStore replaces the in-memory zone with the SQLite copy. Cluster
// snapshot apply uses this so deleted records take effect without waiting
// for AXFR (which may be REFUSED).
func (s *SecondaryPersist) ReloadFromStore(origin string) {
	origin = strings.ToLower(dns.CanonicalName(origin))
	s.zoneMu.RLock()
	z := s.Z[origin]
	s.zoneMu.RUnlock()
	if z == nil {
		return
	}
	s.loadIfPresent(origin, z)
}

// RecordsFor dumps the current contents of origin.
func (s *SecondaryPersist) RecordsFor(origin string) []dns.RR {
	s.zoneMu.RLock()
	z := s.Z[origin]
	s.zoneMu.RUnlock()
	if z == nil {
		return nil
	}
	return dumpRRs(z)
}

// TransferFromFor returns the configured masters.
func (s *SecondaryPersist) TransferFromFor(origin string) []string {
	s.zoneMu.RLock()
	z := s.Z[origin]
	s.zoneMu.RUnlock()
	if z == nil {
		return nil
	}
	return append([]string(nil), z.TransferFrom...)
}

// SetTransferFrom updates the masters for one origin. Empty from is ignored.
func (s *SecondaryPersist) SetTransferFrom(origin string, from []string) {
	origin = strings.ToLower(dns.CanonicalName(origin))
	if len(from) == 0 {
		return
	}
	s.zoneMu.Lock()
	defer s.zoneMu.Unlock()
	if z := s.Z[origin]; z != nil {
		z.TransferFrom = append([]string(nil), from...)
	}
}

// SetAllTransferFrom updates masters for every origin this engine serves.
func (s *SecondaryPersist) SetAllTransferFrom(from []string) {
	if len(from) == 0 {
		return
	}
	s.zoneMu.Lock()
	defer s.zoneMu.Unlock()
	for _, z := range s.Z {
		if z != nil {
			z.TransferFrom = append([]string(nil), from...)
		}
	}
}

// ForceTransfer runs one inbound transfer now.
func (s *SecondaryPersist) ForceTransfer(origin string) error {
	s.zoneMu.RLock()
	z := s.Z[origin]
	s.zoneMu.RUnlock()
	if z == nil {
		return fmt.Errorf("unknown zone %s", origin)
	}
	return s.transferIn(origin, z, s.Xfer)
}
