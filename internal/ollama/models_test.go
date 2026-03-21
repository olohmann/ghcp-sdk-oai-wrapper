package ollama

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTags_MethodNotAllowed(t *testing.T) {
	h := Tags(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/tags", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestShow_MethodNotAllowed(t *testing.T) {
	h := Show(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/show", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestShow_MissingModel(t *testing.T) {
	h := Show(nil, nil)
	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
