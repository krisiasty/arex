package eapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptrace"
	"strings"
	"time"
)

// Option configures a Client. Options are variadic so adding one does not
// disturb existing call sites.
type Option func(*Client)

// WithLogging logs one concise info-level record for each successful eAPI
// request, tagged with name. Failed requests are reported by the collector.
func WithLogging(name string, logger *slog.Logger) Option {
	return func(c *Client) {
		c.logger = logger.With("switch", name)
	}
}

// WithDebug logs one structured record per eAPI request, tagged with name.
//
// Credentials are never included: the Authorization header is not logged, and
// neither is the password, at any verbosity.
func WithDebug(name string, logger *slog.Logger) Option {
	return func(c *Client) {
		c.logger = logger.With("switch", name)
		c.debug = true
	}
}

// listCmdsUpTo is the batch size at or below which the commands themselves are
// logged rather than just counted.
const listCmdsUpTo = 3

// requestLog accumulates what is known about one eAPI round trip.
type requestLog struct {
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
	eapiData []string
	err      error
}

// trace reports connection reuse and negotiated TLS.
//
// Reuse is worth logging because a persistent connection can outlive a
// switch-side configuration change: if authorisation were bound to the
// connection rather than checked per request, a credential or role change
// would not take effect until the connection was replaced.
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

func (r *requestLog) emit(logger *slog.Logger) {
	attrs := []any{
		"method", r.method,
		"path", r.path,
		"duration_ms", time.Since(r.start).Milliseconds(),
		"cmds", len(r.cmds),
		"req_bytes", r.reqBytes,
	}

	// The command list only tells you something when the batch is not the
	// standard one -- during a per-command retry, which command is being
	// retried is the whole question. On a full batch it is the same list on
	// every record, thousands of times a day.
	if len(r.cmds) <= listCmdsUpTo {
		attrs = append(attrs, "commands", r.cmds)
	}

	if r.err != nil {
		attrs = append(attrs, "error", r.err.Error())
	} else {
		attrs = append(attrs, "status", r.status, "resp_bytes", r.respByte)
	}
	if r.proto != "" {
		attrs = append(attrs, "proto", r.proto)
	}
	if r.gotConn {
		conn := "new"
		if r.reused {
			conn = "reused"
		}
		attrs = append(attrs, "conn", conn)
	}
	if r.tls != "" {
		attrs = append(attrs, "tls", r.tls)
	}
	// A 200 can still carry a JSON-RPC error, so the eAPI-level outcome is
	// reported separately from the HTTP status.
	if r.eapiCode != 0 {
		attrs = append(attrs, "eapi_error", r.eapiCode, "eapi_message", r.eapiMsg)
		if len(r.eapiData) > 0 {
			attrs = append(attrs, "eapi_cause", strings.Join(r.eapiData, "; "))
		}
	}
	logger.LogAttrs(context.Background(), slog.LevelDebug, "eapi request", attrsOf(attrs)...)
}

// attrsOf converts alternating key/value pairs to slog attributes.
func attrsOf(kv []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, _ := kv[i].(string)
		out = append(out, slog.Any(key, kv[i+1]))
	}
	return out
}

// countingReader counts bytes read, so the response size is measured without
// buffering the whole body -- "show interfaces phy detail" alone is hundreds of
// kilobytes per switch per poll.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
