package localmetrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// MetricMustGatherTotal must gather total counter
	MetricMustGatherTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "must_gather_operator_must_gather_total",
		Help: "Total number of must gathers performed",
	})

	// MetricMustGatherErrors must gather error counter
	MetricMustGatherErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "must_gather_operator_must_gather_errors",
		Help: "Number of must gathers with errors",
	})

	// MetricsList collector list exported to metrics server
	MetricsList = []prometheus.Collector{
		MetricMustGatherTotal,
		MetricMustGatherErrors,
	}
)
