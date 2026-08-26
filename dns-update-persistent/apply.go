package dnsupdatepersist

import (
	"fmt"

	"github.com/coredns/coredns/plugin/transfer"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

// RcodeError is an RFC 2136 rcode produced by Apply.
type RcodeError struct {
	Rcode int
}

func (e *RcodeError) Error() string {
	return fmt.Sprintf("update rcode %s", dns.RcodeToString[e.Rcode])
}

// Origin implements zonereg.Primary.
func (d *UpdatePersist) Origin() string { return d.Zone }

// Source implements zonereg.Primary.
func (d *UpdatePersist) Source() string {
	if d.source == "" {
		return zonereg.SourceCorefile
	}
	return d.source
}

// Path implements zonereg.Primary.
func (d *UpdatePersist) Path() string { return d.seedPath }

// Records implements zonereg.Primary.
func (d *UpdatePersist) Records() []dns.RR {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]dns.RR, len(d.rrs))
	for i, rr := range d.rrs {
		out[i] = dns.Copy(rr)
	}
	return out
}

// SetSource records whether this instance was created by the API or Corefile.
func (d *UpdatePersist) SetSource(src string) { d.source = src }

// Apply mutates the zone from add/delete RRs without requiring TSIG. HTTP
// callers are already authenticated; RFC 2136 on the wire still requires TSIG.
//
// deletes: class NONE (one RR) or class ANY with empty rdata (whole RRset).
// adds: class IN.
func (d *UpdatePersist) Apply(adds, deletes []dns.RR) error {
	updates := make([]dns.RR, 0, len(adds)+len(deletes))
	for _, rr := range deletes {
		c := dns.Copy(rr)
		if c.Header().Class == dns.ClassNONE {
			c.Header().Ttl = 0
		}
		updates = append(updates, c)
	}
	for _, rr := range adds {
		c := dns.Copy(rr)
		c.Header().Class = dns.ClassINET
		updates = append(updates, c)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if rcode := d.prescan(updates); rcode != dns.RcodeSuccess {
		return &RcodeError{Rcode: rcode}
	}
	rcode := d.commitLocked(d.Zone, updates)
	if rcode != dns.RcodeSuccess {
		return &RcodeError{Rcode: rcode}
	}
	return nil
}

// OutboundTransfer is the Transferer for this origin: the ixfr journal when
// attached, otherwise the file view. The API plugin uses this because its
// per-zone journals are not themselves in the handler chain.
func (d *UpdatePersist) OutboundTransfer() transfer.Transferer {
	if d.ixfr != nil {
		return d.ixfr
	}
	return d
}
