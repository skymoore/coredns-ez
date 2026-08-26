package admin

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-ez/dns-update-persistent"
	secondarypersist "github.com/skymoore/coredns-ez/secondary-persistent"
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
	ButtonText   string
	ButtonImage  string
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
	Password       bool
	passwordSet    bool
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
	views       map[string]map[string]*dnsupdatepersist.UpdatePersist // origin -> acl -> zonefile
	secondaries *secondarypersist.SecondaryPersist

	httpClient *http.Client
	stop       chan struct{}
	tsig       *tsigHub

	filters          *filterEngine
	filterHTTP       *http.Client
	filterAllowLocal bool
	filterSyncMu     sync.Mutex
	filterSyncing    map[string]struct{}
	xferHub          *xferHub
}

func (a *Admin) Name() string { return pluginName }

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
