package metrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/eapi"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

// fixtureRunner serves the repo-root testdata fixtures as eAPI results.
type fixtureRunner struct{ t *testing.T }

var fixtureFile = map[string]string{
	"show version":                        "show_version.json",
	"show processes top once":             "show_processes_top_once.json",
	"show system environment temperature": "show_system_environment_temperature.json",
	"show system environment power":       "show_system_environment_power.json",
	"show system environment cooling":     "show_system_environment_cooling.json",
	"show ntp associations":               "show_ntp_associations.json",
	"show hardware capacity":              "show_hardware_capacity.json",
	"show interfaces":                     "show_interfaces.json",
	"show ip bgp summary vrf all":         "show_ip_bgp_summary_vrf_all.json",
	"show interfaces transceiver detail":  "show_interfaces_transceiver_detail.json",
	"show interfaces phy detail":          "show_interfaces_phy_detail.json",
}

func (f fixtureRunner) Run(cmds []string) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(cmds))
	for _, c := range cmds {
		name, ok := fixtureFile[c]
		if !ok {
			f.t.Fatalf("no fixture for command %q", c)
		}
		raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, nil
}

// render collects the fixtures into a store and returns the exposition text.
//
// The text comes from the registry rather than being written directly, so the
// tests exercise the same path a scrape does.
func render(t *testing.T, mutate func(*collector.SwitchData)) string {
	t.Helper()
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "https://192.0.2.33", Username: "u", Password: "p", Name: "sw1"},
	}, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	collector.Collect(fixtureRunner{t}, sd)
	if mutate != nil {
		mutate(sd)
	}
	return gather(t, store, 90*time.Second)
}

// gather renders a store through the registry, as a scrape would.
func gather(t *testing.T, store *collector.Store, stalenessLimit time.Duration) string {
	t.Helper()
	// Mirrors an unfiltered scrape, minus the Go and process collectors: those
	// are third-party and environment-dependent, so including them would make
	// the golden file unstable without testing anything arex owns. They are
	// covered by the handler tests and end to end.
	reg := prometheus.NewRegistry()
	reg.MustRegister(buildCollector{}, NewCollector(store, stalenessLimit))

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var b bytes.Buffer
	for _, mf := range mfs {
		if _, err := expfmt.MetricFamilyToText(&b, mf); err != nil {
			t.Fatalf("encode %s: %v", mf.GetName(), err)
		}
	}
	return b.String()
}

// sample returns the value of the first series of metric whose line contains
// every given label fragment. Fragments are matched independently, so tests
// do not depend on label ordering.
func sample(out, metric string, labelParts ...string) string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, metric+"{") {
			continue
		}
		match := true
		for _, p := range labelParts {
			if p != "" && !strings.Contains(line, p) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if i := strings.LastIndex(line, " "); i > 0 {
			return line[i+1:]
		}
	}
	return ""
}

// normalize pins the values that cannot be stable across environments, so
// the golden and determinism checks compare everything else exactly.
//
// The scrape age is wall-clock. Build info carries the VCS revision and Go
// version, which would otherwise make the golden file fail on every commit
// and every toolchain upgrade.
var (
	ageLine   = regexp.MustCompile(`(arista_scrape_age_seconds\{[^}]*\}) [^\n]+`)
	buildLine = regexp.MustCompile(`arex_build_info\{[^}]*\} 1`)
)

func normalize(out string) string {
	out = ageLine.ReplaceAllString(out, "$1 <age>")
	return buildLine.ReplaceAllString(out, "arex_build_info{<build>} 1")
}

func countSamples(out string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			n++
		}
	}
	return n
}

// --- staleness ---

// A transient error must not discard data that is still well inside
// stalenessLimit; serving last-known-good is the point of the proxy design.
func TestErrorDoesNotDiscardFreshData(t *testing.T) {
	healthy := countSamples(render(t, nil))

	withErr := render(t, func(sd *collector.SwitchData) {
		sd.ScrapeErr = errors.New("dial tcp: connection refused")
	})
	if got := countSamples(withErr); got < healthy {
		t.Errorf("samples dropped from %d to %d on a transient error with fresh data", healthy, got)
	}
	if sample(withErr, "arista_info", "") == "" {
		t.Error("arista_info absent: last-known-good data was discarded")
	}
	if v := sample(withErr, "arista_scrape_success", ""); v != "0" {
		t.Errorf("arista_scrape_success = %q, want 0", v)
	}
}

