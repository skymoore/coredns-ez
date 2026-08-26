package dnsupdatepersist

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestAddThenReloadFromDisk(t *testing.T) {
	d := newTestPlugin(t, nil)
	before := serialOf(t, d)

	m := newUpdate(nil, []dns.RR{rr(t, `_acme-challenge.example.org. 60 IN TXT "token-value"`)})
	if got := send(t, d, m); got != dns.RcodeSuccess {
		t.Fatalf("rcode = %s, want NOERROR", dns.RcodeToString[got])
	}

	body, err := os.ReadFile(d.seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "token-value") {
		t.Errorf("persist file does not contain the added TXT:\n%s", body)
	}
	if !strings.Contains(string(body), "; persisted by coredns dns-update-persistent") {
		t.Error("persist file is missing the header comment")
	}

	reloaded := reloadFromDisk(t, d)
	if serialOf(t, reloaded) != before+1 {
		t.Errorf("reloaded serial = %d, want %d", serialOf(t, reloaded), before+1)
	}
	resp := query(t, reloaded, "_acme-challenge.example.org.", dns.TypeTXT)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("query after reload: rcode=%s answers=%d", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	txt, ok := resp.Answer[0].(*dns.TXT)
	if !ok || txt.Txt[0] != "token-value" {
		t.Errorf("answer = %v, want the added TXT", resp.Answer[0])
	}
}

func TestDeleteThenReloadFromDisk(t *testing.T) {
	d := newTestPlugin(t, nil)

	del := rr(t, "ns2.example.org. 0 IN A 192.0.2.2")
	del.Header().Class = dns.ClassNONE
	del.Header().Ttl = 0
	if got := send(t, d, newUpdate(nil, []dns.RR{del})); got != dns.RcodeSuccess {
		t.Fatalf("delete: %s", dns.RcodeToString[got])
	}

	body, err := os.ReadFile(d.seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "192.0.2.2") {
		t.Errorf("deleted A is still in the persist file:\n%s", body)
	}

	reloaded := reloadFromDisk(t, d)
	if reloaded.rrsetExists("ns2.example.org.", dns.TypeA) {
		t.Error("deleted A survived reload")
	}
	if !reloaded.rrsetExists("ns.example.org.", dns.TypeA) {
		t.Error("unrelated A disappeared on reload")
	}
}

func TestDeleteRRsetPersistsAndApexSurvives(t *testing.T) {
	d := newTestPlugin(t, nil)

	delset := &dns.ANY{Hdr: dns.RR_Header{
		Name: "www.example.org.", Rrtype: dns.TypeA, Class: dns.ClassANY, Ttl: 0,
	}}
	if got := send(t, d, newUpdate(nil, []dns.RR{delset})); got != dns.RcodeSuccess {
		t.Fatalf("delete www: %s", dns.RcodeToString[got])
	}

	wipe := &dns.ANY{Hdr: dns.RR_Header{
		Name: "example.org.", Rrtype: dns.TypeANY, Class: dns.ClassANY, Ttl: 0,
	}}
	if got := send(t, d, newUpdate(nil, []dns.RR{wipe})); got != dns.RcodeSuccess {
		t.Fatalf("apex wipe: %s", dns.RcodeToString[got])
	}

	reloaded := reloadFromDisk(t, d)
	if soaOf(reloaded.rrs) == nil {
		t.Error("reload lost the SOA")
	}
	if n := countRRset(reloaded.rrs, "example.org.", dns.TypeNS); n != 2 {
		t.Errorf("reload apex NS count = %d, want 2", n)
	}
	if reloaded.rrsetExists("www.example.org.", dns.TypeA) {
		t.Error("www A survived delete+reload")
	}
}

func TestNoopDoesNotRewriteFile(t *testing.T) {
	d := newTestPlugin(t, nil)
	before, err := os.ReadFile(d.seedPath)
	if err != nil {
		t.Fatal(err)
	}

	if got := send(t, d, newUpdate(nil, []dns.RR{rr(t, "www.example.org. 300 IN A 192.0.2.10")})); got != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[got])
	}

	after, err := os.ReadFile(d.seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("no-op UPDATE rewrote the seed file")
	}
}

