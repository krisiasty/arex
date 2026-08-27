package collector

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/krisiasty/arex/config"
)

// fakeRunner returns canned results, or an error, per command.
type fakeRunner struct {
	results map[string]string // command -> raw JSON
	fail    map[string]error  // command -> per-command failure
	allErr  error             // whole-transport failure
	calls   []string
}

func (f *fakeRunner) Run(cmds []string) ([]json.RawMessage, error) {
	f.calls = append(f.calls, cmds...)
	if f.allErr != nil {
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
	f.fail["show interfaces phy detail"] = errors.New("eAPI error 1002: invalid command")

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
	})
	if err == nil {
		t.Fatal("two switches sharing a label must be rejected, not silently merged")
	}
	if !strings.Contains(err.Error(), "spine1") {
		t.Errorf("error should name the duplicate: %v", err)
	}
}
