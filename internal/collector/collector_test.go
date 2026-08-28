package collector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/eapi"
)

// fakeRunner returns canned results, or an error, per command.
type fakeRunner struct {
	results map[string]string // command -> raw JSON
	fail    map[string]error  // command -> per-command failure
	allErr  error             // failure returned by Run
	// failBatchOnly makes allErr apply only to multi-command calls, so a
	// batch rejection can be distinguished from an unreachable switch.
	failBatchOnly bool
	calls         []string
	// batches counts Run calls, so a test can assert that due commands share
	// one request instead of taking one each.
	batches int
}

func (f *fakeRunner) Run(cmds []string) ([]json.RawMessage, error) {
	f.calls = append(f.calls, cmds...)
	f.batches++
	if f.allErr != nil && (!f.failBatchOnly || len(cmds) > 1) {
		return nil, f.allErr
	}
	out := make([]json.RawMessage, 0, len(cmds))
	for _, c := range cmds {
		if err := f.fail[c]; err != nil {
			return nil, err
		}
		body, ok := f.results[c]
		if !ok {
			body = "{}"
		}
		out = append(out, json.RawMessage(body))
	}
	return out, nil
}

func newFake() *fakeRunner {
	return &fakeRunner{results: map[string]string{}, fail: map[string]error{}}
}

// testEpoch is a fixed start time, so scheduling tests do not depend on the
// wall clock.
var testEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newSwitchData builds a SwitchData from a module set.
func newSwitchData(label string, collect map[string]config.ModuleConfig, scope string) *SwitchData {
	return newSwitchDataFromSpecs(label, commandsFor(collect, scope, pollFloor(collect)))
}

// pollFloor is the shortest configured interval, standing in for the poll
// interval the loop would tick at.
func pollFloor(collect map[string]config.ModuleConfig) time.Duration {
	floor := time.Duration(0)
	for _, m := range collect {
		if m.Interval > 0 && (floor == 0 || m.Interval < floor) {
			floor = m.Interval
		}
	}
	if floor == 0 {
		floor = directInterval
	}
	return floor
}

func TestBGPUsesVrfAll(t *testing.T) {
	f := newFake()
	Collect(f, &SwitchData{Label: "sw1"})

	joined := strings.Join(f.calls, "\n")
	if !strings.Contains(joined, "show ip bgp summary vrf all") {
		t.Errorf("collector must query all VRFs; commands were:\n%s", joined)
	}
	for _, c := range f.calls {
		if c == "show ip bgp summary" {
			t.Error(`plain "show ip bgp summary" returns only the default VRF`)
		}
	}
}

func TestCollectsOpticsCommands(t *testing.T) {
	f := newFake()
	Collect(f, &SwitchData{Label: "sw1"})

	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{
		"show interfaces transceiver detail",
		"show interfaces phy detail",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing command %q", want)
		}
	}
}

// One rejected command must not cost every other metric for the switch.
func TestOneFailedCommandDoesNotLoseTheRest(t *testing.T) {
	f := newFake()
	f.results["show version"] = `{"modelName":"DCS-7050CX3-32C-R","memTotal":16280716}`
	// A switch that rejects a command answers with a JSON-RPC error.
	f.fail["show interfaces phy detail"] = &eapi.CommandError{Code: 1002, Message: "invalid command"}

	data := &SwitchData{Label: "sw1"}
	Collect(f, data)

	data.RLock()
	defer data.RUnlock()

	if data.LastSuccess.IsZero() {
		t.Error("LastSuccess not set: a single bad command still zeroed the whole poll")
	}
	if data.Version.ModelName != "DCS-7050CX3-32C-R" {
		t.Errorf("show version was lost: ModelName = %q", data.Version.ModelName)
	}
	if len(data.CommandErrors) == 0 {
		t.Error("the failing command should be recorded in CommandErrors")
	}
	if _, ok := data.CommandErrors["show interfaces phy detail"]; !ok {
		t.Errorf("CommandErrors = %v, want the phy command", data.CommandErrors)
	}
}

// A transport failure is different: nothing was collected at all.
func TestTransportFailureIsAScrapeError(t *testing.T) {
	f := newFake()
	f.allErr = errors.New("dial tcp: connection refused")

	data := &SwitchData{Label: "sw1"}
	Collect(f, data)

	data.RLock()
	defer data.RUnlock()
	if data.ScrapeErr == nil {
		t.Error("ScrapeErr must be set when the transport fails")
	}
	if !data.LastSuccess.IsZero() {
		t.Error("LastSuccess must not advance when nothing was collected")
	}
}

