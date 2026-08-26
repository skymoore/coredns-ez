package admin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/parse"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-plugins/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-plugins/dns-update-persistent"
)

func init() { plugin.Register(pluginName, setup) }

var (
	instanceMu sync.Mutex
	instance   *Admin
)

func setup(c *caddy.Controller) error {
	cfg, empty, err := parseAdmin(c)
	if err != nil {
		return plugin.Error(pluginName, err)
	}

	instanceMu.Lock()
	defer instanceMu.Unlock()

	if instance != nil {
		if !empty {
			if err := instance.sameConfig(cfg); err != nil {
				return plugin.Error(pluginName, err)
			}
		}
		attach(c, instance)
		return nil
	}
	if empty {
		return plugin.Error(pluginName, fmt.Errorf("db, data, and role are required"))
	}

	a, err := newAdmin(cfg)
	if err != nil {
		return plugin.Error(pluginName, err)
	}
	instance = a
	attach(c, a)

	c.OnStartup(func() error {
		if t := dnsserver.GetConfig(c).Handler("transfer"); t != nil {
			if x, ok := t.(*transfer.Transfer); ok {
				a.xfer = x
				a.tsig.SetTransfer(x)
				a.mu.Lock()
				for _, p := range a.primaries {
					p.SetTransfer(x)
				}
				if a.secondaries != nil {
					a.secondaries.SetTransfer(x)
				}
				a.mu.Unlock()
			}
		}
		return a.loadPersistedZones()
	})
	c.OnShutdown(func() error {
		instanceMu.Lock()
		if instance == a {
			instance = nil
		}
		instanceMu.Unlock()
		return a.close()
	})
	return nil
}

// adminChain keeps a per-server-block Next so the process-wide singleton can
// sit in front of a Corefile primary (ACL overlay) without clobbering the
// catch-all block's fallthrough.
type adminChain struct {
	*Admin
	next plugin.Handler
}

func (c *adminChain) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	return c.Admin.serveWithNext(ctx, w, r, c.next)
}

func attach(c *caddy.Controller, a *Admin) {
	cfg := dnsserver.GetConfig(c)
	a.tsig.MergeCorefile(cfg.TsigSecret)
	cfg.TsigSecret = a.tsig.Snapshot()
	if cfg.Transport == "https" || cfg.Transport == "https3" {
		if !installHTTPHandler(cfg, a.mux) {
			log.Warning("CoreDNS was built without HTTPHandler; the admin plugin will not be served on the DoH listener. Apply patches/coredns-http-handler.patch.")
		}
	}
	cfg.AddPlugin(func(next plugin.Handler) plugin.Handler {
		a.mu.Lock()
		a.Next = next
		for _, p := range a.primaries {
			p.SetNext(next)
		}
		for _, m := range a.views {
			for _, p := range m {
				p.SetNext(next)
			}
		}
		if a.secondaries != nil {
			a.secondaries.SetNext(next)
		}
		a.mu.Unlock()
		return &adminChain{Admin: a, next: next}
	})
}

func newAdmin(cfg coreConfig) (*Admin, error) {
	if err := os.MkdirAll(cfg.Data, 0o755); err != nil {
		return nil, err
	}
	db, err := store.Open(cfg.DB)
	if err != nil {
		return nil, err
	}
	if err := db.SetMeta(store.MetaRole, cfg.Role); err != nil {
		_ = db.Close()
		return nil, err
	}
	if cfg.AdvertiseDNS != "" {
		_ = db.SetMeta(store.MetaAdvertise, cfg.AdvertiseDNS)
	}
	if cfg.OIDC != nil {
		_ = db.UpsertOIDC(store.OIDCConfig{
			Issuer: cfg.OIDC.Issuer, ClientID: cfg.OIDC.ClientID,
			ClientSecret: cfg.OIDC.ClientSecret, RedirectURL: cfg.OIDC.RedirectURL,
			ButtonText: cfg.OIDC.ButtonText, ButtonImage: cfg.OIDC.ButtonImage,
		})
	}

	n, err := db.UserCount()
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if !cfg.Password {
		if cfg.OIDC == nil {
			if _, err := db.GetOIDC(); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("password off requires oidc")
			}
		}
	}
	if n == 0 && cfg.Role == rolePrimary && cfg.Password {
		pass := os.Getenv("COREDNS_ADMIN_BOOTSTRAP_PASSWORD")
		if pass == "" {
			pass = os.Getenv("COREDNS_API_BOOTSTRAP_PASSWORD")
		}
		if cfg.BootstrapAdmin == "" || pass == "" {
			_ = db.Close()
			return nil, fmt.Errorf("empty database requires bootstrap_admin and COREDNS_ADMIN_BOOTSTRAP_PASSWORD")
		}
		hash, err := hashPassword(pass)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := db.CreateUser(store.User{
			Username: store.NormalizeUsername(cfg.BootstrapAdmin), PasswordHash: hash, Role: store.RoleAdmin,
		}); err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	a := &Admin{
		cfg:        cfg,
		db:         db,
		primaries:  map[string]*dnsupdatepersist.UpdatePersist{},
		views:      map[string]map[string]*dnsupdatepersist.UpdatePersist{},
		httpClient: &http.Client{Timeout: 15 * time.Second},
		stop:       make(chan struct{}),
		tsig:       newTSIGHub(),
	}
	a.publishTSIG()

	if cfg.OIDC != nil {
		rt, err := newOIDC(context.Background(), *cfg.OIDC)
		if err != nil {
			log.Warningf("oidc provider: %v (OIDC login disabled until reachable)", err)
		} else {
			a.oidc = rt
		}
	} else if oc, err := db.GetOIDC(); err == nil {
		rt, err := newOIDC(context.Background(), oidcSettings{
			Issuer: oc.Issuer, ClientID: oc.ClientID, ClientSecret: oc.ClientSecret, RedirectURL: oc.RedirectURL,
		})
		if err == nil {
			a.oidc = rt
		}
	}

	a.mux = a.routes()

	if cfg.Role == rolePrimary {
		if err := a.ensureSelfMember(""); err != nil {
			log.Warningf("cluster self member: %v", err)
		}
	}

	if cfg.Role == roleSecondary {
		cluster, _ := db.Meta(store.MetaClusterID)
		if cluster == "" && cfg.JoinURL != "" && cfg.JoinToken != "" {
			name, _ := os.Hostname()
			if err := a.joinPrimary(cfg.JoinURL, cfg.JoinToken, name, cfg.AdvertiseDNS, ""); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("join: %w", err)
			}
		}
		go a.pullLoop()
	}
	return a, nil
}

