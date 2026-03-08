package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

// Auth returns middleware that validates Bearer token authentication.
// If apiKey is empty, authentication is disabled and all requests pass through.
// The /healthz and /metrics endpoints are always exempt from authentication.
func Auth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Allow health and metrics endpoints without auth
			if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}

			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			auth := r.Header.Get("Authorization")
			if auth == "" {
				oai.WriteError(w, http.StatusUnauthorized,
					"missing Authorization header",
					"invalid_request_error")
				return
			}

			token := strings.TrimPrefix(auth, "Bearer ")
			if token == auth || subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
				oai.WriteError(w, http.StatusUnauthorized,
					"invalid API key",
					"invalid_request_error")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
