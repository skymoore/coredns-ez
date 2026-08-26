package dnsupdatepersist

import (
	"fmt"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-plugins/internal/zonereg"
	"github.com/skymoore/coredns-plugins/ixfr"
)

// New loads origin from seedPath. Used by the API plugin to create a primary
// at runtime. The caller must AttachIXFR, SetTransfer, SetNext, and register.
func New(origin, seedPath string, mutable map[uint16]bool) (*UpdatePersist, error) {
	origin = strings.ToLower(dns.CanonicalName(origin))
	rrs, err := readZone(seedPath, origin)
	if err != nil {
		return nil, err
	}
	if soaOf(rrs) == nil {
		return nil, fmt.Errorf("%s has no SOA at %s", seedPath, origin)
	}
	d := &UpdatePersist{
		Zone:     origin,
		seedPath: seedPath,
		mutable:  mutable,
		source:   zonereg.SourceAdmin,
		rrs:      rrs,
	}
	if err := d.swap(rrs); err != nil {
		return nil, err
	}
	return d, nil
}

// WriteSeed atomically writes rrs to path. The API uses this to create a
// brand-new primary before New loads it.
func WriteSeed(path, origin string, rrs []dns.RR) error {
	return writeZoneFile(path, origin, rrs)
}

// AttachIXFR registers the journal and makes this plugin's Transfer defer to it.
func (d *UpdatePersist) AttachIXFR(x *ixfr.IXFR) error {
	d.ixfr = x
	d.mu.RLock()
	rrs := d.rrs
	d.mu.RUnlock()
	return x.Register(d.Zone, d.seedPath+".ixfr", rrs)
}

// SetTransfer sets the transfer plugin used for NOTIFY.
func (d *UpdatePersist) SetTransfer(x *transfer.Transfer) { d.Xfer = x }

// SetNext rebuilds the file view so names outside this zone fall through.
func (d *UpdatePersist) SetNext(next plugin.Handler) {
	d.Next = next
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.swap(d.rrs); err != nil {
		log.Errorf("rebuilding %s after SetNext: %v", d.Zone, err)
	}
}
