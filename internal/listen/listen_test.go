package listen

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krisiasty/arex/internal/secret"
)

// writeCert generates a self-signed certificate into dir under the given
// names, returning the paths.
func writeCert(t *testing.T, dir, cn string) (certPath, keyPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost"},
		// The tests dial 127.0.0.1, and Go has matched against SANs only since
		// 1.15 -- the same rule that makes a stock EOS certificate unverifiable.
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, cn+".crt")
	keyPath = filepath.Join(dir, cn+".key")
	// A certificate is public; 0644 is what a mounted secret or cert-manager
	// produces, and the tests should look like the real thing.
	//nolint:gosec // G306: a certificate is not a secret
	if err := os.WriteFile(certPath, pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestNoTLSConfigured(t *testing.T) {
	cfg, err := TLSConfig(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Error("without a certificate there is no TLS config, and the server serves plain HTTP")
	}
}

func TestClientCAWithoutCertIsRejected(t *testing.T) {
	if _, err := TLSConfig(Options{ClientCAFile: "/dev/null"}); err == nil {
		t.Error("a client CA without a server certificate cannot work")
	}
}

// The whole point of serving over TLS: a scrape works, over TLS, using the
// certificate arex was configured with.
//
// A real server rather than httptest: StartTLS installs a certificate of its
// own, which would leave the configuration under test unexercised.
func serveTLS(t *testing.T, cfg *tls.Config, h http.Handler) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, TLSConfig: cfg, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return "https://" + ln.Addr().String()
}

// trustPool builds a pool trusting the given PEM certificate.
func trustPool(t *testing.T, certFile string) *x509.CertPool {
	t.Helper()
	b, err := os.ReadFile(certFile) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		t.Fatal("no certificate in " + certFile)
	}
	return pool
}

func TestServesOverTLS(t *testing.T) {
	cert, key := writeCert(t, t.TempDir(), "server")
	cfg, err := TLSConfig(Options{CertFile: cert, KeyFile: key})
	if err != nil {
		t.Fatal(err)
	}
	url := serveTLS(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: trustPool(t, cert), MinVersion: tls.VersionTLS12},
	}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("TLS scrape failed against the configured certificate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %s", resp.Status)
	}
}

