package eapi

import (
	"errors"
	"fmt"

	"github.com/krisiasty/arex/internal/secret"
)

// WithCredential makes the client take its password from cred, replacing the
// password passed to NewClient. Pass "" there when using this.
func WithCredential(cred *secret.Credential) Option {
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
