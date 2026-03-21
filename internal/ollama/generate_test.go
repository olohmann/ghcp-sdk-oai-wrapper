package ollama

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerate_MethodNotAllowed(t *testing.T) {
	h := Generate(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/generate", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestGenerate_MissingModel(t *testing.T) {
	h := Generate(nil, nil)
	body := `{"prompt":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestGenerate_MissingPrompt(t *testing.T) {
	h := Generate(nil, nil)
	body := `{"model":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
