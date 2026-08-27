package admin

import (
	"encoding/json"
	"time"

	"github.com/skymoore/coredns-ez/admin/store"
)

type queryRange struct {
	Label    string
	Duration time.Duration
	Step     time.Duration
}

func parseQueryRange(s string) queryRange {
	switch s {
	case "5m":
		return queryRange{Label: "5m", Duration: 5 * time.Minute, Step: 10 * time.Second}
	case "15m":
		return queryRange{Label: "15m", Duration: 15 * time.Minute, Step: 10 * time.Second}
	case "6h":
		return queryRange{Label: "6h", Duration: 6 * time.Hour, Step: time.Minute}
	case "24h":
		return queryRange{Label: "24h", Duration: 24 * time.Hour, Step: 5 * time.Minute}
	case "7d":
		return queryRange{Label: "7d", Duration: 7 * 24 * time.Hour, Step: time.Hour}
	default:
		return queryRange{Label: "1h", Duration: time.Hour, Step: 30 * time.Second}
	}
}

type bucketAgg struct {
	Ts       int64
	Queries  int
	Blocked  int
	Nxdomain int
	Servfail int
	Types    map[string]int
	Rcodes   map[string]int
	Names    map[string]int
	Blocks   map[string]int
}

func (h *queryHub) snapshot() queryStatsJSON {
	return h.snapshotRange(nil, parseQueryRange("5m"))
}

func (h *queryHub) trimLocked(now time.Time) {
	cut := now.Add(-queryMemHold)
	i := 0
	for i < len(h.window) && h.window[i].At.Before(cut) {
		i++
	}
	if i > 0 {
		h.window = h.window[i:]
	}
	if len(h.window) > queryWindowCap {
		h.window = h.window[len(h.window)-queryWindowCap:]
	}
}

func (h *queryHub) snapshotRange(stored []store.QueryBucket, rng queryRange) queryStatsJSON {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	from := now.Add(-rng.Duration)
	merged := map[int64]*bucketAgg{}
	for _, row := range stored {
		if row.Ts < from.Unix() {
			continue
		}
		merged[row.Ts] = bucketFromRow(row)
	}
	cur := alignUnix(now, queryBucket)
	for _, ev := range h.window {
		if ev.At.Before(from) {
			continue
		}
		ts := alignUnix(ev.At, queryBucket)
		if _, ok := merged[ts]; ok && ts < cur {
			continue
		}
		b := merged[ts]
		if b == nil {
			b = newBucket(ts)
			merged[ts] = b
		}
		addEvent(b, ev)
	}

	byType := map[string]int{}
	byRcode := map[string]int{}
	byName := map[string]int{}
	byBlock := map[string]int{}
	rangeQ, rangeB, rangeNX, rangeSF := 0, 0, 0, 0
	for _, b := range merged {
		rangeQ += b.Queries
		rangeB += b.Blocked
		rangeNX += b.Nxdomain
		rangeSF += b.Servfail
		for k, v := range b.Types {
			byType[k] += v
		}
		for k, v := range b.Rcodes {
			byRcode[k] += v
		}
		for k, v := range b.Names {
			byName[k] += v
		}
		for k, v := range b.Blocks {
			byBlock[k] += v
		}
	}

	span := rng.Duration.Seconds()
	if span < 1 {
		span = 1
	}
	out := queryStatsJSON{
		GeneratedAt:   now.Unix(),
		Range:         rng.Label,
		RangeSeconds:  int(rng.Duration.Seconds()),
		StepSeconds:   int(rng.Step.Seconds()),
		WindowSeconds: int(rng.Duration.Seconds()),
		Total:         h.total,
		Blocked:       h.blocked,
		NXDomain:      h.nx,
		Servfail:      h.servfail,
		RangeQueries:  rangeQ,
		RangeBlocked:  rangeB,
		RangeNxdomain: rangeNX,
		RangeServfail: rangeSF,
		WindowQueries: rangeQ,
		WindowBlocked: rangeB,
		ByType:        topCounts(byType, queryTopN),
		ByRcode:       topCounts(byRcode, queryTopN),
		TopNames:      topCounts(byName, queryTopN),
		TopBlocked:    topCounts(byBlock, queryTopN),
		Recent:        make([]queryEventJSON, 0, len(h.recent)),
		Series:        rollupSeries(merged, from, now, rng.Step),
	}
	out.QPS = float64(rangeQ) / span
	for n := len(h.recent) - 1; n >= 0; n-- {
		ev := h.recent[n]
		out.Recent = append(out.Recent, queryEventJSON{
			At: ev.At.Unix(), Name: ev.Name, Type: ev.Type, Rcode: ev.Rcode,
			Client: ev.Client, Blocked: ev.Blocked, Ms: ev.Ms,
		})
	}
	return out
}

