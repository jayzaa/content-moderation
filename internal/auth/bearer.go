// Package auth implements a minimal single-token bearer authorization
// middleware for the /api/* endpoints.
//
// This is intentionally simple ("easy mode"): one static token, configured
// via the API_BEARER_TOKEN environment variable, checked against the
// Authorization: Bearer <token> header on every request. There is no
// per-client token issuance, rotation, or expiry — treat this as a basic
// access gate suitable for a small trusted set of clients, not a
// full auth system.
package auth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// BearerMiddleware wraps next, rejecting any request whose Authorization
// header does not present the exact expected bearer token. Comparison is
// constant-time to avoid leaking token length/prefix via timing.
func BearerMiddleware(expectedToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expectedToken == "" {
			writeUnauthorized(w, "server misconfiguration: no API token configured")
			return
		}

		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			writeUnauthorized(w, "missing or malformed Authorization header (expected: Bearer <token>)")
			return
		}

		provided := strings.TrimPrefix(header, prefix)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expectedToken)) != 1 {
			writeUnauthorized(w, "invalid token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="image-detection"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
