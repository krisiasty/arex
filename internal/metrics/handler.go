package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/krisiasty/arex/internal/collector"
)

// NewRegistry returns a registry exposing arex's switch metrics alongside the
// Go runtime and process collectors.
//
// A private registry rather than the default one, so nothing arrives here by
// package side effect: everything exposed is registered explicitly.
//
// The Go and process collectors are what answer questions about arex itself --
// goroutine counts, heap size, resident memory, open files, CPU time. Those
// belong to the runtime, which reports them at scrape time and more accurately
// than this package could.
func NewRegistry(store *collector.Store, stalenessLimit time.Duration) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		NewCollector(store, stalenessLimit),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// Handler serves the registry.
//
// Errors during a scrape are reported to the client rather than logged and
// swallowed: a metric that cannot be rendered is a fault worth surfacing at
// the point something is looking.
func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.HTTPErrorOnError,
	})
}
