package listen

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/krisiasty/arex/internal/secret"
)

// BasicAuth requires a username and password on every request except the
// exempt paths.
//
// Exempt exists for the probes. A Kubernetes liveness probe sends no
// credentials, so requiring them on /livez turns a health check into a restart
// loop -- and those endpoints report only whether arex is up, which is not
// what the authentication is protecting.
func BasicAuth(next http.Handler, username string, cred *secret.Credential, exempt ...string) http.Handler {
	return newBasicAuth(username, cred, slog.Default(), time.Now).handler(next, exempt...)
}

// basicAuth checks credentials against a password that may rotate underneath
// it.
//
// When the kubelet updates a mounted secret, Prometheus starts sending the new
// password and every scrape is rejected until arex reads it too. So a
// rejection re-reads the file -- but not every rejection. The switch client can
// reload on each failure because arex decides when a poll runs; here anyone who
// can reach the port decides when a request arrives, and an unconditional
// re-read would turn each unauthenticated request into a file read.
type basicAuth struct {
	username string
	cred     *secret.Credential
	logger   *slog.Logger
	now      func() time.Time

	// reloaded is shared by every request, so a burst of bad credentials
	// costs one read per interval rather than one per request.
	mu       sync.Mutex
	reloaded time.Time
}

func newBasicAuth(username string, cred *secret.Credential, logger *slog.Logger, now func() time.Time) *basicAuth {
	return &basicAuth{
		username: username,
		cred:     cred,
		logger:   logger,
		now:      now,
		// The credential was read a moment ago, at startup.
		reloaded: now(),
	}
}

func (a *basicAuth) handler(next http.Handler, exempt ...string) http.Handler {
	skip := make(map[string]bool, len(exempt))
	for _, p := range exempt {
		skip[p] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skip[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok || !a.allow(user, pass) {
			// The realm names arex rather than the endpoint: a scraper reads
			// it, and so does whoever is debugging a 401 by hand.
			w.Header().Set("WWW-Authenticate", `Basic realm="arex"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allow reports whether the credentials match, re-reading the password file
// first if they do not and the last read was at least an interval ago.
//
// A changed password is checked against the same request, so the first scrape
// with a rotated secret succeeds rather than the one after it. A failed re-read
// keeps the password in memory, as Credential.Reload does: a half-written file
// must not lock out a scraper that was working a moment ago.
func (a *basicAuth) allow(user, pass string) bool {
	if matches(user, pass, a.username, a.cred.Password()) {
		return true
	}
	if !a.due() {
		return false
	}

	changed, err := a.cred.Reload()
	switch {
	case err != nil:
		a.logger.Warn("scrape password reload failed", "user", a.username, "error", err)
		return false
	case !changed:
		return false
	}
	// Warn, matching the switch client's "credential rotated": info carries
	// only the per-poll success record. The username, never the password.
	a.logger.Warn("scrape password rotated", "user", a.username)
	return matches(user, pass, a.username, a.cred.Password())
}

// due reports whether a re-read is allowed now, and claims it if so. Claiming
// under the lock is what makes concurrent rejections share one read.
func (a *basicAuth) due() bool {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.Sub(a.reloaded) < reloadInterval {
		return false
	}
	a.reloaded = now
	return true
}

// matches compares credentials in constant time.
//
// Both halves are always compared, even when the first has already failed:
// returning early on a wrong username would make the response time reveal
// whether the username was right.
func matches(gotUser, gotPass, wantUser, wantPass string) bool {
	u := subtle.ConstantTimeCompare([]byte(gotUser), []byte(wantUser))
	p := subtle.ConstantTimeCompare([]byte(gotPass), []byte(wantPass))
	return u&p == 1
}
