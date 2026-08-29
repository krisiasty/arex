package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
)

// capacityFixture is the raw capture, with each row left exactly as EOS wrote
// it -- rows are carried as RawMessage so a field arex does not parse still
// survives into whatever the test feeds back.
func capacityFixture(t *testing.T) []json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "show_hardware_capacity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tables []json.RawMessage `json:"tables"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Tables
}

// renderCapacityRows renders the fixtures with the capacity rows replaced by
// the given ones, so a test can reorder them.
func renderCapacityRows(t *testing.T, rows []json.RawMessage) string {
	t.Helper()
	body, err := json.Marshal(struct {
		Tables []json.RawMessage `json:"tables"`
	}{rows})
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
	collector.Collect(overrideRunner{fixtureRunner{t}, collector.CmdHardwareCapacity, string(body)}, sd)
	return gather(t, store, 90*time.Second)
}

// EOS returns these rows in no particular order -- three captures from three
// switches each began with a different one. Anything that depends on the order
// is depending on luck, so reversing the array must change nothing.
func TestCapacityRowOrderDoesNotMatter(t *testing.T) {
	rows := capacityFixture(t)
	reversed := slices.Clone(rows)
	slices.Reverse(reversed)

	forward := capacityLines(renderCapacityRows(t, rows))
	backward := capacityLines(renderCapacityRows(t, reversed))

	if len(forward) != len(backward) {
		t.Fatalf("%d series forward, %d reversed", len(forward), len(backward))
	}
	for i := range forward {
		if forward[i] != backward[i] {
			t.Fatalf("reordering the rows changed series %d:\n  forward:  %s\n  reversed: %s",
				i, forward[i], backward[i])
		}
	}
	if len(forward) == 0 {
		t.Fatal("no capacity series were rendered at all")
	}
}

// capacityLines is the capacity samples alone. The whole exposition cannot be
// compared: arista_scrape_age_seconds is wall-clock and differs between any two
// renderings, whatever the input.
func capacityLines(out string) []string {
	var lines []string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "arista_hardware_capacity") {
			lines = append(lines, line)
		}
	}
	return lines
}

// A table is not a unique key: MMU_MCAST reports MmuReplHead once per chip and
// once with no chip at all, with identical values. Both have to survive as
// separate series, and a collision would fail the whole scrape rather than one
// metric.
func TestCapacityIsKeyedByTableFeatureAndChip(t *testing.T) {
	out := renderCapacityRows(t, capacityFixture(t))

	for _, chip := range []string{`chip="Linecard0/0"`, `chip=""`} {
		got := sample(out, "arista_hardware_capacity_used",
			`table="MMU_MCAST"`, `feature="MmuReplHead"`, chip)
		if got != "641" {
			t.Errorf("MmuReplHead %s used = %q, want 641", chip, got)
		}
	}
}

// The feature="" row is usually the sum of the table's feature rows, but not
// always: NextHop reports 280 against features summing to 281, and the two Ecmp
// tables use it for a different resource entirely, with its own limit. So the
// row is exported as EOS reports it and nothing is derived from the parts.
func TestCapacityRollupRowsAreNotDerived(t *testing.T) {
	out := renderCapacityRows(t, capacityFixture(t))

	if got := sample(out, "arista_hardware_capacity_used", `table="NextHop"`, `feature=""`); got != "280" {
		t.Errorf("NextHop rollup used = %q, want 280 as reported, not the 281 its features sum to", got)
	}
	// OverlayEcmp's "rollup" has a different limit from its Groups row, which
	// is what shows the two are separate resources rather than a whole and a
	// part.
	roll := sample(out, "arista_hardware_capacity_limit", `table="OverlayEcmp"`, `feature=""`)
	groups := sample(out, "arista_hardware_capacity_limit", `table="OverlayEcmp"`, `feature="Groups"`)
	if roll == groups {
		t.Errorf("OverlayEcmp limits are both %q; the fixture no longer shows the two are distinct", roll)
	}
}

// free is the shared pool's remaining, not this feature's. Host/V6Hosts has
// used 0 against a limit of 147455 but only 147208 free, because V4Hosts took
// the difference. limit - used would report 147455 and overstate the headroom.
func TestCapacityFreeIsThePoolNotTheRow(t *testing.T) {
	out := renderCapacityRows(t, capacityFixture(t))
	const v6 = `feature="V6Hosts"`

	used := sample(out, "arista_hardware_capacity_used", `table="Host"`, v6)
	free := sample(out, "arista_hardware_capacity_free", `table="Host"`, v6)
	limit := sample(out, "arista_hardware_capacity_limit", `table="Host"`, v6)

	if used != "0" || free != "147208" || limit != "147455" {
		t.Errorf("Host/V6Hosts used=%q free=%q limit=%q, want 0 / 147208 / 147455", used, free, limit)
	}
}

