package oai

import (
	"encoding/json"
	"testing"
)

func TestChatCompletionRequest_ToolsRoundTrip(t *testing.T) {
	raw := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "weather in Paris?"}],
		"tool_choice": "auto",
		"tools": [{
			"type": "function",
			"function": {
				"name": "get_weather",
				"description": "Get the weather for a city",
				"parameters": {
					"type": "object",
					"properties": {"city": {"type": "string"}},
					"required": ["city"]
				}
			}
		}]
	}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	tool := req.Tools[0]
	if tool.Type != "function" {
		t.Errorf("type: got %q", tool.Type)
	}
	if tool.Function.Name != "get_weather" {
		t.Errorf("name: got %q", tool.Function.Name)
	}
	if tool.Function.Parameters["type"] != "object" {
		t.Errorf("parameters.type: got %v", tool.Function.Parameters["type"])
	}
	if req.ToolChoice != "auto" {
		t.Errorf("tool_choice: got %v", req.ToolChoice)
	}
}

func TestMessage_ToolCallsRoundTrip(t *testing.T) {
	msg := Message{
		Role:    "assistant",
		Content: NewTextContent(""),
		ToolCalls: []ToolCall{{
			ID:       "call_abc",
			Type:     "function",
			Function: FunctionCall{Name: "get_weather", Arguments: `{"city":"Paris"}`},
		}},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(back.ToolCalls))
	}
	tc := back.ToolCalls[0]
	if tc.ID != "call_abc" || tc.Function.Name != "get_weather" {
		t.Errorf("unexpected tool_call: %+v", tc)
	}
	if tc.Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("arguments: got %q", tc.Function.Arguments)
	}
}

func TestMessage_ToolRoleWithNullContent(t *testing.T) {
	// An assistant tool_calls message has content:null; the tool result carries
	// a tool_call_id. Both must parse.
	raw := `[
		{"role": "assistant", "content": null, "tool_calls": [
			{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Paris\"}"}}
		]},
		{"role": "tool", "tool_call_id": "call_1", "content": "18C and sunny"}
	]`

	var msgs []Message
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content.TextContent() != "" {
		t.Errorf("expected empty content for null, got %q", msgs[0].Content.TextContent())
	}
	if len(msgs[0].ToolCalls) != 1 || msgs[0].ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant tool_calls not parsed: %+v", msgs[0].ToolCalls)
	}
	if msgs[1].Role != "tool" || msgs[1].ToolCallID != "call_1" {
		t.Errorf("tool message: %+v", msgs[1])
	}
	if msgs[1].Content.TextContent() != "18C and sunny" {
		t.Errorf("tool content: got %q", msgs[1].Content.TextContent())
	}
}

func TestDeltaMessage_ToolCallsMarshal(t *testing.T) {
	d := DeltaMessage{
		ToolCalls: []ToolCallDelta{{
			Index:    0,
			ID:       "call_1",
			Type:     "function",
			Function: &FunctionCallDelta{Name: "get_weather", Arguments: `{"city":"Paris"}`},
		}},
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	// role and content must be omitted when empty; tool_calls present.
	if got != `{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]}` {
		t.Errorf("unexpected delta JSON: %s", got)
	}
}

func TestDeltaMessage_EmptyOmitsFields(t *testing.T) {
	b, err := json.Marshal(DeltaMessage{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{}` {
		t.Errorf("expected empty object, got %s", string(b))
	}
}
