package eapi

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// transportError turns a failure from http.Client.Do into something an
// operator can act on.
//
// The Go standard library's text is written for the programmer who made the
// call, not the person reading a log at 3am: a firewall silently dropping our
// packets surfaces as `Post "https://10.0.0.1/command-api": context deadline
// exceeded (Client.Timeout exceeded while awaiting headers)`, which names a
// Go concept, a Go field, and the URL we already know, while never saying the
// switch did not answer.
//
// Each case below names what happened and what to check, in the same style as
// statusError does for HTTP statuses. The original error is kept as the cause
// rather than in the text -- errors.Is and errors.As still see it, and -debug
// still logs it verbatim, so nothing is lost for whoever needs the detail.
func transportError(err error, timeout time.Duration) error {
	host, port := endpoint(err)

	switch {
	// Checked before the timeout below: a resolver that times out is still a
	// name that did not resolve, and saying so is more use than reporting a
	// timeout against a host we never found.
	case isDNSFailure(err):
		return &transportErr{
			msg: fmt.Sprintf("cannot resolve %q: check DNS, or configure the switch by address",
				host),
			cause: err,
		}

	case isTimeout(err):
		return &transportErr{
			msg: fmt.Sprintf("no response from %s%s: check routing and firewall rules to TCP %s, "+
				"and that the switch is reachable from this host", host, within(timeout), port),
			cause: err,
		}

	case errors.Is(err, syscall.ECONNREFUSED):
		return &transportErr{
			msg: fmt.Sprintf("%s refused the connection on TCP %s: check that eAPI is enabled "+
				"and listening, including inside the management VRF", host, port),
			cause: err,
		}

	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return &transportErr{
			msg: fmt.Sprintf("no route to %s: check routing from this host, and that the "+
				"management VRF has a path to the switch", host),
			cause: err,
		}

	case isHostnameMismatch(err):
		return &transportErr{
			msg: fmt.Sprintf("the certificate presented by %s is for a different name: check "+
				"the switch's eAPI certificate, or connect using a name it covers", host),
			cause: err,
		}

	case isExpiredCertificate(err):
		return &transportErr{
			msg: fmt.Sprintf("the certificate presented by %s has expired: renew the switch's "+
				"eAPI certificate", host),
			cause: err,
		}

	case isUntrustedCertificate(err):
		return &transportErr{
			msg: fmt.Sprintf("%s presented a certificate we do not trust: set caFile to the CA "+
				"that signed it, or pinnedCertSha256 to the certificate itself", host),
			cause: err,
		}

	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF):
		return &transportErr{
			msg:   host + " closed the connection before responding",
			cause: err,
		}

	case isIdleClose(err):
		return &transportErr{
			msg: fmt.Sprintf("%s closed the connection it was holding open: harmless once, "+
				"but check for an idle timeout on the switch if it repeats", host),
			cause: err,
		}

	default:
		// Anything unclassified keeps its own text, minus the URL prefix
		// net/http adds -- the switch is already named by the log's own
		// field, and a certificate pin mismatch says all it needs to.
		return &transportErr{msg: cause(err), cause: err}
	}
}

// transportErr carries an operator-facing message over a machine-facing cause.
// The cause stays reachable through errors.Is and errors.As but out of the
// message, which is the whole point: the raw text is what made these errors
// unreadable.
type transportErr struct {
	msg   string
	cause error
}

func (e *transportErr) Error() string { return e.msg }
func (e *transportErr) Unwrap() error { return e.cause }

// endpoint names the switch we failed to reach. net/http wraps every failure
// from Do in a *url.Error carrying the request URL, so this is reliable in
// practice, but a caller-supplied error need not have one.
func endpoint(err error) (host, port string) {
	uerr, ok := errors.AsType[*url.Error](err)
	if !ok {
		return "the switch", "443"
	}
	u, perr := url.Parse(uerr.URL)
	if perr != nil {
		return "the switch", "443"
	}
	host = u.Hostname()
	if host == "" {
		host = "the switch"
	}
	port = u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return host, port
}

// within renders the configured timeout, and nothing at all when there is
// none to name.
func within(timeout time.Duration) string {
	if timeout <= 0 {
		return ""
	}
	return " within " + timeout.String()
}

// cause is the error's own text with net/http's `Post "<url>": ` prefix
// removed.
func cause(err error) string {
	if uerr, ok := errors.AsType[*url.Error](err); ok && uerr.Err != nil {
		return uerr.Err.Error()
	}
	return err.Error()
}

// isIdleClose reports whether net/http found the pooled connection already
// closed when it went to reuse it -- what a switch that rebooted or timed the
// session out between polls looks like from here.
//
// Matched on text because net/http builds this from an unexported
// errServerClosedIdle with no code or type to test for. Should the wording
// ever change, this stops matching and the error falls through to the
// unclassified case, which is a worse message rather than a wrong one;
// TestIdleCloseTextIsStillCurrent is there to catch that first.
func isIdleClose(err error) bool {
	return strings.Contains(err.Error(), "server closed idle connection")
}

func isDNSFailure(err error) bool {
	_, ok := errors.AsType[*net.DNSError](err)
	return ok
}

func isTimeout(err error) bool {
	nerr, ok := errors.AsType[net.Error](err)
	return ok && nerr.Timeout()
}

func isHostnameMismatch(err error) bool {
	_, ok := errors.AsType[x509.HostnameError](err)
	return ok
}

func isExpiredCertificate(err error) bool {
	cerr, ok := errors.AsType[x509.CertificateInvalidError](err)
	return ok && cerr.Reason == x509.Expired
}

func isUntrustedCertificate(err error) bool {
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	// Everything else the verifier rejects: a certificate that is not valid
	// for server authentication, or one whose constraints we fail.
	_, ok := errors.AsType[x509.CertificateInvalidError](err)
	return ok
}
