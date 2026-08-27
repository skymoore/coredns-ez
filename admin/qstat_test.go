package admin

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skymoore/coredns-ez/admin/store"
)

func TestQueryHubPersistsBucketsAndRange(t *testing.T) {
	queries.resetForTest()
	t.Cleanup(queries.resetForTest)
	s, err := store.Open(filepath.Join(t.TempDir(), "qstat.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now()
	past := now.Add(-30 * time.Second)
	queries.record(queryEvent{At: past, Name: "a.example.", Type: "A", Rcode: "NOERROR"})
	queries.record(queryEvent{At: past, Name: "a.example.", Type: "AAAA", Rcode: "NOERROR"})
	queries.record(queryEvent{At: now, Name: "a.example.", Type: "TXT", Rcode: "NOERROR"})

	a := &Admin{db: s, stop: make(chan struct{})}
	a.flushQueryStats()
	rows, err := s.ListQueryBuckets(now.Add(-time.Hour).Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("expected completed buckets in sqlite")
	}
	snap := queries.snapshotRange(rows, parseQueryRange("1h"))
	if snap.Range != "1h" || snap.StepSeconds != 30 {
		t.Fatalf("range %+v", snap)
	}
	if snap.RangeQueries < 3 {
		t.Fatalf("range queries %d", snap.RangeQueries)
	}
	if len(snap.Series) == 0 {
		t.Fatal("empty series")
	}
	var sawA, sawAAAA, sawTXT bool
	for _, pt := range snap.Series {
		if pt.Types["A"] > 0 {
			sawA = true
		}
		if pt.Types["AAAA"] > 0 {
			sawAAAA = true
		}
		if pt.Types["TXT"] > 0 {
			sawTXT = true
		}
	}
	if !sawA || !sawAAAA || !sawTXT {
		t.Fatalf("types in series A=%v AAAA=%v TXT=%v", sawA, sawAAAA, sawTXT)
	}
	if err := s.PruneQueryBuckets(now.Unix() + 10); err != nil {
		t.Fatal(err)
	}
	left, err := s.ListQueryBuckets(0, now.Unix()+100)
	if err != nil || len(left) != 0 {
		t.Fatalf("prune leftover %d %v", len(left), err)
	}
}

func TestParseQueryRange(t *testing.T) {
	if parseQueryRange("").Label != "1h" || parseQueryRange("nope").Label != "1h" {
		t.Fatal(parseQueryRange(""))
	}
	if parseQueryRange("7d").Duration != 7*24*time.Hour || parseQueryRange("5m").Step != 10*time.Second {
		t.Fatal(parseQueryRange("7d"))
	}
}

func TestQueryHubTopAndRecent(t *testing.T) {
	queries.resetForTest()
	t.Cleanup(queries.resetForTest)
	now := time.Now()
	queries.record(queryEvent{At: now, Name: "a.example.", Type: "A", Rcode: "NOERROR", Client: "10.0.0.1"})
	queries.record(queryEvent{At: now, Name: "a.example.", Type: "A", Rcode: "NOERROR", Client: "10.0.0.2"})
	queries.record(queryEvent{At: now, Name: "ads.example.", Type: "A", Rcode: "NXDOMAIN", Client: "10.0.0.3", Blocked: true})
	snap := queries.snapshot()
	if snap.Total != 3 || snap.Blocked != 1 || snap.NXDomain != 1 {
		t.Fatalf("counters %+v", snap)
	}
	if len(snap.Recent) != 3 || snap.Recent[0].Name != "ads.example." {
		t.Fatalf("recent newest-first %+v", snap.Recent)
	}
	if len(snap.TopNames) == 0 || snap.TopNames[0].Name != "a.example." || snap.TopNames[0].Count != 2 {
		t.Fatalf("top %+v", snap.TopNames)
	}
	if len(snap.TopBlocked) != 1 || snap.TopBlocked[0].Name != "ads.example." {
		t.Fatalf("blocked %+v", snap.TopBlocked)
	}
	if snap.QPS <= 0 {
		t.Fatalf("qps %v", snap.QPS)
	}
}

func TestInjectQstat(t *testing.T) {
	src := "https://.:8080 {\n\terrors\n\tadmin\n}\n\nrwx.dev {\n\tfile db.rwx.dev\n}\n"
	got, changed := injectQstat(src)
	if !changed {
		t.Fatal("expected inject")
	}
	if !strings.Contains(got, "\tqstat\n") {
		t.Fatalf("missing qstat:\n%s", got)
	}
	if _, changed := injectQstat(got); changed {
		t.Fatal("not idempotent")
	}
}

func TestInjectQstatSkipsSnippets(t *testing.T) {
	src := "(common) {\n\tbind 127.0.0.1\n}\n\nrwx.dev {\n\tfile db.rwx.dev\n}\n"
	got, changed := injectQstat(src)
	if !changed {
		t.Fatal("expected inject into rwx.dev")
	}
	if strings.Contains(got, "(common) {\n\tqstat") {
		t.Fatalf("qstat must not go in snippets:\n%s", got)
	}
	if !strings.Contains(got, "rwx.dev {\n\tqstat\n") {
		t.Fatalf("missing server qstat:\n%s", got)
	}
}
