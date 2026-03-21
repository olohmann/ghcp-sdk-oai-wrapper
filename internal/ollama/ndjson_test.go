package ollama

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewNDJSONWriter_SetsHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	w, err := NewNDJSONWriter(rec)
	if err != nil {
		t.Fatal(err)
	}
	_ = w

	ct := rec.Header().Get("Content-Type")
	if ct != "application/x-ndjson" {
		t.Errorf("expected Content-Type application/x-ndjson, got %s", ct)
	}
	cc := rec.Header().Get("Cache-Control")
	if cc != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %s", cc)
	}
}

func TestNDJSONWriter_WriteLine(t *testing.T) {
	rec := httptest.NewRecorder()
	w, err := NewNDJSONWriter(rec)
	if err != nil {
		t.Fatal(err)
	}

	chunk := ChatStreamChunk{
		Model:     "test",
		CreatedAt: "2024-01-01T00:00:00.000Z",
		Message:   Message{Role: "assistant", Content: "hello"},
		Done:      false,
	}

	if err := w.WriteLine(chunk); err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), body)
	}

	var decoded ChatStreamChunk
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
	if decoded.Model != "test" {
		t.Errorf("expected model 'test', got %q", decoded.Model)
	}
}

func TestNDJSONWriter_MultipleLines(t *testing.T) {
	rec := httptest.NewRecorder()
	w, err := NewNDJSONWriter(rec)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		_ = w.WriteLine(map[string]int{"i": i})
	}

	body := rec.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestWriteError_Format(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, 400, "bad request")

	if rec.Code != 400 {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["error"] != "bad request" {
		t.Errorf("expected error 'bad request', got %q", resp["error"])
	}
}

func TestWriteJSON_Format(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, 200, VersionResponse{Version: "1.0"})

	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	var resp VersionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Version != "1.0" {
		t.Errorf("expected version '1.0', got %q", resp.Version)
	}
}
