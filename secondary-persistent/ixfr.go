package secondarypersist

import (
	"errors"
	"fmt"

	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/pkg/catalog"
	"github.com/coredns/coredns/plugin/transfer"

	"github.com/miekg/dns"
)

var errIXFRFallback = errors.New("ixfr fallback to axfr")

type xfrKind int

const (
	xfrUnknown xfrKind = iota
	xfrUptodate
	xfrAXFR
	xfrIXFR
)

type ixfrIncrement struct {
	oldSerial uint32
	newSerial uint32
	deleted   []dns.RR
	added     []dns.RR
}

func (s *SecondaryPersist) transferIXFR(origin string, z *file.Zone, t *transfer.Transfer, isCatalog bool) error {
	soa := zoneSOA(z)
	if soa == nil {
		return errIXFRFallback
	}

	m := new(dns.Msg)
	m.SetIxfr(dns.Fqdn(origin), soa.Serial, soa.Ns, soa.Mbox)

	if len(z.TransferFrom) == 0 {
		return nil
	}

	var (
		xferErr error
		rrs     []dns.RR
	)
	for _, tr := range z.TransferFrom {
		xfer := new(dns.Transfer)
		ch, err := xfer.In(m, tr)
		if err != nil {
			log.Errorf("Failed to setup IXFR `%s' with `%q': %v", origin, tr, err)
			xferErr = err
			continue
		}
		got, err := collectEnvelopes(ch)
		if err != nil {
			log.Errorf("Failed IXFR `%s' from %q: %v", origin, tr, err)
			xferErr = err
			continue
		}
		rrs = got
		xferErr = nil
		break
	}
	if xferErr != nil {
		return xferErr
	}

	kind, err := classifyXFR(rrs)
	if err != nil {
		return err
	}
	switch kind {
	case xfrUptodate:
		// miekg/dns uses naive serial comparison. shouldTransfer already said
		// the remote is newer, so treat "uptodate" as a lie and fall back.
		return errIXFRFallback
	case xfrAXFR:
		return s.installAXFR(origin, z, t, rrs, isCatalog)
	case xfrIXFR:
		return s.installIXFR(origin, z, t, rrs, isCatalog)
	default:
		return errIXFRFallback
	}
}

func collectEnvelopes(ch <-chan *dns.Envelope) ([]dns.RR, error) {
	var rrs []dns.RR
	for env := range ch {
		if env.Error != nil {
			return nil, env.Error
		}
		rrs = append(rrs, env.RR...)
	}
	if len(rrs) == 0 {
		return nil, errors.New("empty transfer")
	}
	return rrs, nil
}

func classifyXFR(rrs []dns.RR) (xfrKind, error) {
	if len(rrs) == 0 {
		return xfrUnknown, errors.New("empty transfer")
	}
	first, ok := rrs[0].(*dns.SOA)
	if !ok {
		return xfrUnknown, errors.New("transfer does not start with SOA")
	}
	if len(rrs) == 1 {
		return xfrUptodate, nil
	}
	last, ok := rrs[len(rrs)-1].(*dns.SOA)
	if !ok {
		return xfrUnknown, errors.New("transfer does not end with SOA")
	}
	if last.Serial != first.Serial {
		return xfrUnknown, fmt.Errorf("transfer first/last SOA serial mismatch (%d != %d)", first.Serial, last.Serial)
	}
	innerDifferent := false
	for _, rr := range rrs[1 : len(rrs)-1] {
		if soa, ok := rr.(*dns.SOA); ok && soa.Serial != first.Serial {
			innerDifferent = true
			break
		}
	}
	if innerDifferent {
		return xfrIXFR, nil
	}
	return xfrAXFR, nil
}

func (s *SecondaryPersist) installAXFR(origin string, z *file.Zone, t *transfer.Transfer, rrs []dns.RR, isCatalog bool) error {
	candidate := z.CopyWithoutApex()
	var parsed *catalog.Catalog
	for _, rr := range rrs {
		if err := candidate.Insert(rr); err != nil {
			return err
		}
	}
	if isCatalog {
		cat, err := catalog.Parse(origin, rrs)
		if err != nil {
			return err
		}
		parsed = cat
	}
	installZoneData(z, candidate)
	if t != nil {
		if err := t.Notify(origin); err != nil {
			log.Warningf("Failed sending notifies: %s", err)
		}
	}
	if parsed != nil {
		s.storeCatalog(origin, parsed)
		s.startCatalogMembers(origin, parsed, z, t)
		log.Infof("Parsed catalog zone %s with %d member zones", origin, len(parsed.Members))
	}
	return nil
}

