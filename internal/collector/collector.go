package collector

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/eapi"
)

// errResultCount signals that eAPI returned a different number of results
// than commands sent.
var errResultCount = errors.New("result count mismatch")

// Runner executes EOS CLI commands and returns one raw JSON result per
// command. *eapi.Client implements it.
type Runner interface {
	Run(cmds []string) ([]json.RawMessage, error)
}

// SwitchData holds the latest collected data for one switch.
type SwitchData struct {
	mu          sync.RWMutex
	Label       string
	LastSuccess time.Time
	ScrapeErr   error

	// CommandErrors records commands that failed or returned unparseable
	// output in the most recent poll, keyed by CLI string. Data from a
	// failed command is left at its previous value rather than zeroed.
	CommandErrors map[string]error

	Version    eapi.ShowVersion
	ProcessTop eapi.ShowProcessesTop
	EnvTemp    eapi.ShowEnvironmentTemp
	EnvPower   eapi.ShowEnvironmentPower
	EnvCooling eapi.ShowEnvironmentCooling
	Interfaces eapi.ShowInterfaces
	BGPSummary eapi.ShowBGPSummary
	Optics     eapi.ShowTransceiverDetail
	Phy        eapi.ShowPhyDetail
}

// RLock / RUnlock expose the read lock for callers rendering metrics.
func (d *SwitchData) RLock()   { d.mu.RLock() }
func (d *SwitchData) RUnlock() { d.mu.RUnlock() }

// Store holds SwitchData for all configured switches.
// The map is populated once at startup and never modified afterward,
// so the map itself needs no mutex — only the per-switch data does.
type Store struct {
	switches map[string]*SwitchData
	order    []string
}

// NewStore initialises an empty Store for the given switch configs.
//
// Labels must be unique: two switches sharing one would write into the same
// SwitchData, producing a single series alternating between two devices.
func NewStore(switches []config.SwitchConfig) (*Store, error) {
	s := &Store{
		switches: make(map[string]*SwitchData, len(switches)),
		order:    make([]string, 0, len(switches)),
	}
	for _, sw := range switches {
		label := sw.Label()
		if _, dup := s.switches[label]; dup {
			return nil, fmt.Errorf("config: duplicate switch label %q — names must be unique", label)
		}
		s.switches[label] = &SwitchData{Label: label}
		s.order = append(s.order, label)
	}
	return s, nil
}

// All returns every SwitchData in configuration order. Callers must hold
// each entry's read lock when accessing fields.
func (s *Store) All() []*SwitchData {
	out := make([]*SwitchData, 0, len(s.order))
	for _, label := range s.order {
		out = append(out, s.switches[label])
	}
	return out
}

// Get returns the SwitchData for a label.
func (s *Store) Get(label string) *SwitchData {
	return s.switches[label]
}

// snapshot is a poll's parsed output, staged before being committed.
type snapshot struct {
	version    eapi.ShowVersion
	processTop eapi.ShowProcessesTop
	envTemp    eapi.ShowEnvironmentTemp
	envPower   eapi.ShowEnvironmentPower
	envCooling eapi.ShowEnvironmentCooling
	interfaces eapi.ShowInterfaces
	bgp        eapi.ShowBGPSummary
	optics     eapi.ShowTransceiverDetail
	phy        eapi.ShowPhyDetail
}

// cmdSpec binds a CLI command to where its output is parsed and committed.
type cmdSpec struct {
	cli   string
	into  func(*snapshot) interface{}
	apply func(*snapshot, *SwitchData)
}

// commands is the set of EOS CLI commands arex issues per poll.
//
// "vrf all" is required: plain "show ip bgp summary" returns the default VRF
// only, so peers in any other VRF are invisible.
var commands = []cmdSpec{
	{"show version",
		func(s *snapshot) interface{} { return &s.version },
		func(s *snapshot, d *SwitchData) { d.Version = s.version }},
	{"show processes top once",
		func(s *snapshot) interface{} { return &s.processTop },
		func(s *snapshot, d *SwitchData) { d.ProcessTop = s.processTop }},
	{"show system environment temperature",
		func(s *snapshot) interface{} { return &s.envTemp },
		func(s *snapshot, d *SwitchData) { d.EnvTemp = s.envTemp }},
	{"show system environment power",
		func(s *snapshot) interface{} { return &s.envPower },
		func(s *snapshot, d *SwitchData) { d.EnvPower = s.envPower }},
	{"show system environment cooling",
		func(s *snapshot) interface{} { return &s.envCooling },
		func(s *snapshot, d *SwitchData) { d.EnvCooling = s.envCooling }},
	{"show interfaces",
		func(s *snapshot) interface{} { return &s.interfaces },
		func(s *snapshot, d *SwitchData) { d.Interfaces = s.interfaces }},
	{"show ip bgp summary vrf all",
		func(s *snapshot) interface{} { return &s.bgp },
		func(s *snapshot, d *SwitchData) { d.BGPSummary = s.bgp }},
	{"show interfaces transceiver detail",
		func(s *snapshot) interface{} { return &s.optics },
		func(s *snapshot, d *SwitchData) { d.Optics = s.optics }},
	{"show interfaces phy detail",
		func(s *snapshot) interface{} { return &s.phy },
		func(s *snapshot, d *SwitchData) { d.Phy = s.phy }},
}

