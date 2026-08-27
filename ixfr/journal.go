package ixfr

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/miekg/dns"
)

// increment is one RFC 1995 delta: the non-SOA RRs removed and added when the
// serial moved from oldSerial to newSerial.
type increment struct {
	oldSerial uint32
	newSerial uint32
	deleted   []dns.RR
	added     []dns.RR
}

// Journal is the in-memory IXFR history plus the current snapshot used for
// AXFR fallback.
type Journal struct {
	origin  string
	history int
	backend JournalBackend
	incs    []increment
	current []dns.RR
}

func newJournal(origin string, history int, current []dns.RR) *Journal {
	return &Journal{
		origin:  origin,
		history: history,
		current: copyRRs(current),
	}
}

func loadJournal(origin string, history int, current []dns.RR, backend JournalBackend) (*Journal, error) {
	j := newJournal(origin, history, current)
	j.backend = backend
	var incs []increment
	if backend != nil {
		body, err := backend.Load(origin)
		if err != nil {
			return nil, err
		}
		if len(body) > 0 {
			parsed, err := parseJournal(bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			incs = parsed
		}
	}
	cur := serialOf(current)
	j.incs = reconcile(incs, cur)
	return j, nil
}

// reconcile keeps the longest suffix of incs that chains and whose last
// newSerial equals the zone file's current serial. Anything else is junk
// (crash between writes, extra increment, truncated log).
func reconcile(incs []increment, current uint32) []increment {
	if len(incs) == 0 {
		return nil
	}
	end := -1
	for i, inc := range incs {
		if inc.newSerial == current {
			end = i
		}
	}
	if end < 0 {
		return nil
	}
	incs = incs[:end+1]
	for start := 0; start < len(incs); start++ {
		if chained(incs[start:]) {
			return incs[start:]
		}
	}
	return nil
}

func chained(incs []increment) bool {
	for i := 1; i < len(incs); i++ {
		if incs[i].oldSerial != incs[i-1].newSerial {
			return false
		}
	}
	return true
}

func (j *Journal) commit(old, new []dns.RR) error {
	oldSOA, newSOA := soaOf(old), soaOf(new)
	if oldSOA == nil || newSOA == nil {
		return fmt.Errorf("ixfr: commit requires SOA on both generations")
	}
	inc := increment{
		oldSerial: oldSOA.Serial,
		newSerial: newSOA.Serial,
		deleted:   copyRRs(diffMissing(old, new)),
		added:     copyRRs(diffMissing(new, old)),
	}
	next := append(append([]increment(nil), j.incs...), inc)
	if len(next) > j.history {
		next = next[len(next)-j.history:]
	}
	if err := j.persist(newSOA.Serial, next); err != nil {
		return err
	}
	j.incs = next
	j.current = copyRRs(new)
	log.Infof("IXFR committed %s %d -> %d (%d deleted, %d added)", j.origin, inc.oldSerial, inc.newSerial, len(inc.deleted), len(inc.added))
	return nil
}

func (j *Journal) persist(serial uint32, incs []increment) error {
	if j.backend == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := writeJournal(&buf, j.origin, j.history, serial, incs); err != nil {
		return err
	}
	return j.backend.Save(j.origin, buf.Bytes())
}

const (
	kindIXFR     = "ixfr"
	kindAXFR     = "axfr"
	kindUptodate = "uptodate"
)

// answer builds the RR stream for Transfer. serial 0 is AXFR.
func (j *Journal) answer(serial uint32) (kind string, rrs []dns.RR) {
	cur := serialOf(j.current)
	if serial == 0 {
		return kindAXFR, axfrRRs(j.current)
	}
	if serial == cur || serialNewer(serial, cur) {
		if soa := soaOf(j.current); soa != nil {
			return kindUptodate, []dns.RR{dns.Copy(soa)}
		}
		return kindUptodate, nil
	}
	stream, ok := j.ixfrFrom(serial)
	if !ok {
		return kindAXFR, axfrRRs(j.current)
	}
	return kindIXFR, stream
}

func (j *Journal) ixfrFrom(serial uint32) ([]dns.RR, bool) {
	start := -1
	for i, inc := range j.incs {
		if inc.oldSerial == serial {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, false
	}
	newSOA := soaOf(j.current)
	if newSOA == nil {
		return nil, false
	}
	// Envelope SOA is the current serial; inner increments may end earlier
	// if history was truncated in a way that still chains from `serial`.
	if j.incs[len(j.incs)-1].newSerial != newSOA.Serial {
		return nil, false
	}

	var out []dns.RR
	out = append(out, dns.Copy(newSOA))
	for _, inc := range j.incs[start:] {
		oldSOA := soaWithSerial(newSOA, inc.oldSerial)
		incSOA := soaWithSerial(newSOA, inc.newSerial)
		out = append(out, oldSOA)
		out = append(out, copyRRs(inc.deleted)...)
		out = append(out, incSOA)
		out = append(out, copyRRs(inc.added)...)
	}
	out = append(out, dns.Copy(newSOA))
	return out, true
}

func axfrRRs(rrs []dns.RR) []dns.RR {
	soa := soaOf(rrs)
	if soa == nil {
		return copyRRs(rrs)
	}
	out := make([]dns.RR, 0, len(rrs)+1)
	out = append(out, dns.Copy(soa))
	for _, rr := range rrs {
		if rr == soa {
			continue
		}
		if _, ok := rr.(*dns.SOA); ok {
			continue
		}
		out = append(out, dns.Copy(rr))
	}
	out = append(out, dns.Copy(soa))
	return out
}

// diffMissing returns RRs in a that are not in b, excluding SOA. Comparison
// includes TTL, so a TTL-only change is a delete+add.
func diffMissing(a, b []dns.RR) []dns.RR {
	have := make(map[string]struct{}, len(b))
	for _, rr := range b {
		if _, ok := rr.(*dns.SOA); ok {
			continue
		}
		have[ixfrKey(rr)] = struct{}{}
	}
	var out []dns.RR
	for _, rr := range a {
		if _, ok := rr.(*dns.SOA); ok {
			continue
		}
		if _, ok := have[ixfrKey(rr)]; !ok {
			out = append(out, rr)
		}
	}
	return out
}

func ixfrKey(rr dns.RR) string {
	c := dns.Copy(rr)
	h := c.Header()
	h.Name = strings.ToLower(dns.CanonicalName(h.Name))
	h.Class = dns.ClassINET
	return c.String()
}

func soaWithSerial(tmpl *dns.SOA, serial uint32) *dns.SOA {
	s := dns.Copy(tmpl).(*dns.SOA)
	s.Serial = serial
	return s
}

func soaOf(rrs []dns.RR) *dns.SOA {
	for _, rr := range rrs {
		if soa, ok := rr.(*dns.SOA); ok {
			return soa
		}
	}
	return nil
}

func serialOf(rrs []dns.RR) uint32 {
	if soa := soaOf(rrs); soa != nil {
		return soa.Serial
	}
	return 0
}

func copyRRs(rrs []dns.RR) []dns.RR {
	out := make([]dns.RR, len(rrs))
	for i, rr := range rrs {
		out[i] = dns.Copy(rr)
	}
	return out
}

func canonical(name string) string {
	return strings.ToLower(dns.CanonicalName(name))
}

// serialNewer reports whether a is newer than b under RFC 1982 arithmetic.
func serialNewer(a, b uint32) bool {
	if a == b {
		return false
	}
	return int32(a-b) > 0
}
