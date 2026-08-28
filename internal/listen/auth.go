package listen

import (
	"crypto/subtle"
	"net/http"

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
		if !ok || !matches(user, pass, username, cred.Password()) {
			// The realm names arex rather than the endpoint: a scraper reads
			// it, and so does whoever is debugging a 401 by hand.
			w.Header().Set("WWW-Authenticate", `Basic realm="arex"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
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
