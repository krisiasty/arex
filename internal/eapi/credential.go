package eapi

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Credential supplies the password for basic authentication, and can re-read
// it from disk.
//
// Re-reading is the reason this is a type rather than a string. Secrets
// rotate: Vault rotates the account, the External Secrets Operator updates the
// Kubernetes secret, and the kubelet updates the mounted file in place. Caching
// the password at startup would mean every poll failing until someone noticed
// and restarted arex.
type Credential struct {
	mu       sync.RWMutex
	password string

	// path is empty for a static credential, which cannot rotate.
	path string
}

// NewStaticCredential wraps a password given inline.
func NewStaticCredential(password string) *Credential {
	return &Credential{password: password}
}

// NewFileCredential reads a password from path.
//
// It reads immediately so a bad path fails at startup rather than on the first
// poll, when the error would be indistinguishable from an unreachable switch.
func NewFileCredential(path string) (*Credential, error) {
	c := &Credential{path: path}
	pw, err := c.read()
	if err != nil {
		return nil, err
	}
	c.password = pw
	return c, nil
}

// Password returns the current password.
func (c *Credential) Password() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.password
}

// Reload re-reads the password, reporting whether it changed.
//
// On failure the previous password is kept: a partial write or a remounted
// volume would otherwise turn a transient glitch into an authentication
// failure against a switch that was working a moment ago.
func (c *Credential) Reload() (changed bool, err error) {
	if c.path == "" {
		return false, nil
	}
	pw, err := c.read()
	if err != nil {
		return false, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if pw == c.password {
		return false, nil
	}
	c.password = pw
	return true, nil
}

// read loads and trims the secret file.
func (c *Credential) read() (string, error) {
	body, err := os.ReadFile(c.path)
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	// Only the line ending: writing a secret with a shell redirect appends
	// one, and EOS would reject it as part of the password. A space could
	// conceivably be part of a real password, so it is left alone.
	pw := strings.TrimRight(string(body), "\r\n")
	if pw == "" {
		return "", fmt.Errorf("password file %s is empty", c.path)
	}
	return pw, nil
}

// WithCredential makes the client take its password from cred, replacing the
// password passed to NewClient. Pass "" there when using this.
func WithCredential(cred *Credential) Option {
	return func(c *Client) { c.cred = cred }
}

// errUnauthorized marks a 401, the one status worth re-reading a credential
// for. A 403 is an access-group problem rather than a wrong password, so
// re-reading the file would achieve nothing.
var errUnauthorized = errors.New("unauthorized")

// isUnauthorized reports whether the switch rejected our credentials.
func isUnauthorized(err error) bool {
	return errors.Is(err, errUnauthorized)
}

// unauthorizedStatus is 401's share of statusError, kept here so the sentinel
// travels with the message.
func unauthorizedStatus(status string) error {
	return fmt.Errorf("%w: eAPI rejected our credentials (%s): check the username and "+
		"password, and that the user has a role permitting the show commands",
		errUnauthorized, status)
}
