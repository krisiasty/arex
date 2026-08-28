package collector

import (
	"strings"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
)

func mods(spec map[string]time.Duration) map[string]config.ModuleConfig {
	out := make(map[string]config.ModuleConfig, len(spec))
	for k, d := range spec {
		out[k] = config.ModuleConfig{Enabled: true, Interval: d}
	}
	return out
}

// Everything runs on the first tick: a fresh process should populate every
// series rather than leaving the slow modules blank for their first interval.
func TestFirstTickRunsEverything(t *testing.T) {
	f := newFake()
	data := newSwitchData(mods(map[string]time.Duration{
		"interfaces": 30 * time.Second, "phy": 15 * time.Minute,
	}))

	CollectDue(f, data, testEpoch)
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"show version", "show interfaces", "phy detail"} {
		if !strings.Contains(joined, want) {
			t.Errorf("first tick missing %q", want)
		}
	}
}

// A module is not re-polled until its own interval has elapsed, so a slow
// module simply appears in fewer batches.
func TestSlowModuleSkippedUntilDue(t *testing.T) {
	f := newFake()
	data := newSwitchData(mods(map[string]time.Duration{
		"interfaces": 30 * time.Second, "phy": 15 * time.Minute,
	}))

	CollectDue(f, data, testEpoch)
	f.calls = nil

	// One poll interval later: only the fast modules are due.
	CollectDue(f, data, testEpoch.Add(30*time.Second))
	joined := strings.Join(f.calls, "\n")
	if !strings.Contains(joined, "show interfaces") {
		t.Error("the fast module should be due")
	}
	if strings.Contains(joined, "phy detail") {
		t.Error("phy is not due 30s after a 15m interval")
	}

	// Past its interval, it rejoins the batch.
	f.calls = nil
	CollectDue(f, data, testEpoch.Add(16*time.Minute))
	if !strings.Contains(strings.Join(f.calls, "\n"), "phy detail") {
		t.Error("phy should be due after 15m")
	}
}

// Batching is preserved: the due modules go out as one request, not one
// request each, so a slow module costs fewer batches rather than more.
func TestDueModulesShareOneBatch(t *testing.T) {
	f := newFake()
	data := newSwitchData(mods(map[string]time.Duration{
		"interfaces": 30 * time.Second, "power": 30 * time.Second, "phy": 15 * time.Minute,
	}))

	CollectDue(f, data, testEpoch)
	if f.batches != 1 {
		t.Errorf("%d requests on the first tick, want 1 batch", f.batches)
	}
}

// Nothing due means no request at all, rather than an empty one.
func TestNoRequestWhenNothingIsDue(t *testing.T) {
	f := newFake()
	data := newSwitchData(mods(map[string]time.Duration{"phy": 15 * time.Minute}))

	CollectDue(f, data, testEpoch)
	before := f.batches
	CollectDue(f, data, testEpoch.Add(time.Second))
	if f.batches != before {
		t.Error("a tick with nothing due must not issue a request")
	}
}

// Data from a module that was not polled this tick keeps its previous value
// and its previous success time, so it is not mistaken for a failure.
func TestSkippedModuleKeepsItsLastSuccess(t *testing.T) {
	f := newFake()
	f.results["show interfaces phy detail"] = `{"interfacePhyStatuses":{}}`
	data := newSwitchData(mods(map[string]time.Duration{
		"interfaces": 30 * time.Second, "phy": 15 * time.Minute,
	}))

	CollectDue(f, data, testEpoch)
	data.RLock()
	first := data.CommandLastSuccess[CmdPhy]
	data.RUnlock()
	if first.IsZero() {
		t.Fatal("phy should have succeeded on the first tick")
	}

	CollectDue(f, data, testEpoch.Add(30*time.Second))

	data.RLock()
	defer data.RUnlock()
	if !data.CommandLastSuccess[CmdPhy].Equal(first) {
		t.Error("a module not polled this tick must keep its last success time")
	}
	if _, failed := data.CommandErrors[CmdPhy]; failed {
		t.Error("not being due is not a failure")
	}
}

// The interval is exposed so the renderer can bound staleness per module: a
// 15m module must not be judged stale against a 90s limit.
func TestIntervalsAreExposedPerCommand(t *testing.T) {
	data := newSwitchData(mods(map[string]time.Duration{
		"interfaces": 30 * time.Second, "phy": 15 * time.Minute,
	}))

	for cmd, want := range map[string]time.Duration{
		CmdVersion:    30 * time.Second,
		CmdInterfaces: 30 * time.Second,
		CmdPhy:        15 * time.Minute,
	} {
		if got := data.CommandInterval[cmd]; got != want {
			t.Errorf("interval[%s] = %v, want %v", cmd, got, want)
		}
	}
}
