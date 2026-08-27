package admin

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-ez/dns-update-persistent"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

func (a *Admin) attachViews(snap *store.Snapshot) {
	rows, err := a.db.ListZoneViews()
	if err != nil {
		return
	}
	out := make([]store.ZoneView, 0, len(rows))
	for _, v := range rows {
		v.Path = ""
		v.Data = nil
		out = append(out, v)
	}
	snap.Views = out
}

func (a *Admin) replaceViewsFromSnap(views []store.ZoneView) error {
	for _, v := range views {
		v.Origin = strings.ToLower(dns.CanonicalName(v.Origin))
		v.ACL = strings.ToLower(strings.TrimSpace(v.ACL))
		if v.Origin == "" || v.ACL == "" {
			continue
		}
		if len(v.Data) > 0 && !a.db.HasRecords(v.Origin, v.ACL) {
			rrs, err := parseMasterBytes(v.Origin, v.Data)
			if err != nil {
				log.Warningf("view %s %s: %v", v.Origin, v.ACL, err)
				continue
			}
			if err := a.db.ReplaceRecords(v.Origin, v.ACL, rrs); err != nil {
				return err
			}
		}
		if err := a.db.UpsertZoneView(store.ZoneView{Origin: v.Origin, ACL: v.ACL}); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.views = nil
	a.mu.Unlock()
	return a.loadViews()
}

func parseMasterBytes(origin string, body []byte) ([]dns.RR, error) {
	zp := dns.NewZoneParser(bytes.NewReader(body), origin, "")
	var rrs []dns.RR
	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		rrs = append(rrs, rr)
	}
	if err := zp.Err(); err != nil {
		return nil, err
	}
	return rrs, nil
}

func (a *Admin) matchACL(ip net.IP) (store.ACL, bool) {
	if ip == nil {
		return store.ACL{}, false
	}
	acls, err := a.db.ListACLs()
	if err != nil {
		return store.ACL{}, false
	}
	for _, acl := range acls {
		if acl.Contains(ip) {
			return acl, true
		}
	}
	return store.ACL{}, false
}

func (a *Admin) viewOf(origin, acl string) *dnsupdatepersist.UpdatePersist {
	a.mu.RLock()
	defer a.mu.RUnlock()
	m := a.views[origin]
	if m == nil {
		return nil
	}
	return m[acl]
}

func (a *Admin) putView(origin, acl string, d *dnsupdatepersist.UpdatePersist) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.views == nil {
		a.views = map[string]map[string]*dnsupdatepersist.UpdatePersist{}
	}
	if a.views[origin] == nil {
		a.views[origin] = map[string]*dnsupdatepersist.UpdatePersist{}
	}
	a.views[origin][acl] = d
}

func (a *Admin) recordZone(origin, acl string) (zonereg.Primary, error) {
	origin = strings.ToLower(dns.CanonicalName(origin))
	acl = strings.ToLower(strings.TrimSpace(acl))
	if acl == "" || acl == "public" {
		if p := zonereg.PrimaryOf(origin); p != nil {
			return p, nil
		}
		return nil, fmt.Errorf("not a writable primary")
	}
	if _, err := a.db.GetACLByName(acl); err != nil {
		return nil, fmt.Errorf("unknown acl %q", acl)
	}
	return a.ensureView(origin, acl)
}

func (a *Admin) ensureView(origin, acl string) (*dnsupdatepersist.UpdatePersist, error) {
	if v := a.viewOf(origin, acl); v != nil {
		return v, nil
	}
	pub := zonereg.PrimaryOf(origin)
	if pub == nil {
		return nil, fmt.Errorf("not a writable primary")
	}
	var seed []dns.RR
	if a.db.HasRecords(origin, acl) {
		got, err := a.db.ListRecords(origin, acl)
		if err != nil {
			return nil, err
		}
		seed = got
	} else {
		for _, rr := range pub.Records() {
			switch rr.Header().Rrtype {
			case dns.TypeSOA, dns.TypeNS:
				if strings.EqualFold(rr.Header().Name, origin) {
					seed = append(seed, dns.Copy(rr))
				}
			}
		}
		if soaOf(seed) == nil {
			return nil, fmt.Errorf("public zone has no SOA")
		}
		if err := a.db.ReplaceRecords(origin, acl, seed); err != nil {
			return nil, err
		}
	}
	d, err := dnsupdatepersist.NewFromRecords(origin, seed, nil)
	if err != nil {
		return nil, err
	}
	d.SetPersist(a.persistView(origin, acl))
	d.SetTransfer(a.xfer)
	d.SetNext(a.Next)
	if err := a.db.UpsertZoneView(store.ZoneView{Origin: origin, ACL: acl}); err != nil {
		return nil, err
	}
	a.putView(origin, acl, d)
	return d, nil
}

