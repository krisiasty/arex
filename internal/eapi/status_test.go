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
				func(w http.ResponseWriter, r *http.Request) {
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
		func(w http.ResponseWriter, r *http.Request) {
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