// A malformed body for one command must not discard the others either.
func TestUnparseableCommandIsIsolated(t *testing.T) {
	f := newFake()
	f.results["show version"] = `{"modelName":"DCS-7050CX3-32C-R"}`
	f.results["show ip bgp summary vrf all"] = `{"vrfs":"not an object"}`

	data := &SwitchData{Label: "sw1"}
	Collect(f, data)

	data.RLock()
	defer data.RUnlock()
	if data.Version.ModelName != "DCS-7050CX3-32C-R" {
		t.Errorf("good command lost: ModelName = %q", data.Version.ModelName)
	}
	if _, ok := data.CommandErrors["show ip bgp summary vrf all"]; !ok {
		t.Errorf("CommandErrors = %v, want the bgp command", data.CommandErrors)
	}
}

func TestDuplicateSwitchNamesAreRejected(t *testing.T) {
	_, err := NewStore([]config.SwitchConfig{
		{Host: "https://192.0.2.1", Username: "u", Password: "p", Name: "spine1"},
		{Host: "https://192.0.2.2", Username: "u", Password: "p", Name: "spine1"},
	}, allEnabled(), directInterval)
	if err == nil {
		t.Fatal("two switches sharing a label must be rejected, not silently merged")
	}
	if !strings.Contains(err.Error(), "spine1") {
		t.Errorf("error should name the duplicate: %v", err)
	}
}

// An unreachable switch must not be probed once per command: with 9
// commands and a 10s timeout that is 100s per poll, longer than the default
// poll interval and staleness limit. Only an eAPI-level rejection -- the
// switch answered, but disliked a command -- justifies retrying individually.
func TestTransportFailureDoesNotRetryPerCommand(t *testing.T) {
	f := newFake()
	f.allErr = errors.New("dial tcp 192.0.2.99:443: i/o timeout")

	Collect(f, &SwitchData{Label: "sw1"})

	if n := len(commandsFor(allEnabled(), "", directInterval)); len(f.calls) > n {
		t.Errorf("issued %d commands for an unreachable switch, want at most one batch of %d",
			len(f.calls), n)
	}
}

func TestEAPIRejectionDoesRetryPerCommand(t *testing.T) {
	f := newFake()
	f.allErr = &eapi.CommandError{Code: 1002, Message: "invalid command"}
	f.failBatchOnly = true

	Collect(f, &SwitchData{Label: "sw1"})

	if n := len(commandsFor(allEnabled(), "", directInterval)); len(f.calls) <= n {
		t.Errorf("issued %d commands; an eAPI rejection should trigger individual retries",
			len(f.calls))
	}
}

// A switch that has never answered still needs per-command series: a missing
// series is not 0 in Prometheus, so count(arista_command_success == 0) would
// silently exclude the switch that is most broken.
func TestTotalFailureMarksEveryCommandFailed(t *testing.T) {
	f := newFake()
	f.allErr = errors.New("unexpected HTTP status: 401 Unauthorized")

	data := &SwitchData{Label: "sw1"}
	data.specs = commandsFor(allEnabled(), "", directInterval)
	data.Commands = commandNames(data.specs)
	Collect(f, data)

	data.RLock()
	defer data.RUnlock()
	if data.CommandErrors == nil {
		t.Fatal("CommandErrors must be populated so the writer can report per-command state")
	}
	for _, name := range data.Commands {
		if _, failed := data.CommandErrors[name]; !failed {
			t.Errorf("command %q should be marked failed after a total failure", name)
		}
	}
}

// eapi.attemptFor treats a one-command request as a per-command retry, which
// only holds while the batch is larger than one command. With collection
// opt-in, a switch could legitimately be configured down to just
// "show version" -- in which case every request is labelled a retry.
func TestFullCommandSetIsLargerThanOne(t *testing.T) {
	if n := len(commandsFor(allEnabled(), "", directInterval)); n < 2 {
		t.Fatalf("full command set is %d; eAPI request statistics classify a "+
			"single-command call as a retry", n)
	}
}

// A poller must stop when its context is cancelled, so shutdown does not
// depend on the process being killed out from under it.
func TestPollLoopStopsOnContextCancel(t *testing.T) {
	f := newFake()
	data := &SwitchData{Label: "sw1"}
	data.specs = commandsFor(allEnabled(), "", directInterval)
	data.Commands = commandNames(data.specs)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		PollLoop(ctx, f, data, time.Hour, 0)
		close(done)
	}()

	// The first poll happens immediately; cancel while waiting for the tick.
	select {
	case <-done:
		t.Fatal("PollLoop returned before cancellation")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PollLoop did not return within 2s of cancellation")
	}
}

// Cancelling before the staggered start must skip the first poll entirely,
// rather than waiting out the offset.
func TestPollLoopHonoursCancelDuringStartOffset(t *testing.T) {
	f := newFake()
	data := &SwitchData{Label: "sw1"}
	data.specs = commandsFor(allEnabled(), "", directInterval)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		PollLoop(ctx, f, data, time.Hour, time.Hour)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PollLoop waited out the start offset despite cancellation")
	}
	if len(f.calls) != 0 {
		t.Errorf("polled %d commands after cancellation during the offset", len(f.calls))
	}
}
