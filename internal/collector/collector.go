package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
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

	// LastAttempt is when a poll last completed, successfully or not. It is
	// what liveness is judged on: a switch being unreachable is not a
	// liveness failure -- restarting would not fix it -- whereas a poll loop
	// that has stopped cycling is.
	LastAttempt time.Time

	// CommandErrors records commands that failed or returned unparseable
	// output in the most recent poll, keyed by CLI string. Data from a
	// failed command is left at its previous value rather than zeroed.
	CommandErrors map[string]error

	// CommandLastSuccess records when each command last returned usable
	// output. Data from a failed command is retained rather than zeroed, so
	// without a per-command bound one working command would keep the scrape
	// looking fresh while the rest went arbitrarily stale.
	CommandLastSuccess map[string]time.Time

	// Stats counts eAPI requests for this switch. It describes arex rather
	// than the switch, so it is reported even when the switch is
	// unreachable -- which is precisely when request counts matter.
	Stats eapi.Stats

	// Commands lists the stable metric names of the commands this switch
	// collects, in issue order. Per switch, because collection is opt-in and
	// configurable individually.
	Commands []string

	specs []cmdSpec

	// tracker collapses repeated identical failures so a permanently broken
	// switch does not emit one log line per poll indefinitely.
	tracker repeatTracker

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
func NewStore(switches []config.SwitchConfig, defaults map[string]bool) (*Store, error) {
	s := &Store{
		switches: make(map[string]*SwitchData, len(switches)),
		order:    make([]string, 0, len(switches)),
	}
	for _, sw := range switches {
		label := sw.Label()
		if _, dup := s.switches[label]; dup {
			return nil, fmt.Errorf("config: duplicate switch label %q — names must be unique", label)
		}
		specs := commandsFor(sw.EffectiveCollect(defaults), sw.InterfaceScope)
		s.switches[label] = &SwitchData{
			Label:              label,
			CommandLastSuccess: make(map[string]time.Time),
			Commands:           commandNames(specs),
			specs:              specs,
		}
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

// cmdSpec binds a command to where its output is parsed and committed.
//
// name is the stable identifier used for metric labels; cli is what actually
// goes on the wire and may carry an interface scope. Keeping them separate
// stops switches with different scopes from each producing their own
// arista_command_success series for the same logical command.
type cmdSpec struct {
	name  string
	cli   string
	into  func(*snapshot) interface{}
	apply func(*snapshot, *SwitchData)
}

// The CLI commands arex issues. Exported so the writer can decide, per
// command, whether that command's data is still fresh enough to publish.
const (
	CmdVersion      = "show version"
	CmdProcessesTop = "show processes top once"
	CmdEnvTemp      = "show system environment temperature"
	CmdEnvPower     = "show system environment power"
	CmdEnvCooling   = "show system environment cooling"
	CmdInterfaces   = "show interfaces"
	CmdBGPSummary   = "show ip bgp summary vrf all"
	CmdTransceivers = "show interfaces transceiver detail"
	CmdPhy          = "show interfaces phy detail"
)

// versionCommand is collected unconditionally: arista_info is the identity
// metric every other series is joined against, and a scrape without it is
// not useful.
var versionCommand = cmdSpec{
	name: CmdVersion, cli: CmdVersion,
	into:  func(s *snapshot) interface{} { return &s.version },
	apply: func(s *snapshot, d *SwitchData) { d.Version = s.version },
}

// optionalCommands is keyed by the names in config.CollectKeys. Collection is
// opt-in, so a command absent from a switch's set is never issued.
var optionalCommands = map[string]cmdSpec{
	"processes": {
		name: CmdProcessesTop, cli: CmdProcessesTop,
		into:  func(s *snapshot) interface{} { return &s.processTop },
		apply: func(s *snapshot, d *SwitchData) { d.ProcessTop = s.processTop },
	},
	"temperature": {
		name: CmdEnvTemp, cli: CmdEnvTemp,
		into:  func(s *snapshot) interface{} { return &s.envTemp },
		apply: func(s *snapshot, d *SwitchData) { d.EnvTemp = s.envTemp },
	},
	"power": {
		name: CmdEnvPower, cli: CmdEnvPower,
		into:  func(s *snapshot) interface{} { return &s.envPower },
		apply: func(s *snapshot, d *SwitchData) { d.EnvPower = s.envPower },
	},
	"cooling": {
		name: CmdEnvCooling, cli: CmdEnvCooling,
		into:  func(s *snapshot) interface{} { return &s.envCooling },
		apply: func(s *snapshot, d *SwitchData) { d.EnvCooling = s.envCooling },
	},
	"interfaces": {
		name: CmdInterfaces, cli: CmdInterfaces,
		into:  func(s *snapshot) interface{} { return &s.interfaces },
		apply: func(s *snapshot, d *SwitchData) { d.Interfaces = s.interfaces },
	},
	"bgp": {
		name: CmdBGPSummary, cli: CmdBGPSummary,
		into:  func(s *snapshot) interface{} { return &s.bgp },
		apply: func(s *snapshot, d *SwitchData) { d.BGPSummary = s.bgp },
	},
	"transceiver": {
		name: CmdTransceivers, cli: CmdTransceivers,
		into:  func(s *snapshot) interface{} { return &s.optics },
		apply: func(s *snapshot, d *SwitchData) { d.Optics = s.optics },
	},
	"phy": {
		name: CmdPhy, cli: CmdPhy,
		into:  func(s *snapshot) interface{} { return &s.phy },
		apply: func(s *snapshot, d *SwitchData) { d.Phy = s.phy },
	},
}

// commandOrder fixes the sequence commands are issued in, so /metrics output
// and log lines are stable rather than following Go map iteration.
var commandOrder = []string{
	"processes", "temperature", "power", "cooling",
	"interfaces", "bgp", "transceiver", "phy",
}

// scoped inserts an interface scope into the commands that accept one.
//
// The scope is spliced verbatim; EOS is the only thing that knows what it
// means. A per-cage subinterface range returns only the interfaces that
// exist, so it survives breakout changes, while a bad cage fails the command
// outright -- which is the loud failure we want, since the alternative is
// silently monitoring nothing.
func scoped(name, scope string) string {
	if scope == "" {
		return name
	}
	switch name {
	case CmdInterfaces:
		return "show interfaces " + scope
	case CmdTransceivers:
		return "show interfaces " + scope + " transceiver detail"
	case CmdPhy:
		return "show interfaces " + scope + " phy detail"
	default:
		return name
	}
}

// commandsFor builds the command list for one switch.
func commandsFor(collect map[string]bool, scope string) []cmdSpec {
	out := make([]cmdSpec, 0, len(commandOrder)+1)
	out = append(out, versionCommand)
	for _, key := range commandOrder {
		if !collect[key] {
			continue
		}
		spec := optionalCommands[key]
		spec.cli = scoped(spec.name, scope)
		out = append(out, spec)
	}
	return out
}

// commandNames returns the stable metric names for a command list.
func commandNames(specs []cmdSpec) []string {
	out := make([]string, 0, len(specs))
	for _, c := range specs {
		out = append(out, c.name)
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
	specs := data.specs
	if specs == nil {
		// Direct construction, as in tests: collect everything.
		specs = commandsFor(allCollectKeys(), "")
	}

	var snap snapshot
	cmdErrs := make(map[string]error)
	ok := make([]bool, len(specs))

	raws, err := runBatch(client, specs)
	if err != nil && !worthRetryingIndividually(err) {
		// The switch is unreachable or refusing us outright. Retrying each
		// command would multiply the timeout by the command count for nothing.
		setError(data, specs, fmt.Errorf("collection failed: %w", err))
		return
	}
	if err != nil {
		for i, c := range specs {
			raw, cerr := runOne(client, c.cli)
			if cerr != nil {
				cmdErrs[c.name] = cerr
				continue
			}
			raws[i] = raw
		}
	}

	for i, c := range specs {
		if _, failed := cmdErrs[c.name]; failed {
			continue
		}
		if len(raws[i]) == 0 {
			cmdErrs[c.name] = fmt.Errorf("empty result")
			continue
		}
		if perr := json.Unmarshal(raws[i], c.into(&snap)); perr != nil {
			cmdErrs[c.name] = fmt.Errorf("parse: %w", perr)
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
	data.markAttempt()

	if succeeded == 0 {
		reason := err
		if reason == nil {
			reason = fmt.Errorf("no command returned usable output")
		}
		setError(data, specs, fmt.Errorf("collection failed: %w", reason))
		return
	}

	data.mu.Lock()
	defer data.mu.Unlock()

	if len(cmdErrs) > 0 {
		// One message for the whole picture, so an unchanging partial
		// failure is suppressed as a unit rather than per command.
		summary := fmt.Sprintf("%d of %d commands failed: %s",
			len(cmdErrs), len(specs), describeCmdErrors(specs, cmdErrs))
		if line := data.tracker.observe(summary, time.Now()); line != "" {
			slog.Warn("commands failed", "switch", data.Label, "detail", line)
		}
	} else if line := data.tracker.recovered(time.Now()); line != "" {
		slog.Info("switch recovered", "switch", data.Label, "detail", line)
	}
	now := time.Now()
	if data.CommandLastSuccess == nil {
		data.CommandLastSuccess = make(map[string]time.Time, len(specs))
	}
	for i, c := range specs {
		if ok[i] {
			c.apply(&snap, data)
			data.CommandLastSuccess[c.name] = now
		}
	}
	data.CommandErrors = cmdErrs
	data.ScrapeErr = nil
	data.LastSuccess = now
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
func runBatch(client Runner, specs []cmdSpec) ([]json.RawMessage, error) {
	n := len(specs)
	cli := make([]string, 0, n)
	for _, c := range specs {
		cli = append(cli, c.cli)
	}
	raws, err := client.Run(cli)
	if err != nil {
		return make([]json.RawMessage, n), err
	}
	if len(raws) != n {
		return make([]json.RawMessage, n), fmt.Errorf("%w: expected %d, got %d", errResultCount, n, len(raws))
	}
	return raws, nil
}

// allCollectKeys enables every optional command group.
func allCollectKeys() map[string]bool {
	out := make(map[string]bool, len(commandOrder))
	for _, k := range commandOrder {
		out[k] = true
	}
	return out
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

// pollSpacing is how far apart consecutive pollers start, when the fleet is
// small enough to allow it.
//
// A full nine-command poll of a 32-port leaf takes well under two seconds,
// so pollers need only enough separation to avoid overlapping. Spreading
// them across the whole interval instead would delay the last switch's first
// data by most of an interval to no purpose.
const pollSpacing = 3 * time.Second

// PollOffset returns the start delay for the i-th of n pollers.
//
// Offsets are deterministic and increasing rather than random. Random
// offsets only spread pollers on average: observed in the field, three
// switches drew 22.9s, 20.4s and 21.2s from a 30s interval and polled within
// 2.4 seconds of each other, which is the outcome the jitter existed to
// prevent. Assigning positions directly cannot cluster.
//
// The first poller starts immediately, so a restart produces data at once
// instead of leaving every switch blank for an offset. Spacing shrinks when
// the fleet is large enough that pollSpacing would push the last poller past
// one interval, which keeps every switch reporting within one interval of
// startup.
//
// A small variation remains so that two arex instances polling the same
// switches do not align. It is bounded well inside the spacing, so it can
// never reorder pollers back into a cluster.
func PollOffset(i, n int, interval time.Duration) time.Duration {
	if i <= 0 || n <= 1 || interval <= 0 {
		return 0
	}

	spacing := pollSpacing
	if maxSpacing := interval / time.Duration(n); spacing > maxSpacing {
		spacing = maxSpacing
	}

	offset := time.Duration(i) * spacing

	// Vary by up to a third of the spacing, so ordering is preserved.
	if jitter := spacing / 3; jitter > 0 {
		// Not security-sensitive: this decorrelates schedules, it is not a secret.
		offset += time.Duration(rand.Int64N(int64(2*jitter))) - jitter //nolint:gosec // scheduling
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= interval {
		offset %= interval
	}
	return offset
}

// PollLoop runs Collect on every tick until ctx is cancelled.
//
// The first poll is delayed by offset, which staggers pollers so a fleet is
// not polled all at once. Use PollOffset to compute it. Cancellation is
// honoured during that offset as well as between polls, so shutdown is not
// delayed by a poller that has not started yet.
//
// A poll already in flight is not interrupted: the eAPI request has its own
// timeout, and cancelling mid-request would gain nothing over letting it
// finish or time out.
func PollLoop(ctx context.Context, client Runner, data *SwitchData, interval, offset time.Duration) {
	if offset > 0 {
		slog.Info("starting poller", "switch", data.Label,
			"interval", interval.String(), "first_poll_in", offset.Round(time.Millisecond).String())
		timer := time.NewTimer(offset)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
	} else {
		slog.Info("starting poller", "switch", data.Label, "interval", interval.String())
	}

	Collect(client, data)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			Collect(client, data)
		case <-ctx.Done():
			slog.Info("poller stopped", "switch", data.Label)
			return
		}
	}
}

// markAttempt records that a poll finished, whatever the outcome.
func (d *SwitchData) markAttempt() {
	d.mu.Lock()
	d.LastAttempt = time.Now()
	d.mu.Unlock()
}

// describeCmdErrors renders per-command failures in a stable order, so an
// unchanged failure produces an unchanged message and stays suppressed.
func describeCmdErrors(specs []cmdSpec, cmdErrs map[string]error) string {
	parts := make([]string, 0, len(cmdErrs))
	for _, c := range specs {
		if err, ok := cmdErrs[c.name]; ok {
			parts = append(parts, fmt.Sprintf("%s (%v)", c.name, err))
		}
	}
	return strings.Join(parts, "; ")
}

// setError records a poll in which nothing at all was collected.
//
// Every command is marked failed so the writer can still emit
// arista_command_success for each: a missing series is not zero in
// Prometheus, and omitting them would exclude the most broken switch from
// any aggregate over that metric.
func setError(data *SwitchData, specs []cmdSpec, err error) {
	data.mu.Lock()
	defer data.mu.Unlock()

	data.ScrapeErr = err
	data.CommandErrors = make(map[string]error, len(specs))
	for _, c := range specs {
		data.CommandErrors[c.name] = err
	}

	if line := data.tracker.observe(err.Error(), time.Now()); line != "" {
		slog.Error("collection failed", "switch", data.Label, "detail", line)
	}
}
