package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/handler"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

func TestHealth(t *testing.T) {
	h := handler.Health()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected ok, got %s", body["status"])
	}
}

func TestChatCompletions_MethodNotAllowed(t *testing.T) {
	// Use nil client — we expect the method check to reject before using it
	h := handler.ChatCompletions(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}

	var resp oai.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Error.Type != "invalid_request_error" {
		t.Errorf("expected invalid_request_error, got %s", resp.Error.Type)
	}
}
