// Package health serves liveness, readiness and status endpoints over the
// collector's state.
package health

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/krisiasty/arex/internal/collector"
)

// progressMultiple is how many poll intervals a poller may go without
// completing an attempt before liveness fails.
//
// Ten is generous on purpose. Liveness has exactly one remedy -- restarting --
// and that only helps one cause: a poll loop that has wedged. A switch that is
// unreachable, or refusing our credentials, is not that; answering it by
// killing a working process would trade a visible per-switch failure for a
// restart loop. The window is wide enough to sit through a switch reboot.
const progressMultiple = 10

// Checker answers the probes from the store.
type Checker struct {
	store    *collector.Store
	interval time.Duration
	started  time.Time
	now      func() time.Time
}

// New returns a Checker over store. interval is the poll interval, which sets
// the liveness window.
func New(store *collector.Store, interval time.Duration) *Checker {
	return &Checker{store: store, interval: interval, started: time.Now(), now: time.Now}
}

func (c *Checker) maxProgressAge() time.Duration {
	return progressMultiple * c.interval
}

// Live reports whether every poller is still cycling.
//
// A poller that has not run yet is not a failure while the process is younger
// than the window: pollers start at staggered offsets, and a probe that failed
// during startup would restart arex before it had a chance to poll at all.
func (c *Checker) Live() bool {
	now := c.now()
	window := c.maxProgressAge()
	young := now.Sub(c.started) < window

	for _, sw := range c.store.All() {
		sw.RLock()
		last := sw.LastAttempt
		sw.RUnlock()

		if last.IsZero() {
			if young {
				continue
			}
			return false
		}
		if now.Sub(last) > window {
			return false
		}
	}
	return true
}

// Ready reports whether every switch has completed at least one poll, so
// /metrics reflects the whole configured set.
//
// Deliberately not "every switch is healthy". A switch with bad credentials
// fails every poll indefinitely, and tying readiness to switch health would
// take a working exporter out of service over one misconfigured device --
// hiding the metrics that would have shown which device it was. Per-switch
// health is what arista_scrape_success is for.
func (c *Checker) Ready() bool {
	for _, sw := range c.store.All() {
		sw.RLock()
		attempted := !sw.LastAttempt.IsZero()
		sw.RUnlock()
		if !attempted {
			return false
		}
	}
	return true
}

// SwitchReport is one switch's entry in /status. It carries no credentials.
type SwitchReport struct {
	Switch        string    `json:"switch"`
	ScrapeOK      bool      `json:"scrapeOk"`
	Error         string    `json:"error,omitempty"`
	LastSuccessAt time.Time `json:"lastSuccessAt,omitzero"`
	LastAttemptAt time.Time `json:"lastAttemptAt,omitzero"`
	AgeSeconds    float64   `json:"ageSeconds,omitempty"`
	Stale         bool      `json:"stale,omitempty"`

	// Commands is every command this switch collects, and FailedCommands only
	// those that failed in the last poll. Both are listed: "which commands are
	// configured" and "which are broken" are different questions, and the
	// second is meaningless without the first.
	Commands       []string          `json:"commands"`
	FailedCommands map[string]string `json:"failedCommands,omitempty"`
}

// Report is the payload of /status.
type Report struct {
	Live     bool           `json:"live"`
	Ready    bool           `json:"ready"`
	Uptime   string         `json:"uptime"`
	Switches []SwitchReport `json:"switches"`
}

// Report builds the status payload.
func (c *Checker) Report(stalenessLimit time.Duration) Report {
	now := c.now()
	rep := Report{
		Live:   c.Live(),
		Ready:  c.Ready(),
		Uptime: now.Sub(c.started).Round(time.Second).String(),
	}

	for _, sw := range c.store.All() {
		sw.RLock()
		s := SwitchReport{
			Switch:        sw.Label,
			ScrapeOK:      !sw.LastSuccess.IsZero() && sw.ScrapeErr == nil,
			LastSuccessAt: sw.LastSuccess,
			LastAttemptAt: sw.LastAttempt,
			Commands:      append([]string(nil), sw.Commands...),
		}
		if sw.ScrapeErr != nil {
			s.Error = sw.ScrapeErr.Error()
		}
		if !sw.LastSuccess.IsZero() {
			age := now.Sub(sw.LastSuccess)
			s.AgeSeconds = age.Round(time.Millisecond).Seconds()
			s.Stale = age > stalenessLimit
		}
		for cmd, err := range sw.CommandErrors {
			if s.FailedCommands == nil {
				s.FailedCommands = make(map[string]string, len(sw.CommandErrors))
			}
			s.FailedCommands[cmd] = err.Error()
		}
		sw.RUnlock()
		rep.Switches = append(rep.Switches, s)
	}
	return rep
}

// Register adds the probe endpoints to mux.
func (c *Checker) Register(mux *http.ServeMux, logger *slog.Logger, stalenessLimit time.Duration) {
	live := func(w http.ResponseWriter, _ *http.Request) {
		if c.Live() {
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		http.Error(w, "poll loop stalled", http.StatusServiceUnavailable)
	}

	mux.HandleFunc("/livez", live)
	// /health predates the probes and stays as an alias for /livez.
	mux.HandleFunc("/health", live)

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if c.Ready() {
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		http.Error(w, "waiting for every switch to be polled once", http.StatusServiceUnavailable)
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(c.Report(stalenessLimit)); err != nil {
			logger.Warn("writing status response failed", "error", err)
		}
	})
}
