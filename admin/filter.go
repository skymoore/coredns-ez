package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
)

func (a *Admin) handleGetFilters(w http.ResponseWriter, _ *http.Request) {
	manual, err := a.db.ListFilterRules("", store.FilterSourceManual)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	feeds, err := a.db.ListFilterFeeds()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	counts, err := a.db.CountFilterRules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"manual": manual,
		"feeds":  feeds,
		"counts": counts,
	})
}

func (a *Admin) handleCreateFilterRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"`
		Pattern string `json:"pattern"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	action, err := store.NormalizeFilterAction(body.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := parseFilterPattern(body.Pattern)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := a.db.InsertFilterRule(store.FilterRule{
		Action: action, Pattern: p.Pattern, KidsOnly: p.KidsOnly, Source: store.FilterSourceManual,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.publishFilter()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "filter.rule.create", "", rule.Display())
	writeJSON(w, http.StatusCreated, rule)
}

func (a *Admin) handleDeleteFilterRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rule, err := a.db.GetFilterRule(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if rule.Source != store.FilterSourceManual {
		writeError(w, http.StatusBadRequest, "delete the list instead of a synced entry")
		return
	}
	if err := a.db.DeleteFilterRule(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.publishFilter()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "filter.rule.delete", "", rule.Display())
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) handleCreateFilterFeed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name            string `json:"name"`
		Action          string `json:"action"`
		URL             string `json:"url"`
		Sync            string `json:"sync"`
		IntervalSeconds int    `json:"interval_seconds"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	action, err := store.NormalizeFilterAction(body.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	once := strings.EqualFold(strings.TrimSpace(body.Sync), "once")
	syncMode := store.FilterSyncPeriodic
	if once {
		syncMode = store.FilterSyncOff
	} else if body.Sync != "" {
		syncMode, err = store.NormalizeFilterSync(body.Sync)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	rawURL, err := validateFilterURL(body.URL, a.filterAllowLocal)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feed, err := a.db.InsertFilterFeed(store.FilterFeed{
		Name:            feedNameFromURL(rawURL, body.Name),
		Action:          action,
		URL:             rawURL,
		Sync:            syncMode,
		IntervalSeconds: body.IntervalSeconds,
	})
	if err == store.ErrFilterFeedExists {
		if feed.LastCount == 0 {
			go a.runFilterSync(feed.ID)
		}
		writeError(w, http.StatusConflict, "list url already present")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.db.Audit(actorFrom(r).Username, "filter.feed.create", "", feed.URL)
	writeJSON(w, http.StatusCreated, feed)
	go a.runFilterSync(feed.ID)
}

func (a *Admin) handlePatchFilterFeed(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	feed, err := a.db.GetFilterFeed(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var body struct {
		Name            *string `json:"name"`
		Sync            *string `json:"sync"`
		IntervalSeconds *int    `json:"interval_seconds"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Name != nil {
		n := strings.TrimSpace(*body.Name)
		if n != "" {
			feed.Name = n
		}
	}
	if body.Sync != nil {
		syncMode, err := store.NormalizeFilterSync(*body.Sync)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		feed.Sync = syncMode
	}
	if body.IntervalSeconds != nil {
		feed.IntervalSeconds = *body.IntervalSeconds
	}
	if err := a.db.UpdateFilterFeed(feed); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	writeJSON(w, http.StatusOK, feed)
}

func (a *Admin) handleSyncFilterFeed(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := a.db.GetFilterFeed(id); err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	go a.runFilterSync(id)
	feed, _ := a.db.GetFilterFeed(id)
	a.db.Audit(actorFrom(r).Username, "filter.feed.sync", "", feed.URL)
	writeJSON(w, http.StatusAccepted, feed)
}

func (a *Admin) handleDeleteFilterFeed(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	feed, err := a.db.GetFilterFeed(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err := a.db.DeleteFilterFeed(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.publishFilter()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "filter.feed.delete", "", feed.URL)
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) publishFilter() {
	if a.filters == nil {
		a.filters = newFilterEngine()
	}
	rules, err := a.db.ListCompiledFilterRules()
	if err != nil {
		log.Warningf("filter load: %v", err)
		return
	}
	a.filters.replace(rules)
	filterRuleGauge.WithLabelValues(store.FilterAllow).Set(0)
	filterRuleGauge.WithLabelValues(store.FilterBlock).Set(0)
	counts, err := a.db.CountFilterRules()
	if err == nil {
		for action, n := range counts {
			filterRuleGauge.WithLabelValues(action).Set(float64(n))
		}
	}
}

func (a *Admin) filterLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.syncDueFeeds()
		}
	}
}

func (a *Admin) syncDueFeeds() {
	feeds, err := a.db.ListFilterFeeds()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	changed := false
	for _, f := range feeds {
		if f.Sync != store.FilterSyncPeriodic {
			continue
		}
		due := f.LastSyncAt == nil || now-*f.LastSyncAt >= int64(store.ClampFilterInterval(f.IntervalSeconds))
		if !due {
			continue
		}
		if err := a.syncFilterFeed(f.ID); err != nil {
			log.Warningf("filter feed %s: %v", f.URL, err)
			continue
		}
		changed = true
	}
	if changed {
		a.publishFilter()
		_, _ = a.db.BumpGeneration()
		go a.pushSnapshot()
	}
}

func (a *Admin) runFilterSync(id string) {
	if err := a.syncFilterFeed(id); err != nil {
		log.Warningf("filter feed %s: %v", id, err)
		return
	}
	a.publishFilter()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
}

func (a *Admin) syncFilterFeed(id string) error {
	a.filterSyncMu.Lock()
	if a.filterSyncing == nil {
		a.filterSyncing = map[string]struct{}{}
	}
	if _, busy := a.filterSyncing[id]; busy {
		a.filterSyncMu.Unlock()
		return nil
	}
	a.filterSyncing[id] = struct{}{}
	a.filterSyncMu.Unlock()
	defer func() {
		a.filterSyncMu.Lock()
		delete(a.filterSyncing, id)
		a.filterSyncMu.Unlock()
	}()

	feed, err := a.db.GetFilterFeed(id)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, feed.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "coredns-admin-filter")
	if feed.ETag != "" {
		req.Header.Set("If-None-Match", feed.ETag)
	}
	resp, err := a.filterClient().Do(req)
	if err != nil {
		a.markFeedError(feed, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		now := time.Now().Unix()
		feed.LastSyncAt = &now
		feed.LastError = ""
		return a.db.UpdateFilterFeed(feed)
	}
	if resp.StatusCode >= 300 {
		err = fmt.Errorf("list fetch status %d", resp.StatusCode)
		a.markFeedError(feed, err)
		return err
	}
	body := io.LimitReader(resp.Body, filterMaxBody+1)
	parsed := parseFilterList(body)
	if err := a.db.ReplaceFeedRules(feed.ID, rulesFromParsed(feed.Action, feed.ID, parsed)); err != nil {
		a.markFeedError(feed, err)
		return err
	}
	now := time.Now().Unix()
	feed.LastSyncAt = &now
	feed.LastError = ""
	feed.LastCount = len(parsed)
	feed.ETag = resp.Header.Get("ETag")
	return a.db.UpdateFilterFeed(feed)
}

func (a *Admin) markFeedError(feed store.FilterFeed, err error) {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	feed.LastError = msg
	_ = a.db.UpdateFilterFeed(feed)
}

func (a *Admin) filterClient() *http.Client {
	if a.filterHTTP != nil {
		return a.filterHTTP
	}
	return &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			_, err := validateFilterURL(req.URL.String(), a.filterAllowLocal)
			return err
		},
	}
}

func writeFilterBlock(w dns.ResponseWriter, r *dns.Msg) (int, error) {
	m := new(dns.Msg)
	m.SetRcode(r, dns.RcodeNameError)
	if err := w.WriteMsg(m); err != nil {
		return dns.RcodeServerFailure, err
	}
	filterHitCount.WithLabelValues(store.FilterBlock).Inc()
	return dns.RcodeNameError, nil
}
