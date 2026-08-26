package api

import (
	"strings"
	"testing"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
)

func TestParseAPI(t *testing.T) {
	input := `api {
	db /var/lib/coredns/api.sqlite
	data /var/lib/coredns/zones
	role primary
	bootstrap_admin admin
	advertise 192.0.2.53:53
}`
	c := caddy.NewTestController("dns", input)
	dnsserver.GetConfig(c).Transport = "https"
	cfg, empty, err := parseAPI(c)
	if err != nil {
		t.Fatal(err)
	}
	if empty {
		t.Fatal("expected a configured block")
	}
	if cfg.Role != rolePrimary || !strings.HasSuffix(cfg.DB, "api.sqlite") {
		t.Fatalf("%+v", cfg)
	}
}

func TestParseAPIRequiresFields(t *testing.T) {
	c := caddy.NewTestController("dns", "api {\nrole primary\n}")
	_, _, err := parseAPI(c)
	if err == nil {
		t.Fatal("expected error")
	}
}
