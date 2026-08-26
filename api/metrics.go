package api

import (
	"github.com/coredns/coredns/plugin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: "api",
		Name:      "http_requests_total",
		Help:      "HTTP API requests.",
	}, []string{"method", "code"})

	authCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: "api",
		Name:      "auth_total",
		Help:      "API authentication outcomes.",
	}, []string{"result"})

	syncCount = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: plugin.Namespace,
		Subsystem: "api",
		Name:      "cluster_sync_total",
		Help:      "Cluster snapshot pull/push outcomes.",
	}, []string{"result"})

	zoneGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: plugin.Namespace,
		Subsystem: "api",
		Name:      "zones",
		Help:      "Registered zones by kind and source.",
	}, []string{"kind", "source"})
)
