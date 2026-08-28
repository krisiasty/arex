package config

import (
	"errors"
	"fmt"
	"os"
)

// ListenTLS serves /metrics over HTTPS.
//
// Separate from tlsSkipVerify and the per-switch caFile, which are the other
// direction entirely: those decide how arex verifies a switch, this decides
// how Prometheus verifies arex.
type ListenTLS struct {
	// CertFile and KeyFile are the server's certificate and private key, in
	// PEM. Both or neither.
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`

	// ClientCAFile requires clients to present a certificate signed by this
	// bundle. With it, no shared secret is involved at all: Prometheus
	// authenticates with a certificate rather than a password.
	ClientCAFile string `json:"clientCAFile"`
}

// Enabled reports whether HTTPS is configured.
func (t ListenTLS) Enabled() bool { return t.CertFile != "" && t.KeyFile != "" }

// RequiresClientCert reports whether mutual TLS is configured.
func (t ListenTLS) RequiresClientCert() bool { return t.ClientCAFile != "" }

// ListenAuth requires callers to authenticate.
//
// It does not cover /livez and /readyz. A Kubernetes probe sends no
// credentials, so requiring them there turns a liveness check into a restart
// loop, and those endpoints report only whether arex is up.
type ListenAuth struct {
	Basic *BasicAuth `json:"basic"`
}

// BasicAuth is a single username and a password read from a file.
//
// A file rather than an inline password, for the same reasons as the switch
// credentials: it can be delivered by systemd or a Kubernetes secret, given
// restrictive permissions, and rotated without a restart.
type BasicAuth struct {
	Username     string `json:"username"`
	PasswordFile string `json:"passwordFile"`
}

// Enabled reports whether any authentication is required.
func (a ListenAuth) Enabled() bool { return a.Basic != nil }

// validate checks the listener's TLS and authentication settings, returning
// warnings for what is worth saying but not worth refusing to start over.
func (c *Config) validateListen() ([]string, error) {
	var warnings []string

	if c.ProbeAddress != "" && c.ProbeAddress == c.ListenAddress {
		return nil, fmt.Errorf("config: probeAddress %s is the listen address: "+
			"the probe listener needs an address of its own", c.ProbeAddress)
	}

	t := c.ListenTLS
	switch {
	case t.CertFile != "" && t.KeyFile == "",
		t.CertFile == "" && t.KeyFile != "":
		return nil, errors.New("config: listenTLS needs both certFile and keyFile, or neither")
	case !t.Enabled() && t.RequiresClientCert():
		return nil, errors.New("config: listenTLS.clientCAFile requires certFile and keyFile: " +
			"arex cannot ask for a client certificate without serving one")
	}

	if t.Enabled() {
		for _, f := range []struct{ field, path string }{
			{"certFile", t.CertFile},
			{"keyFile", t.KeyFile},
			{"clientCAFile", t.ClientCAFile},
		} {
			if f.path == "" {
				continue
			}
			info, err := os.Stat(f.path)
			if err != nil {
				return nil, fmt.Errorf("config: listenTLS.%s: %w", f.field, err)
			}
			// The private key is the only one of the three that is a secret.
			if f.field == "keyFile" {
				if mode := info.Mode().Perm(); mode&0o077 != 0 {
					warnings = append(warnings, fmt.Sprintf(
						"config: listenTLS.keyFile %s is mode %#o, readable beyond its owner",
						f.path, mode))
				}
			}
		}
	}

	if b := c.ListenAuth.Basic; b != nil {
		if b.Username == "" || b.PasswordFile == "" {
			return nil, errors.New("config: listenAuth.basic needs both username and passwordFile")
		}
		w, err := checkPasswordFile(b.PasswordFile, "listenAuth.basic")
		if err != nil {
			return nil, err
		}
		if w != "" {
			warnings = append(warnings, w)
		}
	}

	// Basic auth over plain HTTP puts the password on the wire, base64-encoded
	// and readable, on every scrape. A private network may make that an
	// accepted risk, so it is said rather than refused.
	if c.ListenAuth.Enabled() && !t.Enabled() {
		warnings = append(warnings,
			"config: listenAuth is set without listenTLS, so credentials are sent in clear on every scrape")
	}

	return warnings, nil
}
