package ixfr

import (
	"github.com/coredns/coredns/plugin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	transferCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "transfer_total",
		Help:      "Outbound zone transfers served by the ixfr plugin.",
	}, []string{"zone", "type"})

	serialGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: pluginName,
		Name:      "serial",
		Help:      "SOA serial of the current IXFR journal snapshot.",
	}, []string{"zone"})
)
