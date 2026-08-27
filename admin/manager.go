package admin

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-ez/dns-update-persistent"
	"github.com/skymoore/coredns-ez/internal/zonereg"
	"github.com/skymoore/coredns-ez/ixfr"
	secondarypersist "github.com/skymoore/coredns-ez/secondary-persistent"
)

type sqliteJournal struct{ db *store.Store }

func (j sqliteJournal) Load(origin string) ([]byte, error) { return j.db.LoadIXFR(origin) }
func (j sqliteJournal) Save(origin string, data []byte) error {
	return j.db.SaveIXFR(origin, data)
}

type sqliteZones struct{ db *store.Store }

func (z sqliteZones) Save(origin string, rrs []dns.RR) error {
	return z.db.ReplaceRecords(origin, "", rrs)
}
func (z sqliteZones) Load(origin string) ([]dns.RR, error) {
	return z.db.ListRecords(origin, "")
}
func (z sqliteZones) Remove(origin string) error {
	return z.db.DeleteRecords(origin, "")
}

func (a *Admin) bindSQLiteSecondaries() {
	zs := sqliteZones{db: a.db}
	for _, s := range secondarypersist.Engines() {
		s.SetRecordStore(zs)
	}
}

func (a *Admin) persistPublic(origin string) dnsupdatepersist.PersistFunc {
	return func(rrs []dns.RR) error { return a.db.ReplaceRecords(origin, "", rrs) }
}

func (a *Admin) persistView(origin, acl string) dnsupdatepersist.PersistFunc {
	return func(rrs []dns.RR) error { return a.db.ReplaceRecords(origin, acl, rrs) }
}

func (a *Admin) attachPrimary(d *dnsupdatepersist.UpdatePersist, view string) error {
	origin := d.Origin()
	if view == "" {
		d.SetPersist(a.persistPublic(origin))
	} else {
		d.SetPersist(a.persistView(origin, view))
	}
	x := ixfr.New(origin, 64)
	x.SetBackend(sqliteJournal{db: a.db})
	if err := d.AttachIXFR(x); err != nil {
		log.Warningf("ixfr attach %s: %v", origin, err)
	}
	d.SetTransfer(a.xfer)
	d.SetNext(a.Next)
	return nil
}

func (a *Admin) primaryFromRRs(origin string, rrs []dns.RR, mutable map[uint16]bool) (*dnsupdatepersist.UpdatePersist, error) {
	d, err := dnsupdatepersist.NewFromRecords(origin, rrs, mutable)
	if err != nil {
		return nil, err
	}
	if err := a.attachPrimary(d, ""); err != nil {
		return nil, err
	}
	return d, nil
}

func (a *Admin) loadZoneRRs(origin, view string) ([]dns.RR, error) {
	rrs, err := a.db.ListRecords(origin, view)
	if err != nil {
		return nil, err
	}
	if soaOf(rrs) == nil {
		return nil, fmt.Errorf("%s has no SOA", origin)
	}
	return rrs, nil
}

func (a *Admin) secondaryEngine() *secondarypersist.SecondaryPersist {
	if a.secondaries == nil {
		a.secondaries = secondarypersist.NewEngine()
		a.secondaries.SetNext(a.Next)
		a.secondaries.SetTransfer(a.xfer)
		a.secondaries.SetRecordStore(sqliteZones{db: a.db})
	}
	return a.secondaries
}

