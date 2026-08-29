package cachegen

import (
	"net"
	"sync/atomic"
)

// Epoch is folded into split-horizon-cache keys. Bump it after a zone or ACL
// mutation so a cached public wildcard cannot outlive the view record that
// should replace it. Entries from the previous epoch become unreachable and
// age out of the shards.
var epoch atomic.Uint64

func Bump() { epoch.Add(1) }

func Get() uint64 { return epoch.Load() }

// Matcher maps a client IP to a cache-key label. Admin registers one that
// returns the first matching ACL name, or "public".
type Matcher func(ip net.IP) string

var matcher atomic.Pointer[Matcher]

func SetMatcher(m Matcher) {
	if m == nil {
		matcher.Store(nil)
		return
	}
	matcher.Store(&m)
}

// Label is the split-horizon cache key for this client. Empty means "do not
// cache": either the matcher is unset (unit tests fall back to netmask) or
// the address could not be parsed (fail closed — never share one bucket).
func Label(ipStr string) string {
	p := matcher.Load()
	if p == nil || *p == nil {
		return ""
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	s := (*p)(ip)
	if s == "" {
		return "public"
	}
	return s
}
