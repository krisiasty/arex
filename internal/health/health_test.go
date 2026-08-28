package health

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
)

const interval = 30 * time.Second

func newStore(t *testing.T, names ...string) *collector.Store {
	t.Helper()
	var switches []config.SwitchConfig
	for _, n := range names {
		switches = append(switches, config.SwitchConfig{
			Host: "https://192.0.2.1", Username: "u", Password: "p", Name: n,
			Collect: map[string]bool{"interfaces": true},
		})
	}
	store, err := collector.NewStore(switches, map[string]bool{"interfaces": true})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A freshly started process is live even though no poller has run: pollers
// start at staggered offsets, and failing here would restart arex before it
// ever polled.
func TestLiveDuringStartupBeforeAnyPoll(t *testing.T) {
	c := New(newStore(t, "sw1", "sw2"), interval)
	if !c.Live() {
		t.Error("a just-started process must be live")
	}
}

// Once past the window, a poller that has never run is a stall.
func TestNotLiveIfAPollerNeverRan(t *testing.T) {
	store := newStore(t, "sw1")
	c := New(store, interval)
	c.started = time.Now().Add(-11 * interval)
	if c.Live() {
		t.Error("a poller that never ran past the window must fail liveness")
	}
}

func TestLiveWhileAttemptsAreRecent(t *testing.T) {
	store := newStore(t, "sw1", "sw2")
	c := New(store, interval)
	c.started = time.Now().Add(-time.Hour)
	for _, sw := range store.All() {
		sw.LastAttempt = time.Now()
	}
	if !c.Live() {
		t.Error("recent attempts must be live")
	}
}

// An unreachable switch is not a liveness failure: attempts still complete,
// they just fail. Restarting would not help.
func TestFailingSwitchIsStillLive(t *testing.T) {
	store := newStore(t, "sw1")
	c := New(store, interval)
	c.started = time.Now().Add(-time.Hour)
	sw := store.All()[0]
	sw.LastAttempt = time.Now()
	sw.ScrapeErr = errors.New("401 Unauthorized")

	if !c.Live() {
		t.Error("a switch failing every poll is not a stalled poll loop")
	}
}

func TestNotLiveWhenAttemptsGoStale(t *testing.T) {
	store := newStore(t, "sw1")
	c := New(store, interval)
	c.started = time.Now().Add(-time.Hour)
	store.All()[0].LastAttempt = time.Now().Add(-11 * interval)

	if c.Live() {
		t.Error("no attempt for eleven intervals must fail liveness")
	}
}

func TestNotReadyUntilEverySwitchPolled(t *testing.T) {
	store := newStore(t, "sw1", "sw2")
	c := New(store, interval)
	if c.Ready() {
		t.Error("must not be ready before any poll")
	}
	store.All()[0].LastAttempt = time.Now()
	if c.Ready() {
		t.Error("must not be ready with one switch still unpolled")
	}
	store.All()[1].LastAttempt = time.Now()
	if !c.Ready() {
		t.Error("should be ready once every switch has been polled")
	}
}

// Readiness tracks whether /metrics covers the configured set, not whether the
// switches are healthy: one bad credential must not take a working exporter
// out of service.
func TestReadyEvenWhenEverySwitchFails(t *testing.T) {
	store := newStore(t, "sw1", "sw2")
	c := New(store, interval)
	for _, sw := range store.All() {
		sw.LastAttempt = time.Now()
		sw.ScrapeErr = errors.New("401 Unauthorized")
	}
	if !c.Ready() {
		t.Error("failing switches are visible in metrics; readiness is about coverage")
	}
}

func TestStatusReportsPerSwitchDetail(t *testing.T) {
	store := newStore(t, "good", "bad")
	c := New(store, interval)

	good := store.Get("good")
	good.LastAttempt = time.Now()
	good.LastSuccess = time.Now().Add(-5 * time.Second)

	bad := store.Get("bad")
	bad.LastAttempt = time.Now()
	bad.ScrapeErr = errors.New("401 Unauthorized")
	bad.CommandErrors = map[string]error{"show interfaces": errors.New("permission denied")}

	rep := c.Report(90 * time.Second)
	if len(rep.Switches) != 2 {
		t.Fatalf("%d switches in report", len(rep.Switches))
	}
	byName := map[string]SwitchReport{}
	for _, s := range rep.Switches {
		byName[s.Switch] = s
	}
	if !byName["good"].ScrapeOK {
		t.Error("good switch should report scrapeOk")
	}
	if byName["good"].AgeSeconds < 4 {
		t.Errorf("age = %v, want about 5s", byName["good"].AgeSeconds)
	}
	if byName["bad"].ScrapeOK {
		t.Error("bad switch should not report scrapeOk")
	}
	if !strings.Contains(byName["bad"].Error, "401") {
		t.Errorf("error = %q", byName["bad"].Error)
	}
	if byName["bad"].FailedCommands["show interfaces"] == "" {
		t.Errorf("failed commands = %v", byName["bad"].FailedCommands)
	}
	if len(byName["good"].Commands) == 0 {
		t.Error("commands should be listed even when nothing failed")
	}
}

// /status must never carry credentials.
func TestStatusCarriesNoCredentials(t *testing.T) {
	store := newStore(t, "sw1")
	c := New(store, interval)
	body := serve(t, c, "/status")
	for _, secret := range []string{"password", "hunter2", `"p"`} {
		if strings.Contains(body, secret) {
			t.Errorf("status leaked %q:\n%s", secret, body)
		}
	}
	var rep Report
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("status is not valid JSON: %v", err)
	}
}

func TestEndpointStatusCodes(t *testing.T) {
	store := newStore(t, "sw1")
	c := New(store, interval)

	// Before any poll: live (young process) but not ready.
	if code := serveCode(t, c, "/livez"); code != http.StatusOK {
		t.Errorf("/livez = %d, want 200", code)
	}
	if code := serveCode(t, c, "/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d, want 503 before the first poll", code)
	}
	// /health is an alias for /livez.
	if code := serveCode(t, c, "/health"); code != http.StatusOK {
		t.Errorf("/health = %d, want 200", code)
	}

	store.All()[0].LastAttempt = time.Now()
	if code := serveCode(t, c, "/readyz"); code != http.StatusOK {
		t.Errorf("/readyz = %d, want 200 once polled", code)
	}

	c.started = time.Now().Add(-time.Hour)
	store.All()[0].LastAttempt = time.Now().Add(-time.Hour)
	if code := serveCode(t, c, "/livez"); code != http.StatusServiceUnavailable {
		t.Errorf("/livez = %d, want 503 when stalled", code)
	}
}

func handler(c *Checker) http.Handler {
	mux := http.NewServeMux()
	c.Register(mux, slog.New(slog.DiscardHandler), 90*time.Second)
	return mux
}

func serve(t *testing.T, c *Checker, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(c).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec.Body.String()
}

func serveCode(t *testing.T, c *Checker, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(c).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec.Code
}