func (a *Admin) loadViews() error {
	rows, err := a.db.ListZoneViews()
	if err != nil {
		return err
	}
	for _, v := range rows {
		rrs, err := a.loadZoneRRs(v.Origin, v.ACL)
		if err != nil {
			log.Warningf("load view %s %s: %v", v.Origin, v.ACL, err)
			continue
		}
		d, err := dnsupdatepersist.NewFromRecords(v.Origin, rrs, nil)
		if err != nil {
			log.Warningf("load view %s %s: %v", v.Origin, v.ACL, err)
			continue
		}
		d.SetPersist(a.persistView(v.Origin, v.ACL))
		d.SetTransfer(a.xfer)
		d.SetNext(a.Next)
		a.putView(v.Origin, v.ACL, d)
	}
	return nil
}

func (a *Admin) dropViews(origin string) {
	a.mu.Lock()
	m := a.views[origin]
	delete(a.views, origin)
	a.mu.Unlock()
	for acl := range m {
		_ = a.db.DeleteRecords(origin, acl)
	}
	_ = a.db.DeleteZoneViews(origin)
}

func (a *Admin) matchViews(qname string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var best string
	for origin := range a.views {
		if qname == origin || dns.IsSubDomain(origin, qname) {
			if len(origin) > len(best) {
				best = origin
			}
		}
	}
	return best
}

func (a *Admin) pickView(origin string, r *dns.Msg, ip net.IP) *dnsupdatepersist.UpdatePersist {
	if ip == nil || r == nil || len(r.Question) == 0 {
		return nil
	}
	acl, ok := a.matchACL(ip)
	if !ok {
		return nil
	}
	v := a.viewOf(origin, acl.Name)
	if v == nil {
		return nil
	}
	q := r.Question[0]
	if v.HasRRset(q.Name, q.Qtype) || (q.Qtype != dns.TypeCNAME && v.HasRRset(q.Name, dns.TypeCNAME)) {
		return v
	}
	return nil
}

func zoneTransfer(r *dns.Msg) bool {
	if r == nil || len(r.Question) == 0 {
		return false
	}
	switch r.Question[0].Qtype {
	case dns.TypeAXFR, dns.TypeIXFR:
		return true
	}
	return false
}

func (a *Admin) pickServe(origin string, r *dns.Msg, ip net.IP) *dnsupdatepersist.UpdatePersist {
	a.mu.RLock()
	pub := a.primaries[origin]
	a.mu.RUnlock()
	if pub == nil {
		return nil
	}
	if !zoneTransfer(r) {
		if v := a.pickView(origin, r, ip); v != nil {
			return v
		}
	}
	return pub
}

func (a *Admin) serveWithNext(ctx context.Context, w dns.ResponseWriter, r *dns.Msg, next plugin.Handler) (int, error) {
	w, done, code, err := a.wrapDNSSEC(w, r)
	if done {
		return code, err
	}
	a.tsig.Install(ctx)
	if a.answerDNSSECMeta(w, r) {
		return dns.RcodeSuccess, nil
	}
	state := request.Request{W: w, Req: r}
	name := state.Name()
	ip := net.ParseIP(state.IP())

	a.mu.RLock()
	origin := matchMap(a.primaries, name)
	a.mu.RUnlock()
	if origin != "" {
		p := a.pickServe(origin, r, ip)
		if p != nil {
			return p.ServeDNS(ctx, w, r)
		}
	}
	if !zoneTransfer(r) {
		if vo := a.matchViews(name); vo != "" {
			if v := a.pickView(vo, r, ip); v != nil {
				return v.ServeDNS(ctx, w, r)
			}
		}
	}
	a.mu.RLock()
	sec := a.secondaries
	a.mu.RUnlock()
	if sec != nil {
		if plugin.Zones(sec.Names).Matches(name) != "" {
			return sec.ServeDNS(ctx, w, r)
		}
	}
	if a.filters != nil && a.filters.blocked(name) {
		return writeFilterBlock(w, r)
	}
	if a.recursionAllowed(ip) {
		return plugin.NextOrFailure(a.Name(), next, ctx, w, r)
	}
	return writeRefused(w, r)
}

func (a *Admin) recursionAllowed(ip net.IP) bool {
	return a.db.RecursionAllows(ip)
}

func writeRefused(w dns.ResponseWriter, r *dns.Msg) (int, error) {
	m := new(dns.Msg)
	m.SetRcode(r, dns.RcodeRefused)
	if err := w.WriteMsg(m); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeRefused, nil
}
