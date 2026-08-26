package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/skymoore/coredns-ez/admin/store"
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
	if !cfg.Password {
		t.Fatal("password login defaults on")
	}
}

func TestApplyPersistedRoleKeepsSecondary(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetMeta(store.MetaRole, roleSecondary); err != nil {
		t.Fatal(err)
	}
	cfg := coreConfig{Role: rolePrimary}
	if err := applyPersistedRole(&cfg, st); err != nil {
		t.Fatal(err)
	}
	if cfg.Role != roleSecondary {
		t.Fatalf("role %q", cfg.Role)
	}
}

func TestParseAdminPasswordOff(t *testing.T) {
	input := `admin {
	db /var/lib/coredns/admin.sqlite
	data /var/lib/coredns/zones
	role primary
	password off
}`
	c := caddy.NewTestController("dns", input)
	cfg, empty, err := parseAdmin(c)
	if err != nil {
		t.Fatal(err)
	}
	if empty || cfg.Password || !cfg.passwordSet {
		t.Fatalf("password off: %+v empty=%v", cfg, empty)
	}
}

func TestParseAdminOIDCButton(t *testing.T) {
	input := `admin {
	db /var/lib/coredns/admin.sqlite
	data /var/lib/coredns/zones
	role primary
	password off
	oidc {
		issuer https://idp.example.com
		client_id coredns
		client_secret s
		redirect_url https://dns.example.com/api/v1/auth/oidc/callback
		button_text Sign in with Google
		button_image https://idp.example.com/g.svg
	}
}`
	c := caddy.NewTestController("dns", input)
	cfg, empty, err := parseAdmin(c)
	if err != nil {
		t.Fatal(err)
	}
	if empty || cfg.OIDC == nil {
		t.Fatalf("%+v", cfg)
	}
	if cfg.OIDC.ButtonText != "Sign in with Google" || cfg.OIDC.ButtonImage != "https://idp.example.com/g.svg" {
		t.Fatalf("button %+v", cfg.OIDC)
	}
}

func TestParseAdminRequiresFields(t *testing.T) {
	c := caddy.NewTestController("dns", "admin {\nrole primary\n}")
	_, _, err := parseAdmin(c)
	if err == nil {
		t.Fatal("expected error")
	}
}
