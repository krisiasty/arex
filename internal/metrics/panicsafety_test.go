package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// getURL issues a GET against a real server, with a context. The get helper in
// target_test.go drives a handler directly; this needs an actual connection,
// because the property under test is that the server survives.
func getURL(t *testing.T, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return http.DefaultClient.Do(req)
}

// panickingCollector stands in for a bug in a metric path.
type panickingCollector struct{}

func (panickingCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc("arex_panic_probe", "probe", nil, nil)
}

func (panickingCollector) Collect(chan<- prometheus.Metric) {
	panic("a bug in a collector")
}

// A panic while rendering metrics must cost that one scrape, not the process.
// arex polls in the background: losing the exporter would stop collection for
// every switch, which is a far worse outcome than a failed scrape Prometheus
// will retry in fifteen seconds.
func TestCollectorPanicDoesNotStopTheExporter(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(panickingCollector{})

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, reg)
	})
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// The scrape fails. How it fails is net/http's business -- a 500, or a
	// dropped connection -- so it is not asserted.
	if resp, err := getURL(t, srv.URL+"/metrics"); err == nil {
		_ = resp.Body.Close()
		t.Logf("scrape returned %s", resp.Status)
	} else {
		t.Logf("scrape failed at the transport: %v", err)
	}

	// What matters: the server is still there afterwards.
	resp, err := getURL(t, srv.URL+"/livez")
	if err != nil {
		t.Fatalf("the exporter died with the scrape: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/livez = %s after a collector panic", resp.Status)
	}
}