// cert-manager renews a certificate without restarting anything, so a
// certificate read once at startup would expire in memory while a valid one
// sat on disk.
func TestCertificateIsReloadedWhenItChanges(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeCert(t, dir, "server")

	c, err := newCertificate(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	first := c.get(time.Now())

	// Replace both files, as a renewal does.
	second, secondKey := writeCert(t, t.TempDir(), "renewed")
	copyFile(t, second, cert)
	copyFile(t, secondKey, key)

	// Before the interval elapses the cached one is still served: the files
	// are not stat-ed on every handshake.
	if got := c.get(time.Now()); got != first {
		t.Error("the certificate should not be re-read on every handshake")
	}

	// Once it has, the new pair is picked up.
	got := c.get(time.Now().Add(reloadInterval))
	if got == first {
		t.Fatal("a renewed certificate was not picked up")
	}
	leaf, err := x509.ParseCertificate(got.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Subject.CommonName != "renewed" {
		t.Errorf("serving CN=%q, want the renewed certificate", leaf.Subject.CommonName)
	}
}

// A half-written pair during renewal must not take the endpoint down while a
// valid certificate is still loaded.
func TestBrokenReloadKeepsTheWorkingCertificate(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeCert(t, dir, "server")
	c, err := newCertificate(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	good := c.get(time.Now())

	//nolint:gosec // G306: matching how the valid certificate above is written
	if err := os.WriteFile(cert, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.get(time.Now().Add(reloadInterval)); got != good {
		t.Error("a broken certificate on disk must not replace the working one in memory")
	}
}

// copyFile replaces to with the contents of from, as a certificate renewal
// does. Both paths come from t.TempDir.
func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(from))
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G703: both paths are t.TempDir, not input
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBasicAuth(t *testing.T) {
	dir := t.TempDir()
	pwPath := filepath.Join(dir, "pw")
	if err := os.WriteFile(pwPath, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, err := secret.NewFileCredential(pwPath)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("served"))
	})
	h := BasicAuth(inner, "prometheus", cred, "/livez", "/readyz")

	call := func(path, user, pass string, withAuth bool) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		if withAuth {
			req.SetBasicAuth(user, pass)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := call("/metrics", "prometheus", "hunter2", true); got != http.StatusOK {
		t.Errorf("correct credentials = %d, want 200", got)
	}
	if got := call("/metrics", "", "", false); got != http.StatusUnauthorized {
		t.Errorf("no credentials = %d, want 401", got)
	}
	if got := call("/metrics", "prometheus", "wrong", true); got != http.StatusUnauthorized {
		t.Errorf("wrong password = %d, want 401", got)
	}
	if got := call("/metrics", "someone", "hunter2", true); got != http.StatusUnauthorized {
		t.Errorf("wrong username = %d, want 401", got)
	}
	// /status names the switches, so it is protected like /metrics.
	if got := call("/status", "", "", false); got != http.StatusUnauthorized {
		t.Errorf("/status without credentials = %d, want 401", got)
	}
	// The probes are not, or a kubelet turns a health check into a restart loop.
	for _, p := range []string{"/livez", "/readyz"} {
		if got := call(p, "", "", false); got != http.StatusOK {
			t.Errorf("%s without credentials = %d, want 200", p, got)
		}
	}
}

// rotating is basic auth over a password file, with a clock the test moves and
// a log the test reads.
type rotating struct {
	t    *testing.T
	path string
	now  time.Time
	h    http.Handler
	logs *bytes.Buffer
}

// The passwords are long enough that finding one in the log means it leaked,
// not that it collided with some other word.
const (
	oldPassword = "old-scrape-secret-0b1c" //nolint:gosec // G101: a test fixture, not a credential
	newPassword = "new-scrape-secret-7e9d" //nolint:gosec // G101: a test fixture, not a credential
)

func newRotating(t *testing.T) *rotating {
	t.Helper()
	r := &rotating{t: t, path: filepath.Join(t.TempDir(), "pw"), now: time.Now(), logs: &bytes.Buffer{}}
	r.write(oldPassword + "\n")
	cred, err := secret.NewFileCredential(r.path)
	if err != nil {
		t.Fatal(err)
	}
	// Debug, so a password logged at any level would show up here.
	logger := slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := newBasicAuth("prometheus", cred, logger, func() time.Time { return r.now })
	r.h = a.handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	return r
}

// write replaces the password file, as the kubelet does when a secret rotates.
func (r *rotating) write(body string) {
	r.t.Helper()
	if err := os.WriteFile(r.path, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rotating) call(pass string) int {
	req := httptest.NewRequestWithContext(r.t.Context(), http.MethodGet, "/metrics", nil)
	req.SetBasicAuth("prometheus", pass)
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec.Code
}

// noPasswordsLogged fails if either password reached the log, at any level.
func (r *rotating) noPasswordsLogged() {
	r.t.Helper()
	for _, pw := range []string{oldPassword, newPassword} {
		if strings.Contains(r.logs.String(), pw) {
			r.t.Errorf("a password was logged:\n%s", r.logs)
		}
	}
}

// The password file is re-read after a rejection, so a rotated secret is
// accepted without a restart -- the same property the switch credentials have.
// Nothing here calls Reload: the middleware has to do it on its own.
func TestBasicAuthFollowsARotatedPassword(t *testing.T) {
	r := newRotating(t)

	if got := r.call(oldPassword); got != http.StatusOK {
		t.Fatalf("the current password = %d, want 200", got)
	}
	r.write(newPassword)
	r.now = r.now.Add(reloadInterval)

	// The first scrape with the new password is the one that finds it, and it
	// succeeds rather than waiting for the next.
	if got := r.call(newPassword); got != http.StatusOK {
		t.Errorf("the rotated password = %d, want 200", got)
	}
	if got := r.call(oldPassword); got != http.StatusUnauthorized {
		t.Errorf("the old password = %d, want 401", got)
	}
	if !strings.Contains(r.logs.String(), "scrape password rotated") {
		t.Errorf("a rotation should be logged, got:\n%s", r.logs)
	}
	r.noPasswordsLogged()
}

// Anyone who can reach the port can send a bad password, so a rejection must
// not be a file read every time. The throttle is shared across requests: a
// burst costs one read per interval, not one per request.
func TestBasicAuthThrottlesRereads(t *testing.T) {
	r := newRotating(t)

	// Inside the interval since the password was read, a rotated file is not
	// looked at: the new password is still refused.
	r.write(newPassword)
	r.now = r.now.Add(reloadInterval - time.Second)
	for range 10 {
		if got := r.call(newPassword); got != http.StatusUnauthorized {
			t.Fatalf("inside the interval = %d, want 401: the file was re-read", got)
		}
	}

	// Once the interval has passed, a burst of wrong passwords spends the one
	// read it allows and finds nothing changed...
	r.write(oldPassword)
	r.now = r.now.Add(time.Second)
	for range 10 {
		if got := r.call("wrong"); got != http.StatusUnauthorized {
			t.Fatalf("a wrong password = %d, want 401", got)
		}
	}
	// ...so a rotation landing just after it waits for the next interval.
	r.write(newPassword)
	if got := r.call(newPassword); got != http.StatusUnauthorized {
		t.Errorf("a second read inside one interval = %d, want 401", got)
	}
	r.now = r.now.Add(reloadInterval)
	if got := r.call(newPassword); got != http.StatusOK {
		t.Errorf("after the next interval = %d, want 200", got)
	}
	r.noPasswordsLogged()
}

// A half-written or missing file must not lock out a scraper that was working a
// moment ago.
func TestBasicAuthKeepsThePasswordWhenTheRereadFails(t *testing.T) {
	for name, broken := range map[string]func(r *rotating){
		"empty": func(r *rotating) { r.write("") },
		"missing": func(r *rotating) {
			if err := os.Remove(r.path); err != nil {
				r.t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRotating(t)
			broken(r)
			r.now = r.now.Add(reloadInterval)

			if got := r.call("wrong"); got != http.StatusUnauthorized {
				t.Fatalf("a wrong password = %d, want 401", got)
			}
			if got := r.call(oldPassword); got != http.StatusOK {
				t.Errorf("the password in memory = %d, want 200", got)
			}
			if !strings.Contains(r.logs.String(), "scrape password reload failed") {
				t.Errorf("a failed re-read should be logged, got:\n%s", r.logs)
			}
			r.noPasswordsLogged()
		})
	}
}

// mTLS: a client without a certificate is refused by the handshake, before any
// request is served.
func TestClientCertificateIsRequired(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey := writeCert(t, dir, "server")
	clientCert, clientKey := writeCert(t, dir, "client")

	cfg, err := TLSConfig(Options{
		CertFile: serverCert, KeyFile: serverKey, ClientCAFile: clientCert,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}

	url := serveTLS(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	pool := trustPool(t, serverCert)

	get := func(t *testing.T, c *http.Client) (*http.Response, error) {
		t.Helper()
		//nolint:govet // shadow: a closure must not write to the enclosing err
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c.Do(req)
	}

	// Without a client certificate: rejected at the handshake.
	bare := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	//nolint:govet // shadow: scoping resp and err to the if is the point
	if resp, err := get(t, bare); err == nil {
		_ = resp.Body.Close()
		t.Error("a client with no certificate should not be served")
	}

	// With one signed by the configured CA: accepted.
	pair, err := tls.LoadX509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	authed := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:      pool,
			Certificates: []tls.Certificate{pair},
			MinVersion:   tls.VersionTLS12,
		},
	}}
	resp, err := get(t, authed)
	if err != nil {
		t.Fatalf("a client with a valid certificate was refused: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %s", resp.Status)
	}
}
