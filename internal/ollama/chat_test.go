package ollama

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildPrompt_SingleMessage(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hello"}}
	result := buildPrompt(msgs)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestBuildPrompt_SkipsSystemMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hello"},
	}
	result := buildPrompt(msgs)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestBuildPrompt_MultiTurn(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "how are you"},
	}
	result := buildPrompt(msgs)
	if !strings.Contains(result, "[user]: hi") {
		t.Errorf("expected multi-turn format, got %q", result)
	}
	if !strings.Contains(result, "[assistant]: hello") {
		t.Errorf("expected assistant turn in prompt, got %q", result)
	}
}

func TestBuildPrompt_Empty(t *testing.T) {
	result := buildPrompt(nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExtractSystemMessage(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "first"},
		{Role: "user", Content: "hello"},
		{Role: "system", Content: "second"},
	}
	result := extractSystemMessage(msgs)
	if result != "first\nsecond" {
		t.Errorf("expected 'first\\nsecond', got %q", result)
	}
}

func TestExtractSystemMessage_None(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hello"}}
	result := extractSystemMessage(msgs)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestChat_MethodNotAllowed(t *testing.T) {
	h := Chat(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestChat_MissingModel(t *testing.T) {
	h := Chat(nil, nil)
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestChat_MissingMessages(t *testing.T) {
	h := Chat(nil, nil)
	body := `{"model":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
