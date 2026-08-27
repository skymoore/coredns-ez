package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestStripZonefileBlocks(t *testing.T) {
	src := `(common) {
	errors
}

https://.:443 {
	admin {
		db /var/lib/coredns/admin.sqlite
		data /var/lib/coredns/zones
		role primary
	}
}

rwx.dev {
	dns-update-persistent {
		file /etc/coredns/zones/db.rwx.dev
		mutable A AAAA TXT
	}
}

k8s.rwx.dev {
	view internal {
		expr incidr(client_ip(), '10.0.0.0/8')
	}
	file /etc/coredns/zones/db.k8s.rwx.dev.internal
}

. {
	admin
	forward . 8.8.8.8
}
`
	got, changed := stripZonefileBlocks(src)
	if !changed {
		t.Fatal("expected strip")
	}
	if strings.Contains(got, "rwx.dev") || strings.Contains(got, "dns-update-persistent") || strings.Contains(got, "file /etc") {
		t.Fatalf("zone blocks remain:\n%s", got)
	}
	if !strings.Contains(got, "https://.:443") || !strings.Contains(got, "(common)") || !strings.Contains(got, "forward . 8.8.8.8") {
		t.Fatalf("lost listeners:\n%s", got)
	}
}

func TestStripPolicyAndMergeListeners(t *testing.T) {
	src := `(common) {
	errors
}

. {
	view internal {
		expr incidr(client_ip(), '10.0.0.0/8') || incidr(client_ip(), '192.168.0.0/16')
	}
	admin
	acl {
		allow net 10.0.0.0/8 192.168.0.0/16
		block net *
	}
	forward . 127.0.0.1:5301
	import common
}

. {
	admin
	acl { block net * }
	import common
}

https://.:443 {
	admin
	import common
}
`
	got, changed := simplifyCorefile(src)
	if !changed {
		t.Fatal("expected simplify")
	}
	if strings.Contains(got, "view ") || strings.Contains(got, "acl {") || strings.Contains(got, "allow net") {
		t.Fatalf("policy left in Corefile:\n%s", got)
	}
	if strings.Count(got, "\n. {") != 1 && strings.Count(got, ". {") != 1 {
		t.Fatalf("expected one catch-all listener:\n%s", got)
	}
	if !strings.Contains(got, "forward . 127.0.0.1:5301") || !strings.Contains(got, "https://.:443") || !strings.Contains(got, "(common)") {
		t.Fatalf("lost plugins:\n%s", got)
	}
}

func TestSimplifyPreservesNestedBlocks(t *testing.T) {
	src := `(xfer) {
	transfer {
		to 192.168.8.54
	}
}

https://.:443 {
	admin {
		db /var/lib/coredns/admin.sqlite
		data /var/lib/coredns/zones
		role primary
		oidc {
			issuer https://auth.example/o/coredns/
			client_id coredns
			redirect_url https://ns1.example/callback
		}
	}
	import common
}

. {
	admin
	cache 86400 {
		success 9984 86400 10
	}
	forward . 127.0.0.1:5301
	acl { block net * }
}

. {
	admin
	import common
}
`
	got, changed := simplifyCorefile(src)
	if !changed {
		t.Fatal("expected simplify")
	}
	if strings.Count(got, "{") != strings.Count(got, "}") {
		t.Fatalf("unbalanced braces:\n%s", got)
	}
	for _, need := range []string{
		"transfer {",
		"to 192.168.8.54",
		"oidc {",
		"issuer https://auth.example/o/coredns/",
		"cache 86400 {",
		"success 9984 86400 10",
		"forward . 127.0.0.1:5301",
	} {
		if !strings.Contains(got, need) {
			t.Fatalf("missing %q in:\n%s", need, got)
		}
	}
	if strings.Contains(got, "acl {") {
		t.Fatalf("acl remained:\n%s", got)
	}
}

