package oai

import (
	"encoding/json"
	"testing"
)

func TestMessageContent_UnmarshalJSON_String(t *testing.T) {
	raw := `"Hello, world!"`
	var mc MessageContent
	if err := json.Unmarshal([]byte(raw), &mc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mc.Text != "Hello, world!" {
		t.Errorf("expected Text='Hello, world!', got %q", mc.Text)
	}
	if len(mc.Parts) != 0 {
		t.Errorf("expected no Parts, got %d", len(mc.Parts))
	}
}

func TestMessageContent_UnmarshalJSON_EmptyString(t *testing.T) {
	raw := `""`
	var mc MessageContent
	if err := json.Unmarshal([]byte(raw), &mc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mc.Text != "" {
		t.Errorf("expected empty Text, got %q", mc.Text)
	}
}

func TestMessageContent_UnmarshalJSON_TextParts(t *testing.T) {
	raw := `[{"type":"text","text":"Hello"},{"type":"text","text":"World"}]`
	var mc MessageContent
	if err := json.Unmarshal([]byte(raw), &mc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mc.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(mc.Parts))
	}
	if mc.Parts[0].Type != "text" || mc.Parts[0].Text != "Hello" {
		t.Errorf("unexpected part[0]: %+v", mc.Parts[0])
	}
	if mc.Parts[1].Type != "text" || mc.Parts[1].Text != "World" {
		t.Errorf("unexpected part[1]: %+v", mc.Parts[1])
	}
}

func TestMessageContent_UnmarshalJSON_MixedParts(t *testing.T) {
	raw := `[
		{"type":"text","text":"Describe this image"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBOR","detail":"high"}}
	]`
	var mc MessageContent
	if err := json.Unmarshal([]byte(raw), &mc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mc.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(mc.Parts))
	}
	if mc.Parts[0].Type != "text" {
		t.Errorf("expected text part, got %q", mc.Parts[0].Type)
	}
	img := mc.Parts[1]
	if img.Type != "image_url" {
		t.Errorf("expected image_url part, got %q", img.Type)
	}
	if img.ImageURL == nil {
		t.Fatal("expected ImageURL to be set")
	}
	if img.ImageURL.URL != "data:image/png;base64,iVBOR" {
		t.Errorf("unexpected URL: %q", img.ImageURL.URL)
	}
	if img.ImageURL.Detail != "high" {
		t.Errorf("expected detail=high, got %q", img.ImageURL.Detail)
	}
}

func TestMessageContent_TextContent_String(t *testing.T) {
	mc := NewTextContent("plain text")
	if got := mc.TextContent(); got != "plain text" {
		t.Errorf("expected 'plain text', got %q", got)
	}
}

func TestMessageContent_TextContent_Parts(t *testing.T) {
	mc := MessageContent{
		Parts: []ContentPart{
			{Type: "text", Text: "Hello"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
			{Type: "text", Text: "World"},
		},
	}
	if got := mc.TextContent(); got != "Hello\nWorld" {
		t.Errorf("expected 'Hello\\nWorld', got %q", got)
	}
}

func TestMessageContent_ImageParts(t *testing.T) {
	mc := MessageContent{
		Parts: []ContentPart{
			{Type: "text", Text: "Hello"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/jpeg;base64,def"}},
		},
	}
	imgs := mc.ImageParts()
	if len(imgs) != 2 {
		t.Fatalf("expected 2 image parts, got %d", len(imgs))
	}
}

func TestMessageContent_ImageParts_NoImages(t *testing.T) {
	mc := NewTextContent("just text")
	imgs := mc.ImageParts()
	if len(imgs) != 0 {
		t.Errorf("expected 0 image parts, got %d", len(imgs))
	}
}

func TestMessageContent_IsMultimodal(t *testing.T) {
	plain := NewTextContent("hello")
	if plain.IsMultimodal() {
		t.Error("plain text should not be multimodal")
	}

	multi := MessageContent{
		Parts: []ContentPart{
			{Type: "text", Text: "Hello"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
		},
	}
	if !multi.IsMultimodal() {
		t.Error("content with image_url should be multimodal")
	}
}

func TestMessageContent_MarshalJSON_String(t *testing.T) {
	mc := NewTextContent("hello")
	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"hello"` {
		t.Errorf("expected '\"hello\"', got %s", data)
	}
}

func TestMessageContent_MarshalJSON_Parts(t *testing.T) {
	mc := MessageContent{
		Parts: []ContentPart{
			{Type: "text", Text: "Hello"},
		},
	}
	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should marshal as array
	if data[0] != '[' {
		t.Errorf("expected array JSON, got %s", data)
	}
}

func TestMessage_UnmarshalJSON_StringContent(t *testing.T) {
	raw := `{"role":"user","content":"Hello"}`
	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != "user" {
		t.Errorf("expected role=user, got %q", msg.Role)
	}
	if msg.Content.TextContent() != "Hello" {
		t.Errorf("expected content=Hello, got %q", msg.Content.TextContent())
	}
}

func TestMessage_UnmarshalJSON_MultimodalContent(t *testing.T) {
	raw := `{
		"role": "user",
		"content": [
			{"type": "text", "text": "What is in this image?"},
			{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBOR"}}
		]
	}`
	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Role != "user" {
		t.Errorf("expected role=user, got %q", msg.Role)
	}
	if msg.Content.TextContent() != "What is in this image?" {
		t.Errorf("unexpected text: %q", msg.Content.TextContent())
	}
	if !msg.Content.IsMultimodal() {
		t.Error("expected multimodal content")
	}
	imgs := msg.Content.ImageParts()
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image part, got %d", len(imgs))
	}
}

func TestChatCompletionRequest_UnmarshalJSON_Multimodal(t *testing.T) {
	raw := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": [
				{"type": "text", "text": "Describe this image"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBOR"}}
			]}
		]
	}`
	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Model != "gpt-4o" {
		t.Errorf("unexpected model: %q", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}

	sysMsg := req.Messages[0]
	if sysMsg.Content.TextContent() != "You are helpful." {
		t.Errorf("unexpected system content: %q", sysMsg.Content.TextContent())
	}

	userMsg := req.Messages[1]
	if !userMsg.Content.IsMultimodal() {
		t.Error("expected multimodal user message")
	}
	if userMsg.Content.TextContent() != "Describe this image" {
		t.Errorf("unexpected text content: %q", userMsg.Content.TextContent())
	}
}
