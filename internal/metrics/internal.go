package metrics

import "github.com/prometheus/client_golang/prometheus"

// buildCollector exposes arex's own identity.
//
// Separate from the switch collector so a filtered scrape can select the
// exporter's own metrics without any switch data, and a switch's scrape
// carries no process-wide series that would then be duplicated across every
// per-switch target.
type buildCollector struct{}

func (buildCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range internalDescs {
		ch <- d
	}
}

func (buildCollector) Collect(ch chan<- prometheus.Metric) {
	b := buildInfo()
	set(ch, "arex_build_info", 1, b.version, b.revision, b.goVersion, b.modified)
}
