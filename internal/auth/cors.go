// Minimal CORS middleware. The api accepts cross-origin requests from
// origins configured via API_CORS_ORIGINS (comma-separated). In dev this
// is http://localhost:3000 so the Next.js UI can reach the api. In
// production CORS should be terminated at the ingress; this middleware
// is a developer convenience.

package auth

import (
	"net/http"
	"os"
	"strings"
)

// CORS returns a middleware that emits Access-Control-* headers and
// short-circuits preflight requests. Origins is a comma-separated allowlist.
func CORS(origins string) func(http.Handler) http.Handler {
	allowed := parseOrigins(origins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allow(allowed, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-AiLab-Signature")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// DefaultCORSOrigins reads API_CORS_ORIGINS from the environment, falling
// back to a single localhost dev origin.
func DefaultCORSOrigins() string {
	if v := os.Getenv("API_CORS_ORIGINS"); v != "" {
		return v
	}
	return "http://localhost:3000"
}

func parseOrigins(csv string) []string {
	out := make([]string, 0)
	for _, p := range strings.Split(csv, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func allow(allowed []string, origin string) bool {
	for _, a := range allowed {
		if a == "*" || a == origin {
			return true
		}
	}
	return false
}
