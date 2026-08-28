package eapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func secretFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The line ending a shell redirect appends is not part of the password. EOS
// would reject it, and the failure looks exactly like a wrong password.
func TestFileCredentialStripsTheTrailingNewline(t *testing.T) {
	for _, body := range []string{"hunter2", "hunter2\n", "hunter2\r\n", "hunter2\n\n"} {
		cred, err := NewFileCredential(secretFile(t, body))
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if got := cred.Password(); got != "hunter2" {
			t.Errorf("password from %q = %q, want %q", body, got, "hunter2")
		}
	}
}

func TestFileCredentialFailsFast(t *testing.T) {
	if _, err := NewFileCredential(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("a missing file must fail at construction, not at the first poll")
	}
	if _, err := NewFileCredential(secretFile(t, "\n")); err == nil {
		t.Error("a file holding only a newline must fail at construction")
	}
}

// Reload reports whether the secret actually changed, so a caller can tell a
// rotation from a repeated failure.
func TestReloadReportsChange(t *testing.T) {
	p := secretFile(t, "old")
	cred, err := NewFileCredential(p)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := cred.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("an unchanged file must not report a change")
	}

	if werr := os.WriteFile(p, []byte("new\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	changed, err = cred.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("a rotated file must report a change")
	}
	if got := cred.Password(); got != "new" {
		t.Errorf("password after reload = %q, want %q", got, "new")
	}
}

// A secret that becomes unreadable must not wipe the working one: a partial
// write or a remounted volume would otherwise turn a transient glitch into an
// authentication failure.
func TestReloadKeepsTheLastGoodSecret(t *testing.T) {
	p := secretFile(t, "good")
	cred, err := NewFileCredential(p)
	if err != nil {
		t.Fatal(err)
	}
	if rerr := os.Remove(p); rerr != nil {
		t.Fatal(rerr)
	}
	if _, err := cred.Reload(); err == nil {
		t.Error("reload should report the read failure")
	}
	if got := cred.Password(); got != "good" {
		t.Errorf("password = %q, want the last good value retained", got)
	}
}

func TestStaticCredentialNeverChanges(t *testing.T) {
	cred := NewStaticCredential("hunter2")
	changed, err := cred.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a static credential cannot rotate")
	}
	if got := cred.Password(); got != "hunter2" {
		t.Errorf("password = %q", got)
	}
}

// authServer accepts exactly one password, and counts requests.
type authServer struct {
	want  *string
	calls int
}

func (a *authServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.calls++
		_, pass, _ := r.BasicAuth()
		if pass != *a.want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[{}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The point of a credential file: a rotated password is picked up without a
// restart, costing one rejected request rather than every poll until someone
// notices.
func TestRotatedPasswordIsPickedUpWithoutRestart(t *testing.T) {
	accepted := "new"
	srv := (&authServer{want: &accepted}).start(t)

	p := secretFile(t, "old")
	cred, err := NewFileCredential(p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(srv.URL, "u", "", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithCredential(cred))
	if err != nil {
		t.Fatal(err)
	}

	// The stale secret is rejected.
	if _, err := c.Run([]string{"show version"}); err == nil {
		t.Fatal("the stale password should have been rejected")
	}

	// The rotation lands in the file; the next poll recovers on its own.
	if werr := os.WriteFile(p, []byte("new\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if _, err := c.Run([]string{"show version"}); err != nil {
		t.Fatalf("after rotation the poll should succeed: %v", err)
	}
}

// Retry amplification is the thing to avoid here: a genuinely wrong password
// must still cost exactly one request per poll, or a locked-out account gets
// hammered.
func TestUnchangedSecretDoesNotRetry(t *testing.T) {
	accepted := "something-else"
	as := &authServer{want: &accepted}
	srv := as.start(t)

	cred, err := NewFileCredential(secretFile(t, "wrong"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(srv.URL, "u", "", 5*time.Second,
		TLSOptions{SkipVerify: true}, WithCredential(cred))
	if err != nil {
		t.Fatal(err)
	}

	as.calls = 0
	if _, err := c.Run([]string{"show version"}); err == nil {
		t.Fatal("expected rejection")
	}
	if as.calls != 1 {
		t.Errorf("%d requests for one poll with an unchanged secret, want 1", as.calls)
	}
}

// A static password has nothing to re-read, so it must not retry either.
func TestStaticPasswordDoesNotRetryOn401(t *testing.T) {
	accepted := "something-else"
	as := &authServer{want: &accepted}
	srv := as.start(t)

	c, err := NewClient(srv.URL, "u", "wrong", 5*time.Second, TLSOptions{SkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	as.calls = 0
	if _, err := c.Run([]string{"show version"}); err == nil {
		t.Fatal("expected rejection")
	}
	if as.calls != 1 {
		t.Errorf("%d requests, want 1", as.calls)
	}
}