func (a *Admin) sameConfig(cfg coreConfig) error {
	if cfg.DB != "" && cfg.DB != a.cfg.DB {
		return fmt.Errorf("conflicting db path")
	}
	if cfg.Data != "" && cfg.Data != a.cfg.Data {
		return fmt.Errorf("conflicting data dir")
	}
	if cfg.Role != "" && cfg.Role != a.cfg.Role {
		return fmt.Errorf("conflicting role")
	}
	if cfg.passwordSet && a.cfg.passwordSet && cfg.Password != a.cfg.Password {
		return fmt.Errorf("conflicting password setting")
	}
	return nil
}

func parseOnOff(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "true", "yes", "1":
		return true, nil
	case "off", "false", "no", "0":
		return false, nil
	default:
		return false, fmt.Errorf("want on or off")
	}
}

func parseAdmin(c *caddy.Controller) (coreConfig, bool, error) {
	cfg := coreConfig{Password: true}
	if !c.Next() {
		return cfg, true, c.ArgErr()
	}
	if len(c.RemainingArgs()) > 0 {
		return cfg, false, c.ArgErr()
	}
	empty := true
	config := dnsserver.GetConfig(c)
	for c.NextBlock() {
		empty = false
		switch c.Val() {
		case "db":
			if !c.NextArg() {
				return cfg, false, c.ArgErr()
			}
			cfg.DB = abs(config.Root, c.Val())
		case "data":
			if !c.NextArg() {
				return cfg, false, c.ArgErr()
			}
			cfg.Data = abs(config.Root, c.Val())
		case "role":
			if !c.NextArg() {
				return cfg, false, c.ArgErr()
			}
			if c.Val() != rolePrimary && c.Val() != roleSecondary {
				return cfg, false, c.Errf("role must be primary or secondary")
			}
			cfg.Role = c.Val()
		case "bootstrap_admin":
			if !c.NextArg() {
				return cfg, false, c.ArgErr()
			}
			cfg.BootstrapAdmin = c.Val()
		case "advertise":
			if !c.NextArg() {
				return cfg, false, c.ArgErr()
			}
			cfg.AdvertiseDNS = c.Val()
		case "join":
			args := c.RemainingArgs()
			if len(args) != 2 {
				return cfg, false, c.ArgErr()
			}
			cfg.JoinURL, cfg.JoinToken = args[0], args[1]
		case "dns":
			if !c.NextArg() {
				return cfg, false, c.ArgErr()
			}
			hp, err := parse.HostPort(c.Val(), "53")
			if err != nil {
				return cfg, false, err
			}
			cfg.PrimaryDNS = hp
		case "cors":
			cfg.CORS = c.RemainingArgs()
		case "password":
			if !c.NextArg() {
				return cfg, false, c.ArgErr()
			}
			on, err := parseOnOff(c.Val())
			if err != nil {
				return cfg, false, c.Errf("password: %v", err)
			}
			cfg.Password = on
			cfg.passwordSet = true
		case "oidc":
			oc, err := parseOIDC(c)
			if err != nil {
				return cfg, false, err
			}
			cfg.OIDC = oc
		default:
			return cfg, false, c.Errf("unknown property %q", c.Val())
		}
	}
	if !empty {
		if cfg.DB == "" || cfg.Data == "" || cfg.Role == "" {
			return cfg, false, fmt.Errorf("db, data, and role are required")
		}
	}
	return cfg, empty, nil
}

func abs(root, p string) string {
	if !filepath.IsAbs(p) && root != "" {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}