// Commands returns the CLI strings arex issues, for metric labelling.
func Commands() []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		out = append(out, c.cli)
	}
	return out
}

// Collect performs a single poll and updates the store.
//
// All commands are sent as one runCmds batch. eAPI fails a whole batch if it
// rejects any single command, so on batch failure each command is retried
// individually: one command unsupported on a platform then costs only its
// own metrics instead of every metric for the switch.
func Collect(client Runner, data *SwitchData) {
	var snap snapshot
	cmdErrs := make(map[string]error)
	ok := make([]bool, len(commands))

	raws, err := runBatch(client, len(commands))
	if err != nil && !worthRetryingIndividually(err) {
		// The switch is unreachable or refusing us outright. Retrying each
		// command would multiply the timeout by len(commands) for nothing.
		setError(data, fmt.Errorf("collection failed: %w", err))
		return
	}
	if err != nil {
		for i, c := range commands {
			raw, cerr := runOne(client, c.cli)
			if cerr != nil {
				cmdErrs[c.cli] = cerr
				continue
			}
			raws[i] = raw
		}
	}

	for i, c := range commands {
		if _, failed := cmdErrs[c.cli]; failed {
			continue
		}
		if len(raws[i]) == 0 {
			cmdErrs[c.cli] = fmt.Errorf("empty result")
			continue
		}
		if perr := json.Unmarshal(raws[i], c.into(&snap)); perr != nil {
			cmdErrs[c.cli] = fmt.Errorf("parse: %w", perr)
			continue
		}
		ok[i] = true
	}

	succeeded := 0
	for _, v := range ok {
		if v {
			succeeded++
		}
	}

	// Nothing landed: the switch is unreachable or refusing everything.
	// Leave LastSuccess alone so stale-but-recent data stays servable.
	if succeeded == 0 {
		reason := err
		if reason == nil {
			reason = fmt.Errorf("no command returned usable output")
		}
		setError(data, fmt.Errorf("collection failed: %w", reason))
		return
	}

	for cli, cerr := range cmdErrs {
		log.Printf("[%s] %s: %v", data.Label, cli, cerr)
	}

	data.mu.Lock()
	defer data.mu.Unlock()
	for i, c := range commands {
		if ok[i] {
			c.apply(&snap, data)
		}
	}
	data.CommandErrors = cmdErrs
	data.ScrapeErr = nil
	data.LastSuccess = time.Now()
}

// worthRetryingIndividually reports whether a failed batch could partly
// succeed if its commands were sent one at a time.
func worthRetryingIndividually(err error) bool {
	var cmdErr *eapi.CommandError
	if errors.As(err, &cmdErr) {
		return true
	}
	// A result-count mismatch is also a per-command problem, not transport.
	return errors.Is(err, errResultCount)
}

// runBatch issues every command in a single request.
func runBatch(client Runner, n int) ([]json.RawMessage, error) {
	raws, err := client.Run(Commands())
	if err != nil {
		return make([]json.RawMessage, n), err
	}
	if len(raws) != n {
		return make([]json.RawMessage, n), fmt.Errorf("%w: expected %d, got %d", errResultCount, n, len(raws))
	}
	return raws, nil
}

// runOne issues a single command on its own.
func runOne(client Runner, cli string) (json.RawMessage, error) {
	raws, err := client.Run([]string{cli})
	if err != nil {
		return nil, err
	}
	if len(raws) != 1 {
		return nil, fmt.Errorf("expected 1 result, got %d", len(raws))
	}
	return raws[0], nil
}

// PollLoop runs Collect on every tick until the process exits.
// It collects once immediately on startup before waiting for the first tick.
func PollLoop(client Runner, data *SwitchData, interval time.Duration) {
	log.Printf("[%s] starting poller (interval: %s)", data.Label, interval)
	Collect(client, data)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		Collect(client, data)
	}
}

func setError(data *SwitchData, err error) {
	log.Printf("[%s] %v", data.Label, err)
	data.mu.Lock()
	defer data.mu.Unlock()
	data.ScrapeErr = err
}
