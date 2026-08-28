package secret

import (
	"os"
	"path/filepath"
	"testing"
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
