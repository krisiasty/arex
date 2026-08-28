package eapi

import (
	"sync"
	"time"
)

// Outcome classifies how an eAPI request ended.
//
// The distinction between an HTTP-level rejection and an eAPI-level one
// matters: a 200 carrying a JSON-RPC error means the switch answered and
// disliked a command, which is worth retrying per command, whereas a 401 or
// a timeout applies to everything and is not.
type Outcome string

// The outcomes an eAPI request can have.
const (
	OutcomeSuccess        Outcome = "success"
	OutcomeEAPIError      Outcome = "eapi_error"
	OutcomeHTTPError      Outcome = "http_error"
	OutcomeTransportError Outcome = "transport_error"
)

// Attempt distinguishes the single batch arex normally sends from the
// per-command retries it falls back to.
//
// This is the label that makes retry amplification visible. A switch
// rejecting authentication should show exactly one request per poll; if the
// retry classification ever regressed, the count would jump to one per
// command -- nine failed authentications per poll instead of one, which is
// how account lockouts happen.
type Attempt string

// The kinds of attempt a request can be part of.
const (
	AttemptBatch Attempt = "batch"
	AttemptRetry Attempt = "retry"
)

// RequestKey identifies one counter series.
type RequestKey struct {
	Outcome Outcome
	Attempt Attempt
}

// Stats accumulates eAPI request statistics for one switch. The zero value
// is ready to use and safe for concurrent use.
type Stats struct {
	mu        sync.Mutex
	requests  map[RequestKey]uint64
	respBytes uint64
	duration  time.Duration
	reloads   map[Reload]uint64
}

// Reload classifies an attempt to re-read a rotated credential.
type Reload string

// The outcomes of a credential reload.
const (
	// ReloadRotated means the secret on disk had changed and was adopted.
	ReloadRotated Reload = "rotated"
	// ReloadUnchanged means the credential is simply being rejected.
	ReloadUnchanged Reload = "unchanged"
	// ReloadFailed means the file could not be read.
	ReloadFailed Reload = "failed"
)

// StatsSnapshot is a consistent copy, so the renderer never holds the lock
// while writing output.
type StatsSnapshot struct {
	Requests        map[RequestKey]uint64
	ResponseBytes   uint64
	DurationSeconds float64
	Reloads         map[Reload]uint64
}

// Record accounts for one request. Called by the client on every path,
// including failures, since the point is to count requests actually made.
func (s *Stats) Record(key RequestKey, respBytes int64, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests == nil {
		s.requests = make(map[RequestKey]uint64, 8)
	}
	s.requests[key]++
	if respBytes > 0 {
		s.respBytes += uint64(respBytes)
	}
	s.duration += d
}

// RecordReload accounts for one credential reload attempt.
func (s *Stats) RecordReload(r Reload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reloads == nil {
		s.reloads = make(map[Reload]uint64, 3)
	}
	s.reloads[r]++
}

// Snapshot returns a copy of the current counters.
func (s *Stats) Snapshot() StatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := StatsSnapshot{
		Requests:        make(map[RequestKey]uint64, len(s.requests)),
		ResponseBytes:   s.respBytes,
		DurationSeconds: s.duration.Seconds(),
	}
	for k, v := range s.requests {
		out.Requests[k] = v
	}
	out.Reloads = make(map[Reload]uint64, len(s.reloads))
	for k, v := range s.reloads {
		out.Reloads[k] = v
	}
	return out
}

// WithStats attaches a Stats to the client. Without one, nothing is recorded.
func WithStats(s *Stats) Option {
	return func(c *Client) { c.stats = s }
}

// attemptFor classifies a call by its size.
//
// This is coupling to how the collector calls Run: every command goes out in
// a single batch, and requests are only ever issued one command at a time
// when falling back to per-command retries. A one-command call is therefore
// a retry. That holds because the command set is fixed and larger than one;
// if it ever shrank to a single command, every request would be labelled a
// retry and this would need to become explicit.
func attemptFor(cmds []string) Attempt {
	if len(cmds) == 1 {
		return AttemptRetry
	}
	return AttemptBatch
}
