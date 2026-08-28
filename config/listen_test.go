package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// certPair writes a self-signed certificate and key, returning both paths.
// Generated in-process rather than by shelling out to openssl, so the test has
// no external dependency and runs the same everywhere.
func certPair(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "arex"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")
	write := func(path string, block *pem.Block, mode os.FileMode) {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(certPath, &pem.Block{Type: "CERTIFICATE", Bytes: der}, 0o644)
	write(keyPath, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}, 0o600)
	return certPath, keyPath
}

func listenCfg(fields string) string {
	return `{"tlsSkipVerify":true,` + fields + `"collect":{"interfaces":{"enabled":true}},
		"switches":[{"host":"https://192.0.2.1","username":"u","password":"p","name":"sw1"}]}`
}

// Both sections are optional: without them arex serves plain HTTP, which is
// what every existing deployment does.
func TestListenTLSAndAuthAreOptional(t *testing.T) {
	cfg, err := Load(writeRaw(t, listenCfg("")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenTLS.Enabled() {
		t.Error("TLS must be off unless configured")
	}
	if cfg.ListenAuth.Enabled() {
		t.Error("auth must be off unless configured")
	}
}

func TestListenTLSLoads(t *testing.T) {
	cert, key := certPair(t)
	cfg, err := Load(writeRaw(t, listenCfg(
		`"listenTLS":{"certFile":"`+cert+`","keyFile":"`+key+`"},`)))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ListenTLS.Enabled() {
		t.Fatal("TLS should be enabled")
	}
	if cfg.ListenTLS.CertFile != cert || cfg.ListenTLS.KeyFile != key {
		t.Errorf("paths = %+v", cfg.ListenTLS)
	}
}

// Half a TLS configuration is a mistake, not a mode.
func TestCertWithoutKeyIsRejected(t *testing.T) {
	cert, key := certPair(t)
	for _, tc := range []struct{ name, fields string }{
		{"cert only", `"listenTLS":{"certFile":"` + cert + `"},`},
		{"key only", `"listenTLS":{"keyFile":"` + key + `"},`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeRaw(t, listenCfg(tc.fields)))
			if err == nil {
				t.Fatal("half a TLS config must be rejected")
			}
			if !strings.Contains(err.Error(), "certFile") || !strings.Contains(err.Error(), "keyFile") {
				t.Errorf("error should name both fields: %v", err)
			}
		})
	}
}

// Requiring client certificates without serving one is not a configuration
// that can work.
func TestClientCAWithoutServerCertIsRejected(t *testing.T) {
	cert, _ := certPair(t)
	_, err := Load(writeRaw(t, listenCfg(`"listenTLS":{"clientCAFile":"`+cert+`"},`)))
	if err == nil {
		t.Fatal("clientCAFile without a server certificate must be rejected")
	}
}

// A bad path must fail at startup, not on the first scrape.
func TestUnreadableTLSFilesAreRejected(t *testing.T) {
	cert, key := certPair(t)
	missing := filepath.Join(t.TempDir(), "nope.pem")
	for _, fields := range []string{
		`"listenTLS":{"certFile":"` + missing + `","keyFile":"` + key + `"},`,
		`"listenTLS":{"certFile":"` + cert + `","keyFile":"` + missing + `"},`,
		`"listenTLS":{"certFile":"` + cert + `","keyFile":"` + key + `","clientCAFile":"` + missing + `"},`,
	} {
		if _, err := Load(writeRaw(t, listenCfg(fields))); err == nil {
			t.Errorf("a missing file must be rejected: %s", fields)
		}
	}
}

// A world-readable private key is worth saying out loud, without refusing to
// start: a Kubernetes secret mount may well be 0644.
func TestWorldReadableKeyWarns(t *testing.T) {
	cert, key := certPair(t)
	//nolint:gosec // deliberately loose: this is what the warning is for
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(writeRaw(t, listenCfg(
		`"listenTLS":{"certFile":"`+cert+`","keyFile":"`+key+`"},`)))
	if err != nil {
		t.Fatalf("loose permissions must not prevent startup: %v", err)
	}
	if !strings.Contains(strings.Join(cfg.Warnings, "\n"), "keyFile") {
		t.Errorf("expected a warning about the key: %q", cfg.Warnings)
	}
}

func TestListenAuthLoads(t *testing.T) {
	pw := writeSecret(t, "hunter2", 0o400)
	cfg, err := Load(writeRaw(t, listenCfg(
		`"listenAuth":{"basic":{"username":"prometheus","passwordFile":"`+pw+`"}},`)))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ListenAuth.Enabled() {
		t.Fatal("auth should be enabled")
	}
	if cfg.ListenAuth.Basic.Username != "prometheus" {
		t.Errorf("username = %q", cfg.ListenAuth.Basic.Username)
	}
}

func TestBasicAuthNeedsBothFields(t *testing.T) {
	pw := writeSecret(t, "hunter2", 0o400)
	for _, fields := range []string{
		`"listenAuth":{"basic":{"username":"prometheus"}},`,
		`"listenAuth":{"basic":{"passwordFile":"` + pw + `"}},`,
	} {
		if _, err := Load(writeRaw(t, listenCfg(fields))); err == nil {
			t.Errorf("incomplete basic auth must be rejected: %s", fields)
		}
	}
}

// Basic auth over plain HTTP sends the password in clear on every scrape. Not
// refused -- a private network may be an accepted risk -- but not silent.
func TestAuthWithoutTLSWarns(t *testing.T) {
	pw := writeSecret(t, "hunter2", 0o400)
	cfg, err := Load(writeRaw(t, listenCfg(
		`"listenAuth":{"basic":{"username":"prometheus","passwordFile":"`+pw+`"}},`)))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	if !strings.Contains(joined, "listenTLS") {
		t.Errorf("expected a warning that credentials travel in clear: %q", cfg.Warnings)
	}
}

// A separate probe listener exists so mutual TLS does not force probes down to
// tcpSocket: RequireAndVerifyClientCert refuses the kubelet at the handshake,
// and a plain port carrying only up/down keeps the real readiness gate.
func TestProbeAddressIsOptional(t *testing.T) {
	cfg, err := Load(writeRaw(t, listenCfg("")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProbeAddress != "" {
		t.Errorf("probeAddress = %q, want empty by default", cfg.ProbeAddress)
	}
}

func TestProbeAddressLoads(t *testing.T) {
	cfg, err := Load(writeRaw(t, listenCfg(`"probeAddress":":9101",`)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProbeAddress != ":9101" {
		t.Errorf("probeAddress = %q", cfg.ProbeAddress)
	}
}

// Two servers cannot share one address, and the mistake is easy to make when
// copying the listen address.
func TestProbeAddressMustDifferFromListenAddress(t *testing.T) {
	_, err := Load(writeRaw(t, listenCfg(`"listenAddress":":9100","probeAddress":":9100",`)))
	if err == nil {
		t.Fatal("the same address for both listeners must be rejected")
	}
	if !strings.Contains(err.Error(), "probeAddress") {
		t.Errorf("error should name the field: %v", err)
	}
}
