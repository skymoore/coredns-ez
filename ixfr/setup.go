package ixfr

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	"github.com/miekg/dns"
)

func init() { plugin.Register(pluginName, setup) }

func setup(c *caddy.Controller) error {
	x, err := parse(c)
	if err != nil {
		return plugin.Error(pluginName, err)
	}
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		x.Next = next
		return x
	})
	return nil
}

func parse(c *caddy.Controller) (*IXFR, error) {
	if !c.Next() {
		return nil, c.ArgErr()
	}

	args := c.RemainingArgs()
	x := &IXFR{history: defaultHistory}
	switch len(args) {
	case 0:
		if len(c.ServerBlockKeys) == 0 {
			return nil, c.Err("no zone given and none inferable from the server block")
		}
		x.Zone = plugin.Host(c.ServerBlockKeys[0]).NormalizeExact()[0]
	case 1:
		x.Zone = plugin.Host(args[0]).NormalizeExact()[0]
	default:
		return nil, c.Err("exactly one zone per ixfr block")
	}
	x.Zone = strings.ToLower(dns.CanonicalName(x.Zone))

	for c.NextBlock() {
		switch c.Val() {
		case "history":
			if !c.NextArg() {
				return nil, c.ArgErr()
			}
			n, err := strconv.Atoi(c.Val())
			if err != nil || n < 1 {
				return nil, c.Errf("history must be a positive integer, got %q", c.Val())
			}
			x.history = n
		case "file":
			if !c.NextArg() {
				return nil, c.ArgErr()
			}
			x.path = c.Val()
		default:
			return nil, c.Errf("unknown property %q", c.Val())
		}
	}

	config := dnsserver.GetConfig(c)
	if x.path != "" && !filepath.IsAbs(x.path) && config.Root != "" {
		x.path = filepath.Join(config.Root, x.path)
	}
	return x, nil
}
