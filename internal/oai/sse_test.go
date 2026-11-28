package oai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	oai.WriteJSON(rec, http.StatusOK, map[string]string{"foo": "bar"})

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if body["foo"] != "bar" {
		t.Errorf("expected bar, got %s", body["foo"])
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	oai.WriteError(rec, http.StatusBadRequest, "bad input", "invalid_request_error")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}

	var resp oai.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Error.Message != "bad input" {
		t.Errorf("expected 'bad input', got %s", resp.Error.Message)
	}
	if resp.Error.Type != "invalid_request_error" {
		t.Errorf("expected 'invalid_request_error', got %s", resp.Error.Type)
	}
}

func TestNewCompletionID(t *testing.T) {
	id := oai.NewCompletionID()
	if len(id) < 10 || id[:9] != "chatcmpl-" {
		t.Errorf("expected chatcmpl- prefix, got %s", id)
	}
}

func TestSSEWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	sse, err := oai.NewSSEWriter(rec)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := sse.WriteEvent(map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("write event failed: %v", err)
	}

	if err := sse.WriteDone(); err != nil {
		t.Fatalf("write done failed: %v", err)
	}

	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", ct)
	}
	if body == "" {
		t.Error("expected non-empty body")
	}
	// Check for SSE framing
	if !contains(body, "data: ") {
		t.Error("expected 'data: ' prefix in body")
	}
	if !contains(body, "[DONE]") {
		t.Error("expected [DONE] in body")
	}
}

func TestSSEWriterConcurrentTermination(t *testing.T) {
	rec := httptest.NewRecorder()
	sse, err := oai.NewSSEWriter(rec)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := sse.WriteEvent(map[string]string{"content": strings.Repeat("synthetic", 256)}); err != nil && err.Error() != "SSE stream already completed" {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if err := sse.WriteDone(); err != nil {
			t.Error(err)
		}
	}()
	close(start)
	wg.Wait()
	body := rec.Body.String()
	if strings.Count(body, "data: [DONE]") != 1 || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("events appeared after stream termination: %s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" && !json.Valid([]byte(strings.TrimPrefix(line, "data: "))) {
			t.Fatalf("concurrent events were interleaved: %s", line)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
