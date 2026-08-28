package eapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// captureLog returns a buffer and a debug-level JSON logger writing to it.
func captureLog(t *testing.T) (*bytes.Buffer, *slog.Logger) {
	t.Helper()
	var buf bytes.Buffer
	return &buf, slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// records parses the captured output, which must be JSON Lines.
func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("debug output is not JSON Lines: %v\n%s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func eapiServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const okBody = `{"jsonrpc":"2.0","id":1,"result":[{"modelName":"DCS-7050CX3-32C-R"}]}`

func TestDebugDisabledLogsNothing(t *testing.T) {
	buf, _ := captureLog(t)
	srv := eapiServer(t, okBody, http.StatusOK)

	c, err := NewClient(srv.URL, "u", "hunter2", 5*time.Second, TLSOptions{SkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run([]string{"show version"}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected silence without debug, got: %s", buf.String())
	}
}

// Every field is a discrete attribute rather than formatted into a message, so
// the output can be queried instead of grepped.
func TestDebugRecordIsStructured(t *testing.T) {
	buf, logger := captureLog(t)
	srv := eapiServer(t, okBody, http.StatusOK)

	c, err := NewClient(srv.URL, "u", "hunter2", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw1", logger))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run([]string{"show version", "show interfaces"}); err != nil {
		t.Fatal(err)
	}

	recs := records(t, buf)
	if len(recs) != 1 {
		t.Fatalf("%d records, want 1", len(recs))
	}
	rec := recs[0]
	t.Logf("%v", rec)

	if rec["level"] != "DEBUG" {
		t.Errorf("level = %v, want DEBUG", rec["level"])
	}
	if rec["switch"] != "sw1" {
		t.Errorf("switch = %v", rec["switch"])
	}
	for _, key := range []string{"method", "path", "status", "duration_ms", "cmds", "req_bytes", "resp_bytes"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("missing attribute %q", key)
		}
	}
	if rec["cmds"] != float64(2) {
		t.Errorf("cmds = %v, want 2", rec["cmds"])
	}
	// A small batch lists its commands, as a real array rather than a string.
	cmds, ok := rec["commands"].([]any)
	if !ok || len(cmds) != 2 || cmds[0] != "show version" {
		t.Errorf("commands = %#v", rec["commands"])
	}
}

// A full batch is counted, not listed: on nine commands it would be the same
// list on every record, thousands of times a day.
func TestDebugOmitsCommandListForLargeBatches(t *testing.T) {
	buf, logger := captureLog(t)
	srv := eapiServer(t, `{"jsonrpc":"2.0","id":1,"result":[{},{},{},{}]}`, http.StatusOK)

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw1", logger))
	_, _ = c.Run([]string{"a", "b", "c", "d"})

	rec := records(t, buf)[0]
	if _, listed := rec["commands"]; listed {
		t.Errorf("a four-command batch should be counted, not listed: %v", rec)
	}
	if rec["cmds"] != float64(4) {
		t.Errorf("cmds = %v, want 4", rec["cmds"])
	}
}

// Credentials must never reach the log, whatever the verbosity.
func TestDebugNeverLogsCredentials(t *testing.T) {
	buf, logger := captureLog(t)
	srv := eapiServer(t, okBody, http.StatusOK)

	c, _ := NewClient(srv.URL, "prometheus", "hunter2", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw1", logger))
	_, _ = c.Run([]string{"show version"})

	for _, secret := range []string{"hunter2", "Authorization", "Basic "} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("debug output leaked %q:\n%s", secret, buf.String())
		}
	}
}

func TestDebugLogsHTTPStatusFailures(t *testing.T) {
	buf, logger := captureLog(t)
	srv := eapiServer(t, "", http.StatusUnauthorized)

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw2", logger))
	_, _ = c.Run([]string{"show version"})

	if rec := records(t, buf)[0]; rec["status"] != float64(401) {
		t.Errorf("status = %v, want 401", rec["status"])
	}
}

// A 200 can still carry a JSON-RPC error, so the eAPI outcome is a separate
// attribute from the HTTP status, and the cause from error.data is included.
func TestDebugLogsEAPIErrorSeparatelyFromStatus(t *testing.T) {
	buf, logger := captureLog(t)
	srv := eapiServer(t,
		`{"jsonrpc":"2.0","id":1,"error":{"code":1002,"message":"failed",`+
			`"data":[{"errors":["Invalid input (privileged mode required)"]}]}}`,
		http.StatusOK)

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw3", logger))
	_, _ = c.Run([]string{"show bogus"})

	rec := records(t, buf)[0]
	if rec["status"] != float64(200) {
		t.Errorf("status = %v, want 200", rec["status"])
	}
	if rec["eapi_error"] != float64(1002) {
		t.Errorf("eapi_error = %v, want 1002", rec["eapi_error"])
	}
	cause, _ := rec["eapi_cause"].(string)
	if !strings.Contains(cause, "privileged mode required") {
		t.Errorf("eapi_cause = %q", cause)
	}
}

// A transport failure has no status or sizes, but still needs a record.
func TestDebugLogsTransportFailures(t *testing.T) {
	buf, logger := captureLog(t)

	c, _ := NewClient("https://192.0.2.99", "u", "p", 200*time.Millisecond,
		TLSOptions{SkipVerify: true}, WithDebug("sw4", logger))
	_, _ = c.Run([]string{"show version"})

	rec := records(t, buf)[0]
	if rec["switch"] != "sw4" {
		t.Errorf("switch = %v", rec["switch"])
	}
	if _, ok := rec["error"]; !ok {
		t.Errorf("transport failure should carry an error attribute: %v", rec)
	}
	if _, ok := rec["status"]; ok {
		t.Errorf("a request that never completed should have no status: %v", rec)
	}
}

// Connection reuse is the field question that motivated logging it: whether a
// persistent connection could carry stale switch-side authorisation.
func TestDebugReportsConnectionReuse(t *testing.T) {
	buf, logger := captureLog(t)
	srv := eapiServer(t, okBody, http.StatusOK)

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw5", logger))
	_, _ = c.Run([]string{"show version"})
	_, _ = c.Run([]string{"show version"})

	recs := records(t, buf)
	if len(recs) != 2 {
		t.Fatalf("%d records, want 2", len(recs))
	}
	if recs[0]["conn"] != "new" {
		t.Errorf("first conn = %v, want new", recs[0]["conn"])
	}
	if recs[1]["conn"] != "reused" {
		t.Errorf("second conn = %v, want reused", recs[1]["conn"])
	}
}
