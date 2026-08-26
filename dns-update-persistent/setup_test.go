package dnsupdatepersist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/miekg/dns"
)

func writeSeed(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.example.org")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParse(t *testing.T) {
	seed := writeSeed(t, seedZone)
	noSOA := writeSeed(t, "www.example.org. 300 IN A 192.0.2.10\n")

	cases := []struct {
		name    string
		input   string
		wantErr string // substring; empty means success
		check   func(*testing.T, *UpdatePersist)
	}{
		{
			name:  "zone from the server block",
			input: "dns-update-persistent {\nfile " + seed + "\n}",
			check: func(t *testing.T, d *UpdatePersist) {
				if d.Zone != "example.org." {
					t.Errorf("zone = %q, want example.org.", d.Zone)
				}
				if d.mutable != nil {
					t.Error("mutable set without a `mutable` directive")
				}
				if d.seedPath != seed {
					t.Errorf("seedPath = %q, want %q", d.seedPath, seed)
				}
			},
		},
		{
			name:  "explicit zone is canonicalised",
			input: "dns-update-persistent EXAMPLE.ORG {\nfile " + seed + "\n}",
			check: func(t *testing.T, d *UpdatePersist) {
				if d.Zone != "example.org." {
					t.Errorf("zone = %q, want example.org.", d.Zone)
				}
			},
		},
		{
			name:  "mutable type list",
			input: "dns-update-persistent {\nfile " + seed + "\nmutable TXT AAAA\n}",
			check: func(t *testing.T, d *UpdatePersist) {
				if !d.mutable[dns.TypeTXT] || !d.mutable[dns.TypeAAAA] {
					t.Errorf("mutable = %v, want TXT and AAAA", d.mutable)
				}
				if d.mutable[dns.TypeA] {
					t.Error("A is mutable but was not listed")
				}
			},
		},
		{
			// The failure this guards against is silent: with no SOA the
			// serial cannot advance, so every secondary keeps serving the old
			// zone while the primary reports every update as a success.
			name:    "seed without an SOA is rejected",
			input:   "dns-update-persistent {\nfile " + noSOA + "\n}",
			wantErr: "no SOA",
		},
		{
			name:    "seed file is required",
			input:   "dns-update-persistent {\n}",
			wantErr: "seed zone is required",
		},
		{
			name:    "two zones in one block",
			input:   "dns-update-persistent a.example.org b.example.org {\nfile " + seed + "\n}",
			wantErr: "exactly one zone",
		},
		{
			name:    "unknown property",
			input:   "dns-update-persistent {\nfile " + seed + "\nwibble yes\n}",
			wantErr: "unknown property",
		},
		{
			name:    "unknown RR type",
			input:   "dns-update-persistent {\nfile " + seed + "\nmutable NOTATYPE\n}",
			wantErr: "unknown RR type",
		},
		{
			name:    "missing zone file",
			input:   "dns-update-persistent {\nfile /nonexistent/db.example.org\n}",
			wantErr: "opening zone directory",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := caddy.NewTestController("dns", tc.input)
			c.ServerBlockKeys = []string{"example.org."}

			d, err := parse(c)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("parse succeeded, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if tc.check != nil {
				tc.check(t, d)
			}
		})
	}
}

func TestParseJoinsRelativePathWithRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db.example.org"), []byte(seedZone), 0o600); err != nil {
		t.Fatal(err)
	}

	c := caddy.NewTestController("dns", "dns-update-persistent {\nfile db.example.org\n}")
	c.ServerBlockKeys = []string{"example.org."}
	dnsserver.GetConfig(c).Root = dir

	d, err := parse(c)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := filepath.Join(dir, "db.example.org")
	if d.seedPath != want {
		t.Errorf("seedPath = %q, want %q", d.seedPath, want)
	}
}

// A seed file reached through a symlink pointing outside its directory must
// not load: the path comes from a config file, and os.OpenRoot is what keeps
// `file ../../etc/shadow` from being a zone.
func TestSeedCannotEscapeItsDirectory(t *testing.T) {
	outside := writeSeed(t, seedZone)

	dir := t.TempDir()
	link := filepath.Join(dir, "db.example.org")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := readZone(link, "example.org."); err == nil {
		t.Error("a symlink out of the zone directory was followed")
	}
}
