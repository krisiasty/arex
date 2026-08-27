package eapi

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// captureLog redirects the standard logger for the duration of a test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
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
	buf := captureLog(t)
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

func TestDebugLogsRequestDetail(t *testing.T) {
	buf := captureLog(t)
	srv := eapiServer(t, okBody, http.StatusOK)

	c, err := NewClient(srv.URL, "u", "hunter2", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run([]string{"show version", "show interfaces"}); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	t.Logf("line: %s", strings.TrimSpace(got))
	for _, want := range []string{
		"sw1",          // which switch
		"POST",         // method
		"/command-api", // path
		"200",          // status
		"cmds=2",       // how many commands, and which
		"show version", // the actual payload, which is what eAPI debugging needs
		"req=",         // request size
		"resp=",        // response size
		"duration=",    // response time
	} {
		if !strings.Contains(got, want) {
			t.Errorf("debug line missing %q", want)
		}
	}
}

// Credentials must never reach the log, whatever the verbosity.
func TestDebugNeverLogsCredentials(t *testing.T) {
	buf := captureLog(t)
	srv := eapiServer(t, okBody, http.StatusOK)

	c, _ := NewClient(srv.URL, "prometheus", "hunter2", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw1"))
	_, _ = c.Run([]string{"show version"})

	got := buf.String()
	for _, secret := range []string{"hunter2", "Authorization", "Basic "} {
		if strings.Contains(got, secret) {
			t.Errorf("debug output leaked %q:\n%s", secret, got)
		}
	}
}

// An HTTP-level rejection must be visible with its status.
func TestDebugLogsHTTPStatusFailures(t *testing.T) {
	buf := captureLog(t)
	srv := eapiServer(t, "", http.StatusUnauthorized)

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw2"))
	_, _ = c.Run([]string{"show version"})

	if got := buf.String(); !strings.Contains(got, "401") {
		t.Errorf("debug line should carry the status: %s", got)
	}
}

// A 200 can still carry a JSON-RPC error, so the eAPI-level outcome has to
// be logged separately from the HTTP status.
func TestDebugLogsEAPIErrorSeparatelyFromStatus(t *testing.T) {
	buf := captureLog(t)
	srv := eapiServer(t,
		`{"jsonrpc":"2.0","id":1,"error":{"code":1002,"message":"invalid command"}}`,
		http.StatusOK)

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw3"))
	_, _ = c.Run([]string{"show bogus"})

	got := buf.String()
	if !strings.Contains(got, "200") {
		t.Errorf("HTTP status should still be 200: %s", got)
	}
	if !strings.Contains(got, "1002") {
		t.Errorf("eAPI error code should be logged: %s", got)
	}
}

// A transport failure has no status or sizes, but still needs a line.
func TestDebugLogsTransportFailures(t *testing.T) {
	buf := captureLog(t)

	c, _ := NewClient("https://192.0.2.99", "u", "p", 200*time.Millisecond,
		TLSOptions{SkipVerify: true}, WithDebug("sw4"))
	_, _ = c.Run([]string{"show version"})

	got := buf.String()
	if !strings.Contains(got, "sw4") || !strings.Contains(got, "error=") {
		t.Errorf("transport failure should be logged: %s", got)
	}
}

// Connection reuse is the field question that motivated logging it: whether
// a persistent connection could carry stale switch-side authorisation.
func TestDebugReportsConnectionReuse(t *testing.T) {
	buf := captureLog(t)
	srv := eapiServer(t, okBody, http.StatusOK)

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithDebug("sw5"))
	_, _ = c.Run([]string{"show version"})
	_, _ = c.Run([]string{"show version"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "conn=new") {
		t.Errorf("first request should report a new connection: %s", lines[0])
	}
	if !strings.Contains(lines[1], "conn=reused") {
		t.Errorf("second request should report connection reuse: %s", lines[1])
	}
}
