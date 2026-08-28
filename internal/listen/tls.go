// Package listen configures the HTTP server arex exposes: TLS toward
// Prometheus, and authentication of the callers that scrape it.
//
// This is the opposite direction from internal/eapi, which is about arex
// authenticating itself to a switch.
package listen

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// certificate holds the server certificate and reloads it when the files on
// disk change.
//
// Reloading matters because certificates are rotated by things that do not
// restart the process: cert-manager renews well before expiry, and a
// certificate that arex only reads at startup would expire in memory while a
// valid one sat on disk.
type certificate struct {
	certFile, keyFile string

	mu      sync.RWMutex
	current *tls.Certificate
	stamp   stamp
	// checked throttles the stat calls: a handshake happens per connection,
	// and Prometheus opens new ones regularly.
	checked time.Time
}

// stamp is what a file change looks like without reading the file.
type stamp struct {
	certSize, keySize int64
	certMod, keyMod   time.Time
}

// reloadInterval bounds how often the files are stat-ed. A renewed
// certificate is picked up within this, without a stat on every handshake.
const reloadInterval = 30 * time.Second

func newCertificate(certFile, keyFile string) (*certificate, error) {
	c := &certificate{certFile: certFile, keyFile: keyFile}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// load reads the pair and replaces the cached certificate.
func (c *certificate) load() error {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return fmt.Errorf("load certificate: %w", err)
	}
	s, err := c.stat()
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = &cert
	c.stamp = s
	c.checked = time.Now()
	return nil
}

// stat describes both files without reading them.
func (c *certificate) stat() (stamp, error) {
	ci, err := os.Stat(c.certFile)
	if err != nil {
		return stamp{}, fmt.Errorf("stat certificate: %w", err)
	}
	ki, err := os.Stat(c.keyFile)
	if err != nil {
		return stamp{}, fmt.Errorf("stat key: %w", err)
	}
	return stamp{ci.Size(), ki.Size(), ci.ModTime(), ki.ModTime()}, nil
}

// get returns the certificate for a handshake, reloading it if the files have
// changed since the last check.
//
// A failed reload keeps the certificate already in memory: a half-written pair
// during renewal must not take the endpoint down when a valid certificate is
// still loaded.
func (c *certificate) get(now time.Time) *tls.Certificate {
	c.mu.RLock()
	due := now.Sub(c.checked) >= reloadInterval
	cur := c.current
	c.mu.RUnlock()
	if !due {
		return cur
	}

	s, err := c.stat()
	c.mu.Lock()
	c.checked = now
	changed := err == nil && s != c.stamp
	c.mu.Unlock()
	if err != nil || !changed {
		return cur
	}

	if err := c.load(); err != nil {
		return cur
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// Options describe the listener's TLS.
type Options struct {
	CertFile     string
	KeyFile      string
	ClientCAFile string
}

// TLSConfig builds the server's TLS configuration, or nil when TLS is not
// configured.
func TLSConfig(o Options) (*tls.Config, error) {
	if o.CertFile == "" && o.KeyFile == "" {
		if o.ClientCAFile != "" {
			return nil, errors.New("a client CA requires a server certificate")
		}
		return nil, nil //nolint:nilnil // no TLS configured is a valid outcome
	}

	cert, err := newCertificate(o.CertFile, o.KeyFile)
	if err != nil {
		return nil, err
	}

	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return cert.get(time.Now()), nil
		},
	}

	if o.ClientCAFile != "" {
		pem, err := os.ReadFile(o.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("client CA %s contains no certificate", o.ClientCAFile)
		}
		cfg.ClientCAs = pool
		// Verify, not just request: asking for a certificate and accepting one
		// that does not verify would be authentication in appearance only.
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return cfg, nil
}
