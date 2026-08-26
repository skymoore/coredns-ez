package secondarypersist

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/pkg/catalog"
	"github.com/coredns/coredns/plugin/pkg/fall"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/pkg/parse"
	"github.com/coredns/coredns/plugin/pkg/upstream"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/skymoore/coredns-plugins/internal/zonereg"
)

var log = clog.NewWithPlugin(pluginName)

func init() { plugin.Register(pluginName, setup) }

type persistConfig struct {
	path string
	dir  string
}

func setup(c *caddy.Controller) error {
	zones, f, catalogZones, pc, err := parseConfig(c)
	if err != nil {
		return plugin.Error(pluginName, err)
	}

	s := newSecondaryPersist(zones, f, catalogZones, pc)

	if s.persistDir != "" {
		if err := os.MkdirAll(s.persistDir, 0o755); err != nil {
			return plugin.Error(pluginName, err)
		}
	}

	for _, n := range zones.Names {
		s.loadIfPresent(n, zones.Z[n])
	}

	var loadedMemberStarts []dynamicZoneStart
	for origin := range catalogZones {
		z := zones.Z[origin]
		if zoneSOA(z) == nil {
			continue
		}
		cat, err := catalog.Parse(origin, dumpRRs(z))
		if err != nil {
			log.Warningf("Failed to parse persisted catalog %s: %s", origin, err)
			continue
		}
		s.storeCatalog(origin, cat)
		starts := s.applyCatalog(origin, cat, z)
		for i := range starts {
			if !starts[i].hasData {
				s.loadIfPresent(starts[i].origin, starts[i].zone)
			}
		}
		loadedMemberStarts = append(loadedMemberStarts, starts...)
		log.Infof("Loaded persisted catalog zone %s with %d member zones", origin, len(cat.Members))
	}

	var x *transfer.Transfer
	c.OnStartup(func() error {
		t := dnsserver.GetConfig(c).Handler("transfer")
		if t != nil {
			x = t.(*transfer.Transfer)
			s.Xfer = x
		}
		return nil
	})

	for i := range zones.Names {
		n := zones.Names[i]
		z := zones.Z[n]
		if len(z.TransferFrom) > 0 {
			updateShutdown := make(chan bool)
			var updateShutdownOnce sync.Once

			c.OnStartup(func() error {
				z.StartupOnce.Do(func() {
					go s.transferAndUpdate(n, z, x, updateShutdown)
				})
				return nil
			})
			c.OnShutdown(func() error {
				updateShutdownOnce.Do(func() { close(updateShutdown) })
				return nil
			})
		}
	}
	for i := range loadedMemberStarts {
		st := loadedMemberStarts[i]
		c.OnStartup(func() error {
			go s.transferAndUpdate(st.origin, st.zone, x, st.shutdown)
			return nil
		})
	}

	c.OnStartup(func() error {
		for _, n := range zones.Names {
			if err := zonereg.RegisterSecondary(&originView{s: s, origin: n}); err != nil {
				log.Warningf("zonereg %s: %v", n, err)
			}
		}
		return nil
	})

	c.OnShutdown(func() error {
		for _, n := range zones.Names {
			zonereg.Unregister(n)
		}
		s.stopDynamicZones()
		s.closePersist()
		return nil
	})

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		s.Next = next
		return s
	})

	return nil
}

func newSecondaryPersist(zones file.Zones, f fall.F, catalogZones map[string]plugin.Zones, pc persistConfig) *SecondaryPersist {
	s := &SecondaryPersist{
		File:               file.File{Zones: zones, Fall: f},
		zoneNames:          make(map[*file.Zone]string, len(zones.Z)),
		dynamicZones:       make(map[string]*dynamicZone),
		catalogs:           make(map[string]*catalog.Catalog),
		catalogZones:       catalogZones,
		catalogMemberZones: make(map[string]map[string]struct{}),
		persistPaths:       make(map[string]string),
		persistDir:         pc.dir,
		lastSerial:         make(map[string]uint32),
		hasWritten:         make(map[string]bool),
		writing:            make(map[string]bool),
		pending:            make(map[string]zoneSnapshot),
		persistStop:        make(chan struct{}),
	}
	if pc.path != "" {
		for _, name := range zones.Names {
			s.persistPaths[name] = pc.path
		}
	}
	for name, zone := range zones.Z {
		s.zoneNames[zone] = name
	}
	s.ZoneLookupFunc = s.lookupZone
	s.TransferInFunc = func(z *file.Zone, t *transfer.Transfer) error {
		return s.transferIn(s.zoneName(z), z, t)
	}
	return s
}

