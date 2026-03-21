package middleware

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// Auth returns middleware that validates Bearer token authentication.
// If apiKey is empty, authentication is disabled and all requests pass through.
// Paths listed in exemptPaths are always allowed without authentication.
func Auth(apiKey string, exemptPaths ...string) func(http.Handler) http.Handler {
	exempt := make(map[string]bool, len(exemptPaths))
	for _, p := range exemptPaths {
		exempt[p] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if exempt[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			auth := r.Header.Get("Authorization")
			if auth == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing Authorization header")
				return
			}

			token := strings.TrimPrefix(auth, "Bearer ")
			if token == auth || subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
				writeAuthError(w, http.StatusUnauthorized, "invalid API key")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    "invalid_request_error",
		},
	})
}