// The watermark is the peak since boot, and it is what makes a slow poll
// interval safe: a spike between two polls still shows up here.
func TestCapacityHighWatermarkOutlivesTheSpike(t *testing.T) {
	out := renderCapacityRows(t, capacityFixture(t))
	const head = `feature="MmuReplHead"`

	used := sample(out, "arista_hardware_capacity_used", `table="MMU_MCAST"`, head, `chip=""`)
	peak := sample(out, "arista_hardware_capacity_high_watermark", `table="MMU_MCAST"`, head, `chip=""`)
	if used != "641" || peak != "856" {
		t.Errorf("MmuReplHead used=%q peak=%q, want 641 / 856", used, peak)
	}
}

// sharedFeatures says what is consuming a slice, and the assignment differs
// between switches, so it cannot be documented instead of exported. Only rows
// that have any get a series.
func TestCapacityInfoCarriesSharedFeatures(t *testing.T) {
	out := renderCapacityRows(t, capacityFixture(t))

	if got := sample(out, "arista_hardware_capacity_info", `table="IFP"`, `feature="Slice-1"`); got != "1" {
		t.Errorf("IFP/Slice-1 info = %q, want 1", got)
	}
	if !strings.Contains(out, `shared_features="IFP Group L2 Ctrl Arp Inspection,`+
		`IFP Group L2 Ctrl Dot1x Mba,IFP Group L2 Ctrl L3 Routing"`) {
		t.Error("IFP/Slice-1 should carry all three of its shared features, comma-joined")
	}
	// A row with an empty sharedFeatures array gets no series at all rather
	// than one with an empty label.
	if got := sample(out, "arista_hardware_capacity_info", `table="IFP"`, `feature="Slice-8"`); got != "" {
		t.Errorf("IFP/Slice-8 has no shared features but produced info = %q", got)
	}
}

// usedPercent is a truncated integer: MmuReplHead sits at 0.978% and EOS
// reports 0. Exporting it would hand people a series that reads zero for
// everything below one percent, when used/limit is right there.
func TestCapacityDoesNotExportEOSPercentageOrCommitted(t *testing.T) {
	out := renderCapacityRows(t, capacityFixture(t))

	for _, name := range []string{
		"arista_hardware_capacity_used_percent",
		"arista_hardware_capacity_committed",
	} {
		if strings.Contains(out, name) {
			t.Errorf("%s should not be exported", name)
		}
	}

	// The resolution the ratio gives instead.
	used, _ := strconv.ParseFloat(sample(out, "arista_hardware_capacity_used",
		`table="MMU_MCAST"`, `feature="MmuReplHead"`, `chip=""`), 64)
	limit, _ := strconv.ParseFloat(sample(out, "arista_hardware_capacity_limit",
		`table="MMU_MCAST"`, `feature="MmuReplHead"`, `chip=""`), 64)
	if ratio := 100 * used / limit; ratio < 0.9 || ratio > 1.1 {
		t.Errorf("used/limit = %.3f%%, want about 0.978%%", ratio)
	}
}

// The tightest row is the one that matters: an ACL that does not fit in a
// slice fails even though the aggregate has room. IFP is 7% overall and 37% in
// two of its slices.
func TestCapacityExportsSlicesNotJustTheAggregate(t *testing.T) {
	out := renderCapacityRows(t, capacityFixture(t))

	for _, c := range []struct{ feature, used, limit string }{
		{"", "707", "9216"},
		{"Slice-1", "287", "768"},
		{"Slice-2", "287", "768"},
	} {
		f := `feature="` + c.feature + `"`
		if got := sample(out, "arista_hardware_capacity_used", `table="IFP"`, f); got != c.used {
			t.Errorf("IFP/%q used = %q, want %s", c.feature, got, c.used)
		}
		if got := sample(out, "arista_hardware_capacity_limit", `table="IFP"`, f); got != c.limit {
			t.Errorf("IFP/%q limit = %q, want %s", c.feature, got, c.limit)
		}
	}
}
