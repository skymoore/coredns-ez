package dnsupdatepersist

import (
	"fmt"
	"strings"

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
func (d *UpdatePersist) Path() string { return "" }

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
//
// Apex NS and SOA are special. RFC 2136 leaves a class-ANY wipe of either as
// a no-op, and an added SOA is ignored unless its serial is greater. The
// admin UI replace path uses wipe-then-add, so HTTP rewrites those into a
// real replace: new NS values replace the old set, and a new SOA replaces
// the existing one after bumping serial. A wipe with no replacement is
// refused.
func (d *UpdatePersist) Apply(adds, deletes []dns.RR) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	updates, err := d.httpUpdatesLocked(adds, deletes)
	if err != nil {
		return err
	}
	if rcode := d.prescan(updates, false); rcode != dns.RcodeSuccess {
		return &RcodeError{Rcode: rcode}
	}
	rcode := d.commitLocked(d.Zone, updates)
	if rcode != dns.RcodeSuccess {
		return &RcodeError{Rcode: rcode}
	}
	return nil
}

func copyIN(rr dns.RR) dns.RR {
	c := dns.Copy(rr)
	c.Header().Class = dns.ClassINET
	return c
}

func copyDelete(rr dns.RR) dns.RR {
	c := dns.Copy(rr)
	if c.Header().Class == dns.ClassNONE {
		c.Header().Ttl = 0
	}
	return c
}

func isApexType(zone string, rr dns.RR, typ uint16) bool {
	h := rr.Header()
	return h.Rrtype == typ && strings.EqualFold(dns.CanonicalName(h.Name), dns.CanonicalName(zone))
}

func (d *UpdatePersist) httpUpdatesLocked(adds, deletes []dns.RR) ([]dns.RR, error) {
	var nsAdds, soaAdds, otherAdds []dns.RR
	for _, rr := range adds {
		c := copyIN(rr)
		switch {
		case isApexType(d.Zone, c, dns.TypeNS):
			nsAdds = append(nsAdds, c)
		case isApexType(d.Zone, c, dns.TypeSOA):
			soaAdds = append(soaAdds, c)
		default:
			otherAdds = append(otherAdds, c)
		}
	}

	var early, nsDels []dns.RR
	wipeNS := false
	wipeSOA := false
	for _, rr := range deletes {
		c := copyDelete(rr)
		switch {
		case isApexType(d.Zone, c, dns.TypeSOA):
			if c.Header().Class == dns.ClassANY {
				wipeSOA = true
			}
			continue
		case isApexType(d.Zone, c, dns.TypeNS):
			if c.Header().Class == dns.ClassANY {
				wipeNS = true
				continue
			}
			nsDels = append(nsDels, c)
		default:
			early = append(early, c)
		}
	}

	if wipeSOA && len(soaAdds) == 0 {
		return nil, fmt.Errorf("cannot delete the SOA")
	}
	if len(soaAdds) > 0 {
		cur := soaOf(d.rrs)
		if cur == nil {
			return nil, fmt.Errorf("zone has no SOA")
		}
		for _, rr := range soaAdds {
			soa, ok := rr.(*dns.SOA)
			if !ok {
				return nil, fmt.Errorf("invalid SOA")
			}
			if !serialGreater(soa.Serial, cur.Serial) {
				soa.Serial = cur.Serial + 1
			}
		}
	}

	if wipeNS {
		if len(nsAdds) == 0 {
			return nil, fmt.Errorf("cannot delete the apex NS set")
		}
		nsDels = nsDels[:0]
		for _, cur := range d.rrsetOf(d.Zone, dns.TypeNS) {
			if indexOfRR(nsAdds, cur) >= 0 {
				continue
			}
			c := dns.Copy(cur)
			c.Header().Class = dns.ClassNONE
			c.Header().Ttl = 0
			nsDels = append(nsDels, c)
		}
	}

	out := make([]dns.RR, 0, len(early)+len(otherAdds)+len(soaAdds)+len(nsAdds)+len(nsDels))
	out = append(out, early...)
	out = append(out, otherAdds...)
	out = append(out, soaAdds...)
	// Apex NS adds land before NS deletes so replacing the last nameserver
	// does not trip the last-NS guard in apply().
	out = append(out, nsAdds...)
	out = append(out, nsDels...)
	return out, nil
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
