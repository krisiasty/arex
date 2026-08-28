package collector

import (
	"testing"
	"time"
)

// A switch that stays broken must not emit one log line per poll forever:
// at a 30s interval that is 2880 lines a day per switch, which buries real
// events in the field.
func TestRepeatedIdenticalErrorsAreCollapsed(t *testing.T) {
	var r repeatTracker
	base := time.Unix(1787900000, 0)
	msg := "unexpected HTTP status: 401 Unauthorized"

	logged := 0
	for i := range 30 {
		if r.observe(msg, base.Add(time.Duration(i)*30*time.Second)) != "" {
			logged++
		}
	}
	// First occurrence plus a periodic summary, not one line per poll.
	if logged >= 30 {
		t.Errorf("logged %d of 30 identical failures; expected suppression", logged)
	}
	if logged == 0 {
		t.Error("the first failure must always be logged")
	}
	t.Logf("%d lines for 30 identical failures", logged)
}

func TestFirstErrorIsAlwaysLogged(t *testing.T) {
	var r repeatTracker
	if got := r.observe("boom", time.Unix(0, 0)); got == "" {
		t.Error("first occurrence must be logged")
	}
}

// A summary must say how many times and for how long, or suppression just
// hides the problem.
func TestSummaryReportsCountAndDuration(t *testing.T) {
	var r repeatTracker
	base := time.Unix(1787900000, 0)
	var summary string
	for i := range 20 {
		if got := r.observe("boom", base.Add(time.Duration(i)*30*time.Second)); got != "" && i > 0 {
			summary = got
			break
		}
	}
	if summary == "" {
		t.Fatal("expected a summary line after repeated failures")
	}
	// The original message, an attempt count and an elapsed duration must
	// all survive suppression, or it hides the problem rather than the noise.
	for _, want := range []string{"boom", "still failing", "attempts over", "m0s"} {
		if !contains(summary, want) {
			t.Errorf("summary %q should mention %q", summary, want)
		}
	}
	if !contains(summary, "11") {
		t.Errorf("summary %q should report the cumulative attempt count", summary)
	}
	t.Logf("summary: %s", summary)
}

// A different error is a new event and must surface immediately.
func TestDifferentErrorLogsImmediately(t *testing.T) {
	var r repeatTracker
	base := time.Unix(1787900000, 0)
	r.observe("first", base)
	if got := r.observe("second", base.Add(30*time.Second)); got == "" {
		t.Error("a changed error message must be logged at once")
	}
}

// Recovery is worth exactly one line: without it a suppressed failure just
// goes quiet and you cannot tell recovery from continued suppression.
func TestRecoveryIsReportedOnce(t *testing.T) {
	var r repeatTracker
	base := time.Unix(1787900000, 0)
	for i := range 5 {
		r.observe("boom", base.Add(time.Duration(i)*30*time.Second))
	}
	got := r.recovered(base.Add(5 * 30 * time.Second))
	if got == "" {
		t.Fatal("recovery after failures must be logged")
	}
	if !contains(got, "5") {
		t.Errorf("recovery line should say how many failures preceded it: %q", got)
	}
	t.Logf("recovery: %s", got)
	if again := r.recovered(base.Add(6 * 30 * time.Second)); again != "" {
		t.Errorf("recovery must not repeat on every subsequent success: %q", again)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
