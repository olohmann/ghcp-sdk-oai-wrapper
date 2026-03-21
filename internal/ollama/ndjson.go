package ollama

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// NDJSONWriter writes newline-delimited JSON to an http.ResponseWriter.
type NDJSONWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewNDJSONWriter creates a writer and sets headers for NDJSON streaming.
func NewNDJSONWriter(w http.ResponseWriter) (*NDJSONWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	return &NDJSONWriter{w: w, flusher: flusher}, nil
}

// WriteLine writes a single JSON line followed by a newline, then flushes.
func (n *NDJSONWriter) WriteLine(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal NDJSON line: %w", err)
	}
	if _, err := n.w.Write(data); err != nil {
		return fmt.Errorf("write NDJSON line: %w", err)
	}
	if _, err := n.w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write newline: %w", err)
	}
	n.flusher.Flush()
	return nil
}

// WriteJSON writes a non-streaming JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes an Ollama-style error response.
func WriteError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
