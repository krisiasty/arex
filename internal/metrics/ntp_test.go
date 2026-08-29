package metrics

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
)

// renderNTP renders every fixture with show ntp associations replaced by the
// named capture, so both the synchronised and the degraded switch go through
// the same path a scrape takes.
func renderNTP(t *testing.T, fixture string) string {
	t.Helper()
	//nolint:gosec // the path is a fixture name chosen by the test, not input
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, collectAll(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	collector.Collect(overrideRunner{fixtureRunner{t}, collector.CmdNTPAssociations, string(body)}, sd)
	return gather(t, store, 90*time.Second)
}

// sampleFloat is sample, parsed. Comparing numbers rather than the exposition
// text keeps unit assertions independent of how Prometheus formats them.
func sampleFloat(t *testing.T, out, metric string, labelParts ...string) float64 {
	t.Helper()
	raw := sample(out, metric, labelParts...)
	if raw == "" {
		t.Fatalf("%s%v is not exported", metric, labelParts)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("%s = %q: %v", metric, raw, err)
	}
	return v
}

func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-12 }

// The selected peer is what "synchronised" means: ntpd's sys.peer tally is the
// source it is actually steering the clock to.
func TestNTPSelectedPeerDrivesSynchronised(t *testing.T) {
	out := renderNTP(t, "show_ntp_associations_synced.json")

	if got := sample(out, "arista_ntp_synchronised", ""); got != "1" {
		t.Errorf("synchronised = %q, want 1", got)
	}
	if got := sample(out, "arista_ntp_peer_selected", `peer="192.0.2.36"`); got != "1" {
		t.Errorf("peer_selected = %q, want 1", got)
	}
}

// A switch with no sys.peer is unsynchronised, and its timestamps are dated by
// a clock nothing is disciplining.
func TestNTPNoSelectedPeerMeansUnsynchronised(t *testing.T) {
	out := renderNTP(t, "show_ntp_associations.json")

	if got := sample(out, "arista_ntp_synchronised", ""); got != "0" {
		t.Errorf("synchronised = %q, want 0", got)
	}
	for _, peer := range []string{`peer="192.0.2.36"`, `peer="192.0.2.37"`} {
		if got := sample(out, "arista_ntp_peer_selected", peer); got != "0" {
			t.Errorf("peer_selected{%s} = %q, want 0", peer, got)
		}
	}
}

// The headline offset series exists only while a peer is selected. An
// unsynchronised switch has no meaningful offset, and emitting one anyway is
// how a dead server comes to look like a perfectly disciplined clock.
func TestNTPOffsetIsAbsentWhenUnsynchronised(t *testing.T) {
	if got := sample(renderNTP(t, "show_ntp_associations.json"), "arista_ntp_offset_seconds", ""); got != "" {
		t.Errorf("offset_seconds = %q, want no series at all", got)
	}
	out := renderNTP(t, "show_ntp_associations_synced.json")
	if got := sampleFloat(t, out, "arista_ntp_offset_seconds", ""); !closeTo(got, 0.242092/1000) {
		t.Errorf("offset_seconds = %v, want %v", got, 0.242092/1000)
	}
}

// EOS reports delay, offset and jitter in milliseconds. Prometheus convention
// is base units, so the conversion belongs here rather than in every query.
func TestNTPMillisecondsBecomeSeconds(t *testing.T) {
	out := renderNTP(t, "show_ntp_associations_synced.json")
	const peer = `peer="192.0.2.36"`

	for _, c := range []struct {
		metric string
		ms     float64
	}{
		{"arista_ntp_peer_offset_seconds", 0.242092},
		{"arista_ntp_peer_delay_seconds", 0.318305},
		{"arista_ntp_peer_jitter_seconds", 0.284624},
	} {
		if got := sampleFloat(t, out, c.metric, peer); !closeTo(got, c.ms/1000) {
			t.Errorf("%s = %v, want %v", c.metric, got, c.ms/1000)
		}
	}
}

