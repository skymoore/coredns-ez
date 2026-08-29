package admin

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// limiterEntry holds one per-client token bucket and its last hit time.
type limiterEntry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// limiterStore keeps a per-client token bucket. Buckets for idle clients are
// evicted lazily so the map stays bounded without a background goroutine.
type limiterStore struct {
	mu      sync.Mutex
	entries map[string]*limiterEntry
	limit   rate.Limit
	burst   int
	trust   bool
	cleaned time.Time
}

func newLimiterStore(limit rate.Limit, burst int, trustProxy bool) *limiterStore {
	return &limiterStore{
		entries: map[string]*limiterEntry{},
		limit:   limit,
		burst:   burst,
		trust:   trustProxy,
		cleaned: time.Now(),
	}
}

const (
	limiterIdleTTL    = 5 * time.Minute
	limiterSweepEvery = time.Minute
	limiterSweepMin   = 1024
)

func (s *limiterStore) allow(key string) bool {
	now := time.Now()
	s.mu.Lock()
	e, ok := s.entries[key]
	if !ok {
		if len(s.entries) >= limiterSweepMin && now.Sub(s.cleaned) > limiterSweepEvery {
			for k, v := range s.entries {
				if now.Sub(v.lastSeen) > limiterIdleTTL {
					delete(s.entries, k)
				}
			}
			s.cleaned = now
		}
		e = &limiterEntry{lim: rate.NewLimiter(s.limit, s.burst)}
		s.entries[key] = e
	}
	e.lastSeen = now
	s.mu.Unlock()
	return e.lim.Allow()
}

func (s *limiterStore) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allow(clientIP(r, s.trust)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP keys buckets on the originating client. X-Forwarded-For is only
// honoured when the immediate peer is itself a trusted hop: a loopback or
// private proxy on the same host, or any peer once trust_proxy is configured.
// Otherwise the header is attacker-controlled (rotate it and every request
// gets a fresh bucket), so the RemoteAddr host stays authoritative.
func clientIP(r *http.Request, trustProxy bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	xf := r.Header.Get("X-Forwarded-For")
	if xf == "" {
		return host
	}
	if ip := net.ParseIP(host); !trustProxy && (ip == nil || !(ip.IsLoopback() || ip.IsPrivate())) {
		return host
	}
	if i := strings.IndexByte(xf, ','); i > 0 {
		xf = xf[:i]
	}
	return strings.TrimSpace(xf)
}
