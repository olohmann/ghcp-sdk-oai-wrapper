package middleware

import (
	"net/http"
	"strings"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

// Auth returns middleware that validates Bearer token authentication.
// If apiKey is empty, authentication is disabled and all requests pass through.
func Auth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			if token == auth || token != apiKey {
				oai.WriteError(w, http.StatusUnauthorized,
					"invalid API key",
					"invalid_request_error")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
