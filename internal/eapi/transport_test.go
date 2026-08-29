package eapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// errServerClosedIdle reproduces the error net/http returns when the
// connection it took from the pool had already been closed. net/http does not
// export it, which is why transportError has to match on the text.
var errServerClosedIdle = errors.New("http: server closed idle connection")

// failure is one provoked transport error and the message it must become.
type failure struct {
	err     error
	want    string
	timeout time.Duration
}

// post drives a real request through client and returns the error it produced,
// which is what transportError has to classify. Provoking the failure rather
// than hand-building a *url.Error is the point: the shapes net/http returns for
// a refused connection or an untrusted certificate are not documented and have
// changed between releases, so a test built on assumed shapes proves nothing.
func post(t *testing.T, client *http.Client, target string) error {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target,
		strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build request for %s: %v", target, err)
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("request to %s unexpectedly succeeded", target)
	}
	return err
}

// listen opens a loopback listener on a free port.
func listen(t *testing.T) net.Listener {
	t.Helper()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

// deadPort returns an address with nothing listening on it: a listener is
// opened so the port is real and known free, then closed.
func deadPort(t *testing.T) string {
	t.Helper()

	ln := listen(t)
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return addr
}

func TestTransportError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		provoke func(t *testing.T) failure
	}{
		{
			name: "timeout waiting for a response",
			provoke: func(t *testing.T) failure {
				t.Helper()

				// Held open until cleanup rather than for a fixed sleep, so
				// Close does not block on the handler once the test is done.
				release := make(chan struct{})
				srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					<-release
				}))
				t.Cleanup(srv.Close)
				t.Cleanup(func() { close(release) })

				host, port := split(t, srv.URL)
				return failure{
					err: post(t, &http.Client{Timeout: 50 * time.Millisecond}, srv.URL),
					want: fmt.Sprintf("no response from %s within 50ms: check routing and "+
						"firewall rules to TCP %s, and that the switch is reachable from "+
						"this host", host, port),
					timeout: 50 * time.Millisecond,
				}
			},
		},
		{
			name: "connection refused",
			provoke: func(t *testing.T) failure {
				t.Helper()

				addr := deadPort(t)
				host, port, _ := net.SplitHostPort(addr)
				return failure{
					err: post(t, &http.Client{}, "http://"+addr),
					want: fmt.Sprintf("%s refused the connection on TCP %s: check that eAPI "+
						"is enabled and listening, including inside the management VRF",
						host, port),
				}
			},
		},
		{
			name: "hostname does not resolve",
			provoke: func(t *testing.T) failure {
				t.Helper()

				// .invalid is reserved by RFC 2606 and never resolves.
				return failure{
					err:  post(t, &http.Client{}, "https://switch.invalid/command-api"),
					want: `cannot resolve "switch.invalid": check DNS, or configure the switch by address`,
				}
			},
		},
		{
			name: "certificate signed by an unknown authority",
			provoke: func(t *testing.T) failure {
				t.Helper()

				srv := quietTLSServer(t)
				host, _ := split(t, srv.URL)
				return failure{
					err: post(t, &http.Client{}, srv.URL),
					want: fmt.Sprintf("%s presented a certificate we do not trust: set caFile "+
						"to the CA that signed it, or pinnedCertSha256 to the certificate "+
						"itself", host),
				}
			},
		},
		{
			name: "certificate is for a different name",
			provoke: func(t *testing.T) failure {
				t.Helper()

				srv := quietTLSServer(t)

				// The server's certificate covers example.com and 127.0.0.1.
				// Trust it, then ask for a name it does not cover.
				client := srv.Client()
				tr, ok := client.Transport.(*http.Transport)
				if !ok {
					t.Fatalf("test server client transport is %T, want *http.Transport",
						client.Transport)
				}
				tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, network, srv.Listener.Addr().String())
				}
				return failure{
					err: post(t, client, "https://switch.example/command-api"),
					want: "the certificate presented by switch.example is for a different " +
						"name: check the switch's eAPI certificate, or connect using a name " +
						"it covers",
				}
			},
		},
		{
			name: "certificate has expired",
			provoke: func(t *testing.T) failure {
				t.Helper()

				srv, pool := expiredCertServer(t)
				client := &http.Client{Transport: &http.Transport{
					TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
				}}
				host, _ := split(t, srv.URL)
				return failure{
					err: post(t, client, srv.URL),
					want: fmt.Sprintf("the certificate presented by %s has expired: renew "+
						"the switch's eAPI certificate", host),
				}
			},
		},
		{
			name: "connection closed before a response",
			provoke: func(t *testing.T) failure {
				t.Helper()

				ln := listen(t)
				t.Cleanup(func() { _ = ln.Close() })
				go func() {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					// Read the request before closing. Closing on accept
					// races the client's write, and the two sides of that
					// race are different errors -- one of them being the
					// pooled-connection case covered separately below.
					_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					_, _ = conn.Read(make([]byte, 1024))
					_ = conn.Close()
				}()

				host, _, _ := net.SplitHostPort(ln.Addr().String())
				return failure{
					err:  post(t, &http.Client{}, "http://"+ln.Addr().String()),
					want: host + " closed the connection before responding",
				}
			},
		},
		{
			name: "anything else keeps the cause without the URL prefix",
			provoke: func(t *testing.T) failure {
				t.Helper()

				return failure{
					err:  post(t, &http.Client{}, "ftp://switch.example/command-api"),
					want: `unsupported protocol scheme "ftp"`,
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := tc.provoke(t)
			got := transportError(f.err, f.timeout)

			if got.Error() != f.want {
				t.Errorf("transportError:\n got: %s\nwant: %s", got, f.want)
			}
			if !errors.Is(got, f.err) {
				t.Errorf("transportError dropped the cause: %v", got)
			}
		})
	}
}

