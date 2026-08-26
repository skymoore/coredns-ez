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
	"github.com/skymoore/coredns-plugins/internal/zonereg"
)

const adminCatalog = "admin"

// NewWithDirectory returns an empty secondary engine that persists members
// under dir. The API plugin owns one of these for cluster-synced zones.
func NewWithDirectory(dir string) *SecondaryPersist {
	return newSecondaryPersist(
		file.Zones{Z: map[string]*file.Zone{}, Names: []string{}},
		fall.F{},
		map[string]plugin.Zones{},
		persistConfig{dir: dir},
	)
}

// SetNext sets the fallthrough handler.
func (s *SecondaryPersist) SetNext(next plugin.Handler) { s.Next = next }

// SetTransfer sets the transfer plugin used for inbound AXFR/IXFR.
func (s *SecondaryPersist) SetTransfer(x *transfer.Transfer) { s.Xfer = x }

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