func parseConfig(c *caddy.Controller) (file.Zones, fall.F, map[string]plugin.Zones, persistConfig, error) {
	z := make(map[string]*file.Zone)
	names := []string{}
	f := fall.F{}
	catalogZones := map[string]plugin.Zones{}
	pc := persistConfig{}
	config := dnsserver.GetConfig(c)

	for c.Next() {
		if c.Val() != pluginName {
			continue
		}
		origins := plugin.OriginsFromArgsOrServerBlock(c.RemainingArgs(), c.ServerBlockKeys)
		for i := range origins {
			z[origins[i]] = file.NewZone(origins[i], "stdin")
			names = append(names, origins[i])
		}

		hasTransfer := false
		for c.NextBlock() {
			var transferFrom []string

			switch c.Val() {
			case "transfer":
				var err error
				transferFrom, err = parse.TransferIn(c)
				if err != nil {
					return file.Zones{}, f, nil, pc, err
				}
				hasTransfer = true
			case "catalog":
				memberZones, err := catalogMemberZonesFromArgs(c.RemainingArgs())
				if err != nil {
					return file.Zones{}, f, nil, pc, err
				}
				for _, origin := range origins {
					catalogZones[origin] = mergeCatalogMemberZones(catalogZones, origin, memberZones)
				}
			case "fallthrough":
				f.SetZonesFromArgs(c.RemainingArgs())
			case "persist":
				if !c.NextArg() {
					return file.Zones{}, f, nil, pc, c.ArgErr()
				}
				if pc.path != "" || pc.dir != "" {
					return file.Zones{}, f, nil, pc, c.Err("persist path or directory already specified")
				}
				pc.path = c.Val()
				if !filepath.IsAbs(pc.path) && config.Root != "" {
					pc.path = filepath.Join(config.Root, pc.path)
				}
				pc.path = filepath.Clean(pc.path)
			case "directory":
				if !c.NextArg() {
					return file.Zones{}, f, nil, pc, c.ArgErr()
				}
				if pc.path != "" || pc.dir != "" {
					return file.Zones{}, f, nil, pc, c.Err("persist path or directory already specified")
				}
				pc.dir = c.Val()
				if !filepath.IsAbs(pc.dir) && config.Root != "" {
					pc.dir = filepath.Join(config.Root, pc.dir)
				}
				pc.dir = filepath.Clean(pc.dir)
			default:
				return file.Zones{}, f, nil, pc, c.Errf("unknown property '%s'", c.Val())
			}

			for _, origin := range origins {
				if transferFrom != nil {
					z[origin].TransferFrom = append(z[origin].TransferFrom, transferFrom...)
				}
				z[origin].Upstream = upstream.New()
			}
		}
		if !hasTransfer {
			return file.Zones{}, f, nil, pc, c.Err("secondary-persistent zones require a transfer from property")
		}
	}

	if pc.path == "" && pc.dir == "" {
		return file.Zones{}, f, nil, pc, fmt.Errorf("exactly one of persist or directory is required")
	}
	if pc.path != "" && pc.dir != "" {
		return file.Zones{}, f, nil, pc, fmt.Errorf("persist and directory are mutually exclusive")
	}
	if pc.path != "" && len(names) > 1 {
		return file.Zones{}, f, nil, pc, fmt.Errorf("persist PATH requires a single origin")
	}
	if len(catalogZones) > 0 && pc.dir == "" {
		return file.Zones{}, f, nil, pc, fmt.Errorf("catalog requires directory")
	}

	return file.Zones{Z: z, Names: names}, f, catalogZones, pc, nil
}

func catalogMemberZonesFromArgs(args []string) (plugin.Zones, error) {
	zones := make(plugin.Zones, 0, len(args))
	for _, arg := range args {
		normalized := plugin.Host(arg).NormalizeExact()
		if len(normalized) == 0 {
			return nil, fmt.Errorf("invalid catalog member zone %q", arg)
		}
		zones = append(zones, normalized...)
	}
	return zones, nil
}

func mergeCatalogMemberZones(config map[string]plugin.Zones, origin string, zones plugin.Zones) plugin.Zones {
	existing, configured := config[origin]
	if configured && (len(existing) == 0 || len(zones) == 0) {
		return nil
	}

	merged := append(plugin.Zones(nil), existing...)
	seen := make(map[string]struct{}, len(merged)+len(zones))
	for _, zone := range merged {
		seen[zone] = struct{}{}
	}
	for _, zone := range zones {
		if _, ok := seen[zone]; ok {
			continue
		}
		merged = append(merged, zone)
		seen[zone] = struct{}{}
	}
	return merged
}