func (h *queryHub) completedBuckets(now time.Time) []store.QueryBucket {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := alignUnix(now, queryBucket)
	agg := map[int64]*bucketAgg{}
	for _, ev := range h.window {
		ts := alignUnix(ev.At, queryBucket)
		if ts >= cur {
			continue
		}
		b := agg[ts]
		if b == nil {
			b = newBucket(ts)
			agg[ts] = b
		}
		addEvent(b, ev)
	}
	out := make([]store.QueryBucket, 0, len(agg))
	for _, b := range agg {
		out = append(out, b.toRow())
	}
	return out
}

func (a *Admin) queryFlushLoop() {
	t := time.NewTicker(queryBucket)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.flushQueryStats()
		}
	}
}

func (a *Admin) flushQueryStats() {
	if a.db == nil {
		return
	}
	now := time.Now()
	for _, b := range queries.completedBuckets(now) {
		if err := a.db.ReplaceQueryBucket(b); err != nil {
			log.Warningf("query stats: %v", err)
		}
	}
	if err := a.db.PruneQueryBuckets(now.Add(-queryRetain).Unix()); err != nil {
		log.Warningf("query stats prune: %v", err)
	}
}

func newBucket(ts int64) *bucketAgg {
	return &bucketAgg{
		Ts:     ts,
		Types:  map[string]int{},
		Rcodes: map[string]int{},
		Names:  map[string]int{},
		Blocks: map[string]int{},
	}
}

func addEvent(b *bucketAgg, ev queryEvent) {
	b.Queries++
	b.Types[ev.Type]++
	b.Rcodes[ev.Rcode]++
	b.Names[ev.Name]++
	if ev.Blocked {
		b.Blocked++
		b.Blocks[ev.Name]++
	}
	if ev.Rcode == "NXDOMAIN" {
		b.Nxdomain++
	}
	if ev.Rcode == "SERVFAIL" {
		b.Servfail++
	}
}

func bucketFromRow(row store.QueryBucket) *bucketAgg {
	return &bucketAgg{
		Ts:       row.Ts,
		Queries:  row.Queries,
		Blocked:  row.Blocked,
		Nxdomain: row.Nxdomain,
		Servfail: row.Servfail,
		Types:    unmarshalCounts(row.Types),
		Rcodes:   unmarshalCounts(row.Rcodes),
		Names:    unmarshalCounts(row.Names),
		Blocks:   unmarshalCounts(row.Blocks),
	}
}

func (b *bucketAgg) toRow() store.QueryBucket {
	return store.QueryBucket{
		Ts:       b.Ts,
		Queries:  b.Queries,
		Blocked:  b.Blocked,
		Nxdomain: b.Nxdomain,
		Servfail: b.Servfail,
		Types:    marshalCounts(clampCounts(b.Types, 0)),
		Rcodes:   marshalCounts(b.Rcodes),
		Names:    marshalCounts(clampCounts(b.Names, queryNameCap)),
		Blocks:   marshalCounts(clampCounts(b.Blocks, queryNameCap)),
	}
}

func alignUnix(t time.Time, step time.Duration) int64 {
	n := int64(step.Seconds())
	if n <= 0 {
		return t.Unix()
	}
	return t.Unix() / n * n
}

func rollupSeries(merged map[int64]*bucketAgg, from, now time.Time, step time.Duration) []querySeriesPoint {
	if step <= 0 {
		step = queryBucket
	}
	start := alignUnix(from, step)
	end := alignUnix(now, step)
	if end < start {
		return nil
	}
	rolled := map[int64]*bucketAgg{}
	for _, b := range merged {
		ts := b.Ts / int64(step.Seconds()) * int64(step.Seconds())
		d := rolled[ts]
		if d == nil {
			d = newBucket(ts)
			rolled[ts] = d
		}
		d.Queries += b.Queries
		d.Blocked += b.Blocked
		d.Nxdomain += b.Nxdomain
		d.Servfail += b.Servfail
		for k, v := range b.Types {
			d.Types[k] += v
		}
	}
	n := int((end-start)/int64(step.Seconds())) + 1
	if n < 1 {
		n = 1
	}
	if n > 2000 {
		n = 2000
	}
	out := make([]querySeriesPoint, 0, n)
	for ts := start; ts <= end && len(out) < 2000; ts += int64(step.Seconds()) {
		b := rolled[ts]
		pt := querySeriesPoint{T: ts, Types: map[string]int{}}
		if b != nil {
			pt.Queries = b.Queries
			pt.Blocked = b.Blocked
			pt.Nxdomain = b.Nxdomain
			pt.Servfail = b.Servfail
			pt.Types = b.Types
		}
		out = append(out, pt)
	}
	return out
}

func marshalCounts(m map[string]int) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func unmarshalCounts(s string) map[string]int {
	out := map[string]int{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	if out == nil {
		return map[string]int{}
	}
	return out
}

func clampCounts(m map[string]int, n int) map[string]int {
	if n <= 0 || len(m) <= n {
		return m
	}
	top := topCounts(m, n)
	out := make(map[string]int, len(top))
	for _, c := range top {
		out[c.Name] = c.Count
	}
	return out
}
