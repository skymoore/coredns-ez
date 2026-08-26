package secondarypersist

import (
	"github.com/coredns/coredns/plugin"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const subsystem = "secondary_persistent"

var (
	loadCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: subsystem,
		Name:      "load_total",
		Help:      "Counter of persist-file loads at startup or catalog member attach.",
	}, []string{"zone", "status"})

	transferCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: subsystem,
		Name:      "transfer_total",
		Help:      "Counter of inbound zone transfers.",
	}, []string{"zone", "type", "status"})

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
		Help:      "SOA serial of the last successfully persisted zone.",
	}, []string{"zone"})
)
