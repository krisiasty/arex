package eapi

import (
	"net/http"
	"testing"
	"time"
)

func TestStatsCountSuccessfulRequests(t *testing.T) {
	srv := eapiServer(t, okBody, http.StatusOK)
	var stats Stats

	c, err := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true}, WithStats(&stats))
	if err != nil {
		t.Fatal(err)
	}
	// Multiple commands: that is what arex sends normally, and it is how a
	// batch is distinguished from a per-command retry.
	for range 3 {
		if _, err := c.Run([]string{"show version", "show interfaces"}); err != nil {
			t.Fatal(err)
		}
	}

	snap := stats.Snapshot()
	if got := snap.Requests[RequestKey{Outcome: OutcomeSuccess, Attempt: AttemptBatch}]; got != 3 {
		t.Errorf("successful batch requests = %d, want 3", got)
	}
	if snap.ResponseBytes == 0 {
		t.Error("response bytes should accumulate")
	}
	if snap.DurationSeconds <= 0 {
		t.Error("duration should accumulate")
	}
}

// The point of the metric: an HTTP-level rejection must cost exactly one
// request per poll. If the retry classification ever regressed, this count
// would jump to the number of commands.
func TestStatsDistinguishOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		outcome Outcome
	}{
		{"http error", http.StatusUnauthorized, "", OutcomeHTTPError},
		{
			"eapi error", http.StatusOK,
			`{"jsonrpc":"2.0","id":1,"error":{"code":1002,"message":"invalid command"}}`,
			OutcomeEAPIError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := eapiServer(t, tc.body, tc.status)
			var stats Stats
			c, _ := NewClient(srv.URL, "u", "p", 5*time.Second,
				TLSOptions{SkipVerify: true}, WithStats(&stats))
			_, _ = c.Run([]string{"show version", "show interfaces"})

			snap := stats.Snapshot()
			if got := snap.Requests[RequestKey{Outcome: tc.outcome, Attempt: AttemptBatch}]; got != 1 {
				t.Errorf("%s count = %d, want 1 (all: %v)", tc.outcome, got, snap.Requests)
			}
		})
	}
}

func TestStatsRecordTransportFailures(t *testing.T) {
	var stats Stats
	c, _ := NewClient("https://192.0.2.99", "u", "p", 200*time.Millisecond,
		TLSOptions{SkipVerify: true}, WithStats(&stats))
	_, _ = c.Run([]string{"show version", "show interfaces"})

	snap := stats.Snapshot()
	if got := snap.Requests[RequestKey{Outcome: OutcomeTransportError, Attempt: AttemptBatch}]; got != 1 {
		t.Errorf("transport failures = %d, want 1", got)
	}
	// A failed request has no response body, but the attempt still took time.
	if snap.DurationSeconds <= 0 {
		t.Error("a timed-out request still consumed time")
	}
}

// A single-command call is a retry: that is what makes amplification visible.
func TestStatsLabelSingleCommandCallsAsRetries(t *testing.T) {
	srv := eapiServer(t, okBody, http.StatusOK)
	var stats Stats
	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true}, WithStats(&stats))

	_, _ = c.Run([]string{"show version", "show interfaces"})
	_, _ = c.Run([]string{"show version"})

	snap := stats.Snapshot()
	if got := snap.Requests[RequestKey{Outcome: OutcomeSuccess, Attempt: AttemptBatch}]; got != 1 {
		t.Errorf("batch = %d, want 1", got)
	}
	if got := snap.Requests[RequestKey{Outcome: OutcomeSuccess, Attempt: AttemptRetry}]; got != 1 {
		t.Errorf("retry = %d, want 1", got)
	}
}

func TestStatsAreSafeForConcurrentUse(t *testing.T) {
	srv := eapiServer(t, okBody, http.StatusOK)
	var stats Stats
	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true}, WithStats(&stats))

	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 20 {
				_, _ = c.Run([]string{"show version"})
			}
		}()
	}
	for range 8 {
		<-done
	}
	if got := stats.Snapshot().Requests[RequestKey{Outcome: OutcomeSuccess, Attempt: AttemptRetry}]; got != 160 {
		t.Errorf("requests = %d, want 160", got)
	}
}

// Nothing should be recorded when no Stats is attached.
func TestStatsOptionalWhenNotConfigured(t *testing.T) {
	srv := eapiServer(t, okBody, http.StatusOK)
	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true})
	if _, err := c.Run([]string{"show version"}); err != nil {
		t.Fatal(err)
	}
}
