package dnsupdatepersist

import (
	"fmt"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/internal/zonereg"
	"github.com/skymoore/coredns-ez/ixfr"
)

// NewFromRecords serves origin from rrs. Persist is in-memory until SetPersist.
func NewFromRecords(origin string, rrs []dns.RR, mutable map[uint16]bool) (*UpdatePersist, error) {
	origin = strings.ToLower(dns.CanonicalName(origin))
	if soaOf(rrs) == nil {
		return nil, fmt.Errorf("%s has no SOA", origin)
	}
	copies := make([]dns.RR, len(rrs))
	for i, rr := range rrs {
		copies[i] = dns.Copy(rr)
	}
	d := &UpdatePersist{
		Zone:    origin,
		mutable: mutable,
		source:  zonereg.SourceAdmin,
		rrs:     copies,
	}
	if err := d.swap(copies); err != nil {
		return nil, err
	}
	return d, nil
}

// ReadZoneFile parses a master file the same way as the file plugin.
func ReadZoneFile(path, origin string) ([]dns.RR, error) {
	return readZone(path, origin)
}

// SetPersist installs the SQLite (or test) persist backend. Required for mutations.
func (d *UpdatePersist) SetPersist(fn PersistFunc) { d.persistFn = fn }

// ReplaceRecords swaps the in-memory zone for rrs (startup reload from sqlite).
func (d *UpdatePersist) ReplaceRecords(rrs []dns.RR) error {
	if soaOf(rrs) == nil {
		return fmt.Errorf("%s has no SOA", d.Zone)
	}
	copies := make([]dns.RR, len(rrs))
	for i, rr := range rrs {
		copies[i] = dns.Copy(rr)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.swap(copies)
}

// AttachIXFR registers the journal and makes this plugin's Transfer defer to it.
func (d *UpdatePersist) AttachIXFR(x *ixfr.IXFR) error {
	d.ixfr = x
	d.mu.RLock()
	rrs := d.rrs
	d.mu.RUnlock()
	return x.Register(d.Zone, rrs)
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