func TestPastStalenessLimitStopsEmitting(t *testing.T) {
	// Polls stopping ages the scrape and every command together; ageing
	// LastSuccess alone is a state that cannot occur, since it is set from the
	// same instant as the command times.
	out := render(t, func(sd *collector.SwitchData) {
		old := time.Now().Add(-200 * time.Second)
		sd.LastSuccess = old
		for cmd := range sd.CommandLastSuccess {
			sd.CommandLastSuccess[cmd] = old
		}
	})
	if sample(out, "arista_info", "") != "" {
		t.Error("arista_info present for data 200s old with a 90s staleness limit")
	}
	if v := sample(out, "arista_scrape_age_seconds", ""); v == "" {
		t.Error("scrape age must still be emitted when stale")
	}
}

func TestNeverCollectedEmitsOnlyScrapeMetrics(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	out := gather(t, store, 90*time.Second)

	if v := sample(out, "arista_scrape_age_seconds", ""); v != "-1" {
		t.Errorf("age = %q, want -1", v)
	}
	// Scrape health and arex's own request counters are fine to emit; what
	// must not appear is any data purporting to describe the switch.
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "arista_scrape_"),
			strings.HasPrefix(line, "arista_command_"),
			strings.HasPrefix(line, "arista_eapi_"),
			strings.HasPrefix(line, "arex_"):
		default:
			t.Errorf("uncollected switch emitted switch data: %s", line)
		}
	}
	// A switch that has never answered has not had a successful scrape,
	// even though no error has been recorded yet either.
	if v := sample(out, "arista_scrape_success", ""); v != "0" {
		t.Errorf("arista_scrape_success = %q for a never-contacted switch, want 0", v)
	}
}

// --- the six field-name bugs, at the exposition layer ---

func TestInterfaceErrorMetricsAreNotZero(t *testing.T) {
	out := render(t, nil)
	for _, tc := range []struct{ metric, want string }{
		{"arista_interface_in_errors_total", "4211"},
		{"arista_interface_out_errors_total", "19"},
		{"arista_interface_out_discards_total", "23"},
		{"arista_interface_link_status_changes_total", "431"},
	} {
		if got := sample(out, tc.metric, `interface="Ethernet2/1"`); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.metric, got, tc.want)
		}
	}
}

func TestInterfaceErrorDetailIsEmitted(t *testing.T) {
	out := render(t, nil)
	if got := sample(out, "arista_interface_in_errors_detail_total",
		`interface="Ethernet2/1"`, `cause="fcsErrors"`); got != "4190" {
		t.Errorf("fcsErrors = %q, want 4190", got)
	}
	if got := sample(out, "arista_interface_out_errors_detail_total",
		`interface="Ethernet2/1"`, `cause="txPause"`); got != "13" {
		t.Errorf("txPause = %q, want 13", got)
	}
}

func TestBGPPeerASNLabelAndStateChange(t *testing.T) {
	out := render(t, nil)
	if got := sample(out, "arista_bgp_peer_up", `peer="198.51.100.10"`); got != "1" {
		t.Errorf("peer_up = %q, want 1", got)
	}
	if !strings.Contains(out, `asn="4200000000"`) {
		t.Error(`asn label should be the string "4200000000"`)
	}
	ts := sample(out, "arista_bgp_peer_state_change_timestamp_seconds", `peer="198.51.100.10"`)
	if ts == "" {
		t.Fatal("state change timestamp missing")
	}
	if v, _ := strconv.ParseFloat(ts, 64); v < 1e9 {
		t.Errorf("state change timestamp = %q, want a Unix epoch", ts)
	}
}

// A down peer's transition time is the useful case, so it must be emitted.
func TestBGPDownPeerStillReportsStateChange(t *testing.T) {
	out := render(t, nil)
	if got := sample(out, "arista_bgp_peer_up", `peer="203.0.113.168"`); got != "0" {
		t.Errorf("down peer up = %q, want 0", got)
	}
	if sample(out, "arista_bgp_peer_state_change_timestamp_seconds", `peer="203.0.113.168"`) == "" {
		t.Error("down peer must still report when it changed state")
	}
}

func TestBGPNonDefaultVRFPeersAppear(t *testing.T) {
	out := render(t, nil)
	if sample(out, "arista_bgp_peer_up", `vrf="INTERNET"`) == "" {
		t.Error("no INTERNET vrf peers in output")
	}
}

