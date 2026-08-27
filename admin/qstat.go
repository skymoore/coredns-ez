package admin

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
)

const (
	qstatName      = "qstat"
	queryBucket    = 10 * time.Second
	queryMemHold   = 15 * time.Minute
	queryRetain    = 7 * 24 * time.Hour
	queryRecentCap = 250
	queryWindowCap = 30000
	queryTopN      = 15
	queryNameCap   = 20
)

type queryEvent struct {
	At      time.Time
	Name    string
	Type    string
	Rcode   string
	Client  string
	Blocked bool
	Ms      float64
}

type queryHub struct {
	mu       sync.Mutex
	recent   []queryEvent
	window   []queryEvent
	total    uint64
	blocked  uint64
	nx       uint64
	servfail uint64
}

var queries = &queryHub{}

func (h *queryHub) resetForTest() {
	h.mu.Lock()
	h.recent = nil
	h.window = nil
	h.total, h.blocked, h.nx, h.servfail = 0, 0, 0, 0
	h.mu.Unlock()
}

func setupQstat(c *caddy.Controller) error {
	if !c.Next() {
		return c.ArgErr()
	}
	if c.NextArg() {
		return c.ArgErr()
	}
	for c.NextBlock() {
		return c.Errf("unknown property '%s'", c.Val())
	}
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return &queryTap{Next: next}
	})
	return nil
}

type queryTap struct {
	Next plugin.Handler
}

func (q *queryTap) Name() string { return qstatName }

func (q *queryTap) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	if r == nil || r.Opcode != dns.OpcodeQuery || len(r.Question) == 0 {
		return plugin.NextOrFailure(qstatName, q.Next, ctx, w, r)
	}
	start := time.Now()
	tw := &rcodeRW{ResponseWriter: w, rcode: dns.RcodeServerFailure}
	code, err := plugin.NextOrFailure(qstatName, q.Next, ctx, tw, r)
	state := request.Request{W: w, Req: r}
	rcode := tw.rcode
	if code != 0 {
		rcode = code
	}
	name := strings.ToLower(dns.CanonicalName(state.Name()))
	ev := queryEvent{
		At:     start,
		Name:   name,
		Type:   dns.TypeToString[state.QType()],
		Rcode:  dns.RcodeToString[rcode],
		Client: state.IP(),
		Ms:     float64(time.Since(start).Microseconds()) / 1000,
	}
	if ev.Type == "" {
		ev.Type = "TYPE" + strconv.Itoa(int(state.QType()))
	}
	if ev.Rcode == "" {
		ev.Rcode = "SERVFAIL"
	}
	if inst := instance; inst != nil && inst.filters != nil {
		ev.Blocked = inst.filters.blocked(name)
	}
	queries.record(ev)
	return code, err
}

type rcodeRW struct {
	dns.ResponseWriter
	rcode int
}

func (r *rcodeRW) WriteMsg(m *dns.Msg) error {
	if m != nil {
		r.rcode = m.Rcode
	}
	return r.ResponseWriter.WriteMsg(m)
}

func (h *queryHub) record(ev queryEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total++
	if ev.Blocked {
		h.blocked++
	}
	if ev.Rcode == "NXDOMAIN" {
		h.nx++
	}
	if ev.Rcode == "SERVFAIL" {
		h.servfail++
	}
	h.recent = append(h.recent, ev)
	if len(h.recent) > queryRecentCap {
		h.recent = h.recent[len(h.recent)-queryRecentCap:]
	}
	h.window = append(h.window, ev)
	h.trimLocked(ev.At)
}

type queryCountJSON struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type queryEventJSON struct {
	At      int64   `json:"at"`
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Rcode   string  `json:"rcode"`
	Client  string  `json:"client"`
	Blocked bool    `json:"blocked"`
	Ms      float64 `json:"ms"`
}

type querySeriesPoint struct {
	T        int64          `json:"t"`
	Queries  int            `json:"queries"`
	Blocked  int            `json:"blocked"`
	Nxdomain int            `json:"nxdomain"`
	Servfail int            `json:"servfail"`
	Types    map[string]int `json:"types"`
}

type queryStatsJSON struct {
	GeneratedAt   int64              `json:"generated_at"`
	Range         string             `json:"range"`
	RangeSeconds  int                `json:"range_seconds"`
	StepSeconds   int                `json:"step_seconds"`
	WindowSeconds int                `json:"window_seconds"`
	QPS           float64            `json:"qps"`
	Total         uint64             `json:"total"`
	Blocked       uint64             `json:"blocked"`
	NXDomain      uint64             `json:"nxdomain"`
	Servfail      uint64             `json:"servfail"`
	RangeQueries  int                `json:"range_queries"`
	RangeBlocked  int                `json:"range_blocked"`
	RangeNxdomain int                `json:"range_nxdomain"`
	RangeServfail int                `json:"range_servfail"`
	WindowQueries int                `json:"window_queries"`
	WindowBlocked int                `json:"window_blocked"`
	ByType        []queryCountJSON   `json:"by_type"`
	ByRcode       []queryCountJSON   `json:"by_rcode"`
	TopNames      []queryCountJSON   `json:"top_names"`
	TopBlocked    []queryCountJSON   `json:"top_blocked"`
	Recent        []queryEventJSON   `json:"recent"`
	Series        []querySeriesPoint `json:"series"`
}

func (a *Admin) handleQueries(w http.ResponseWriter, r *http.Request) {
	rng := parseQueryRange(r.URL.Query().Get("range"))
	var stored []store.QueryBucket
	if a.db != nil {
		from := time.Now().Add(-rng.Duration).Unix()
		rows, err := a.db.ListQueryBuckets(from, time.Now().Unix())
		if err != nil {
			log.Warningf("query buckets: %v", err)
		} else {
			stored = rows
		}
	}
	writeJSON(w, http.StatusOK, queries.snapshotRange(stored, rng))
}

func topCounts(m map[string]int, n int) []queryCountJSON {
	out := make([]queryCountJSON, 0, len(m))
	for k, v := range m {
		out = append(out, queryCountJSON{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
