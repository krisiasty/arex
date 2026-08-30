package metrics

import (
	"fmt"
	"net/http"
	"sort"
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
	index := make(map[string]string, len(switches))
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

// validModules are the filterable metric groups: the configurable collect
// keys, plus version, which is always collected and so has no key.
var validModules = func() map[string]bool {
	out := map[string]bool{"version": true}
	for _, k := range config.CollectKeys {
		out[k] = true
	}
	return out
}()

func moduleNames() []string {
	out := make([]string, 0, len(validModules))
	for k := range validModules {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// knownInterface reports whether the last poll saw this interface, on the
// named switch or on any of them.
//
// Checked against collected data rather than configuration: interfaces are
// discovered, not declared, so the last poll is the only authority on which
// names exist.
func knownInterface(store *collector.Store, label, name string) bool {
	switches := store.All()
	if label != "" {
		sw := store.Get(label)
		if sw == nil {
			return false
		}
		switches = []*collector.SwitchData{sw}
	}
	for _, sw := range switches {
		sw.RLock()
		_, inCounters := sw.Interfaces.Interfaces[name]
		_, inOptics := sw.Optics.Interfaces[name]
		_, inPhy := sw.Phy.Interfaces[name]
		sw.RUnlock()
		if inCounters || inOptics || inPhy {
			return true
		}
	}
	return false
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
//
// Must, because this runs at startup: a collector that cannot be registered is
// a programming error, and failing immediately is better than serving a
// partial exposition for the life of the process.
func newRegistry(cs ...prometheus.Collector) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(cs...)
	return reg
}

// registryFor builds a per-request registry, reporting a failure instead of
// panicking. The same registration succeeds at startup, so this cannot fail in
// practice -- but a Must on a request path is a panic waiting for a future
// edit, and the caller is already holding a ResponseWriter it can answer with.
func registryFor(c prometheus.Collector) (*prometheus.Registry, error) {
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		return nil, err
	}
	return reg, nil
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
		q := r.URL.Query()
		target := q.Get("target")
		filter := Filter{Module: q.Get("module"), Interface: q.Get("interface")}

		// Neither filter means anything for process metrics, so combining them
		// with the internal target is a mistake worth reporting rather than
		// quietly ignoring.
		if target == InternalTarget && (filter.Module != "" || filter.Interface != "") {
			http.Error(w, fmt.Sprintf("target %q takes no module or interface filter", InternalTarget),
				http.StatusBadRequest)
			return
		}

		if filter.Module != "" && !validModules[filter.Module] {
			http.Error(w, fmt.Sprintf("unknown module %q: expected one of %s",
				filter.Module, strings.Join(moduleNames(), ", ")), http.StatusBadRequest)
			return
		}

		var label string
		if target != "" && target != InternalTarget {
			resolved, ok := index[target]
			if !ok {
				// An empty body would leave Prometheus reporting a healthy
				// scrape with no series, so a bad target fails loudly. A typo
				// in relabeling should be visible.
				http.Error(w, fmt.Sprintf(
					"unknown target %q: expected a configured switch, or %q for arex's own metrics",
					target, InternalTarget), http.StatusBadRequest)
				return
			}
			label = resolved
		}

		// A typo in an interface name would otherwise render nothing, which a
		// human reads as "this interface has no data" rather than "no such
		// interface".
		if filter.Interface != "" && !knownInterface(store, label, filter.Interface) {
			where := "any switch"
			if label != "" {
				where = fmt.Sprintf("switch %q", label)
			}
			http.Error(w, fmt.Sprintf("no interface %q in the last poll of %s",
				filter.Interface, where), http.StatusBadRequest)
			return
		}

		switch {
		case target == InternalTarget:
			serve(w, r, internal)
		case target == "" && filter == Filter{}:
			serve(w, r, full)
		default:
			reg, err := registryFor(NewSwitchCollector(store, stalenessLimit, label, filter))
			if err != nil {
				http.Error(w, "building the filtered registry: "+err.Error(),
					http.StatusInternalServerError)
				return
			}
			serve(w, r, reg)
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
