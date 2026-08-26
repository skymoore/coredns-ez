package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
)

func TestParseFilterPattern(t *testing.T) {
	p, err := parseFilterPattern("Ads.Example.COM")
	if err != nil || p.Pattern != "ads.example.com." || p.KidsOnly {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = parseFilterPattern("*.example.com")
	if err != nil || p.Pattern != "example.com." || !p.KidsOnly {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := parseFilterPattern("com"); err == nil {
		t.Fatal("single label")
	}
	if _, err := parseFilterPattern("*.*"); err == nil {
		t.Fatal("star")
	}
	if _, err := parseFilterPattern("localhost"); err == nil {
		t.Fatal("localhost")
	}
}

func TestParseFilterList(t *testing.T) {
	raw := `# comment
0.0.0.0 doubleclick.net
127.0.0.1 ads.example.com # trail
||tracker.io^
||com
example.org
localhost
1.2.3.4
@@||exception.com^
`
	got := parseFilterList(strings.NewReader(raw))
	have := map[string]bool{}
	for _, p := range got {
		have[p.Pattern] = true
	}
	for _, want := range []string{"doubleclick.net.", "ads.example.com.", "tracker.io.", "example.org."} {
		if !have[want] {
			t.Fatalf("missing %s in %+v", want, got)
		}
	}
	if have["localhost."] || have["com."] {
		t.Fatalf("unexpected %+v", got)
	}
}

func TestFilterEngineAllowWins(t *testing.T) {
	e := newFilterEngine()
	e.replace([]store.FilterRule{
		{Action: store.FilterBlock, Pattern: "example.com."},
		{Action: store.FilterAllow, Pattern: "maps.example.com."},
		{Action: store.FilterBlock, Pattern: "blocked.net.", KidsOnly: true},
	})
	if !e.blocked("ads.example.com.") {
		t.Fatal("subdomain of blocked apex")
	}
	if !e.blocked("example.com.") {
		t.Fatal("apex")
	}
	if e.blocked("maps.example.com.") {
		t.Fatal("allow should win")
	}
	if e.blocked("blocked.net.") {
		t.Fatal("kids-only must not match apex")
	}
	if !e.blocked("x.blocked.net.") {
		t.Fatal("kids-only subdomain")
	}
	if e.blocked("other.org.") {
		t.Fatal("unlisted")
	}
}

type dnsRec struct{ msg *dns.Msg }

func (d *dnsRec) LocalAddr() net.Addr  { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53} }
func (d *dnsRec) RemoteAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53} }
func (d *dnsRec) WriteMsg(m *dns.Msg) error {
	d.msg = m
	return nil
}
func (d *dnsRec) Write([]byte) (int, error) { return 0, nil }
func (d *dnsRec) Close() error              { return nil }
func (d *dnsRec) TsigStatus() error         { return nil }
func (d *dnsRec) TsigTimersOnly(bool)       {}
func (d *dnsRec) Hijack()                   {}

type stubNext struct{ called bool }

func (s *stubNext) Name() string { return "stub" }
func (s *stubNext) ServeDNS(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	s.called = true
	m := new(dns.Msg)
	m.SetReply(r)
	_ = w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

func TestServeDNSAppliesFilter(t *testing.T) {
	a := testAdmin(t)
	if _, err := a.db.InsertFilterRule(store.FilterRule{Action: store.FilterBlock, Pattern: "ads.example.com."}); err != nil {
		t.Fatal(err)
	}
	a.publishFilter()
	req := new(dns.Msg)
	req.SetQuestion("ads.example.com.", dns.TypeA)
	rec := &dnsRec{}
	next := &stubNext{}
	code, err := a.serveWithNext(context.Background(), rec, req, next)
	if err != nil || code != dns.RcodeNameError || rec.msg == nil || rec.msg.Rcode != dns.RcodeNameError {
		t.Fatalf("code=%d rcode=%v err=%v", code, rec.msg, err)
	}
	if next.called {
		t.Fatal("next must not run for a blocked name")
	}

	req.SetQuestion("ok.example.com.", dns.TypeA)
	rec = &dnsRec{}
	next = &stubNext{}
	code, err = a.serveWithNext(context.Background(), rec, req, next)
	if err != nil || !next.called || code != dns.RcodeSuccess {
		t.Fatalf("passthrough code=%d err=%v called=%v", code, err, next.called)
	}
}

func TestFilterHTTP(t *testing.T) {
	a := testAdmin(t)
	tok := loginToken(t, a)

	body, _ := json.Marshal(map[string]string{"action": "block", "pattern": "*.tracker.test"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/filters/rules", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create rule: %d %s", w.Code, w.Body.Bytes())
	}
	var rule store.FilterRule
	if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil || !rule.KidsOnly || rule.Pattern != "tracker.test." {
		t.Fatalf("rule %+v %v", rule, err)
	}
	if !a.filters.blocked("x.tracker.test.") {
		t.Fatal("engine not updated")
	}

	lists := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"1"`)
		_, _ = w.Write([]byte("0.0.0.0 evil.list.test\n||ads.list.test^\n"))
	}))
	t.Cleanup(lists.Close)
	a.filterHTTP = lists.Client()

	feedBody, _ := json.Marshal(map[string]any{
		"action": "block", "url": lists.URL, "sync": "once", "name": "test-list",
	})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/filters/feeds", bytes.NewReader(feedBody))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create feed: %d %s", w.Code, w.Body.Bytes())
	}
	var feed store.FilterFeed
	if err := json.Unmarshal(w.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if feed.Sync != store.FilterSyncOff {
		t.Fatalf("feed %+v", feed)
	}
	dup := httptest.NewRequest(http.MethodPost, "/api/v1/filters/feeds", bytes.NewReader(feedBody))
	dup.Header.Set("Authorization", "Bearer "+tok)
	dup.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, dup)
	if w.Code != http.StatusConflict {
		t.Fatalf("dup feed: %d %s", w.Code, w.Body.Bytes())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := a.db.GetFilterFeed(feed.ID)
		if got.LastCount >= 2 && a.filters.blocked("evil.list.test.") && a.filters.blocked("ads.list.test.") {
			feed = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if feed.LastCount < 2 || !a.filters.blocked("evil.list.test.") {
		t.Fatalf("async sync %+v blocked=%v", feed, a.filters.blocked("evil.list.test."))
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/filters", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.Bytes())
	}
	var listing struct {
		Manual []store.FilterRule `json:"manual"`
		Feeds  []store.FilterFeed `json:"feeds"`
		Counts map[string]int     `json:"counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Manual) != 1 || len(listing.Feeds) != 1 || listing.Counts[store.FilterBlock] < 3 {
		t.Fatalf("listing %+v", listing)
	}

	r = httptest.NewRequest(http.MethodDelete, "/api/v1/filters/feeds/"+feed.ID, nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete feed: %d %s", w.Code, w.Body.Bytes())
	}
	if a.filters.blocked("evil.list.test.") {
		t.Fatal("feed rules survived delete")
	}
}

func TestValidateFilterURL(t *testing.T) {
	if _, err := validateFilterURL("https://example.com/hosts.txt", false); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFilterURL("http://127.0.0.1/x", false); err == nil {
		t.Fatal("loopback")
	}
	if _, err := validateFilterURL("http://127.0.0.1/x", true); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFilterURL("file:///etc/passwd", false); err == nil {
		t.Fatal("file")
	}
	if _, err := validateFilterURL("http://10.0.0.1/list", false); err == nil {
		t.Fatal("private")
	}
}
