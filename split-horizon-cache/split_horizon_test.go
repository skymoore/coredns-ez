package cache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/skymoore/coredns-ez/internal/cachegen"

	"github.com/miekg/dns"
)

func serveFrom(t *testing.T, c *Cache, remote, qname string) *dns.Msg {
	t.Helper()
	req := new(dns.Msg)
	req.SetQuestion(qname, dns.TypeA)
	rec := dnstest.NewRecorder(&test.ResponseWriter{RemoteIP: remote})
	if _, err := c.ServeDNS(context.Background(), rec, req); err != nil {
		t.Fatal(err)
	}
	if rec.Msg == nil {
		t.Fatal("no response written")
	}
	return rec.Msg
}

func answerOf(t *testing.T, m *dns.Msg) string {
	t.Helper()
	if len(m.Answer) == 0 {
		return ""
	}
	a, ok := m.Answer[0].(*dns.A)
	if !ok {
		return ""
	}
	return a.A.String()
}

// The regression guard: an internal client primes the cache, then a public
// client asks the same name and must not receive the internal answer. With
// the upstream name-only key the public client gets the cached internal IP.
func TestSplitHorizonKeysPerSourceBucket(t *testing.T) {
	calls := 0
	c := New()
	c.minpttl = 0
	c.Next = plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		calls++
		host, _, _ := net.SplitHostPort(w.RemoteAddr().String())
		ip := net.ParseIP(host)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		if ip4 := ip.To4(); ip4 != nil && ip4[0] == 10 {
			m.Answer = []dns.RR{test.A("split.example. 300 IN A 10.1.2.3")}
		} else {
			m.Answer = []dns.RR{test.A("split.example. 300 IN A 192.0.2.10")}
		}
		return dns.RcodeSuccess, w.WriteMsg(m)
	})

	if got := answerOf(t, serveFrom(t, c, "10.9.8.7", "split.example.")); got != "10.1.2.3" {
		t.Fatalf("internal client got %q, want 10.1.2.3", got)
	}
	// A public client on a different source network must miss the cache and
	// get its own answer, not the cached internal one.
	if got := answerOf(t, serveFrom(t, c, "8.8.8.8", "split.example.")); got != "192.0.2.10" {
		t.Fatalf("public client got %q, want 192.0.2.10 (cached view answer leaked)", got)
	}
	if calls != 2 {
		t.Fatalf("expected one upstream call per source bucket, got %d", calls)
	}

	// A second internal client inside the same /24 bucket hits the cache.
	before := calls
	if got := answerOf(t, serveFrom(t, c, "10.9.8.9", "split.example.")); got != "10.1.2.3" {
		t.Fatalf("second internal client got %q, want 10.1.2.3", got)
	}
	if calls != before {
		t.Fatalf("same-bucket client should hit the cache, upstream calls %d -> %d", before, calls)
	}
}

