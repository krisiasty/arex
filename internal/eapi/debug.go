package eapi

import (
	"fmt"
	"io"
	"log"
	"net/http/httptrace"
	"strings"
	"time"
)

// Option configures a Client. Options are variadic so adding one does not
// disturb existing call sites.
type Option func(*Client)

// WithDebug logs one line per eAPI request, tagged with name.
//
// Credentials are never included: the Authorization header is not logged,
// and neither is the password, at any verbosity.
func WithDebug(name string) Option {
	return func(c *Client) {
		c.debug = true
		c.name = name
	}
}

// listCmdsUpTo is the batch size at or below which the commands themselves
// are logged rather than just counted.
const listCmdsUpTo = 3

// requestLog accumulates what is known about one eAPI round trip.
type requestLog struct {
	name     string
	method   string
	path     string
	cmds     []string
	start    time.Time
	status   int
	proto    string
	reqBytes int
	respByte int64
	reused   bool
	gotConn  bool
	tls      string
	eapiCode int
	eapiMsg  string
	err      error
}

// trace reports connection reuse and negotiated TLS.
//
// Reuse is worth logging because a persistent connection can outlive a
// switch-side configuration change: if authorisation is bound to the
// connection rather than checked per request, a credential or role change
// will not take effect until the connection is replaced.
func (r *requestLog) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			r.gotConn = true
			r.reused = info.Reused
		},
		TLSHandshakeDone: func(state tlsState, err error) {
			if err == nil {
				r.tls = tlsVersionName(state.Version)
			}
		},
	}
}

func (r *requestLog) emit() {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] eapi %s %s", r.name, r.method, r.path)

	if r.err != nil {
		fmt.Fprintf(&b, " -> error=%q", r.err.Error())
	} else {
		fmt.Fprintf(&b, " -> %d", r.status)
	}

	fmt.Fprintf(&b, " duration=%s", time.Since(r.start).Round(time.Millisecond))
	// The command list only tells you something when the batch is not the
	// standard one -- during a per-command retry, which command is being
	// retried is the whole question. On a full batch it is the same 200
	// characters on every line, thousands of times a day.
	if len(r.cmds) <= listCmdsUpTo {
		fmt.Fprintf(&b, " cmds=%d[%s]", len(r.cmds), truncate(strings.Join(r.cmds, ", "), 200))
	} else {
		fmt.Fprintf(&b, " cmds=%d", len(r.cmds))
	}
	fmt.Fprintf(&b, " req=%s", humanBytes(int64(r.reqBytes)))

	if r.err == nil {
		fmt.Fprintf(&b, " resp=%s", humanBytes(r.respByte))
	}
	if r.proto != "" {
		fmt.Fprintf(&b, " proto=%s", r.proto)
	}
	if r.gotConn {
		if r.reused {
			b.WriteString(" conn=reused")
		} else {
			b.WriteString(" conn=new")
		}
	}
	if r.tls != "" {
		fmt.Fprintf(&b, " tls=%s", r.tls)
	}
	// A 200 can still carry a JSON-RPC error, so the eAPI-level outcome is
	// reported separately from the HTTP status.
	if r.eapiCode != 0 {
		fmt.Fprintf(&b, " eapi_error=%d msg=%q", r.eapiCode, truncate(r.eapiMsg, 120))
	}
	log.Print(b.String())
}

// countingReader counts bytes read, so the response size is measured without
// buffering the whole body -- "show interfaces phy detail" alone is hundreds
// of kilobytes per switch per poll.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fkB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}
