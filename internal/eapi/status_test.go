package eapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The three statuses actually hit in the field each have a different fix, so
// the error should name it rather than leaving the operator to guess.
func TestHTTPStatusErrorsAreActionable(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   []string
	}{
		{http.StatusUnauthorized, []string{"401", "credentials", "role"}},
		{http.StatusForbidden, []string{"403", "access-group"}},
		{http.StatusNotFound, []string{"404", "eAPI"}},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
				}))
			defer srv.Close()

			c, err := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Run([]string{"show version"})
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q: %v", want, err)
				}
			}
			t.Logf("%d -> %v", tc.status, err)
		})
	}
}

// An HTTP-level rejection is not a per-command problem: retrying each
// command individually would multiply failed auth attempts by the number of
// commands, which is how account lockouts happen.
func TestHTTPStatusErrorIsNotACommandError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
	defer srv.Close()

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true})
	_, err := c.Run([]string{"show version"})

	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		t.Error("a 401 must not be a CommandError, or it would trigger per-command retries")
	}
}

// eAPI puts the generic failure in error.message and the actual cause in
// error.data. Discarding data turns "privileged mode required" into
// "invalid command", which is the difference between a diagnosis and a
// guess.
func TestCommandErrorCarriesEAPIErrorData(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"error":{"code":1002,` +
		`"message":"CLI command 1 of 1 'show running-config' failed: invalid command",` +
		`"data":[{"errors":["Invalid input (privileged mode required)"]}]}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true})
	_, err := c.Run([]string{"show running-config"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "privileged mode required") {
		t.Errorf("the cause from error.data must reach the operator: %v", err)
	}
	if !strings.Contains(err.Error(), "1002") {
		t.Errorf("the code should still be present: %v", err)
	}

	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatal("should still be a CommandError, so per-command retries still apply")
	}
	if len(cmdErr.Details) != 1 || cmdErr.Details[0] != "Invalid input (privileged mode required)" {
		t.Errorf("Details = %v", cmdErr.Details)
	}
}

// error.data holds one entry per command, so a batch reports which failed.
func TestCommandErrorCollectsDetailPerCommand(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"error":{"code":1002,"message":"failed",` +
		`"data":[{},{"errors":["Invalid input (privileged mode required)"]},` +
		`{"errors":["% Invalid input"]}]}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true})
	_, err := c.Run([]string{"show version", "show running-config", "show bogus"})

	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatal("expected a CommandError")
	}
	if len(cmdErr.Details) != 2 {
		t.Errorf("Details = %v, want the two failures", cmdErr.Details)
	}
}

// An error with no data must still produce a usable message.
func TestCommandErrorWithoutData(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"error":{"code":1000,"message":"something broke"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true})
	_, err := c.Run([]string{"show version"})
	if err == nil || !strings.Contains(err.Error(), "something broke") {
		t.Errorf("err = %v", err)
	}
}