// netmask widens the IPv4 bucket: with netmask 16, hosts in 192.168.1.0/24 and
// 192.168.2.0/24 share entries; with the default /24 they do not.
func TestSplitHorizonNetmaskOption(t *testing.T) {
	for _, tc := range []struct {
		name      string
		block     string
		wantCalls int
	}{
		{"shared /16", "netmask 16", 1},
		{"default /24", "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := caddy.NewTestController("dns", "cache . {\n"+tc.block+"\n}\n")
			c, err := cacheParse(ctrl)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			c.minpttl = 0
			c.Next = plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
				calls++
				m := new(dns.Msg)
				m.SetReply(r)
				m.Answer = []dns.RR{test.A("a.example. 300 IN A 192.0.2.1")}
				return dns.RcodeSuccess, w.WriteMsg(m)
			})

			serveFrom(t, c, "192.168.1.5", "a.example.")
			serveFrom(t, c, "192.168.2.5", "a.example.")
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestBucketOf(t *testing.T) {
	c := New()
	eq := func(a, b []byte) bool { return string(a) == string(b) }

	if b := c.bucketOf("192.168.8.53"); !eq(b, c.bucketOf("192.168.8.200")) {
		t.Fatalf("same /24 should share a bucket: %v vs %v", b, c.bucketOf("192.168.8.200"))
	}
	if eq(c.bucketOf("192.168.8.53"), c.bucketOf("10.0.0.1")) {
		t.Fatal("different /24 must not share a bucket")
	}
	if eq(c.bucketOf("2001:db8:1::1"), c.bucketOf("2001:db8:2::1")) {
		t.Fatal("different /64 v6 must not share a bucket")
	}
	if !eq(c.bucketOf("2001:db8:1::1"), c.bucketOf("2001:db8:1::ffff")) {
		t.Fatal("same /64 should share a bucket")
	}
	if b := c.bucketOf(""); b != nil {
		t.Fatalf("empty address should yield nil bucket: %v", b)
	}

	c.mask4 = 0
	if !eq(c.bucketOf("192.168.8.53"), c.bucketOf("10.0.0.1")) {
		t.Fatal("mask 0 must collapse all v4 into one bucket")
	}
	if eq(c.bucketOf("192.168.8.53"), c.bucketOf("2001:db8::1")) {
		t.Fatal("v4 and v6 buckets must not collide")
	}
}

func TestPrefetchDoesNotPoisonSplitHorizon(t *testing.T) {
	t0, err := time.Parse(time.RFC3339, "2018-01-01T14:00:00+00:00")
	if err != nil {
		t.Fatal(err)
	}
	c := New()
	c.minpttl = 0
	c.prefetch = 1
	c.percentage = 10
	c.now = func() time.Time { return t0 }
	c.Next = plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		host, _, _ := net.SplitHostPort(w.RemoteAddr().String())
		ip := net.ParseIP(host)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		if ip4 := ip.To4(); ip4 != nil && ip4[0] == 10 {
			m.Answer = []dns.RR{test.A("split.example. 80 IN A 10.1.2.3")}
		} else {
			m.Answer = []dns.RR{test.A("split.example. 80 IN A 192.0.2.10")}
		}
		return dns.RcodeSuccess, w.WriteMsg(m)
	})

	if got := answerOf(t, serveFrom(t, c, "10.9.8.7", "split.example.")); got != "10.1.2.3" {
		t.Fatalf("prime: got %q", got)
	}
	c.now = func() time.Time { return t0.Add(73 * time.Second) }
	if got := answerOf(t, serveFrom(t, c, "10.9.8.7", "split.example.")); got != "10.1.2.3" {
		t.Fatalf("hit triggering prefetch: got %q", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.now = func() time.Time { return t0.Add(80 * time.Second) }
		got := answerOf(t, serveFrom(t, c, "10.9.8.7", "split.example."))
		if got == "10.1.2.3" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after prefetch LAN got %q; public answer poisoned the internal bucket", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCacheKeyedByACLNameNotNetmask(t *testing.T) {
	cachegen.SetMatcher(func(ip net.IP) string {
		if v4 := ip.To4(); v4 != nil && v4[0] == 10 {
			return "internal"
		}
		return "public"
	})
	t.Cleanup(func() { cachegen.SetMatcher(nil) })

	calls := 0
	c := New()
	c.minpttl = 0
	c.Next = plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		calls++
		host, _, _ := net.SplitHostPort(w.RemoteAddr().String())
		ip := net.ParseIP(host)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		if ip4 := ip.To4(); ip4 != nil && ip4[0] == 10 {
			m.Answer = []dns.RR{test.A("split.example. 300 IN A 10.1.2.3")}
		} else {
			m.Answer = []dns.RR{test.A("split.example. 300 IN A 192.0.2.10")}
		}
		return dns.RcodeSuccess, w.WriteMsg(m)
	})

	if got := answerOf(t, serveFrom(t, c, "10.1.1.1", "split.example.")); got != "10.1.2.3" {
		t.Fatalf("internal: got %q", got)
	}
	before := calls
	if got := answerOf(t, serveFrom(t, c, "10.9.8.7", "split.example.")); got != "10.1.2.3" {
		t.Fatalf("other /24 in same ACL must share the cache: got %q", got)
	}
	if calls != before {
		t.Fatalf("ACL-keyed cache should hit across /24s, calls %d -> %d", before, calls)
	}
	if got := answerOf(t, serveFrom(t, c, "8.8.8.8", "split.example.")); got != "192.0.2.10" {
		t.Fatalf("public client got %q", got)
	}
	if calls != before+1 {
		t.Fatalf("public client must miss the internal ACL key, calls %d", calls)
	}
}

func TestEpochBumpInvalidatesCache(t *testing.T) {
	calls := 0
	c := New()
	c.minpttl = 0
	c.Next = plugin.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
		calls++
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Answer = []dns.RR{test.A("pg.db.example. 300 IN A 192.0.2.10")}
		return dns.RcodeSuccess, w.WriteMsg(m)
	})

	if got := answerOf(t, serveFrom(t, c, "192.168.8.7", "pg.db.example.")); got != "192.0.2.10" {
		t.Fatalf("prime: got %q", got)
	}
	before := calls
	if got := answerOf(t, serveFrom(t, c, "192.168.8.7", "pg.db.example.")); got != "192.0.2.10" {
		t.Fatalf("cached: got %q", got)
	}
	if calls != before {
		t.Fatal("same client should hit the cache before a zone mutation")
	}

	cachegen.Bump()
	if got := answerOf(t, serveFrom(t, c, "192.168.8.7", "pg.db.example.")); got != "192.0.2.10" {
		t.Fatalf("after bump: got %q", got)
	}
	if calls != before+1 {
		t.Fatalf("epoch bump must miss the cache, upstream calls %d -> %d", before, calls)
	}
}
