package eapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// aristaLikeCert mirrors ARISTA_SELF_SIGNED.crt as reported by EOS 4.35.4M:
// CN is the profile name rather than a hostname, there are no SANs at all,
// and there is no basicConstraints extension. Validity is 1970 to 9999.
func aristaLikeCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ARISTA_DEFAULT_SELF_SIGNED_PROFILE"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(253402300799, 0),
		// Deliberately no DNSNames, no IPAddresses, no BasicConstraints.
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, der
}

// serverWithCert starts an HTTPS server presenting cert and returning an
// empty eAPI result for any request.
func serverWithCert(t *testing.T, cert tls.Certificate) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[{}]}`))
		}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// The premise for everything else: a cert shaped like Arista's default
// cannot be verified even when it is fully trusted, because it names nothing.
func TestNoSANCertFailsEvenWhenTrusted(t *testing.T) {
	cert, der := aristaLikeCert(t)
	srv := serverWithCert(t, cert)

	pool := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool.AddCert(parsed)

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected verification to fail: the cert carries no SANs")
	}
	if !strings.Contains(err.Error(), "SAN") && !strings.Contains(err.Error(), "not contain") {
		t.Logf("failed as expected, though not with a SAN message: %v", err)
	} else {
		t.Logf("confirmed: %v", err)
	}
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func TestPinnedCertificateIsAccepted(t *testing.T) {
	cert, der := aristaLikeCert(t)
	srv := serverWithCert(t, cert)

	c, err := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{
		PinnedCertSHA256: fingerprint(der),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run([]string{"show version"}); err != nil {
		t.Errorf("pinned cert should be accepted: %v", err)
	}
}

func TestPinnedCertificateMismatchIsRejected(t *testing.T) {
	cert, _ := aristaLikeCert(t)
	srv := serverWithCert(t, cert)
	_, otherDER := aristaLikeCert(t) // a different key

	c, err := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{
		PinnedCertSHA256: fingerprint(otherDER),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Run([]string{"show version"})
	if err == nil {
		t.Fatal("a different certificate must be rejected")
	}
	// The observed fingerprint belongs in the error so it can be verified
	// out of band and configured.
	if !strings.Contains(err.Error(), "pin") {
		t.Errorf("error should mention the pin: %v", err)
	}
	if !strings.Contains(err.Error(), "presented sha256:") {
		t.Errorf("error should report what was presented: %v", err)
	}
	t.Logf("operator sees: %v", err)
}

func TestPinIsCaseAndSeparatorInsensitive(t *testing.T) {
	cert, der := aristaLikeCert(t)
	srv := serverWithCert(t, cert)

	// openssl prints colon-separated uppercase; accept that verbatim.
	fp := strings.ToUpper(fingerprint(der))
	var withColons strings.Builder
	for i := 0; i < len(fp); i += 2 {
		if i > 0 {
			withColons.WriteByte(':')
		}
		withColons.WriteString(fp[i : i+2])
	}

	c, err := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{
		PinnedCertSHA256: withColons.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run([]string{"show version"}); err != nil {
		t.Errorf("colon-separated uppercase pin should work: %v", err)
	}
}

func TestMalformedPinIsRejectedAtConstruction(t *testing.T) {
	for _, bad := range []string{"nothex", "ab12", strings.Repeat("a", 63)} {
		if _, err := NewClient("https://192.0.2.1", "u", "p", time.Second,
			TLSOptions{PinnedCertSHA256: bad}); err == nil {
			t.Errorf("pin %q should be rejected", bad)
		}
	}
}

// caFile is the path for switches whose certs have been replaced properly.
func TestCAFileVerifiesAProperCert(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "sw-leaf-01"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              []string{"localhost"},
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := serverWithCert(t, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key})

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if werr := os.WriteFile(caPath, pemBytes, 0o600); werr != nil {
		t.Fatal(werr)
	}

	// httptest serves on 127.0.0.1; the cert names localhost.
	url := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	c, err := NewClient(url, "u", "p", 5*time.Second, TLSOptions{CAFile: caPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run([]string{"show version"}); err != nil {
		t.Errorf("cert signed by the configured CA should verify: %v", err)
	}
}

func TestMissingCAFileIsRejectedAtConstruction(t *testing.T) {
	_, err := NewClient("https://192.0.2.1", "u", "p", time.Second,
		TLSOptions{CAFile: "/nonexistent/ca.pem"})
	if err == nil {
		t.Fatal("an unreadable caFile should fail at construction, not per request")
	}
}

func TestSkipVerifyStillWorks(t *testing.T) {
	cert, _ := aristaLikeCert(t)
	srv := serverWithCert(t, cert)

	c, err := NewClient(srv.URL, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run([]string{"show version"}); err != nil {
		t.Errorf("skipVerify should accept anything: %v", err)
	}
}

func TestPinAndCAFileTogetherIsRejected(t *testing.T) {
	_, err := NewClient("https://192.0.2.1", "u", "p", time.Second, TLSOptions{
		CAFile:           "/some/ca.pem",
		PinnedCertSHA256: strings.Repeat("ab", 32),
	})
	if err == nil {
		t.Fatal("caFile and pinnedCertSha256 are mutually exclusive")
	}
}
