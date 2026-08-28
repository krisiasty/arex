package eapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// TLSOptions selects how a switch's certificate is verified.
//
// At most one of CAFile and PinnedCertSHA256 may be set. SkipVerify applies
// only when neither is.
type TLSOptions struct {
	// SkipVerify disables verification entirely.
	SkipVerify bool

	// CAFile is a PEM bundle to verify the switch's certificate against.
	// This is the option to use once switch certificates have been replaced
	// with ones carrying correct subject alternative names.
	CAFile string

	// PinnedCertSHA256 is the SHA-256 of the switch's leaf certificate in
	// DER form -- what "openssl x509 -fingerprint -sha256" prints. Colons
	// and case are ignored.
	//
	// Pinning exists because a stock EOS switch cannot be verified any
	// other way. ARISTA_SELF_SIGNED.crt has no subjectAltName extension at
	// all and a common name of ARISTA_DEFAULT_SELF_SIGNED_PROFILE, so no
	// hostname or address can ever match it; EOS itself reports the profile
	// with errorType "hostnameMismatch". Trust is not the obstacle, naming
	// is, and no trust store fixes that. Pinning verifies the exact
	// certificate instead of a name, so an attacker needs the switch's
	// actual private key rather than any certificate at all.
	PinnedCertSHA256 string
}

// buildTLSConfig turns options into a *tls.Config, failing fast on anything
// misconfigured rather than at the first request.
func buildTLSConfig(opts TLSOptions) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if opts.CAFile != "" && opts.PinnedCertSHA256 != "" {
		return nil, errors.New("caFile and pinnedCertSha256 are mutually exclusive")
	}

	switch {
	case opts.PinnedCertSHA256 != "":
		want, err := parsePin(opts.PinnedCertSHA256)
		if err != nil {
			return nil, err
		}
		// Go's own verification must be disabled: it would reject the
		// certificate on the missing SAN before the pin is ever consulted.
		// The callbacks below replace it, so this is not unverified.
		cfg.InsecureSkipVerify = true
		cfg.VerifyPeerCertificate = pinVerifier(want)
		// VerifyPeerCertificate is skipped entirely on a resumed session,
		// which would let a resumption bypass the pin. VerifyConnection is
		// called on both fresh and resumed handshakes, so the pin is
		// enforced either way.
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("certificate pin: connection presented no certificate")
			}
			return checkPin(cs.PeerCertificates[0].Raw, want)
		}

	case opts.CAFile != "":
		pem, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read caFile: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("caFile %s contains no PEM certificates", opts.CAFile)
		}
		cfg.RootCAs = pool

	case opts.SkipVerify:
		cfg.InsecureSkipVerify = true
	}

	return cfg, nil
}

// parsePin accepts a hex SHA-256, with or without colons, in any case.
func parsePin(s string) ([]byte, error) {
	clean := strings.ToLower(strings.NewReplacer(":", "", " ", "", "-", "").Replace(s))
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("pinnedCertSha256 is not hex: %w", err)
	}
	if len(raw) != sha256.Size {
		return nil, fmt.Errorf("pinnedCertSha256 must be %d hex bytes, got %d", sha256.Size, len(raw))
	}
	return raw, nil
}

// pinVerifier checks the leaf certificate against the expected fingerprint.
func pinVerifier(want []byte) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("certificate pin: server presented no certificate")
		}
		return checkPin(rawCerts[0], want)
	}
}

// checkPin compares a DER-encoded certificate against the expected digest.
func checkPin(der, want []byte) error {
	got := sha256.Sum256(der)
	if subtle.ConstantTimeCompare(got[:], want) == 1 {
		return nil
	}
	// Report what was presented so it can be checked out of band and
	// configured, rather than leaving the operator to guess.
	return fmt.Errorf("certificate pin mismatch: presented sha256:%s, expected sha256:%s",
		hex.EncodeToString(got[:]), hex.EncodeToString(want))
}
