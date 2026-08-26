package dnsupdatepersist

import (
	"github.com/coredns/coredns/plugin"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const subsystem = "dns_update_persistent"

var (
	updateCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: subsystem,
		Name:      "updates_total",
		Help:      "Counter of RFC 2136 UPDATE replies.",
	}, []string{"zone", "rcode"})

	writeCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: subsystem,
		Name:      "writes_total",
		Help:      "Counter of atomic persist writes.",
	}, []string{"zone", "status"})

	writeDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace:                   plugin.Namespace,
		Subsystem:                   subsystem,
		Name:                        "write_duration_seconds",
		Help:                        "Histogram of persist write+rename duration.",
		Buckets:                     plugin.TimeBuckets,
		NativeHistogramBucketFactor: plugin.NativeHistogramBucketFactor,
	}, []string{"zone"})

	serialGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: subsystem,
		Name:      "serial",
		Help:      "SOA serial currently served.",
	}, []string{"zone"})
)