// --- memory ---

func TestMemoryExposesAvailableAndStrictFree(t *testing.T) {
	out := render(t, nil)
	avail := sample(out, "arista_memory_available_bytes", "")
	free := sample(out, "arista_memory_free_bytes", "")
	if avail == "" || free == "" {
		t.Fatalf("available=%q free=%q, want both", avail, free)
	}
	a, _ := strconv.ParseFloat(avail, 64)
	f, _ := strconv.ParseFloat(free, 64)
	if !(a > f) {
		t.Errorf("available (%v) should exceed strict free (%v)", a, f)
	}
	if sample(out, "arista_memory_used_bytes", "") == "" {
		t.Error("arista_memory_used_bytes missing")
	}
}

// --- cooling / temperature gaps ---

func TestCoolingFansStatusIsEmitted(t *testing.T) {
	if got := sample(render(t, nil), "arista_cooling_fans_ok", ""); got != "1" {
		t.Errorf("arista_cooling_fans_ok = %q, want 1", got)
	}
}

// PSU sensors must come from the temperature command, which is the only
// source carrying thresholds and human-readable descriptions.
func TestPSUTemperatureCarriesThresholds(t *testing.T) {
	out := render(t, nil)
	if got := sample(out, "arista_psu_temperature_celsius", `sensor="TempSensorP1/1"`); got != "21" {
		t.Errorf("psu sensor temp = %q, want 21", got)
	}
	if got := sample(out, "arista_psu_temperature_overheat_threshold_celsius",
		`sensor="TempSensorP1/1"`); got != "65" {
		t.Errorf("psu overheat threshold = %q, want 65", got)
	}
	if !strings.Contains(out, `description="Inlet ambient sensor"`) {
		t.Error("PSU sensor description missing")
	}
}

// --- optics ---

func TestTransceiverReadingsEmitted(t *testing.T) {
	out := render(t, nil)
	if got := sample(out, "arista_transceiver_rx_power_dbm", `interface="Ethernet1/1"`); got == "" {
		t.Error("rx power missing")
	}
	if got := sample(out, "arista_transceiver_temperature_celsius",
		`interface="Ethernet1/1"`); got != "35.09765625" {
		t.Errorf("optic temperature = %q", got)
	}
	if !strings.Contains(out, `media_type="40GBASE-SR4"`) {
		t.Error("media_type label missing from info metric")
	}
}

// An absent limit must produce no series rather than a fabricated 0.
func TestTransceiverAbsentThresholdIsOmitted(t *testing.T) {
	out := render(t, nil)
	if strings.Contains(out, `parameter="totalRxPower"`) {
		t.Error("totalRxPower has no limits in EOS; it must emit no threshold series")
	}
	if got := sample(out, "arista_transceiver_tx_bias_threshold_milliamps",
		`interface="Ethernet1/1"`, `level="low_alarm"`); got != "0" {
		t.Errorf("txBias low_alarm = %q, want a real 0", got)
	}
}

// --- phy ---

func TestPhyFECMetricsForFECLinksOnly(t *testing.T) {
	out := render(t, nil)
	if got := sample(out, "arista_phy_fec_uncorrected_codewords",
		`interface="Ethernet4/1"`); got != "3" {
		t.Errorf("uncorrected codewords = %q, want 3", got)
	}
	if got := sample(out, "arista_phy_fec_uncorrected_codewords_changes_total",
		`interface="Ethernet4/1"`); got != "13" {
		t.Errorf("uncorrected changes = %q, want 13", got)
	}
	// 10G runs no FEC, so it must emit no FEC series at all.
	if sample(out, "arista_phy_fec_alignment_lock", `interface="Ethernet1/1"`) != "" {
		t.Error("10G link has no FEC; alignment lock must be absent")
	}
	// ...but it does have PCS block lock, which FEC replaces.
	if got := sample(out, "arista_phy_pcs_block_lock", `interface="Ethernet1/1"`); got != "1" {
		t.Errorf("10G pcs block lock = %q, want 1", got)
	}
	if sample(out, "arista_phy_pcs_block_lock", `interface="Ethernet4/1"`) != "" {
		t.Error("25G link has FEC; pcs block lock must be absent")
	}
}

