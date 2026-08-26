package secondarypersist

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/test"

	"github.com/miekg/dns"
)

func TestWriteAtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.example.org")
	origin := "example.org."

	z := file.NewZone(origin, path)
	for _, rr := range []dns.RR{
		test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 2 7200 3600 1209600 3600"),
		test.NS("example.org. 3600 IN NS ns.example.org."),
		test.A("www.example.org. 3600 IN A 192.0.2.1"),
		test.AAAA("www.example.org. 3600 IN AAAA 2001:db8::1"),
	} {
		if err := z.Insert(rr); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	snap, ok := snapshotZone(origin, path, z)
	if !ok {
		t.Fatal("expected snapshot")
	}
	if err := writeAtomic(snap); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}

	loaded, err := loadZone(path, origin)
	if err != nil {
		t.Fatalf("loadZone: %v", err)
	}
	if zoneSOA(loaded) == nil || zoneSOA(loaded).Serial != 2 {
		t.Fatalf("expected serial 2, got %+v", zoneSOA(loaded))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "secondary-persistent") {
		t.Fatalf("expected persist comment, got %s", text)
	}
	if strings.Contains(text, "$INCLUDE") {
		t.Fatal("persist file must not contain $INCLUDE")
	}
}

func TestLoadZoneRejectsInclude(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.example.org")
	included := filepath.Join(dir, "other")
	if err := os.WriteFile(included, []byte("www.example.org. 3600 IN A 192.0.2.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 1 7200 3600 1209600 3600\n$INCLUDE " + included + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadZone(path, "example.org."); err == nil {
		t.Fatal("expected $INCLUDE to be rejected")
	}
}

func TestLoadZoneMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadZone(filepath.Join(dir, "missing"), "example.org."); err == nil {
		t.Fatal("expected missing file error")
	}

	path := filepath.Join(dir, "db.example.org")
	if err := os.WriteFile(path, []byte("www.example.org. 3600 IN A 192.0.2.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadZone(path, "example.org."); err == nil {
		t.Fatal("expected SOA-less file to fail")
	}
}

func TestWriteAtomicLeavesPreviousOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.example.org")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := zoneSnapshot{origin: "example.org.", path: path}
	if err := writeAtomic(snap); err == nil {
		t.Fatal("expected write of SOA-less snapshot to fail")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\n" {
		t.Fatalf("expected previous dest to remain, got %q", got)
	}
}

func TestPathForDirectory(t *testing.T) {
	dir := t.TempDir()
	s := &SecondaryPersist{persistDir: dir}
	p := s.pathFor("example.org.")
	if p != filepath.Join(dir, "db.example.org") {
		t.Fatalf("got %s", p)
	}
	if persistFileName("example.org.") != "db.example.org" {
		t.Fatalf("unexpected file name %s", persistFileName("example.org."))
	}
}

func TestLoadIfPresent(t *testing.T) {
	dir := t.TempDir()
	origin := "example.org."
	path := filepath.Join(dir, persistFileName(origin))
	z := file.NewZone(origin, "stdin")
	for _, rr := range []dns.RR{
		test.SOA("example.org. 3600 IN SOA ns.example.org. hostmaster.example.org. 9 7200 3600 1209600 3600"),
		test.NS("example.org. 3600 IN NS ns.example.org."),
	} {
		if err := z.Insert(rr); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ := snapshotZone(origin, path, z)
	if err := writeAtomic(snap); err != nil {
		t.Fatal(err)
	}

	dst := file.NewZone(origin, "stdin")
	s := &SecondaryPersist{
		persistDir:  dir,
		persistStop: make(chan struct{}),
		lastSerial:  map[string]uint32{},
		hasWritten:  map[string]bool{},
		writing:     map[string]bool{},
		pending:     map[string]zoneSnapshot{},
	}
	s.loadIfPresent(origin, dst)
	if soa := zoneSOA(dst); soa == nil || soa.Serial != 9 {
		t.Fatalf("expected loaded serial 9, got %+v", soa)
	}
}

func TestLoadIfPresentMissingIsOK(t *testing.T) {
	origin := "example.org."
	z := file.NewZone(origin, "stdin")
	s := &SecondaryPersist{
		persistDir:  t.TempDir(),
		persistStop: make(chan struct{}),
		lastSerial:  map[string]uint32{},
		hasWritten:  map[string]bool{},
		writing:     map[string]bool{},
		pending:     map[string]zoneSnapshot{},
	}
	s.loadIfPresent(origin, z)
	if zoneSOA(z) != nil {
		t.Fatal("missing persist file should leave zone empty")
	}
}

func TestRemovePersistFile(t *testing.T) {
	dir := t.TempDir()
	origin := "example.org."
	path := filepath.Join(dir, persistFileName(origin))
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &SecondaryPersist{
		persistDir: dir,
		lastSerial: map[string]uint32{path: 1},
		hasWritten: map[string]bool{path: true},
		pending:    map[string]zoneSnapshot{},
	}
	s.removePersistFile(origin)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected file removed, stat err %v", err)
	}
}
