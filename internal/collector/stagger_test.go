package collector

import (
	"testing"
	"time"
)

const interval = 30 * time.Second

// The first poller starts at once, so a restart gives immediate feedback
// rather than leaving every switch blank for the length of an offset.
func TestFirstPollerStartsImmediately(t *testing.T) {
	if d := PollOffset(0, 5, interval); d != 0 {
		t.Errorf("first offset = %v, want 0", d)
	}
}

// A poll takes under two seconds, so pollers only need enough separation to
// avoid overlapping -- not to be spread across the whole interval, which
// would delay the last switch's first data for no benefit.
func TestSmallFleetIsSpacedByAFewSeconds(t *testing.T) {
	// Offsets are i*pollSpacing plus a variation of up to a third of the
	// spacing either way, so consecutive gaps lie in
	// [spacing - 2*jitter, spacing + 2*jitter]. Asserting a tighter band than
	// the design permits makes the test flaky rather than strict -- the
	// original bound of 1.5s-4.5s failed 40% of runs.
	jitter := pollSpacing / 3
	minGap, maxGap := pollSpacing-2*jitter, pollSpacing+2*jitter

	var mean time.Duration
	const trials = 200
	for range trials {
		var prev time.Duration
		for i := 1; i < 5; i++ {
			d := PollOffset(i, 5, interval)
			gap := d - prev
			if gap < minGap || gap > maxGap {
				t.Fatalf("offset[%d]-offset[%d] = %v, want within [%v, %v]", i, i-1, gap, minGap, maxGap)
			}
			prev = d
		}
		mean += prev / 4
	}
	// Over many draws the average spacing is the configured spacing; the
	// variation is symmetric and must not bias the schedule.
	mean /= trials
	if mean < pollSpacing-jitter/2 || mean > pollSpacing+jitter/2 {
		t.Errorf("mean spacing = %v, want about %v", mean, pollSpacing)
	}
	if last := PollOffset(4, 5, interval); last > 15*time.Second {
		t.Errorf("last offset = %v; a five-switch fleet should not wait this long", last)
	}
}

// Offsets must stay inside one interval however many switches there are, or
// the last poller would wait minutes before reporting anything.
func TestLargeFleetStaysWithinTheInterval(t *testing.T) {
	const n = 200
	for i := range n {
		if d := PollOffset(i, n, interval); d < 0 || d >= interval {
			t.Fatalf("offset[%d] = %v, outside [0, %v)", i, d, interval)
		}
	}
}

// With more switches than the spacing allows, the spacing shrinks so they
// still spread across the interval instead of piling into the first seconds.
func TestLargeFleetSpreadsAcrossTheInterval(t *testing.T) {
	const n = 60
	buckets := map[int]int{}
	for i := range n {
		buckets[int(PollOffset(i, n, interval).Seconds())]++
	}
	if len(buckets) < 20 {
		t.Errorf("offsets occupy only %d one-second buckets of 30; not spread", len(buckets))
	}
}

// Ordering is preserved, so the variation cannot reshuffle pollers into a
// cluster -- which is exactly what random offsets did in the field.
func TestOffsetsIncreaseMonotonically(t *testing.T) {
	for range 50 {
		prev := time.Duration(-1)
		for i := range 8 {
			d := PollOffset(i, 8, interval)
			if d <= prev && i > 0 {
				t.Fatalf("offset[%d] = %v not greater than previous %v", i, d, prev)
			}
			prev = d
		}
	}
}

// Some variation remains, so two arex instances polling the same switches do
// not align.
func TestOffsetsVaryBetweenRuns(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 100 {
		seen[PollOffset(3, 8, interval)] = true
	}
	if len(seen) < 10 {
		t.Errorf("offset[3] took only %d distinct values; no variation", len(seen))
	}
}

func TestSingleSwitchHasNoOffset(t *testing.T) {
	if d := PollOffset(0, 1, interval); d != 0 {
		t.Errorf("single switch offset = %v, want 0", d)
	}
}