func TestPhyPerLaneSymbolsOnlyWhenReported(t *testing.T) {
	out := render(t, nil)
	if sample(out, "arista_phy_fec_corrected_symbols", `interface="Ethernet29/1"`, `lane="3"`) == "" {
		t.Error("native 100G reports per-lane corrected symbols")
	}
	if sample(out, "arista_phy_fec_corrected_symbols", `interface="Ethernet4/1"`) != "" {
		t.Error("breakout lane reports no per-lane symbols")
	}
}

func TestPhyMacFaultsEmitted(t *testing.T) {
	out := render(t, nil)
	if got := sample(out, "arista_phy_mac_local_fault", `interface="Ethernet4/1"`); got != "0" {
		t.Errorf("local fault = %q, want 0", got)
	}
	if got := sample(out, "arista_phy_mac_local_fault_changes_total",
		`interface="Ethernet4/1"`); got != "62" {
		t.Errorf("local fault changes = %q, want 62", got)
	}
}

// Serdes was excluded by design; make sure it stays out.
func TestSerdesIsNotEmitted(t *testing.T) {
	out := render(t, nil)
	for _, banned := range []string{"eye_left", "eye_right", "dfe_tap", "tx_tap", "vga", "peaking"} {
		if strings.Contains(out, banned) {
			t.Errorf("serdes metric %q leaked into output", banned)
		}
	}
}

// --- per-command visibility ---

func TestCommandSuccessMetricPerCommand(t *testing.T) {
	out := render(t, nil)
	if got := sample(out, "arista_command_success", `command="show version"`); got != "1" {
		t.Errorf("command success = %q, want 1", got)
	}
	// render enables every group, so one series per optional command plus
	// show version, which is always collected.
	want := len(config.CollectKeys) + 1
	if n := strings.Count(out, "arista_command_success{"); n != want {
		t.Errorf("%d arista_command_success series, want %d", n, want)
	}
}

// --- exposition hygiene ---

var labelValue = regexp.MustCompile(`="((?:[^"\\]|\\.)*)"`)

// Prometheus defines exactly three escapes in a label value: \\ \n \".
func TestLabelValuesUseOnlyValidEscapes(t *testing.T) {
	out := render(t, func(sd *collector.SwitchData) {
		ifs := sd.Interfaces.Interfaces
		i := ifs["Ethernet1/1"]
		i.Description = "tab\there \"quoted\" back\\slash and\nnewline"
		ifs["Ethernet1/1"] = i
	})
	for _, m := range labelValue.FindAllStringSubmatch(out, -1) {
		for i := 0; i < len(m[1]); i++ {
			if m[1][i] != '\\' {
				continue
			}
			if i+1 >= len(m[1]) {
				t.Errorf("trailing backslash in label value %q", m[1])
				break
			}
			switch m[1][i+1] {
			case '\\', 'n', '"':
				i++
			default:
				t.Errorf("invalid escape \\%c in label value %q", m[1][i+1], m[1])
			}
		}
	}
	if strings.Contains(out, `\t`) {
		t.Error(`\t is not a valid Prometheus label escape`)
	}
}

func TestEveryMetricHasHelpAndType(t *testing.T) {
	out := render(t, nil)
	declared := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "# TYPE ") {
			declared[strings.Fields(l)[2]] = true
		}
	}
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		name := l
		if i := strings.IndexAny(l, "{ "); i > 0 {
			name = l[:i]
		}
		seen[name] = true
	}
	for name := range seen {
		if !declared[name] {
			t.Errorf("metric %s emitted with no # TYPE line", name)
		}
	}
}

// --- golden exposition ---

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/expected_metrics.txt")

const goldenPath = "../../testdata/expected_metrics.txt"

// The full rendered output is checked in, so any change to any metric shows
// up as a reviewable diff rather than passing unnoticed.
func TestGoldenExposition(t *testing.T) {
	got := normalize(render(t, nil))

	if *updateGolden {
		// Golden fixture, deliberately world-readable like the rest of testdata.
		err := os.WriteFile(goldenPath, []byte(got), 0o644) //nolint:gosec // a golden fixture, world-readable like the rest of testdata
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d lines)", goldenPath, strings.Count(got, "\n"))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%v (regenerate with: go test ./internal/metrics/ -update-golden)", err)
	}
	if got == string(want) {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		g, w := "", ""
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			t.Errorf("line %d:\n  got:  %s\n  want: %s", i+1, g, w)
			if i > 0 {
				break
			}
		}
	}
	t.Error("output differs from golden; if intended, rerun with -update-golden")
}

