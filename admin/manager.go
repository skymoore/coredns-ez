package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-plugins/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-plugins/dns-update-persistent"
	"github.com/skymoore/coredns-plugins/internal/zonereg"
	"github.com/skymoore/coredns-plugins/ixfr"
	secondarypersist "github.com/skymoore/coredns-plugins/secondary-persistent"
)

func persistName(origin string) string {
	return "db." + strings.TrimSuffix(origin, ".")
}

func (a *Admin) createPrimary(origin string, nsHost string, mutable []string) error {
	origin = strings.ToLower(dns.CanonicalName(origin))
	if zonereg.PrimaryOf(origin) != nil || zonereg.SecondaryOf(origin) != nil {
		return errExists
	}
	if err := os.MkdirAll(a.cfg.Data, 0o755); err != nil {
		return err
	}
	path := filepath.Join(a.cfg.Data, persistName(origin))
	if nsHost == "" {
		nsHost = "ns1." + origin
	}
	nsHost = strings.ToLower(dns.CanonicalName(nsHost))
	soa := &dns.SOA{
		Hdr:     dns.RR_Header{Name: origin, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 300},
		Ns:      nsHost,
		Mbox:    "hostmaster." + origin,
		Serial:  uint32(time.Now().Unix()),
		Refresh: 3600, Retry: 600, Expire: 86400, Minttl: 60,
	}
	ns := &dns.NS{
		Hdr: dns.RR_Header{Name: origin, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 300},
		Ns:  nsHost,
	}
	if err := dnsupdatepersist.WriteSeed(path, origin, []dns.RR{soa, ns}); err != nil {
		return err
	}
	mut := parseMutable(mutable)
	d, err := dnsupdatepersist.New(origin, path, mut)
	if err != nil {
		return err
	}
	x := ixfr.New(origin, path+".ixfr", 64)
	if err := d.AttachIXFR(x); err != nil {
		log.Warningf("ixfr attach %s: %v", origin, err)
	}
	d.SetTransfer(a.xfer)
	d.SetNext(a.Next)
	if err := zonereg.RegisterPrimary(d); err != nil {
		return err
	}
	a.mu.Lock()
	a.primaries[origin] = d
	a.mu.Unlock()
	row := store.ZoneRow{
		Origin: origin, Kind: zonereg.KindPrimary, Source: zonereg.SourceAdmin,
		PersistPath: path, Mutable: strings.Join(mutable, ","),
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
	if err := os.MkdirAll(a.cfg.Data, 0o755); err != nil {
		return err
	}
	a.mu.Lock()
	if a.secondaries == nil {
		a.secondaries = secondarypersist.NewWithDirectory(a.cfg.Data)
		a.secondaries.SetNext(a.Next)
		a.secondaries.SetTransfer(a.xfer)
	}
	sec := a.secondaries
	a.mu.Unlock()
	if err := sec.StartOrigin(origin, from, a.xfer); err != nil {
		return err
	}
	row := store.ZoneRow{
		Origin: origin, Kind: zonereg.KindSecondary, Source: zonereg.SourceAdmin,
		PersistPath:  filepath.Join(a.cfg.Data, persistName(origin)),
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
	a.mu.Lock()
	if p, ok := a.primaries[origin]; ok {
		delete(a.primaries, origin)
		a.mu.Unlock()
		zonereg.Unregister(origin)
		_ = os.Remove(p.Path())
		_ = os.Remove(p.Path() + ".ixfr")
		a.dropViews(origin)
		_ = a.db.DeleteZone(origin)
		_, _ = a.db.BumpGeneration()
		go a.pushSnapshot()
		a.refreshZoneMetrics()
		return nil
	}
	sec := a.secondaries
	a.mu.Unlock()
	if sec != nil {
		if err := sec.StopOrigin(origin); err == nil {
			_ = a.db.DeleteZone(origin)
			_, _ = a.db.BumpGeneration()
			a.refreshZoneMetrics()
			return nil
		}
	}
	return fmt.Errorf("unknown zone")
}

func (a *Admin) loadPersistedZones() error {
	rows, err := a.db.ListZones()
	if err != nil {
		return err
	}
	for _, z := range rows {
		if z.Source != zonereg.SourceAdmin {
			continue
		}
		switch z.Kind {
		case zonereg.KindPrimary:
			d, err := dnsupdatepersist.New(z.Origin, z.PersistPath, parseMutable(store.SplitCSV(z.Mutable)))
			if err != nil {
				log.Warningf("load primary %s: %v", z.Origin, err)
				continue
			}
			x := ixfr.New(z.Origin, z.PersistPath+".ixfr", 64)
			_ = d.AttachIXFR(x)
			d.SetTransfer(a.xfer)
			d.SetNext(a.Next)
			if err := zonereg.RegisterPrimary(d); err != nil {
				log.Warningf("zonereg %s: %v", z.Origin, err)
				continue
			}
			a.mu.Lock()
			a.primaries[z.Origin] = d
			a.mu.Unlock()
		case zonereg.KindSecondary:
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
	return nil
}

func (a *Admin) createSecondaryNoPersist(origin string, from []string) error {
	a.mu.Lock()
	if a.secondaries == nil {
		a.secondaries = secondarypersist.NewWithDirectory(a.cfg.Data)
		a.secondaries.SetNext(a.Next)
		a.secondaries.SetTransfer(a.xfer)
	}
	sec := a.secondaries
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
