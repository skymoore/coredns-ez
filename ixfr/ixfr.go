// Package ixfr implements RFC 1995 incremental zone transfer as a CoreDNS
// plugin. In-tree file/auto accept IXFR queries but always dump the whole zone
// when the serial has moved; this plugin keeps a journal of increments and
// answers IXFR with a real delta stream.
//
// dns-update-persistent commits each mutating UPDATE into the journal. This
// plugin is the Transferer for the origin so the transfer plugin serves those
// deltas instead of file.Zone's AXFR fallback.
package ixfr

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/transfer"

	"github.com/miekg/dns"
)

var log = clog.NewWithPlugin(pluginName)

const pluginName = "ixfr"

const defaultHistory = 64

// JournalBackend stores the journal text. The admin plugin uses SQLite.
type JournalBackend interface {
	Load(origin string) ([]byte, error)
	Save(origin string, data []byte) error
}

// New returns an unregistered journal for origin. The API plugin uses this
// for runtime primaries; Corefile setup still goes through parse().
func New(origin string, history int) *IXFR {
	if history < 1 {
		history = defaultHistory
	}
	return &IXFR{
		Zone:    strings.ToLower(dns.CanonicalName(origin)),
		history: history,
	}
}

// IXFR is the plugin. It does not answer queries; ServeDNS falls through.
type IXFR struct {
	Next plugin.Handler

	// Zone is the origin this journal belongs to. Empty until Register.
	Zone string

	backend JournalBackend

	history int

	mu      sync.Mutex
	journal *Journal
}

// SetBackend stores the journal in SQLite (or any blob store) instead of a file.
func (x *IXFR) SetBackend(b JournalBackend) { x.backend = b }

// ServeDNS implements plugin.Handler. Queries are not ours.
func (x *IXFR) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	return plugin.NextOrFailure(x.Name(), x.Next, ctx, w, r)
}

// Name implements plugin.Handler.
func (x *IXFR) Name() string { return pluginName }

// Transfer implements transfer.Transferer.
func (x *IXFR) Transfer(zone string, serial uint32) (<-chan []dns.RR, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.journal == nil || zone != x.Zone {
		return nil, transfer.ErrNotAuthoritative
	}

	kind, rrs := x.journal.answer(serial)
	transferCount.WithLabelValues(x.Zone, kind).Inc()

	ch := make(chan []dns.RR)
	go func() {
		if len(rrs) > 0 {
			ch <- copyRRs(rrs)
		}
		close(ch)
	}()
	return ch, nil
}

// Register attaches this journal to origin and reconciles it against the
// currently served zone. Safe to call from OnStartup.
func (x *IXFR) Register(origin string, current []dns.RR) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	origin = canonical(origin)
	if x.Zone != "" && x.Zone != origin {
		return fmt.Errorf("ixfr already registered for %s, not %s", x.Zone, origin)
	}
	x.Zone = origin
	if x.history <= 0 {
		x.history = defaultHistory
	}

	j, err := loadJournal(origin, x.history, current, x.backend)
	if err != nil {
		log.Warningf("loading journal for %s: %v (serving without IXFR history)", origin, err)
		j = newJournal(origin, x.history, current)
		j.backend = x.backend
	}
	x.journal = j
	if soa := soaOf(j.current); soa != nil {
		serialGauge.WithLabelValues(origin).Set(float64(soa.Serial))
	}
	log.Infof("IXFR journal for %s (%d increments, serial %d)", origin, len(j.incs), serialOf(j.current))
	return nil
}

// Commit records the diff from old to new, persists the journal, and updates
// the in-memory snapshot. The caller holds the zone lock.
func (x *IXFR) Commit(old, new []dns.RR) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.journal == nil {
		return fmt.Errorf("ixfr: Commit before Register")
	}
	if err := x.journal.commit(old, new); err != nil {
		return err
	}
	if soa := soaOf(x.journal.current); soa != nil {
		serialGauge.WithLabelValues(x.Zone).Set(float64(soa.Serial))
	}
	return nil
}