// Output must be byte-stable across renders: Go randomises map iteration, so
// unsorted loops would reorder series on every scrape.
func TestOutputIsDeterministic(t *testing.T) {
	first := normalize(render(t, nil))
	for i := range 5 {
		if got := normalize(render(t, nil)); got != first {
			for j, line := range strings.Split(got, "\n") {
				w := strings.Split(first, "\n")
				if j < len(w) && line != w[j] {
					t.Fatalf("render %d differs at line %d:\n  %s\n  %s", i+2, j+1, line, w[j])
				}
			}
			t.Fatalf("render %d differs in length", i+2)
		}
	}
}

// Prometheus rejects a scrape containing the same series twice.
func TestNoDuplicateSeries(t *testing.T) {
	seen := map[string]int{}
	for _, line := range strings.Split(render(t, nil), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key := line
		if i := strings.LastIndex(line, " "); i > 0 {
			key = line[:i]
		}
		seen[key]++
	}
	for key, n := range seen {
		if n > 1 {
			t.Errorf("series emitted %d times: %s", n, key)
		}
	}
}

// A HELP or TYPE line repeated for one metric name is a parse error.
func TestNoDuplicateHelpOrType(t *testing.T) {
	help, typ := map[string]int{}, map[string]int{}
	for _, l := range strings.Split(render(t, nil), "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || f[0] != "#" {
			continue
		}
		switch f[1] {
		case "HELP":
			help[f[2]]++
		case "TYPE":
			typ[f[2]]++
		}
	}
	for name, n := range help {
		if n > 1 {
			t.Errorf("# HELP %s declared %d times", name, n)
		}
	}
	for name, n := range typ {
		if n > 1 {
			t.Errorf("# TYPE %s declared %d times", name, n)
		}
	}
}

// FEC encoding and codeword size are configuration, not measurements. On the
// value series they would change series identity whenever a link's FEC
// config changes, breaking rate() continuity, and they are redundant on
// nine series per interface.
func TestFECConfigLivesOnAnInfoMetric(t *testing.T) {
	out := render(t, nil)

	if sample(out, "arista_phy_fec_info", `interface="Ethernet4/1"`) != "1" {
		t.Error("expected an arista_phy_fec_info series")
	}
	if !strings.Contains(out, `encoding="reedSolomon"`) {
		t.Error("encoding label missing from the info metric")
	}
	for _, m := range []string{
		"arista_phy_fec_corrected_codewords",
		"arista_phy_fec_uncorrected_codewords",
		"arista_phy_fec_alignment_lock",
	} {
		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, m+"{") {
				continue
			}
			if strings.Contains(line, "encoding=") || strings.Contains(line, "codeword_size=") {
				t.Errorf("%s must not carry FEC config labels:\n  %s", m, line)
			}
		}
	}
}

// Scrape health is metadata, not switch data: it must be emitted even when
// nothing has ever been collected.
func TestCommandSuccessEmittedForNeverCollectedSwitch(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	// Simulate a poll that failed outright, as a 401 does.
	collector.Collect(failingRunner{}, sd)

	out := gather(t, store, 90*time.Second)

	if v := sample(out, "arista_scrape_success", ""); v != "0" {
		t.Errorf("scrape_success = %q, want 0", v)
	}
	n := strings.Count(out, "arista_command_success{")
	if n != len(sd.Commands) {
		t.Errorf("%d command_success series, want %d", n, len(sd.Commands))
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "arista_command_success{") && !strings.HasSuffix(line, " 0") {
			t.Errorf("every command should report 0 after a total failure: %s", line)
		}
	}
}

type failingRunner struct{}

func (failingRunner) Run([]string) ([]json.RawMessage, error) {
	return nil, errors.New("unexpected HTTP status: 401 Unauthorized")
}

// staleRunner fails one nominated command and serves fixtures for the rest.
type staleRunner struct {
	t    *testing.T
	fail string
}

func (s staleRunner) Run(cmds []string) ([]json.RawMessage, error) {
	for _, c := range cmds {
		if c == s.fail {
			// A switch rejecting one command answers with a JSON-RPC error,
			// which drives the per-command retry path.
			if len(cmds) > 1 {
				return nil, &eapi.CommandError{Code: 1002, Message: "invalid command"}
			}
			return nil, &eapi.CommandError{Code: 1002, Message: "invalid command"}
		}
	}
	return fixtureRunner{s.t}.Run(cmds)
}

