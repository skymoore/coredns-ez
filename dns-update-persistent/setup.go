package dnsupdatepersist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/skymoore/coredns-ez/internal/zonereg"
	"github.com/skymoore/coredns-ez/ixfr"

	"github.com/miekg/dns"
)

func init() { plugin.Register(pluginName, setup) }

func setup(c *caddy.Controller) error {
	d, err := parse(c)
	if err != nil {
		return plugin.Error(pluginName, err)
	}

	if soa := soaOf(d.rrs); soa != nil {
		serialGauge.WithLabelValues(d.Zone).Set(float64(soa.Serial))
	}

	cfg := dnsserver.GetConfig(c)
	cfg.AddPlugin(func(next plugin.Handler) plugin.Handler {
		d.Next = next
		// Rebuild once Next is known: the file view falls through to it for
		// names outside this zone, and a view built at parse time would carry
		// a nil Next and terminate the chain.
		d.mu.Lock()
		defer d.mu.Unlock()
		if err := d.swap(d.rrs); err != nil {
			log.Errorf("building %s: %v", d.Zone, err)
		}
		return d
	})

	c.OnStartup(func() error {
		// The transfer and ixfr plugins register in the same server block, so
		// they can only be found after every setup function has run.
		if t := dnsserver.GetConfig(c).Handler("transfer"); t != nil {
			if xfer, ok := t.(*transfer.Transfer); ok {
				d.Xfer = xfer
			}
		}
		if h := dnsserver.GetConfig(c).Handler("ixfr"); h != nil {
			if x, ok := h.(*ixfr.IXFR); ok {
				d.ixfr = x
				if err := x.Register(d.Zone, d.seedPath+".ixfr", d.rrs); err != nil {
					log.Warningf("ixfr register %s: %v", d.Zone, err)
				}
			}
		}
		if err := zonereg.RegisterPrimary(d); err != nil {
			log.Warningf("zonereg %s: %v", d.Zone, err)
		}
		return nil
	})

	c.OnShutdown(func() error {
		zonereg.Unregister(d.Zone)
		return nil
	})

	return nil
}

func parse(c *caddy.Controller) (*UpdatePersist, error) {
	if !c.Next() {
		return nil, c.ArgErr()
	}

	args := c.RemainingArgs()
	var origin string
	switch len(args) {
	case 0:
		// Default to the server block's own zone, the convention every other
		// zone-serving plugin follows.
		if len(c.ServerBlockKeys) == 0 {
			return nil, c.Err("no zone given and none inferable from the server block")
		}
		origin = plugin.Host(c.ServerBlockKeys[0]).NormalizeExact()[0]
	case 1:
		origin = plugin.Host(args[0]).NormalizeExact()[0]
	default:
		// One zone per block, deliberately. Two zones would share one record
		// slice and one rebuild, so an update to either would take a lock the
		// other's readers are waiting on, and the blast radius of a bad
		// update would be both zones.
		return nil, c.Err("exactly one zone per dns-update-persistent block")
	}
	origin = strings.ToLower(dns.CanonicalName(origin))

	d := &UpdatePersist{Zone: origin}
	var seed string

	for c.NextBlock() {
		switch c.Val() {
		case "file":
			if !c.NextArg() {
				return nil, c.ArgErr()
			}
			seed = c.Val()

		case "mutable":
			types := c.RemainingArgs()
			if len(types) == 0 {
				return nil, c.ArgErr()
			}
			d.mutable = map[uint16]bool{}
			for _, t := range types {
				code, ok := dns.StringToType[strings.ToUpper(t)]
				if !ok {
					return nil, c.Errf("unknown RR type %q", t)
				}
				d.mutable[code] = true
			}

		default:
			return nil, c.Errf("unknown property %q", c.Val())
		}
	}

	if seed == "" {
		return nil, c.Err("a `file` seed zone is required: an UPDATE is applied to a zone, and a zone without an SOA cannot have its serial advanced or be transferred")
	}

	config := dnsserver.GetConfig(c)
	if !filepath.IsAbs(seed) && config.Root != "" {
		seed = filepath.Join(config.Root, seed)
	}

	rrs, err := readZone(seed, origin)
	if err != nil {
		return nil, err
	}
	if soaOf(rrs) == nil {
		return nil, fmt.Errorf("%s has no SOA at %s", seed, origin)
	}
	d.rrs = rrs
	d.seedPath = seed

	return d, nil
}

// readZone parses a seed zone file into a flat record slice.
//
// os.OpenRoot rather than a bare os.Open: the path comes from a config file,
// and this keeps a symlink inside the zone directory from reading something
// outside it.
func readZone(path, origin string) ([]dns.RR, error) {
	dir, name := filepath.Split(path)
	if name == "" {
		return nil, fmt.Errorf("zone file %q has no file component", path)
	}
	if dir == "" {
		dir = "."
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening zone directory %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("opening zone file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Parsing through file.Parse rather than a bare zone parser keeps $INCLUDE
	// handling, $ORIGIN and the generate directive identical to what the
	// `file` plugin would have done with the same file. The first mutating
	// UPDATE flattens PATH (no $INCLUDE / $GENERATE / comments).
	z, err := file.Parse(f, origin, path, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	var rrs []dns.RR
	if z.SOA != nil {
		rrs = append(rrs, z.SOA)
	}
	rrs = append(rrs, z.NS...)
	rrs = append(rrs, z.SIGSOA...)
	rrs = append(rrs, z.SIGNS...)
	for _, e := range z.All() {
		rrs = append(rrs, e.All()...)
	}
	return rrs, nil
}