// lastReceived is -2208988800 for a peer that has never answered: the NTP
// epoch, 1900-01-01, expressed in Unix seconds. Exported literally it is a
// timestamp 126 years old, which makes every age-based panel and alert
// meaningless.
func TestNTPNeverAnsweredPeerHasNoLastReceivedTimestamp(t *testing.T) {
	out := renderNTP(t, "show_ntp_associations.json")

	if got := sample(out, "arista_ntp_peer_last_received_timestamp_seconds", `peer="192.0.2.37"`); got != "" {
		t.Errorf("last_received for a peer that never answered = %q, want no series", got)
	}
	// The peer that has answered still reports one, or the guard is too broad.
	if got := sampleFloat(t, out, "arista_ntp_peer_last_received_timestamp_seconds",
		`peer="192.0.2.36"`); got < 1e9 {
		t.Errorf("last_received = %v, want a Unix epoch", got)
	}
}

// A peer that has never exchanged a packet reports offset 0.0 -- indistinguishable
// from a flawless clock. The reachability and selected series are what let an
// alert tell the two apart, so they have to be there.
func TestNTPDeadPeerIsDistinguishableFromAPerfectOne(t *testing.T) {
	out := renderNTP(t, "show_ntp_associations.json")
	const dead = `peer="192.0.2.37"`

	if got := sampleFloat(t, out, "arista_ntp_peer_offset_seconds", dead); got != 0 {
		t.Fatalf("fixture no longer carries the zero-offset trap: offset = %v", got)
	}
	if got := sample(out, "arista_ntp_peer_reachable_samples", dead); got != "0" {
		t.Errorf("reachable_samples = %q, want 0", got)
	}
	if got := sample(out, "arista_ntp_peer_selected", dead); got != "0" {
		t.Errorf("selected = %q, want 0", got)
	}
}

// reachabilityHistory is ntpd's reach register as eight booleans. Only the
// count is exported: whether index 0 is the oldest or the newest poll is not
// something the captures settle, and a count does not depend on knowing.
func TestNTPReachabilityCountsSuccesses(t *testing.T) {
	degraded := renderNTP(t, "show_ntp_associations.json")
	if got := sample(degraded, "arista_ntp_peer_reachable_samples", `peer="192.0.2.36"`); got != "1" {
		t.Errorf("recovering peer reachable_samples = %q, want 1", got)
	}
	synced := renderNTP(t, "show_ntp_associations_synced.json")
	if got := sample(synced, "arista_ntp_peer_reachable_samples", `peer="192.0.2.36"`); got != "8" {
		t.Errorf("healthy peer reachable_samples = %q, want 8", got)
	}
}

// Stratum 16 is NTP's "unsynchronised" sentinel, and refid .INIT. means no
// packet has ever come back. Both are exported as reported: they are the
// clearest signal that a configured server has never worked at all.
func TestNTPStratumAndRefidSurviveTheDeadPeer(t *testing.T) {
	out := renderNTP(t, "show_ntp_associations.json")

	if got := sample(out, "arista_ntp_peer_stratum", `peer="192.0.2.37"`); got != "16" {
		t.Errorf("dead peer stratum = %q, want 16", got)
	}
	if got := sample(out, "arista_ntp_peer_stratum", `peer="192.0.2.36"`); got != "1" {
		t.Errorf("GPS peer stratum = %q, want 1", got)
	}
	if sample(out, "arista_ntp_peer_info", `peer="192.0.2.37"`, `refid=".INIT."`) != "1" {
		t.Error(`peer_info should carry refid=".INIT." for a peer that never answered`)
	}
	if sample(out, "arista_ntp_peer_info", `peer="192.0.2.36"`, `refid="GPS"`, `peer_type="unicast"`) != "1" {
		t.Error("peer_info should carry the refid and peer type of a live peer")
	}
}

// ntpd backs its poll interval off to 64s while hunting and stretches it to
// 1024s once settled, so the interval is itself a signal.
func TestNTPPollIntervalIsExported(t *testing.T) {
	if got := sample(renderNTP(t, "show_ntp_associations.json"),
		"arista_ntp_peer_poll_interval_seconds", `peer="192.0.2.36"`); got != "64" {
		t.Errorf("hunting poll interval = %q, want 64", got)
	}
	if got := sample(renderNTP(t, "show_ntp_associations_synced.json"),
		"arista_ntp_peer_poll_interval_seconds", `peer="192.0.2.36"`); got != "1024" {
		t.Errorf("settled poll interval = %q, want 1024", got)
	}
}
