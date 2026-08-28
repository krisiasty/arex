package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
)

func twoSwitchStore(t *testing.T) (*collector.Store, map[string]string) {
	t.Helper()
	switches := []config.SwitchConfig{
		{Host: "https://192.0.2.11", Username: "u", Password: "p", Name: "leaf-1"},
		{Host: "https://192.0.2.12", Username: "u", Password: "p", Name: "leaf-2"},
	}
	store, err := collector.NewStore(switches, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, sw := range store.All() {
		collector.Collect(fixtureRunner{t}, sw)
	}
	return store, TargetIndex(switches)
}

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// Unfiltered is the default and must be unchanged: every switch plus arex's
// own metrics in one response.
func TestNoTargetServesEverything(t *testing.T) {
	store, index := twoSwitchStore(t)
	h := NewHandler(store, 90*time.Second, index)

	code, body := get(t, h, "/metrics")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{`switch="leaf-1"`, `switch="leaf-2"`, "arex_build_info", "go_goroutines"} {
		if !strings.Contains(body, want) {
			t.Errorf("unfiltered response missing %q", want)
		}
	}
}

func TestTargetSelectsOneSwitch(t *testing.T) {
	store, index := twoSwitchStore(t)
	h := NewHandler(store, 90*time.Second, index)

	code, body := get(t, h, "/metrics?target=leaf-1")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	if !strings.Contains(body, `switch="leaf-1"`) {
		t.Error("missing the requested switch")
	}
	if strings.Contains(body, `switch="leaf-2"`) {
		t.Error("a filtered response must not carry other switches")
	}
	// Exporter internals belong to target=internal, not to a switch.
	for _, unwanted := range []string{"arex_build_info", "go_goroutines", "process_"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("filtered response should not carry %q", unwanted)
		}
	}
}

// The host works as a target too, so Prometheus relabeling can use whatever
// identifier it already has.
func TestTargetMatchesHostAndBareAddress(t *testing.T) {
	store, index := twoSwitchStore(t)
	h := NewHandler(store, 90*time.Second, index)

	for _, target := range []string{"https://192.0.2.11", "192.0.2.11"} {
		code, body := get(t, h, "/metrics?target="+target)
		if code != http.StatusOK {
			t.Errorf("target %q: status %d", target, code)
			continue
		}
		if !strings.Contains(body, `switch="leaf-1"`) {
			t.Errorf("target %q did not resolve to leaf-1", target)
		}
	}
}

func TestInternalTargetServesOnlyExporterMetrics(t *testing.T) {
	store, index := twoSwitchStore(t)
	h := NewHandler(store, 90*time.Second, index)

	code, body := get(t, h, "/metrics?target=internal")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{"arex_build_info", "go_goroutines", "process_"} {
		if !strings.Contains(body, want) {
			t.Errorf("internal target missing %q", want)
		}
	}
	if strings.Contains(body, "arista_") {
		t.Error("internal target must not carry switch metrics")
	}
}

// A typo in relabeling must fail the scrape rather than produce an empty one:
// an empty body would leave Prometheus reporting up=1 with no series.
func TestUnknownTargetIsAnError(t *testing.T) {
	store, index := twoSwitchStore(t)
	h := NewHandler(store, 90*time.Second, index)

	code, body := get(t, h, "/metrics?target=leaf-99")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	if !strings.Contains(body, "leaf-99") {
		t.Errorf("error should name the unknown target: %s", body)
	}
}

func TestEmptyTargetServesEverything(t *testing.T) {
	store, index := twoSwitchStore(t)
	h := NewHandler(store, 90*time.Second, index)

	code, body := get(t, h, "/metrics?target=")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, `switch="leaf-2"`) {
		t.Error("an empty target should behave as no target")
	}
}

func TestTargetIndexCoversLabelHostAndAddress(t *testing.T) {
	index := TargetIndex([]config.SwitchConfig{
		{Host: "https://192.0.2.11", Name: "leaf-1"},
		{Host: "http://switch2.example.com:8080", Name: "leaf-2"},
		{Host: "https://192.0.2.13"}, // unnamed: label falls back to host
	})
	for target, want := range map[string]string{
		"leaf-1":                          "leaf-1",
		"https://192.0.2.11":              "leaf-1",
		"192.0.2.11":                      "leaf-1",
		"leaf-2":                          "leaf-2",
		"switch2.example.com:8080":        "leaf-2",
		"http://switch2.example.com:8080": "leaf-2",
		"https://192.0.2.13":              "https://192.0.2.13",
		"192.0.2.13":                      "https://192.0.2.13",
	} {
		if got := index[target]; got != want {
			t.Errorf("index[%q] = %q, want %q", target, got, want)
		}
	}
}

// Two switches sharing a host make that host ambiguous as a target. Resolving
// it to whichever came last would silently return one switch's metrics under
// the other's identity, so the host is dropped and only the labels resolve.
func TestAmbiguousHostIsNotAddressable(t *testing.T) {
	index := TargetIndex([]config.SwitchConfig{
		{Host: "https://192.0.2.11", Name: "leaf-1"},
		{Host: "https://192.0.2.11", Name: "leaf-2"},
		{Host: "https://192.0.2.12", Name: "leaf-3"},
	})
	for _, ambiguous := range []string{"https://192.0.2.11", "192.0.2.11"} {
		if label, ok := index[ambiguous]; ok {
			t.Errorf("index[%q] = %q, want no entry for a shared host", ambiguous, label)
		}
	}
	// Labels stay addressable, and an unshared host still resolves.
	for target, want := range map[string]string{
		"leaf-1": "leaf-1", "leaf-2": "leaf-2", "192.0.2.12": "leaf-3",
	} {
		if got := index[target]; got != want {
			t.Errorf("index[%q] = %q, want %q", target, got, want)
		}
	}
}
