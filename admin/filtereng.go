package admin

import (
	"sync"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
)

type filterEngine struct {
	mu sync.RWMutex
	// selfAndKids: pattern matches the name and every descendant.
	allowSelf map[string]struct{}
	blockSelf map[string]struct{}
	// kidsOnly: pattern matches descendants only (*.example.com).
	allowKids map[string]struct{}
	blockKids map[string]struct{}
}

func newFilterEngine() *filterEngine {
	return &filterEngine{
		allowSelf: map[string]struct{}{},
		blockSelf: map[string]struct{}{},
		allowKids: map[string]struct{}{},
		blockKids: map[string]struct{}{},
	}
}

func (e *filterEngine) replace(rules []store.FilterRule) {
	allowSelf := map[string]struct{}{}
	blockSelf := map[string]struct{}{}
	allowKids := map[string]struct{}{}
	blockKids := map[string]struct{}{}
	for _, r := range rules {
		pat := r.Pattern
		if r.KidsOnly {
			if r.Action == store.FilterAllow {
				allowKids[pat] = struct{}{}
			} else {
				blockKids[pat] = struct{}{}
			}
			continue
		}
		if r.Action == store.FilterAllow {
			allowSelf[pat] = struct{}{}
		} else {
			blockSelf[pat] = struct{}{}
		}
	}
	e.mu.Lock()
	e.allowSelf, e.allowKids = allowSelf, allowKids
	e.blockSelf, e.blockKids = blockSelf, blockKids
	e.mu.Unlock()
}

func (e *filterEngine) empty() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.allowSelf)+len(e.allowKids)+len(e.blockSelf)+len(e.blockKids) == 0
}

// blocked reports whether qname should be NXDOMAIN. Allow wins.
func (e *filterEngine) blocked(qname string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.blockSelf)+len(e.blockKids) == 0 {
		return false
	}
	name := stringsToCanon(qname)
	if filterHit(name, e.allowSelf, e.allowKids) {
		return false
	}
	return filterHit(name, e.blockSelf, e.blockKids)
}

func stringsToCanon(qname string) string {
	return stringsLowerCanon(qname)
}

func stringsLowerCanon(qname string) string {
	return dns.CanonicalName(qname)
}

func filterHit(name string, self, kids map[string]struct{}) bool {
	if _, ok := self[name]; ok {
		return true
	}
	for rest := parentName(name); rest != "" && rest != "."; rest = parentName(rest) {
		if _, ok := self[rest]; ok {
			return true
		}
		if _, ok := kids[rest]; ok {
			return true
		}
	}
	return false
}

func parentName(name string) string {
	off, end := dns.NextLabel(name, 0)
	if end || off <= 0 || off >= len(name) {
		return ""
	}
	return name[off:]
}
