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
	cleaned time.Time
}

func newLimiterStore(limit rate.Limit, burst int) *limiterStore {
	return &limiterStore{
		entries: map[string]*limiterEntry{},
		limit:   limit,
		burst:   burst,
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
		if !s.allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP keys buckets on the originating client. Behind the TLS-terminating
// proxy the real client sits in X-Forwarded-For, so its first hop wins; the
// RemoteAddr host is the fallback for direct connections.
func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		if i := strings.IndexByte(xf, ','); i > 0 {
			xf = xf[:i]
		}
		return strings.TrimSpace(xf)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
