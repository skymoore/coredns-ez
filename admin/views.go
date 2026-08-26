package admin

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-plugins/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-plugins/dns-update-persistent"
	"github.com/skymoore/coredns-plugins/internal/zonereg"
)

func persistNameView(origin, acl string) string {
	return persistName(origin) + "." + acl
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
	if err := os.MkdirAll(a.cfg.Data, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(a.cfg.Data, persistNameView(origin, acl))
	var seed []dns.RR
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
	if err := dnsupdatepersist.WriteSeed(path, origin, seed); err != nil {
		return nil, err
	}
	d, err := dnsupdatepersist.New(origin, path, nil)
	if err != nil {
		return nil, err
	}
	d.SetTransfer(a.xfer)
	d.SetNext(a.Next)
	if err := a.db.UpsertZoneView(store.ZoneView{Origin: origin, ACL: acl, Path: path}); err != nil {
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
		d, err := dnsupdatepersist.New(v.Origin, v.Path, nil)
		if err != nil {
			log.Warningf("load view %s %s: %v", v.Origin, v.ACL, err)
			continue
		}
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
	for _, d := range m {
		_ = os.Remove(d.Path())
		_ = os.Remove(d.Path() + ".ixfr")
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

func (a *Admin) pickServe(origin string, r *dns.Msg, ip net.IP) *dnsupdatepersist.UpdatePersist {
	a.mu.RLock()
	pub := a.primaries[origin]
	a.mu.RUnlock()
	if pub == nil {
		return nil
	}
	if v := a.pickView(origin, r, ip); v != nil {
		return v
	}
	return pub
}

func (a *Admin) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	return a.serveWithNext(ctx, w, r, a.Next)
}

func (a *Admin) serveWithNext(ctx context.Context, w dns.ResponseWriter, r *dns.Msg, next plugin.Handler) (int, error) {
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
	if vo := a.matchViews(name); vo != "" {
		if v := a.pickView(vo, r, ip); v != nil {
			return v.ServeDNS(ctx, w, r)
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
	return plugin.NextOrFailure(a.Name(), next, ctx, w, r)
}
