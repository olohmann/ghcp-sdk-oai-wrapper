package oai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// SSEWriter writes Server-Sent Events to an http.ResponseWriter.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
	done    bool
}

// NewSSEWriter creates a new SSE writer and sets appropriate headers.
// Returns an error if the ResponseWriter does not support flushing.
func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	return &SSEWriter{w: w, flusher: flusher}, nil
}

// WriteEvent writes a single SSE data event with JSON payload.
func (s *SSEWriter) WriteEvent(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return fmt.Errorf("SSE stream already completed")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal SSE event: %w", err)
	}

	_, err = fmt.Fprintf(s.w, "data: %s\n\n", data)
	if err != nil {
		return fmt.Errorf("write SSE event: %w", err)
	}

	s.flusher.Flush()
	return nil
}

// WriteDone writes the final [DONE] marker.
func (s *SSEWriter) WriteDone() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	s.done = true
	_, err := fmt.Fprint(s.w, "data: [DONE]\n\n")
	if err != nil {
		return fmt.Errorf("write SSE done: %w", err)
	}

	s.flusher.Flush()
	return nil
}

// WriteError writes an error as a JSON response (non-SSE).
func WriteError(w http.ResponseWriter, status int, message, errType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error: ErrorDetail{
			Message: message,
			Type:    errType,
		},
	})
}

// WriteJSON writes a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