func TestExtractCorefileACLs(t *testing.T) {
	src := `. {
	view lan {
		expr incidr(client_ip(), '10.0.0.0/8')
	}
	acl {
		allow net 192.168.0.0/16 10.0.0.0/8
		block net *
	}
}
`
	got := extractCorefileACLs(src)
	if len(got["lan"]) < 2 {
		t.Fatalf("lan nets: %+v", got)
	}
}

func TestImportCorefileACLs(t *testing.T) {
	a := testAdmin(t)
	src := `. {
	view internal {
		expr incidr(client_ip(), '10.0.0.0/8')
	}
	acl { allow net 192.168.0.0/16 }
}
`
	if n := a.importCorefileACLs(src); n != 1 {
		t.Fatalf("imported %d", n)
	}
	acl, err := a.db.GetACLByName("internal")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(acl.Networks, " ")
	if !strings.Contains(joined, "10.0.0.0/8") || !strings.Contains(joined, "192.168.0.0/16") {
		t.Fatalf("networks %+v", acl.Networks)
	}
}

func TestExtractAndImportZonefile(t *testing.T) {
	a := testAdmin(t)
	dir := a.cfg.Data
	path := filepath.Join(dir, "db.example.com")
	zone := `$ORIGIN example.com.
$TTL 300
@ SOA ns.example.com. host.example.com. 1 3600 600 86400 60
@ NS ns.example.com.
www A 192.0.2.10
`
	if err := os.WriteFile(path, []byte(zone), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "Corefile")
	core := `. {
	admin
}

example.com {
	dns-update-persistent {
		file ` + path + `
		mutable A TXT
	}
}
`
	if err := os.WriteFile(conf, []byte(core), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := os.Args
	os.Args = []string{"coredns", "-conf", conf}
	t.Cleanup(func() { os.Args = orig })

	if err := a.importCorefileZones(); err != nil {
		t.Fatal(err)
	}
	rrs, err := a.db.ListRecords("example.com.", "")
	if err != nil || soaOf(rrs) == nil {
		t.Fatalf("imported %+v %v", rrs, err)
	}
	found := false
	for _, rr := range rrs {
		if a, ok := rr.(*dns.A); ok && a.A.String() == "192.0.2.10" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing A: %v", rrs)
	}
	got, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "dns-update-persistent") {
		t.Fatalf("Corefile still has zone plugin:\n%s", got)
	}
	z, err := a.db.GetZone("example.com.")
	if err != nil || z.Kind != "primary" {
		t.Fatalf("zone row %+v %v", z, err)
	}
}

func TestImportViewOnlyCreatesZoneRow(t *testing.T) {
	a := testAdmin(t)
	dir := a.cfg.Data
	path := filepath.Join(dir, "db.k8s.rwx.dev.internal")
	zone := `$ORIGIN k8s.rwx.dev.
$TTL 300
@ SOA ns.rwx.dev. host.rwx.dev. 1 3600 600 86400 60
@ NS ns.rwx.dev.
svc A 10.1.2.3
`
	if err := os.WriteFile(path, []byte(zone), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "Corefile")
	core := `k8s.rwx.dev {
	view internal {
		expr incidr(client_ip(), '10.0.0.0/8')
	}
	file ` + path + `
}
`
	if err := os.WriteFile(conf, []byte(core), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := os.Args
	os.Args = []string{"coredns", "-conf", conf}
	t.Cleanup(func() { os.Args = orig })

	if err := a.importCorefileZones(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.GetZone("k8s.rwx.dev."); err != nil {
		t.Fatal("view-only origin must still get a zones row")
	}
	if !a.db.HasRecords("k8s.rwx.dev.", "internal") {
		t.Fatal("internal view records missing")
	}
	if !a.db.HasSOA("k8s.rwx.dev.") {
		t.Fatal("public apex stub missing")
	}
	pub, err := a.db.ListRecords("k8s.rwx.dev.", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rr := range pub {
		if _, ok := rr.(*dns.A); ok {
			t.Fatalf("internal A leaked to public: %s", rr)
		}
	}
	if err := a.importCorefileZones(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.GetZone("k8s.rwx.dev."); err != nil {
		t.Fatal("re-import dropped the zone row")
	}
}
