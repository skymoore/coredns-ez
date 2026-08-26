// Package dnsupdatepersist implements RFC 2136 Dynamic Updates for one zone
// held in memory and rewritten in place to its master file after every
// mutating UPDATE.
//
// Protocol behaviour is copied from the out-of-tree dynupdate plugin; this
// package adds durability. It exists so an ACME client's RFC 2136 DNS-01
// solver can publish records that survive a CoreDNS restart.
//
// # Why this owns the zone rather than overlaying one
//
// The obvious shape — hold dynamically-added records in a side table and fall
// through to `file` for everything else — is quietly wrong. CoreDNS's
// `transfer` plugin takes the FIRST Transferer that does not return
// ErrNotAuthoritative; it does not merge. A side table would therefore be
// invisible to AXFR, so a TXT record added by an ACME client would never
// reach the secondary that the public NS records actually point at.
//
// So this plugin owns the zone. Reads, wildcards, delegation, NODATA proofs
// and AXFR all come from CoreDNS's own file.Zone.
//
// # How a change is applied
//
// The zone is kept as a flat []dns.RR. An UPDATE is applied to a COPY of that
// slice, and only if every prerequisite passed, the whole update section
// prescanned clean, and the resulting master file was atomically replaced is
// a fresh file.Zone built and swapped in under a write lock. RFC 2136
// §3.4.2.1 requires the update be atomic — a reader must never observe half
// of it — and rebuilding is the cheapest way to get that without
// reimplementing the tree's delete semantics.
//
// The on-disk file is the same path as the seed (`file PATH`). Adds appear
// there; deletes disappear. A no-op UPDATE does not rewrite the file.
//
// Rebuilding is O(zone) per update. That is a deliberate trade: this plugin
// exists for ACME challenges and similar low-rate mutation, where correctness
// and atomicity are worth far more than update throughput.
package dnsupdatepersist

import (
	"context"
	"sync"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/file"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/skymoore/coredns-plugins/ixfr"

	"github.com/miekg/dns"
)

var log = clog.NewWithPlugin(pluginName)

const pluginName = "dns-update-persistent"

// UpdatePersist serves one zone, accepts RFC 2136 UPDATE messages for it, and
// rewrites the zone's master file after every mutating update.
type UpdatePersist struct {
	Next plugin.Handler

	// Zone is the canonical origin, always fully qualified and lower case.
	Zone string

	// Xfer, when the `transfer` plugin is configured in the same server
	// block, is used to send a NOTIFY after a change. Without it a secondary
	// only picks the change up at its next refresh, which for an ACME
	// challenge is indistinguishable from the update never happening.
	Xfer *transfer.Transfer

	// ixfr, when the `ixfr` plugin is in the same server block, is the
	// Transferer for this origin. This plugin then returns ErrNotAuthoritative
	// from Transfer so first-Transferer-wins lands on the journal.
	ixfr *ixfr.IXFR

	// mutable, when non-nil, is the set of RR types this plugin will let an
	// UPDATE touch. nil means "no type policy" — RFC 2136's own rules still
	// apply. The point of an allowlist is that an UPDATE key which only needs
	// to publish TXT challenges should not also be able to repoint an A
	// record, and TSIG alone cannot express that.
	mutable map[uint16]bool

	// seedPath is both the startup load and the persist destination.
	seedPath string

	// source is zonereg.SourceAPI or zonereg.SourceCorefile. Empty means corefile.
	source string

	mu   sync.RWMutex
	rrs  []dns.RR   // authoritative content; the source of truth
	view *file.File // rebuilt from rrs on every change, serves reads and AXFR
}

// ServeDNS routes UPDATE to the RFC 2136 machinery and everything else to the
// file view.
//
// CoreDNS's own server does not filter on opcode: it requires a question
// section, which an UPDATE's Zone section occupies, and a ClassINET qclass,
// which ZCLASS is. So an UPDATE reaches the plugin chain like any query, and a
// plugin that does not expect one will treat the Zone section as a question and
// answer nonsense.
func (d *UpdatePersist) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	if r.Opcode == dns.OpcodeUpdate {
		return d.serveUpdate(w, r)
	}

	d.mu.RLock()
	view := d.view
	d.mu.RUnlock()

	return view.ServeDNS(ctx, w, r)
}

// Transfer implements transfer.Transferer so AXFR of this zone includes
// whatever the last UPDATE left behind. Without it the records this plugin
// exists to publish would be invisible to every secondary.
func (d *UpdatePersist) Transfer(zone string, serial uint32) (<-chan []dns.RR, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.ixfr != nil {
		return nil, transfer.ErrNotAuthoritative
	}
	view := d.view
	return view.Transfer(zone, serial)
}

// Name implements plugin.Handler.
func (d *UpdatePersist) Name() string { return pluginName }

// build turns a flat record slice into the servable view. The returned view's
// Next is d.Next, so a name outside this zone still falls through the chain.
func (d *UpdatePersist) build(rrs []dns.RR) (*file.File, error) {
	z := file.NewZone(d.Zone, d.seedPath)
	for _, rr := range rrs {
		// Insert mutates the RR it is given (it lower-cases owner and target
		// names), so hand it a copy — d.rrs is shared with readers of the
		// previous view until the swap completes.
		if err := z.Insert(dns.Copy(rr)); err != nil {
			return nil, err
		}
	}

	return &file.File{
		Next: d.Next,
		Zones: file.Zones{
			Z:     map[string]*file.Zone{d.Zone: z},
			Names: []string{d.Zone},
		},
	}, nil
}

// swap installs a new record set. The caller must hold d.mu for writing.
func (d *UpdatePersist) swap(rrs []dns.RR) error {
	view, err := d.build(rrs)
	if err != nil {
		return err
	}
	d.rrs, d.view = rrs, view
	return nil
}
