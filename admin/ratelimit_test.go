package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestLimiterStoreDeniesAfterBurst(t *testing.T) {
	s := newLimiterStore(rate.Limit(0), 3)
	h := s.middleware(okHandler())
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request: %d", w.Code)
	}
}

func TestLimiterStoreSeparateBucketsPerIP(t *testing.T) {
	s := newLimiterStore(rate.Limit(0), 1)
	h := s.middleware(okHandler())
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Forwarded-For", ip)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s first hit: %d", ip, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("same ip again: %d", w.Code)
	}
}

func TestClientIPPrefersForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("xff: %q", got)
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	if got := clientIP(r); got != "192.0.2.1" {
		t.Fatalf("remote addr: %q", got)
	}
}