// One command failing must not let the other eight mask it: stalenessLimit
// has to bound each command's data, not just the scrape as a whole.
// Otherwise a single working command keeps LastSuccess fresh for ever while
// arex serves arbitrarily old data for everything else.
func TestPerCommandStalenessSuppressesOneCommand(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]

	// A healthy poll, then polls where BGP is rejected.
	collector.Collect(fixtureRunner{t}, sd)
	collector.Collect(staleRunner{t, "show ip bgp summary vrf all"}, sd)

	fresh := gather(t, store, 90*time.Second)

	// Immediately after, BGP data is still inside stalenessLimit.
	if sample(fresh, "arista_bgp_peer_up", `peer="198.51.100.10"`) == "" {
		t.Error("BGP data should survive briefly after one failed poll")
	}

	// Age that command's last success past the limit.
	sd.CommandLastSuccess[collector.CmdBGPSummary] = time.Now().Add(-200 * time.Second)

	out := gather(t, store, 90*time.Second)

	if sample(out, "arista_bgp_peer_up", "") != "" {
		t.Error("BGP data older than stalenessLimit must be suppressed")
	}
	// Everything else must be unaffected.
	if sample(out, "arista_info", "") == "" {
		t.Error("show version data should still be served")
	}
	if sample(out, "arista_interface_link_up", `interface="Ethernet1/1"`) == "" {
		t.Error("interface data should still be served")
	}
	// The scrape itself is still succeeding, and the failure is visible.
	if v := sample(out, "arista_scrape_success", ""); v != "1" {
		t.Errorf("scrape_success = %q, want 1: other commands are working", v)
	}
	if v := sample(out, "arista_command_success", `command="show ip bgp summary vrf all"`); v != "0" {
		t.Errorf("command_success for bgp = %q, want 0", v)
	}
}

// The counter exists so retry amplification is visible from Prometheus
// rather than only from logs: an auth failure must cost one request per
// poll, and if the retry classification regressed this would jump to one
// per command.
func TestEAPIRequestMetricsExposed(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	collector.Collect(fixtureRunner{t}, sd)

	sd.Stats.Record(eapi.RequestKey{Outcome: eapi.OutcomeSuccess, Attempt: eapi.AttemptBatch}, 486000, time.Second)
	sd.Stats.Record(eapi.RequestKey{Outcome: eapi.OutcomeHTTPError, Attempt: eapi.AttemptBatch}, 0, time.Millisecond)

	out := gather(t, store, 90*time.Second)

	if got := sample(out, "arista_eapi_requests_total",
		`outcome="success"`, `attempt="batch"`); got != "1" {
		t.Errorf("successful batch requests = %q, want 1", got)
	}
	if got := sample(out, "arista_eapi_requests_total",
		`outcome="http_error"`, `attempt="batch"`); got != "1" {
		t.Errorf("http_error requests = %q, want 1", got)
	}
	if sample(out, "arista_eapi_response_bytes_total", "") == "" {
		t.Error("response bytes counter missing")
	}
	if sample(out, "arista_eapi_request_duration_seconds_total", "") == "" {
		t.Error("duration counter missing")
	}
}

// Self-metrics describe arex, not the switch, so they must survive a switch
// that is unreachable -- that is exactly when request counts matter.
func TestEAPIRequestMetricsSurviveTotalFailure(t *testing.T) {
	store, _ := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, collectAll(), 30*time.Second)
	sd := store.All()[0]
	collector.Collect(failingRunner{}, sd)
	sd.Stats.Record(eapi.RequestKey{Outcome: eapi.OutcomeHTTPError, Attempt: eapi.AttemptBatch}, 0, time.Millisecond)

	out := gather(t, store, 90*time.Second)

	if sample(out, "arista_eapi_requests_total", `outcome="http_error"`) == "" {
		t.Error("request counters must be emitted for an unreachable switch")
	}
	if v := sample(out, "arista_scrape_success", ""); v != "0" {
		t.Errorf("scrape_success = %q, want 0", v)
	}
}

// collectAll enables every optional command group, which is what most of
// these tests want: they are about rendering, not about collection control.
func collectAll() map[string]config.ModuleConfig {
	m := make(map[string]config.ModuleConfig, len(config.CollectKeys))
	for _, k := range config.CollectKeys {
		m[k] = config.ModuleConfig{Enabled: true}
	}
	return m
}
