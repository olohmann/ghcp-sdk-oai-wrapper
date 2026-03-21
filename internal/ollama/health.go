package ollama

import (
	"fmt"
	"net/http"
)

// Root returns the handler for GET / (Ollama health check).
func Root(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Ollama is running")
	}
}

// Version returns the handler for GET /api/version.
func Version(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		WriteJSON(w, http.StatusOK, VersionResponse{Version: version})
	}
}