func TestPersistFailureIsServFailAndLeavesMemory(t *testing.T) {
	d := newTestPlugin(t, nil)
	beforeSerial := serialOf(t, d)
	beforeFile, err := os.ReadFile(d.seedPath)
	if err != nil {
		t.Fatal(err)
	}

	persistWrite = func(path, origin string, rrs []dns.RR) error {
		return errors.New("injected persist failure")
	}
	t.Cleanup(func() { persistWrite = writeZoneFile })

	m := newUpdate(nil, []dns.RR{rr(t, `_acme-challenge.example.org. 60 IN TXT "should-not-land"`)})
	if got := send(t, d, m); got != dns.RcodeServerFailure {
		t.Fatalf("rcode = %s, want SERVFAIL", dns.RcodeToString[got])
	}

	if d.rrsetExists("_acme-challenge.example.org.", dns.TypeTXT) {
		t.Error("failed persist still applied the TXT in memory")
	}
	if serialOf(t, d) != beforeSerial {
		t.Errorf("serial moved on a failed persist: %d -> %d", beforeSerial, serialOf(t, d))
	}

	resp := query(t, d, "www.example.org.", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("live query after failed persist: rcode=%s answers=%d", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}

	afterFile, err := os.ReadFile(d.seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFile) != string(beforeFile) {
		t.Error("failed persist mutated the seed file")
	}
}

func TestCrashBeforeRenameKeepsPreviousDest(t *testing.T) {
	d := newTestPlugin(t, nil)

	tmp := filepath.Join(filepath.Dir(d.seedPath), ".persist-leftover")
	if err := os.WriteFile(tmp, []byte("; leftover temp from a crashed write\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	reloaded := reloadFromDisk(t, d)
	resp := query(t, reloaded, "www.example.org.", dns.TypeA)
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("load with leftover temp: rcode=%s answers=%d", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
	if reloaded.rrsetExists("_acme-challenge.example.org.", dns.TypeTXT) {
		t.Error("leftover temp was treated as the zone")
	}
}

func TestFileModePreserved(t *testing.T) {
	d := newTestPlugin(t, nil)
	if err := os.Chmod(d.seedPath, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := send(t, d, newUpdate(nil, []dns.RR{rr(t, `_acme-challenge.example.org. 60 IN TXT "x"`)})); got != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[got])
	}

	st, err := os.Stat(d.seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", st.Mode().Perm())
	}
}

func TestFirstUpdateFlattensInclude(t *testing.T) {
	dir := t.TempDir()
	extra := filepath.Join(dir, "extra.db")
	if err := os.WriteFile(extra, []byte("foo.example.org. 300 IN A 192.0.2.99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(dir, "db.example.org")
	body := `$ORIGIN example.org.
$TTL 300
@   SOA ns.example.org. admin.example.org. 100 3600 900 86400 300
@   NS  ns.example.org.
$INCLUDE ` + extra + `
`
	if err := os.WriteFile(seed, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	rrs, err := readZone(seed, "example.org.")
	if err != nil {
		t.Fatalf("readZone: %v", err)
	}
	d := &UpdatePersist{Zone: "example.org.", rrs: rrs, seedPath: seed}
	if err := d.swap(rrs); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if !d.rrsetExists("foo.example.org.", dns.TypeA) {
		t.Fatal("$INCLUDE was not loaded")
	}

	if got := send(t, d, newUpdate(nil, []dns.RR{rr(t, `_acme-challenge.example.org. 60 IN TXT "x"`)})); got != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[got])
	}

	out, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Contains(text, "$INCLUDE") {
		t.Errorf("persist file still has $INCLUDE:\n%s", text)
	}
	if !strings.Contains(text, "192.0.2.99") {
		t.Errorf("included A missing from flattened file:\n%s", text)
	}
	if !strings.Contains(text, "_acme-challenge") {
		t.Errorf("added TXT missing from flattened file:\n%s", text)
	}

	reloaded := reloadFromDisk(t, d)
	if !reloaded.rrsetExists("foo.example.org.", dns.TypeA) {
		t.Error("included A did not survive flatten+reload")
	}
	if !reloaded.rrsetExists("_acme-challenge.example.org.", dns.TypeTXT) {
		t.Error("added TXT did not survive flatten+reload")
	}
}

func TestSuccessfulUpdateLeavesNoTemp(t *testing.T) {
	d := newTestPlugin(t, nil)
	if got := send(t, d, newUpdate(nil, []dns.RR{rr(t, `_acme-challenge.example.org. 60 IN TXT "x"`)})); got != dns.RcodeSuccess {
		t.Fatalf("rcode = %s", dns.RcodeToString[got])
	}

	ents, err := os.ReadDir(filepath.Dir(d.seedPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".persist-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}
