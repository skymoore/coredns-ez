package admin

import (
	"strings"
	"testing"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
)

func TestParseAdmin(t *testing.T) {
	input := `admin {
	db /var/lib/coredns/admin.sqlite
	data /var/lib/coredns/zones
	role primary
	bootstrap_admin admin
	advertise 192.0.2.53:53
}`
	c := caddy.NewTestController("dns", input)
	dnsserver.GetConfig(c).Transport = "https"
	cfg, empty, err := parseAdmin(c)
	if err != nil {
		t.Fatal(err)
	}
	if empty {
		t.Fatal("expected a configured block")
	}
	if cfg.Role != rolePrimary || !strings.HasSuffix(cfg.DB, "admin.sqlite") {
		t.Fatalf("%+v", cfg)
	}
}

func TestParseAdminRequiresFields(t *testing.T) {
	c := caddy.NewTestController("dns", "admin {\nrole primary\n}")
	_, _, err := parseAdmin(c)
	if err == nil {
		t.Fatal("expected error")
	}
}
