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
		"secondary-persistent {",
		"transfer from 192.168.8.53:53",
		"persist /etc/coredns/zones/db.rwx.dev.internal",
		"persist /etc/coredns/zones/db.rwx.dev.public",
	}
	for _, s := range checks {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q in:\n%s", s, got)
		}
	}
	bans := []string{"role primary", "bootstrap_admin", "advertise 192.168.8.53", "dns-update-persistent", "\tixfr\n", "\tfile /etc/coredns/zones/"}
	for _, s := range bans {
		if strings.Contains(got, s) {
			t.Fatalf("still has %q in:\n%s", s, got)
		}
	}
}

func TestMergeClusteredCorefileKeepsLocalListener(t *testing.T) {
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
	if !strings.Contains(got, "rwx.dev") || !strings.Contains(got, "secondary-persistent") {
		t.Fatalf("missing zone block:\n%s", got)
	}
	if strings.Contains(got, "dns-update-persistent") {
		t.Fatal(got)
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

func TestApplyCorefileWritesAndHashes(t *testing.T) {
	a := testAdmin(t)
	conf := filepath.Join(a.cfg.Data, "Corefile")
	t.Setenv("FAKE", "1")
	origArgs := os.Args
	os.Args = []string{"coredns", "-conf", conf}
	t.Cleanup(func() { os.Args = origArgs })

	src := ".:53 {\n\tadmin {\n\t\trole primary\n\t\tadvertise 10.0.0.1:53\n\t}\n}\n"
	snap := storeSnapshot(src)
	if err := a.db.SetMeta(store.MetaMemberID, "me"); err != nil {
		t.Fatal(err)
	}
	snap.Members = []store.Member{{ID: "me", DNSAddr: "10.0.0.2:53", APIURL: "http://10.0.0.2:8080"}}
	reload, err := a.applyCorefile(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !reload {
		t.Fatal("expected reload")
	}
	got, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "role secondary") || !strings.Contains(string(got), "dns 10.0.0.1:53") {
		t.Fatalf("%s", got)
	}
	reload, err = a.applyCorefile(snap)
	if err != nil || reload {
		t.Fatalf("second apply reload=%v err=%v", reload, err)
	}
}

func storeSnapshot(text string) store.Snapshot {
	return store.Snapshot{Corefile: text, CorefileHash: corefileHash(text)}
}
