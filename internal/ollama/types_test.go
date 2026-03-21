package ollama

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRequest_IsStreaming_DefaultTrue(t *testing.T) {
	req := ChatRequest{Model: "test"}
	if !req.IsStreaming() {
		t.Error("expected IsStreaming() to default to true when Stream is nil")
	}
}

func TestChatRequest_IsStreaming_ExplicitFalse(t *testing.T) {
	req := ChatRequest{Model: "test", Stream: BoolPtr(false)}
	if req.IsStreaming() {
		t.Error("expected IsStreaming() to be false when Stream is explicitly false")
	}
}

func TestChatRequest_IsStreaming_ExplicitTrue(t *testing.T) {
	req := ChatRequest{Model: "test", Stream: BoolPtr(true)}
	if !req.IsStreaming() {
		t.Error("expected IsStreaming() to be true when Stream is explicitly true")
	}
}

func TestGenerateRequest_IsStreaming_DefaultTrue(t *testing.T) {
	req := GenerateRequest{Model: "test", Prompt: "hello"}
	if !req.IsStreaming() {
		t.Error("expected IsStreaming() to default to true when Stream is nil")
	}
}

func TestGenerateRequest_IsStreaming_ExplicitFalse(t *testing.T) {
	req := GenerateRequest{Model: "test", Prompt: "hello", Stream: BoolPtr(false)}
	if req.IsStreaming() {
		t.Error("expected IsStreaming() to be false when Stream is explicitly false")
	}
}

func TestChatRequest_JSONUnmarshal_StreamOmitted(t *testing.T) {
	data := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	var req ChatRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatal(err)
	}
	if req.Stream != nil {
		t.Error("expected Stream to be nil when omitted from JSON")
	}
	if !req.IsStreaming() {
		t.Error("expected IsStreaming() to be true when Stream is nil")
	}
}

func TestChatRequest_JSONUnmarshal_StreamFalse(t *testing.T) {
	data := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":false}`
	var req ChatRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatal(err)
	}
	if req.Stream == nil || *req.Stream != false {
		t.Error("expected Stream to be false")
	}
}

func TestChatResponse_JSONMarshal(t *testing.T) {
	resp := ChatResponse{
		Model:     "test",
		CreatedAt: "2024-01-01T00:00:00.000Z",
		Message:   Message{Role: "assistant", Content: "hello"},
		Done:      true,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"model":"test"`) {
		t.Errorf("expected model in JSON, got: %s", s)
	}
	if !strings.Contains(s, `"done":true`) {
		t.Errorf("expected done:true in JSON, got: %s", s)
	}
}

func TestNowRFC3339Milli_Format(t *testing.T) {
	ts := NowRFC3339Milli()
	// Should match pattern like "2024-01-01T00:00:00.000Z"
	if len(ts) != 24 {
		t.Errorf("expected 24-char timestamp, got %d: %s", len(ts), ts)
	}
	if !strings.HasSuffix(ts, "Z") {
		t.Errorf("expected timestamp to end with Z, got: %s", ts)
	}
}

func TestTagsResponse_JSONMarshal(t *testing.T) {
	resp := TagsResponse{
		Models: []ModelInfo{
			{Name: "gpt-4.1", Model: "gpt-4.1", ModifiedAt: "2024-01-01T00:00:00.000Z"},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"models"`) {
		t.Error("expected 'models' key in JSON")
	}
}