// The failures below are the ones that cannot be provoked on demand, so they
// are the one place here where the error is constructed rather than caused.
//
// The two routing errnos have no portable trigger: a host that is reliably
// unreachable on every developer machine and CI runner does not exist, and a
// bogus address usually times out instead. The idle close is worse -- it is a
// race inside net/http, and the same test provoked it five times running and
// then not once in forty.
func TestTransportErrorUnprovokableFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cause error
		want  string
	}{
		{
			name:  "no route to host",
			cause: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)},
			want: "no route to 198.51.100.7: check routing from this host, and that the " +
				"management VRF has a path to the switch",
		},
		{
			name:  "network is unreachable",
			cause: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ENETUNREACH)},
			want: "no route to 198.51.100.7: check routing from this host, and that the " +
				"management VRF has a path to the switch",
		},
		{
			// arex holds keep-alive connections between polls, so a switch
			// that reboots or times the session out is met on the next poll
			// by a connection net/http believed was good.
			name:  "pooled connection closed between requests",
			cause: errServerClosedIdle,
			want: "198.51.100.7 closed the connection it was holding open: harmless once, " +
				"but check for an idle timeout on the switch if it repeats",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := &url.Error{Op: "Post", URL: "https://198.51.100.7/command-api", Err: tc.cause}
			if got := transportError(raw, 0); got.Error() != tc.want {
				t.Errorf("transportError:\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestIdleCloseTextIsStillCurrent guards the one classification that has to
// match on message text. If net/http ever rewords it this fails here, rather
// than quietly reverting that case to the unclassified fallback in production.
func TestIdleCloseTextIsStillCurrent(t *testing.T) {
	t.Parallel()

	// The string net/http builds its unexported errServerClosedIdle from.
	const current = "http: server closed idle connection"

	if errServerClosedIdle.Error() != current {
		t.Fatalf("test sentinel drifted: %q", errServerClosedIdle)
	}
	if !isIdleClose(errServerClosedIdle) {
		t.Errorf("isIdleClose no longer matches %q", current)
	}
}

// TestRunReportsUnreachableSwitch is the end of the path the operator actually
// sees: whatever Run returns is what reaches the log, so the classification has
// to survive the wrapping between here and there.
func TestRunReportsUnreachableSwitch(t *testing.T) {
	t.Parallel()

	addr := deadPort(t)
	host, port, _ := net.SplitHostPort(addr)

	c, err := NewClient("https://"+addr, "u", "p", 5*time.Second, TLSOptions{SkipVerify: true})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if _, err = c.Run([]string{"show version"}); err == nil {
		t.Fatal("Run against a dead port unexpectedly succeeded")
	}

	want := fmt.Sprintf("%s refused the connection on TCP %s: check that eAPI is enabled "+
		"and listening, including inside the management VRF", host, port)
	if err.Error() != want {
		t.Errorf("Run:\n got: %s\nwant: %s", err, want)
	}
}

// expiredCertServer serves TLS with a self-signed certificate that expired
// yesterday, and returns a pool trusting it -- so verification fails on the
// expiry alone rather than on the signer.
func expiredCertServer(t *testing.T) (*httptest.Server, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "expired.test"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}},
		MinVersion:   tls.VersionTLS12,
	}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, pool
}

// quietTLSServer is an httptest TLS server that does not log the handshake
// failures these tests exist to provoke.
func quietTLSServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// split returns the host and port of a test server URL.
func split(t *testing.T, raw string) (host, port string) {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return u.Hostname(), u.Port()
}
