package admin

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-plugins/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-plugins/dns-update-persistent"
	secondarypersist "github.com/skymoore/coredns-plugins/secondary-persistent"
)

var log = clog.NewWithPlugin(pluginName)

const pluginName = "admin"

const (
	rolePrimary   = "primary"
	roleSecondary = "secondary"
	sessionCookie = "coredns_admin_session"
	jwtTTL        = 12 * time.Hour
)

type oidcSettings struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type coreConfig struct {
	DB             string
	Data           string
	Role           string
	BootstrapAdmin string
	AdvertiseDNS   string
	JoinURL        string
	JoinToken      string
	PrimaryDNS     string
	CORS           []string
	OIDC           *oidcSettings
}

// Admin is the process-wide management plugin: HTTP mux + runtime zone owner.
type Admin struct {
	Next plugin.Handler
	cfg  coreConfig
	db   *store.Store
	mux  http.Handler
	xfer *transfer.Transfer
	oidc *oidcRuntime

	mu          sync.RWMutex
	primaries   map[string]*dnsupdatepersist.UpdatePersist
	secondaries *secondarypersist.SecondaryPersist

	httpClient *http.Client
	stop       chan struct{}
}

func (a *Admin) Name() string { return pluginName }

func (a *Admin) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	name := state.Name()

	a.mu.RLock()
	if origin := matchMap(a.primaries, name); origin != "" {
		p := a.primaries[origin]
		a.mu.RUnlock()
		return p.ServeDNS(ctx, w, r)
	}
	sec := a.secondaries
	a.mu.RUnlock()
	if sec != nil {
		if plugin.Zones(sec.Names).Matches(name) != "" {
			return sec.ServeDNS(ctx, w, r)
		}
	}
	return plugin.NextOrFailure(a.Name(), a.Next, ctx, w, r)
}

func (a *Admin) Transfer(zone string, serial uint32) (<-chan []dns.RR, error) {
	zone = strings.ToLower(dns.CanonicalName(zone))
	a.mu.RLock()
	if p, ok := a.primaries[zone]; ok {
		t := p.OutboundTransfer()
		a.mu.RUnlock()
		return t.Transfer(zone, serial)
	}
	sec := a.secondaries
	a.mu.RUnlock()
	if sec != nil {
		if plugin.Zones(sec.Names).Matches(zone) != "" {
			return sec.Transfer(zone, serial)
		}
	}
	return nil, transfer.ErrNotAuthoritative
}

func matchMap(m map[string]*dnsupdatepersist.UpdatePersist, qname string) string {
	var best string
	for origin := range m {
		if qname == origin || dns.IsSubDomain(origin, qname) {
			if len(origin) > len(best) {
				best = origin
			}
		}
	}
	return best
}

func (a *Admin) close() error {
	if a.stop != nil {
		select {
		case <-a.stop:
		default:
			close(a.stop)
		}
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}
