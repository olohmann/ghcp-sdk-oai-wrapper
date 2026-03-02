package handler

import (
	"net/http"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

// Health returns a simple health check handler.
func Health() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		oai.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
