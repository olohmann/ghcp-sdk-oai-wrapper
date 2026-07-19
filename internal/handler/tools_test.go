package handler

import (
	"testing"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

func TestRequestHasTools(t *testing.T) {
	if requestHasTools(&oai.ChatCompletionRequest{}) {
		t.Error("expected false for no tools")
	}
	req := &oai.ChatCompletionRequest{Tools: []oai.Tool{{Type: "function", Function: oai.FunctionDef{Name: "x"}}}}
	if !requestHasTools(req) {
		t.Error("expected true when tools present")
	}
}

func TestHasToolResults(t *testing.T) {
	msgs := []oai.Message{
		{Role: "user", Content: oai.NewTextContent("hi")},
		{Role: "assistant", Content: oai.NewTextContent("")},
	}
	if hasToolResults(msgs) {
		t.Error("expected false without tool role")
	}
	msgs = append(msgs, oai.Message{Role: "tool", ToolCallID: "c1", Content: oai.NewTextContent("res")})
	if !hasToolResults(msgs) {
		t.Error("expected true with tool role")
	}
}

func TestToolsToSDK(t *testing.T) {
	tools := []oai.Tool{
		{Type: "function", Function: oai.FunctionDef{
			Name:        "get_weather",
			Description: "gets weather",
			Parameters:  map[string]any{"type": "object"},
		}},
		{Type: "function", Function: oai.FunctionDef{Name: ""}}, // skipped
	}
	out := toolsToSDK(tools)
	if len(out) != 1 {
		t.Fatalf("expected 1 tool (empty name skipped), got %d", len(out))
	}
	if out[0].Name != "get_weather" || out[0].Description != "gets weather" {
		t.Errorf("unexpected tool: %+v", out[0])
	}
	if !out[0].SkipPermission {
		t.Error("expected SkipPermission true")
	}
	if out[0].Handler != nil {
		t.Error("expected nil handler (declaration-only)")
	}
}

func TestArgumentsToJSON(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "{}"},
		{map[string]any{"a": 1}, `{"a":1}`},
		{`{"already":"json"}`, `{"already":"json"}`},
		{"not json", `"not json"`}, // quoted -> a valid JSON string
	}
	for _, c := range cases {
		if got := argumentsToJSON(c.in); got != c.want {
			t.Errorf("argumentsToJSON(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
