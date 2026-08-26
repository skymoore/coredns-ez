// Package zonereg is a process-wide index of authoritative zones owned by
// this repo's plugins. The API plugin lists and mutates through it; Corefile
// plugins register on startup so a mixed process still has one inventory.
package zonereg

import (
	"fmt"
	"strings"
	"sync"

	"github.com/coredns/coredns/plugin"
	"github.com/miekg/dns"
)

const (
	SourceAdmin    = "admin"
	SourceCorefile = "corefile"

	KindPrimary   = "primary"
	KindSecondary = "secondary"
)

// Primary is a writable zone (dns-update-persistent, or an API-created copy).
type Primary interface {
	Origin() string
	Source() string
	Records() []dns.RR
	Apply(adds, deletes []dns.RR) error
	Path() string
}

// Secondary is a transferred zone (secondary-persistent, or an API-created copy).
type Secondary interface {
	Origin() string
	Source() string
	Records() []dns.RR
	TransferFrom() []string
	ForceTransfer() error
	Path() string
}

type entry struct {
	kind string
	pri  Primary
	sec  Secondary
}

var (
	mu    sync.RWMutex
	zones = map[string]entry{}
	names []string
)

// RegisterPrimary fails if origin is already claimed.
func RegisterPrimary(p Primary) error {
	return register(p.Origin(), entry{kind: KindPrimary, pri: p})
}

// RegisterSecondary fails if origin is already claimed.
func RegisterSecondary(s Secondary) error {
	return register(s.Origin(), entry{kind: KindSecondary, sec: s})
}

func register(origin string, e entry) error {
	origin = canonical(origin)
	if origin == "" {
		return fmt.Errorf("empty origin")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := zones[origin]; ok {
		return fmt.Errorf("zone %s already registered", origin)
	}
	zones[origin] = e
	names = append(names, origin)
	return nil
}

// Unregister drops origin. Missing origins are a no-op.
func Unregister(origin string) {
	origin = canonical(origin)
	mu.Lock()
	defer mu.Unlock()
	if _, ok := zones[origin]; !ok {
		return
	}
	delete(zones, origin)
	out := names[:0]
	for _, n := range names {
		if n != origin {
			out = append(out, n)
		}
	}
	names = out
}

// PrimaryOf returns the writable zone for origin, or nil.
func PrimaryOf(origin string) Primary {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := zones[canonical(origin)]
	if !ok || e.pri == nil {
		return nil
	}
	return e.pri
}

// SecondaryOf returns the transferred zone for origin, or nil.
func SecondaryOf(origin string) Secondary {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := zones[canonical(origin)]
	if !ok || e.sec == nil {
		return nil
	}
	return e.sec
}

// Lookup longest-matches qname against registered origins.
func Lookup(qname string) (origin, kind string) {
	mu.RLock()
	defer mu.RUnlock()
	origin = plugin.Zones(names).Matches(qname)
	if origin == "" {
		return "", ""
	}
	return origin, zones[origin].kind
}

// Info is a listing row for the HTTP API.
type Info struct {
	Origin string
	Kind   string
	Source string
	Path   string
}

// All returns a snapshot of registered zones.
func All() []Info {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Info, 0, len(names))
	for _, n := range names {
		e := zones[n]
		info := Info{Origin: n, Kind: e.kind}
		if e.pri != nil {
			info.Source = e.pri.Source()
			info.Path = e.pri.Path()
		}
		if e.sec != nil {
			info.Source = e.sec.Source()
			info.Path = e.sec.Path()
		}
		out = append(out, info)
	}
	return out
}

// ResetForTest clears the registry. Tests only.
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	zones = map[string]entry{}
	names = nil
}

func canonical(origin string) string {
	if origin == "" {
		return ""
	}
	return strings.ToLower(dns.CanonicalName(origin))
}
