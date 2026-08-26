package dnsupdatepersist

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/miekg/dns"
)

// serveUpdate implements RFC 2136 §3. The section names below are the RFC's,
// and the order is load-bearing: every prerequisite is checked before any
// change is prescanned, and the whole update section is prescanned before any
// of it is applied. An update that fails halfway is the one outcome RFC 2136
// §3.4.2.1 forbids.
//
// A mutating update is persisted to seedPath before the in-memory view is
// swapped. NOERROR therefore means the new zone is on disk. A write failure
// leaves memory and disk on the previous generation and returns SERVFAIL.
func (d *UpdatePersist) serveUpdate(w dns.ResponseWriter, r *dns.Msg) (int, error) {
	// §3.1.1 — the Zone section is exactly one record, of type SOA, in the
	// zone's own class.
	if len(r.Question) != 1 || r.Question[0].Qtype != dns.TypeSOA || r.Question[0].Qclass != dns.ClassINET {
		return d.reply(w, r, dns.RcodeFormatError)
	}

	zone := strings.ToLower(dns.CanonicalName(r.Question[0].Name))
	if zone != d.Zone {
		// NOTAUTH, not REFUSED: the distinction tells an updater "you have the
		// wrong server" rather than "you have the wrong credentials", and
		// those lead to very different debugging.
		return d.reply(w, r, dns.RcodeNotAuth)
	}

	// Authentication is the tsig plugin's job — it owns the keys and the
	// verification. This checks only that it happened, because a dynamic-update
	// endpoint that answers an unsigned request is an open zone-mutation API,
	// and "the operator surely configured tsig in front" is not an access
	// control. There is deliberately no insecure mode.
	if !tsigVerified(w, r) {
		log.Warningf("refusing unsigned or unverified UPDATE for %s from %s writer=%T",
			zone, w.RemoteAddr(), w)
		return d.reply(w, r, dns.RcodeRefused)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if rcode := d.checkPrereqs(r.Answer); rcode != dns.RcodeSuccess {
		return d.reply(w, r, rcode)
	}
	if rcode := d.prescan(r.Ns); rcode != dns.RcodeSuccess {
		return d.reply(w, r, rcode)
	}

	return d.reply(w, r, d.commitLocked(zone, r.Ns))
}

// commitLocked applies updates to a copy, persists, journals, and swaps.
// The caller holds d.mu. Returns the RFC 2136 rcode.
func (d *UpdatePersist) commitLocked(zone string, updates []dns.RR) int {
	updated, changed := d.apply(updates)
	if !changed {
		// RFC 2136 §3.4.2.7: an update that changes nothing is still a
		// success. Silently doing nothing and reporting NOERROR is correct,
		// and is why an ACME client re-adding an identical TXT does not fail.
		writeCount.WithLabelValues(d.Zone, "skipped").Inc()
		return dns.RcodeSuccess
	}

	bumpSerial(updated)
	if err := d.persistUpdated(updated); err != nil {
		log.Errorf("persisting %s to %s: %v", zone, d.seedPath, err)
		writeCount.WithLabelValues(d.Zone, "error").Inc()
		return dns.RcodeServerFailure
	}
	if d.ixfr != nil {
		if err := d.ixfr.Commit(d.rrs, updated); err != nil {
			log.Errorf("ixfr journal for %s: %v", zone, err)
			writeCount.WithLabelValues(d.Zone, "error").Inc()
			return dns.RcodeServerFailure
		}
	}
	writeCount.WithLabelValues(d.Zone, "ok").Inc()
	if soa := soaOf(updated); soa != nil {
		serialGauge.WithLabelValues(d.Zone).Set(float64(soa.Serial))
	}

	if err := d.swap(updated); err != nil {
		// Disk already holds the new generation; the next restart is correct.
		// This process keeps serving the previous view until then.
		log.Errorf("rebuilding %s after UPDATE: %v", zone, err)
		return dns.RcodeServerFailure
	}

	if xfer := d.Xfer; xfer != nil {
		// Without this a secondary only sees the change at its next refresh,
		// which for an ACME challenge with a 60s validation window is
		// indistinguishable from the update never having happened.
		// Notify is UDP and can block for seconds if a peer does not
		// answer; the zone is already on disk, so do not hold the
		// UPDATE/HTTP caller on that timeout.
		go func() {
			if err := xfer.Notify(zone); err != nil {
				log.Warningf("NOTIFY for %s after UPDATE: %v", zone, err)
			}
		}()
	}

	return dns.RcodeSuccess
}

// tsigVerified reports whether the request carried a TSIG that the server
// validated. w is wrapped by CoreDNS (ScrubWriter, and the tsig plugin's own
// writer), so the status is reached through an interface assertion rather than
// off the concrete type.
//
// The tsig plugin strips the TSIG RR after a successful verify, so a live
// request reaching this plugin often has r.IsTsig() == nil. That wrapper is
// unexported (*tsig.restoreTsigWriter); seeing it with a nil TsigStatus is
// the production signal that verification happened. Unit tests keep the RR
// on the message and use a fake writer.
func tsigVerified(w dns.ResponseWriter, r *dns.Msg) bool {
	s, ok := w.(interface{ TsigStatus() error })
	if !ok || s.TsigStatus() != nil {
		return false
	}
	if r.IsTsig() != nil {
		return true
	}
	// tsig plugin strips the RR and wraps w; NextOrFailure then wraps again
	// with *plugin.pluginWriter. Walk the exported ResponseWriter field.
	for cur, n := w, 0; cur != nil && n < 8; n++ {
		if strings.HasSuffix(fmt.Sprintf("%T", cur), "tsig.restoreTsigWriter") {
			return true
		}
		next := unwrapResponseWriter(cur)
		if next == nil || next == cur {
			break
		}
		cur = next
	}
	return false
}

func unwrapResponseWriter(w dns.ResponseWriter) dns.ResponseWriter {
	v := reflect.ValueOf(w)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	f := v.FieldByName("ResponseWriter")
	if !f.IsValid() || !f.CanInterface() {
		return nil
	}
	inner, _ := f.Interface().(dns.ResponseWriter)
	return inner
}

// checkPrereqs implements §3.2. All five prerequisite forms are supported;
// anything else is a format error rather than a guess.
func (d *UpdatePersist) checkPrereqs(prereqs []dns.RR) int {
	// Value-dependent prerequisites (§3.2.3) are collected and compared as
	// whole RRsets at the end, because "this RRset equals exactly these
	// records" cannot be decided one record at a time.
	valueDependent := map[rrsetKey][]dns.RR{}

	for _, rr := range prereqs {
		h := rr.Header()
		if h.Ttl != 0 {
			return dns.RcodeFormatError
		}
		if !d.inZone(h.Name) {
			return dns.RcodeNotZone
		}

		switch h.Class {
		case dns.ClassANY:
			if h.Rdlength != 0 {
				return dns.RcodeFormatError
			}
			if h.Rrtype == dns.TypeANY {
				if !d.nameInUse(h.Name) {
					return dns.RcodeNameError // NXDOMAIN
				}
			} else if !d.rrsetExists(h.Name, h.Rrtype) {
				return dns.RcodeNXRrset
			}

		case dns.ClassNONE:
			if h.Rdlength != 0 {
				return dns.RcodeFormatError
			}
			if h.Rrtype == dns.TypeANY {
				if d.nameInUse(h.Name) {
					return dns.RcodeYXDomain
				}
			} else if d.rrsetExists(h.Name, h.Rrtype) {
				return dns.RcodeYXRrset
			}

		case dns.ClassINET:
			k := keyOf(h.Name, h.Rrtype)
			valueDependent[k] = append(valueDependent[k], rr)

		default:
			return dns.RcodeFormatError
		}
	}

	for k, want := range valueDependent {
		if !sameRRset(d.rrsetOf(k.name, k.rrtype), want) {
			return dns.RcodeNXRrset
		}
	}

	return dns.RcodeSuccess
}

// prescan implements §3.4.1: reject the entire update if any single record in
// it is malformed, out of zone, or against policy. Nothing has been applied at
// this point, which is the whole reason the RFC separates the two passes.
func (d *UpdatePersist) prescan(updates []dns.RR) int {
	for _, rr := range updates {
		h := rr.Header()
		if !d.inZone(h.Name) {
			return dns.RcodeNotZone
		}
		// Meta-types are queries, not records; none of them can appear in an
		// update section in any class.
		deleteEverythingAtName := h.Class == dns.ClassANY && h.Rrtype == dns.TypeANY
		if isMetaType(h.Rrtype) && !deleteEverythingAtName {
			return dns.RcodeFormatError
		}

		switch h.Class {
		case dns.ClassINET:
			if h.Rrtype == dns.TypeANY {
				return dns.RcodeFormatError
			}
		case dns.ClassANY:
			if h.Ttl != 0 || h.Rdlength != 0 {
				return dns.RcodeFormatError
			}
		case dns.ClassNONE:
			if h.Ttl != 0 || h.Rrtype == dns.TypeANY {
				return dns.RcodeFormatError
			}
		default:
			return dns.RcodeFormatError
		}

		// Type policy is checked here, in the prescan, so a disallowed type
		// rejects the whole update rather than letting part of it land. An
		// UPDATE key that only needs to publish ACME challenges should not be
		// able to repoint an A record, and TSIG cannot express that.
		if d.mutable != nil && h.Rrtype != dns.TypeANY && !d.mutable[h.Rrtype] {
			log.Warningf("UPDATE for %s rejected: type %s is not in the mutable set",
				h.Name, dns.TypeToString[h.Rrtype])
			return dns.RcodeRefused
		}
	}
	return dns.RcodeSuccess
}

// apply implements §3.4.2 against a copy, returning the new record set and
// whether anything actually changed. Records are deep-copied so a later persist
// failure cannot leak TTL or serial mutations into the live zone.
func (d *UpdatePersist) apply(updates []dns.RR) ([]dns.RR, bool) {
	out := make([]dns.RR, len(d.rrs))
	for i, rr := range d.rrs {
		out[i] = dns.Copy(rr)
	}
	changed := false

	for _, rr := range updates {
		h := rr.Header()
		name := strings.ToLower(dns.CanonicalName(h.Name))
		apex := name == d.Zone

		switch h.Class {
		case dns.ClassINET:
			// §3.4.2.3. SOA is special: an added SOA only takes effect if its
			// serial is greater than the current one, so a stale updater
			// cannot wind the zone backwards.
			if h.Rrtype == dns.TypeSOA {
				cur := soaOf(out)
				new, ok := rr.(*dns.SOA)
				if !ok || cur == nil || !serialGreater(new.Serial, cur.Serial) {
					continue
				}
			}
			// CNAME exclusivity, both directions. Silently ignored rather than
			// rejected, per §3.4.2.3.
			if h.Rrtype == dns.TypeCNAME && hasNonCNAME(out, name) {
				continue
			}
			if h.Rrtype != dns.TypeCNAME && h.Rrtype != dns.TypeSOA && hasCNAME(out, name) {
				continue
			}

			if i := indexOfRR(out, rr); i >= 0 {
				// Identical record already present: only the TTL is updated.
				if out[i].Header().Ttl != h.Ttl {
					out[i].Header().Ttl = h.Ttl
					changed = true
				}
				continue
			}
			out = append(out, dns.Copy(rr))
			changed = true

		case dns.ClassANY:
			if h.Rrtype == dns.TypeANY {
				// Delete every RRset at the name. At the apex, SOA and NS
				// survive: a zone without them is not a zone, and §3.4.2.3
				// says to leave them rather than to fail.
				out, changed = deleteWhere(out, changed, func(x dns.RR) bool {
					if !sameName(x, name) {
						return false
					}
					if apex && (x.Header().Rrtype == dns.TypeSOA || x.Header().Rrtype == dns.TypeNS) {
						return false
					}
					return true
				})
				continue
			}
			if apex && (h.Rrtype == dns.TypeSOA || h.Rrtype == dns.TypeNS) {
				continue
			}
			out, changed = deleteWhere(out, changed, func(x dns.RR) bool {
				return sameName(x, name) && x.Header().Rrtype == h.Rrtype
			})

		case dns.ClassNONE:
			// §3.4.2.4 — delete one specific record. The apex SOA is never
			// deletable, and the last apex NS is kept for the same reason as
			// above.
			if apex && h.Rrtype == dns.TypeSOA {
				continue
			}
			if apex && h.Rrtype == dns.TypeNS && countRRset(out, name, dns.TypeNS) <= 1 {
				continue
			}
			if i := indexOfRR(out, rr); i >= 0 {
				out = append(out[:i], out[i+1:]...)
				changed = true
			}
		}
	}

	return out, changed
}
