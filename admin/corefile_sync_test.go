package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skymoore/coredns-ez/admin/store"
)

func TestAdaptCorefileForSecondary(t *testing.T) {
	src := `(common) {
	bind 192.168.8.53
	errors
}

https://.:443 {
	tls /etc/coredns/tls/tls.crt /etc/coredns/tls/tls.key
	admin {
		db /var/lib/coredns/admin.sqlite
		data /var/lib/coredns/admin-zones
		role primary
		bootstrap_admin admin
		advertise 192.168.8.53:53
		password off
		oidc {
			issuer https://auth.example/o/coredns/
			redirect_url https://ns1.example/api/v1/auth/oidc/callback
		}
	}
	import common
}

rwx.dev {
	admin
	file /etc/coredns/zones/db.rwx.dev.internal
	import xfer
}

rwx.dev {
	admin
	dns-update-persistent {
		file /etc/coredns/zones/db.rwx.dev.public
		mutable A AAAA TXT
	}
	ixfr
	import xfer
}
`
	got := adaptCorefileForSecondary(src, corefileAdapt{
		DB:          "/var/lib/coredns/admin.sqlite",
		Data:        "/var/lib/coredns/zones",
		PrimaryDNS:  "192.168.8.53:53",
		PrimaryIP:   "192.168.8.53",
		SelfIP:      "192.168.8.54",
		RedirectURL: "http://192.168.8.54:8080/api/v1/auth/oidc/callback",
	})
	checks := []string{
		"role secondary",
		"dns 192.168.8.53:53",
		"bind 192.168.8.54",
		"data /var/lib/coredns/zones",
		"redirect_url http://192.168.8.54:8080/api/v1/auth/oidc/callback",
	}
	for _, s := range checks {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q in:\n%s", s, got)
		}
	}
	bans := []string{"role primary", "bootstrap_admin", "advertise 192.168.8.53", "dns-update-persistent", "secondary-persistent", "rwx.dev {", "\tfile /etc/coredns/zones/"}
	for _, s := range bans {
		if strings.Contains(got, s) {
			t.Fatalf("still has %q in:\n%s", s, got)
		}
	}
}

func TestMergeClusteredCorefileDoesNotInjectSnippets(t *testing.T) {
	local := `https://.:8080 {
	admin {
		db /var/lib/coredns/admin.sqlite
		data /var/lib/coredns/zones
		role primary
	}
}
`
	primary := `(xfer) {
	transfer { to 192.168.8.54 }
}

(common) {
	errors
}

rwx.dev {
	admin
	dns-update-persistent {
		file /etc/coredns/zones/db.rwx.dev.public
	}
	ixfr
}
`
	adapted := adaptCorefileForSecondary(primary, corefileAdapt{PrimaryDNS: "192.168.8.53:53"})
	got := mergeClusteredCorefile(local, adapted)
	if !strings.Contains(got, "https://.:8080") {
		t.Fatalf("lost local UI listener:\n%s", got)
	}
	if strings.Contains(got, clusterBegin) || strings.Contains(got, "(xfer)") || strings.Contains(got, "(common)") || strings.Contains(got, "rwx.dev") {
		t.Fatalf("primary Corefile leaked into secondary:\n%s", got)
	}
}

func TestHostPart(t *testing.T) {
	if hostPart("192.168.8.53:53") != "192.168.8.53" || hostPart("192.168.8.53") != "192.168.8.53" {
		t.Fatal(hostPart("192.168.8.53:53"))
	}
}

func TestCollectCorefileFiles(t *testing.T) {
	dir := t.TempDir()
	zdir := filepath.Join(dir, "zones")
	if err := os.Mkdir(zdir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(zdir, "db.example")
	if err := os.WriteFile(path, []byte("zone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := "example.com {\n\tfile " + path + "\n}\n"
	files := collectCorefileFiles(text, dir)
	if _, ok := files[path]; !ok {
		t.Fatalf("%v", files)
	}
}

func TestApplyCorefileStripsClusterSection(t *testing.T) {
	a := testAdmin(t)
	conf := filepath.Join(a.cfg.Data, "Corefile")
	origArgs := os.Args
	os.Args = []string{"coredns", "-conf", conf}
	t.Cleanup(func() { os.Args = origArgs })

	local := `(common) {
	bind 192.168.8.54
	errors
}

. {
	admin
	import common
}

` + clusterBegin + `
(common) {
	bind 192.168.8.54
	errors
}
` + clusterEnd + "\n"
	if err := os.WriteFile(conf, []byte(local), 0o640); err != nil {
		t.Fatal(err)
	}
	reload, err := a.applyCorefile(store.Snapshot{})
	if err != nil || !reload {
		t.Fatalf("reload=%v err=%v", reload, err)
	}
	got, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), clusterBegin) || strings.Count(string(got), "(common)") != 1 {
		t.Fatalf("cluster leftovers still present:\n%s", got)
	}
	if !strings.Contains(string(got), "import common") {
		t.Fatalf("stripped too much:\n%s", got)
	}
	reload, err = a.applyCorefile(store.Snapshot{})
	if err != nil || reload {
		t.Fatalf("second strip reload=%v err=%v", reload, err)
	}
}