func (s *SecondaryPersist) installIXFR(origin string, z *file.Zone, t *transfer.Transfer, rrs []dns.RR, isCatalog bool) error {
	first, _ := rrs[0].(*dns.SOA)
	increments, err := parseIXFR(rrs)
	if err != nil {
		return err
	}
	current := dumpRRs(z)
	updated, err := applyIncrements(current, increments, first)
	if err != nil {
		return err
	}

	candidate := file.NewZone(origin, z.File())
	candidate.TransferFrom = append([]string(nil), z.TransferFrom...)
	for _, rr := range updated {
		if err := candidate.Insert(rr); err != nil {
			return err
		}
	}
	if zoneSOA(candidate) == nil {
		return errors.New("ixfr result has no SOA")
	}

	var parsed *catalog.Catalog
	if isCatalog {
		cat, err := catalog.Parse(origin, dumpRRs(candidate))
		if err != nil {
			return err
		}
		parsed = cat
	}

	installZoneData(z, candidate)
	if t != nil {
		if err := t.Notify(origin); err != nil {
			log.Warningf("Failed sending notifies: %s", err)
		}
	}
	if parsed != nil {
		s.storeCatalog(origin, parsed)
		s.startCatalogMembers(origin, parsed, z, t)
		log.Infof("Parsed catalog zone %s with %d member zones", origin, len(parsed.Members))
	}
	return nil
}

func parseIXFR(rrs []dns.RR) ([]ixfrIncrement, error) {
	if len(rrs) < 2 {
		return nil, errors.New("ixfr too short")
	}
	first, ok := rrs[0].(*dns.SOA)
	if !ok {
		return nil, errors.New("ixfr does not start with SOA")
	}
	last, ok := rrs[len(rrs)-1].(*dns.SOA)
	if !ok || last.Serial != first.Serial {
		return nil, errors.New("ixfr does not end with matching SOA")
	}

	const (
		modeNone = iota
		modeDel
		modeAdd
	)
	mode := modeNone
	var incs []ixfrIncrement
	var cur ixfrIncrement

	flush := func() error {
		if mode == modeNone {
			return nil
		}
		if mode != modeAdd {
			return errors.New("ixfr increment missing add section")
		}
		if cur.oldSerial == cur.newSerial || !less(cur.oldSerial, cur.newSerial) {
			return fmt.Errorf("ixfr increment serial %d is not newer than %d", cur.newSerial, cur.oldSerial)
		}
		incs = append(incs, cur)
		cur = ixfrIncrement{}
		return nil
	}

	for _, rr := range rrs[1 : len(rrs)-1] {
		soa, isSOA := rr.(*dns.SOA)
		if !isSOA {
			switch mode {
			case modeDel:
				cur.deleted = append(cur.deleted, rr)
			case modeAdd:
				cur.added = append(cur.added, rr)
			default:
				return nil, errors.New("ixfr record outside delete/add section")
			}
			continue
		}
		if soa.Serial != first.Serial {
			if mode == modeAdd {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			mode = modeDel
			cur.oldSerial = soa.Serial
			continue
		}
		if mode != modeDel {
			return nil, errors.New("ixfr add SOA without preceding delete SOA")
		}
		mode = modeAdd
		cur.newSerial = soa.Serial
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(incs) == 0 {
		return nil, errors.New("ixfr contained no increments")
	}
	if incs[len(incs)-1].newSerial != first.Serial {
		return nil, fmt.Errorf("ixfr final increment serial %d does not match envelope %d", incs[len(incs)-1].newSerial, first.Serial)
	}
	return incs, nil
}

func applyIncrements(current []dns.RR, incs []ixfrIncrement, newSOA *dns.SOA) ([]dns.RR, error) {
	out := append([]dns.RR(nil), current...)
	for _, inc := range incs {
		out = removeRRs(out, inc.deleted)
		out = append(out, inc.added...)
	}
	out = replaceSOA(out, newSOA)
	return out, nil
}

func removeRRs(from, del []dns.RR) []dns.RR {
	out := from[:0:0]
	if cap(from) > 0 {
		out = make([]dns.RR, 0, len(from))
	}
Next:
	for _, rr := range from {
		for i, d := range del {
			if d != nil && dns.IsDuplicate(rr, d) {
				del[i] = nil
				continue Next
			}
		}
		out = append(out, rr)
	}
	return out
}

func replaceSOA(rrs []dns.RR, soa *dns.SOA) []dns.RR {
	found := false
	out := make([]dns.RR, 0, len(rrs))
	for _, rr := range rrs {
		if _, ok := rr.(*dns.SOA); ok {
			if !found {
				out = append(out, soa)
				found = true
			}
			continue
		}
		out = append(out, rr)
	}
	if !found {
		out = append([]dns.RR{soa}, out...)
	}
	return out
}
