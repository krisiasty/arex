package metrics

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
)

// InternalTarget selects arex's own metrics rather than a switch's.
//
// Reserved: config rejects a switch named this, so the two can never be
// ambiguous.
const InternalTarget = "internal"

// TargetIndex maps every accepted spelling of a target to a switch label.
//
// A switch is addressable by its label, its configured host, and that host
// without the scheme, so Prometheus relabeling can use whichever identifier a
// job already has -- typically an address from service discovery rather than a
// name only arex knows.
// Labels are unique, so they always resolve. A host shared by more than one
// switch is ambiguous and is dropped rather than resolved to whichever came
// last, which would return one switch's metrics under another's identity.
func TargetIndex(switches []config.SwitchConfig) map[string]string {
	index := make(map[string]string, len(switches)*3)
	ambiguous := make(map[string]bool)

	add := func(key, label string) {
		if key == "" {
			return
		}
		if existing, seen := index[key]; seen && existing != label {
			ambiguous[key] = true
			return
		}
		index[key] = label
	}

	for _, sw := range switches {
		label := sw.Label()
		add(label, label)
		add(sw.Host, label)
		add(stripScheme(sw.Host), label)
	}
	for key := range ambiguous {
		// Unless the key is also a label, which is unique and unambiguous.
		if index[key] != key {
			delete(index, key)
		}
	}
	return index
}

func stripScheme(host string) string {
	if i := strings.Index(host, "://"); i >= 0 {
		return host[i+3:]
	}
	return host
}

// internalCollectors are the process's own metrics: build identity plus the Go
// runtime and process collectors.
//
// Those two are what answer questions about arex itself -- goroutines, heap,
// resident memory, open files, CPU time. They belong to the runtime, which
// reports them at scrape time and more accurately than this package could.
func internalCollectors() []prometheus.Collector {
	return []prometheus.Collector{
		buildCollector{},
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	}
}

// newRegistry builds a private registry. Private rather than the default one,
// so nothing arrives by package side effect.
func newRegistry(cs ...prometheus.Collector) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(cs...)
	return reg
}

// NewHandler serves /metrics, optionally filtered by a target parameter.
//
// Unfiltered is the default and renders everything: every switch plus arex's
// own metrics. A target narrows the response to one switch, or to the
// exporter's own metrics with target=internal. Filtering only changes what a
// scrape renders -- collection is unaffected, so one poll of a switch serves
// any number of scrapers, however they are configured.
func NewHandler(store *collector.Store, stalenessLimit time.Duration, index map[string]string) http.Handler {
	// The unfiltered registry is built once: its collectors read the store at
	// scrape time, so there is nothing per-request about it.
	full := newRegistry(append(internalCollectors(),
		NewCollector(store, stalenessLimit))...)
	internal := newRegistry(internalCollectors()...)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")

		switch target {
		case "":
			serve(w, r, full)
		case InternalTarget:
			serve(w, r, internal)
		default:
			label, ok := index[target]
			if !ok {
				// An empty body would leave Prometheus reporting a healthy
				// scrape with no series, so a bad target fails loudly. A typo
				// in relabeling should be visible.
				http.Error(w, fmt.Sprintf(
					"unknown target %q: expected a configured switch, or %q for arex's own metrics",
					target, InternalTarget), http.StatusBadRequest)
				return
			}
			serve(w, r, newRegistry(NewSwitchCollector(store, stalenessLimit, label)))
		}
	})
}

// serve renders one registry.
//
// Scrape errors are reported to the client rather than logged and swallowed: a
// metric that cannot be rendered is a fault worth surfacing where something is
// looking.
func serve(w http.ResponseWriter, r *http.Request, reg *prometheus.Registry) {
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.HTTPErrorOnError,
	}).ServeHTTP(w, r)
}
