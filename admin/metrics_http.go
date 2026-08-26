package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type histBucket struct {
	Le    float64 `json:"le"`
	Count uint64  `json:"count"`
}

type metricPoint struct {
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels,omitempty"`
	Type    string            `json:"type"`
	Value   float64           `json:"value,omitempty"`
	Count   uint64            `json:"count,omitempty"`
	Sum     float64           `json:"sum,omitempty"`
	Buckets []histBucket      `json:"buckets,omitempty"`
}

func keepMetric(name string) bool {
	switch {
	case name == "coredns_dns_requests_total",
		name == "coredns_dns_responses_total",
		name == "coredns_dns_request_duration_seconds",
		strings.HasPrefix(name, "coredns_admin_"),
		strings.HasPrefix(name, "coredns_dns_update_persistent_"),
		strings.HasPrefix(name, "coredns_ixfr_"),
		strings.HasPrefix(name, "coredns_secondary_persistent_"):
		return true
	default:
		return false
	}
}

func (a *Admin) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	series := make([]metricPoint, 0, 64)
	for _, mf := range mfs {
		name := mf.GetName()
		if !keepMetric(name) {
			continue
		}
		kind := strings.ToLower(mf.GetType().String())
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			pt := metricPoint{Name: name, Labels: labels, Type: kind}
			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				pt.Value = m.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				pt.Value = m.GetGauge().GetValue()
			case dto.MetricType_HISTOGRAM:
				h := m.GetHistogram()
				pt.Count = h.GetSampleCount()
				pt.Sum = h.GetSampleSum()
				for _, b := range h.GetBucket() {
					pt.Buckets = append(pt.Buckets, histBucket{Le: b.GetUpperBound(), Count: b.GetCumulativeCount()})
				}
			case dto.MetricType_SUMMARY:
				s := m.GetSummary()
				pt.Count = s.GetSampleCount()
				pt.Sum = s.GetSampleSum()
			default:
				continue
			}
			series = append(series, pt)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scraped_at": time.Now().Unix(),
		"series":     series,
	})
}
