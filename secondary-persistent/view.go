package secondarypersist

import (
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

// originView exposes one origin of a SecondaryPersist as zonereg.Secondary.
type originView struct {
	s      *SecondaryPersist
	origin string
}

func (v *originView) Origin() string { return v.origin }

func (v *originView) Source() string {
	v.s.zoneMu.RLock()
	defer v.s.zoneMu.RUnlock()
	if dyn, ok := v.s.dynamicZones[v.origin]; ok && dyn.catalog == adminCatalog {
		return zonereg.SourceAdmin
	}
	return zonereg.SourceCorefile
}

func (v *originView) Path() string { return "" }

func (v *originView) Records() []dns.RR { return v.s.RecordsFor(v.origin) }

func (v *originView) TransferFrom() []string { return v.s.TransferFromFor(v.origin) }

func (v *originView) ForceTransfer() error { return v.s.ForceTransfer(v.origin) }