func qualifyMbox(mbox, origin string) (string, error) {
	mbox = strings.TrimSpace(mbox)
	if mbox == "" {
		return "hostmaster." + origin, nil
	}
	if i := strings.LastIndex(mbox, "@"); i >= 0 {
		local := strings.TrimSpace(mbox[:i])
		domain := strings.TrimSpace(mbox[i+1:])
		if local == "" || domain == "" {
			return "", fmt.Errorf("invalid rname")
		}
		local = strings.ReplaceAll(local, `\`, "")
		local = strings.ReplaceAll(local, ".", `\.`)
		mbox = local + "." + domain
	}
	return strings.ToLower(dns.CanonicalName(mbox)), nil
}

func (a *Admin) createPrimary(origin string, nsHost, mbox string, mutable []string) error {
	origin = strings.ToLower(dns.CanonicalName(origin))
	if zonereg.PrimaryOf(origin) != nil || zonereg.SecondaryOf(origin) != nil {
		return errExists
	}
	if nsHost == "" {
		nsHost = "ns1." + origin
	}
	nsHost = strings.ToLower(dns.CanonicalName(nsHost))
	mbox, err := qualifyMbox(mbox, origin)
	if err != nil {
		return err
	}
	soa := &dns.SOA{
		Hdr:     dns.RR_Header{Name: origin, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
		Ns:      nsHost,
		Mbox:    mbox,
		Serial:  uint32(time.Now().Unix()),
		Refresh: 3600, Retry: 600, Expire: 86400, Minttl: 60,
	}
	ns := &dns.NS{
		Hdr: dns.RR_Header{Name: origin, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 300},
		Ns:  nsHost,
	}
	seed := []dns.RR{soa, ns}
	if err := a.db.ReplaceRecords(origin, "", seed); err != nil {
		return err
	}
	d, err := a.primaryFromRRs(origin, seed, parseMutable(mutable))
	if err != nil {
		return err
	}
	if err := zonereg.RegisterPrimary(d); err != nil {
		return err
	}
	a.mu.Lock()
	a.primaries[origin] = d
	a.mu.Unlock()
	row := store.ZoneRow{
		Origin: origin, Kind: zonereg.KindPrimary, Source: zonereg.SourceAdmin,
		Mutable: strings.Join(mutable, ","),
	}
	if err := a.db.UpsertZone(row); err != nil {
		return err
	}
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.refreshZoneMetrics()
	return nil
}

func (a *Admin) createSecondary(origin string, from []string) error {
	origin = strings.ToLower(dns.CanonicalName(origin))
	if zonereg.PrimaryOf(origin) != nil || zonereg.SecondaryOf(origin) != nil {
		return errExists
	}
	a.mu.Lock()
	sec := a.secondaryEngine()
	a.mu.Unlock()
	if err := sec.StartOrigin(origin, from, a.xfer); err != nil {
		return err
	}
	row := store.ZoneRow{
		Origin: origin, Kind: zonereg.KindSecondary, Source: zonereg.SourceAdmin,
		TransferFrom: store.JoinCSV(from),
	}
	if err := a.db.UpsertZone(row); err != nil {
		return err
	}
	_, _ = a.db.BumpGeneration()
	a.refreshZoneMetrics()
	return nil
}

func (a *Admin) deleteZone(origin string) error {
	origin = strings.ToLower(dns.CanonicalName(origin))
	_, dbErr := a.db.GetZone(origin)
	mem := zonereg.PrimaryOf(origin) != nil || zonereg.SecondaryOf(origin) != nil
	if dbErr != nil && !mem {
		return fmt.Errorf("unknown zone")
	}
	a.mu.Lock()
	delete(a.primaries, origin)
	sec := a.secondaries
	a.mu.Unlock()
	zonereg.Unregister(origin)
	a.dropViews(origin)
	if sec != nil {
		_ = sec.StopOrigin(origin)
	}
	_ = a.db.DeleteZone(origin)
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.refreshZoneMetrics()
	a.rebuildSigner()
	return nil
}

func (a *Admin) loadPersistedZones() error {
	a.ensureOriginsFromViews()
	rows, err := a.db.ListZones()
	if err != nil {
		return err
	}
	coreTxt := ""
	if b, err := os.ReadFile(corefilePath()); err == nil {
		coreTxt = string(b)
	}
	for _, z := range rows {
		if z.Source != zonereg.SourceAdmin {
			continue
		}
		switch z.Kind {
		case zonereg.KindPrimary:
			if a.cfg.Role == roleSecondary {
				if corefileHasOrigin(coreTxt, z.Origin) {
					continue
				}
				if _, err := a.loadZoneRRs(z.Origin, ""); err != nil {
					log.Warningf("skip secondary %s: %v", z.Origin, err)
					continue
				}
				from := store.SplitCSV(z.TransferFrom)
				if len(from) == 0 {
					from = a.primaryTransferFrom()
				}
				if err := a.createSecondaryNoPersist(z.Origin, from); err != nil {
					log.Warningf("load secondary %s: %v", z.Origin, err)
				}
				continue
			}
			if existing := zonereg.PrimaryOf(z.Origin); existing != nil {
				if d, ok := existing.(*dnsupdatepersist.UpdatePersist); ok {
					d.SetPersist(a.persistPublic(z.Origin))
					if rrs, err := a.loadZoneRRs(z.Origin, ""); err != nil {
						log.Warningf("reload sqlite %s: %v", z.Origin, err)
					} else if err := d.ReplaceRecords(rrs); err != nil {
						log.Warningf("reload sqlite %s: %v", z.Origin, err)
					}
					a.mu.Lock()
					a.primaries[z.Origin] = d
					a.mu.Unlock()
				}
				continue
			}
			rrs, err := a.loadZoneRRs(z.Origin, "")
			if err != nil {
				log.Warningf("load primary %s: %v", z.Origin, err)
				continue
			}
			d, err := a.primaryFromRRs(z.Origin, rrs, parseMutable(store.SplitCSV(z.Mutable)))
			if err != nil {
				log.Warningf("load primary %s: %v", z.Origin, err)
				continue
			}
			if err := zonereg.RegisterPrimary(d); err != nil {
				log.Warningf("zonereg %s: %v", z.Origin, err)
				continue
			}
			a.mu.Lock()
			a.primaries[z.Origin] = d
			a.mu.Unlock()
		case zonereg.KindSecondary:
			if corefileHasOrigin(coreTxt, z.Origin) {
				continue
			}
			if _, err := a.loadZoneRRs(z.Origin, ""); err != nil {
				log.Warningf("skip secondary %s: %v", z.Origin, err)
				continue
			}
			from := store.SplitCSV(z.TransferFrom)
			if a.cfg.PrimaryDNS != "" {
				from = []string{a.cfg.PrimaryDNS}
			}
			if err := a.createSecondaryNoPersist(z.Origin, from); err != nil {
				log.Warningf("load secondary %s: %v", z.Origin, err)
			}
		}
	}
	if err := a.loadViews(); err != nil {
		log.Warningf("load views: %v", err)
	}
	a.refreshZoneMetrics()
	a.rebuildSigner()
	return nil
}

// ensureOriginsFromViews creates a zone row (and a public SOA/NS stub) for
// origins that only exist as ACL views. Import used to write zone_views and
// skip zones, so those origins vanished from the UI.
func (a *Admin) ensureOriginsFromViews() {
	views, err := a.db.ListZoneViews()
	if err != nil {
		return
	}
	changed := false
	for _, v := range views {
		if _, err := a.db.GetZone(v.Origin); err != nil {
			kind := zonereg.KindPrimary
			if a.cfg.Role == roleSecondary {
				kind = zonereg.KindSecondary
			}
			_ = a.db.UpsertZone(store.ZoneRow{
				Origin: v.Origin, Kind: kind, Source: zonereg.SourceAdmin,
			})
			changed = true
		}
		rrs, err := a.db.ListRecords(v.Origin, v.ACL)
		if err != nil {
			continue
		}
		had := a.db.HasSOA(v.Origin)
		if err := a.seedPublicApex(v.Origin, rrs); err != nil {
			log.Warningf("seed public apex %s: %v", v.Origin, err)
			continue
		}
		if !had && a.db.HasSOA(v.Origin) {
			changed = true
		}
	}
	if changed {
		_, _ = a.db.BumpGeneration()
	}
}

func (a *Admin) seedPublicApex(origin string, from []dns.RR) error {
	origin = strings.ToLower(dns.CanonicalName(origin))
	if a.db.HasSOA(origin) {
		return nil
	}
	var apex []dns.RR
	for _, rr := range from {
		if rr == nil {
			continue
		}
		h := rr.Header()
		if !strings.EqualFold(h.Name, origin) {
			continue
		}
		switch h.Rrtype {
		case dns.TypeSOA, dns.TypeNS:
			apex = append(apex, dns.Copy(rr))
		}
	}
	if soaOf(apex) == nil {
		return nil
	}
	return a.db.ReplaceRecords(origin, "", apex)
}

func (a *Admin) createSecondaryNoPersist(origin string, from []string) error {
	a.mu.Lock()
	sec := a.secondaryEngine()
	a.mu.Unlock()
	return sec.StartOrigin(origin, from, a.xfer)
}

func parseMutable(types []string) map[uint16]bool {
	if len(types) == 0 {
		return nil
	}
	m := map[uint16]bool{}
	for _, t := range types {
		code, ok := dns.StringToType[strings.ToUpper(t)]
		if ok {
			m[code] = true
		}
	}
	return m
}

func (a *Admin) refreshZoneMetrics() {
	zoneGauge.Reset()
	for _, z := range zonereg.All() {
		zoneGauge.WithLabelValues(z.Kind, z.Source).Inc()
	}
}
