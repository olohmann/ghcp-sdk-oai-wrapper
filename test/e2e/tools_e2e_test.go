//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// weatherTool is the OpenAI tool declaration reused across the tool-calling
// e2e tests.
var weatherTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "get_weather",
		"description": "Get the current weather for a given city.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "The city to get the weather for, e.g. 'Paris'.",
				},
			},
			"required": []string{"city"},
		},
	},
}

// toolCallResponse is the subset of the non-streaming response we assert on.
type toolCallResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

func postJSON(t *testing.T, path string, payload any) *http.Response {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	resp, err := doRequest(t, "POST", path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

// TestChatCompletions_ToolCall_NonStreaming verifies that a request declaring a
// tool, with a prompt that forces its use, yields finish_reason:"tool_calls"
// and a well-formed get_weather tool call.
func TestChatCompletions_ToolCall_NonStreaming(t *testing.T) {
	payload := map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "system", "content": "You must use the get_weather tool to answer weather questions. Do not answer from your own knowledge."},
			{"role": "user", "content": "What's the weather in Paris right now? Use the get_weather tool."},
		},
		"tools":       []any{weatherTool},
		"tool_choice": "auto",
	}

	resp := postJSON(t, "/v1/chat/completions", payload)
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)
	assertContentType(t, resp, "application/json")

	var body toolCallResponse
	decodeJSON(t, resp.Body, &body)

	if len(body.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	choice := body.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Fatalf("expected finish_reason=tool_calls, got %q (content=%q)", choice.FinishReason, choice.Message.Content)
	}
	if len(choice.Message.ToolCalls) == 0 {
		t.Fatal("expected at least one tool_call")
	}
	tc := choice.Message.ToolCalls[0]
	if tc.Function.Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %q", tc.Function.Name)
	}
	if tc.ID == "" {
		t.Error("expected non-empty tool_call id")
	}
	if tc.Type != "function" {
		t.Errorf("expected type function, got %q", tc.Type)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v (%q)", err, tc.Function.Arguments)
	}
	t.Logf("tool call: %s(%s)", tc.Function.Name, tc.Function.Arguments)
}

// TestChatCompletions_ToolRoundTrip_NonStreaming verifies the full two-turn
// loop: emit tool_calls, feed a tool result back, and confirm the final answer
// reflects the injected result.
func TestChatCompletions_ToolRoundTrip_NonStreaming(t *testing.T) {
	tools := []any{weatherTool}

	// Turn 1: get the tool call.
	turn1 := map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "system", "content": "You must use the get_weather tool to answer weather questions. Do not answer from your own knowledge."},
			{"role": "user", "content": "What's the weather in Paris right now? Use the get_weather tool."},
		},
		"tools":       tools,
		"tool_choice": "auto",
	}

	resp := postJSON(t, "/v1/chat/completions", turn1)
	var first toolCallResponse
	func() {
		defer resp.Body.Close()
		assertStatus(t, resp, http.StatusOK)
		decodeJSON(t, resp.Body, &first)
	}()

	if len(first.Choices) == 0 || len(first.Choices[0].Message.ToolCalls) == 0 {
		t.Fatalf("turn 1 did not produce a tool call: %+v", first)
	}
	tc := first.Choices[0].Message.ToolCalls[0]

	// Turn 2: send the assistant tool_calls + a tool result with a distinctive
	// fact that could not come from the model's own knowledge.
	const distinctive = "the weather is 42 degrees Celsius and raining frogs"
	assistantMsg := map[string]any{
		"role":    "assistant",
		"content": nil,
		"tool_calls": []map[string]any{{
			"id":   tc.ID,
			"type": "function",
			"function": map[string]any{
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			},
		}},
	}
	toolMsg := map[string]any{
		"role":         "tool",
		"tool_call_id": tc.ID,
		"content":      "In Paris " + distinctive + ".",
	}
	turn2 := map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "system", "content": "You must use the get_weather tool to answer weather questions. Report exactly what the tool returns."},
			{"role": "user", "content": "What's the weather in Paris right now? Use the get_weather tool."},
			assistantMsg,
			toolMsg,
		},
		"tools":       tools,
		"tool_choice": "auto",
	}

	resp2 := postJSON(t, "/v1/chat/completions", turn2)
	defer resp2.Body.Close()
	assertStatus(t, resp2, http.StatusOK)

	var second toolCallResponse
	decodeJSON(t, resp2.Body, &second)
	if len(second.Choices) == 0 {
		t.Fatal("turn 2: expected a choice")
	}
	choice := second.Choices[0]
	// The parked session resolved the original pending call, so the model
	// continues to a final answer instead of re-invoking the tool.
	if choice.FinishReason != "stop" {
		t.Errorf("expected finish_reason=stop on resume, got %q", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 0 {
		t.Errorf("expected the tool NOT to be re-invoked on resume, got %d tool_calls", len(choice.Message.ToolCalls))
	}
	content := strings.ToLower(choice.Message.Content)
	t.Logf("turn 2 final answer: %s", choice.Message.Content)
	if !strings.Contains(content, "42") && !strings.Contains(content, "frog") {
		t.Errorf("expected final answer to reflect injected tool result (42 / frogs), got: %q", choice.Message.Content)
	}
}

// TestChatCompletions_ToolSessionExpired_409 verifies that a turn-2 request whose
// tool_call_id does not map to a live parked session returns HTTP 409 with the
// tool_session_expired error type (there is no replay fallback).
func TestChatCompletions_ToolSessionExpired_409(t *testing.T) {
	// A well-formed but never-parked token: call_<token>_<index>. The token is
	// underscore-free hex so it decodes cleanly, but no session exists for it.
	fakeID := "call_deadbeefdeadbeefdeadbeefdeadbeef_0"
	turn2 := map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "system", "content": "You must use the get_weather tool."},
			{"role": "user", "content": "What's the weather in Paris? Use the get_weather tool."},
			{"role": "assistant", "content": nil, "tool_calls": []map[string]any{{
				"id":       fakeID,
				"type":     "function",
				"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Paris"}`},
			}}},
			{"role": "tool", "tool_call_id": fakeID, "content": "In Paris it is 18C."},
		},
		"tools":       []any{weatherTool},
		"tool_choice": "auto",
	}

	resp := postJSON(t, "/v1/chat/completions", turn2)
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusConflict)

	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	decodeJSON(t, resp.Body, &errBody)
	if errBody.Error.Type != "tool_session_expired" {
		t.Errorf("expected error.type=tool_session_expired, got %q (message=%q)", errBody.Error.Type, errBody.Error.Message)
	}
}

// TestChatCompletions_ToolRoundTrip_Streaming verifies the two-turn loop over the
// streaming path: turn-1 streams tool_calls deltas, turn-2 resolves the parked
// session and streams the final answer terminated by finish_reason:"stop".
func TestChatCompletions_ToolRoundTrip_Streaming(t *testing.T) {
	tools := []any{weatherTool}

	// Turn 1 (non-streaming) to obtain the minted tool_call id cleanly.
	turn1 := map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "system", "content": "You must use the get_weather tool to answer weather questions. Do not answer from your own knowledge."},
			{"role": "user", "content": "What's the weather in Paris right now? Use the get_weather tool."},
		},
		"tools":       tools,
		"tool_choice": "auto",
	}
	resp := postJSON(t, "/v1/chat/completions", turn1)
	var first toolCallResponse
	func() {
		defer resp.Body.Close()
		assertStatus(t, resp, http.StatusOK)
		decodeJSON(t, resp.Body, &first)
	}()
	if len(first.Choices) == 0 || len(first.Choices[0].Message.ToolCalls) == 0 {
		t.Fatalf("turn 1 did not produce a tool call: %+v", first)
	}
	tc := first.Choices[0].Message.ToolCalls[0]

	// Turn 2 (streaming): resolve the parked call with a distinctive result.
	const distinctive = "the weather is 42 degrees Celsius and raining frogs"
	turn2 := map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "system", "content": "You must use the get_weather tool to answer weather questions. Report exactly what the tool returns."},
			{"role": "user", "content": "What's the weather in Paris right now? Use the get_weather tool."},
			{"role": "assistant", "content": nil, "tool_calls": []map[string]any{{
				"id":       tc.ID,
				"type":     "function",
				"function": map[string]any{"name": tc.Function.Name, "arguments": tc.Function.Arguments},
			}}},
			{"role": "tool", "tool_call_id": tc.ID, "content": "In Paris " + distinctive + "."},
		},
		"tools":       tools,
		"tool_choice": "auto",
		"stream":      true,
	}

	resp2 := postJSON(t, "/v1/chat/completions", turn2)
	defer resp2.Body.Close()
	assertStatus(t, resp2, http.StatusOK)
	assertContentType(t, resp2, "text/event-stream")

	scanner := bufio.NewScanner(resp2.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var content strings.Builder
	finishReason := ""
	gotDone := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if line == "data: [DONE]" {
			gotDone = true
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatalf("failed to parse chunk: %v", err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		content.WriteString(chunk.Choices[0].Delta.Content)
		if chunk.Choices[0].FinishReason != nil {
			finishReason = *chunk.Choices[0].FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("SSE scanner error: %v", err)
	}
	if !gotDone {
		t.Error("expected [DONE] marker")
	}
	if finishReason != "stop" {
		t.Errorf("expected finish_reason=stop on streamed resume, got %q", finishReason)
	}
	lc := strings.ToLower(content.String())
	t.Logf("streamed final answer: %s", content.String())
	if !strings.Contains(lc, "42") && !strings.Contains(lc, "frog") {
		t.Errorf("expected streamed final answer to reflect injected tool result (42 / frogs), got: %q", content.String())
	}
}

// TestChatCompletions_ToolMultiStep_Parked exercises multi-step agentic tool use
// across HTTP round-trips: after the first tool result is delivered, the model
// may request a second tool call on the same parked session. The test asserts
// the mechanism is sound — the round-trip always completes with HTTP 200 and,
// if a second tool is requested, its call is well-formed and embeds a parking
// token so a further turn could resolve it. Whether the model chains a second
// call is model-dependent, so a direct final answer is also accepted.
func TestChatCompletions_ToolMultiStep_Parked(t *testing.T) {
	tools := []any{weatherTool}

	turn1 := map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "system", "content": "You must use the get_weather tool for EACH city, one call at a time. Never answer a city's weather from your own knowledge."},
			{"role": "user", "content": "Compare the weather in Paris and Berlin. Call get_weather for Paris first."},
		},
		"tools":       tools,
		"tool_choice": "auto",
	}
	resp := postJSON(t, "/v1/chat/completions", turn1)
	var first toolCallResponse
	func() {
		defer resp.Body.Close()
		assertStatus(t, resp, http.StatusOK)
		decodeJSON(t, resp.Body, &first)
	}()
	if len(first.Choices) == 0 || len(first.Choices[0].Message.ToolCalls) == 0 {
		t.Fatalf("turn 1 did not produce a tool call: %+v", first)
	}

	// Deliver a result for every tool call the model made on turn 1.
	msgs := []map[string]any{
		{"role": "system", "content": "You must use the get_weather tool for EACH city, one call at a time. Never answer a city's weather from your own knowledge."},
		{"role": "user", "content": "Compare the weather in Paris and Berlin. Call get_weather for Paris first."},
	}
	assistantCalls := make([]map[string]any, 0, len(first.Choices[0].Message.ToolCalls))
	toolMsgs := make([]map[string]any, 0, len(first.Choices[0].Message.ToolCalls))
	for _, c := range first.Choices[0].Message.ToolCalls {
		assistantCalls = append(assistantCalls, map[string]any{
			"id":       c.ID,
			"type":     "function",
			"function": map[string]any{"name": c.Function.Name, "arguments": c.Function.Arguments},
		})
		toolMsgs = append(toolMsgs, map[string]any{
			"role":         "tool",
			"tool_call_id": c.ID,
			"content":      "Paris is 18 degrees Celsius and sunny.",
		})
	}
	msgs = append(msgs, map[string]any{"role": "assistant", "content": nil, "tool_calls": assistantCalls})
	for _, tm := range toolMsgs {
		msgs = append(msgs, tm)
	}

	turn2 := map[string]any{
		"model":       "gpt-5.4",
		"messages":    msgs,
		"tools":       tools,
		"tool_choice": "auto",
	}
	resp2 := postJSON(t, "/v1/chat/completions", turn2)
	defer resp2.Body.Close()
	assertStatus(t, resp2, http.StatusOK)

	var second toolCallResponse
	decodeJSON(t, resp2.Body, &second)
	if len(second.Choices) == 0 {
		t.Fatal("turn 2: expected a choice")
	}
	choice := second.Choices[0]
	switch choice.FinishReason {
	case "tool_calls":
		if len(choice.Message.ToolCalls) == 0 {
			t.Fatal("finish_reason=tool_calls but no tool_calls present")
		}
		next := choice.Message.ToolCalls[0]
		if next.Function.Name != "get_weather" {
			t.Errorf("expected a second get_weather call, got %q", next.Function.Name)
		}
		if !strings.HasPrefix(next.ID, "call_") {
			t.Errorf("multi-step tool_call id should embed a fresh parking token, got %q", next.ID)
		}
		t.Logf("multi-step: model chained a second tool call %s(%s)", next.Function.Name, next.Function.Arguments)
	case "stop":
		t.Logf("multi-step: model answered directly after first result: %s", choice.Message.Content)
	default:
		t.Errorf("unexpected finish_reason %q on multi-step resume", choice.FinishReason)
	}
}

// TestChatCompletions_ToolCall_Streaming verifies that streaming tool calls are
// emitted as tool_calls deltas and terminated with finish_reason:"tool_calls".
func TestChatCompletions_ToolCall_Streaming(t *testing.T) {
	payload := map[string]any{
		"model": "gpt-5.4",
		"messages": []map[string]any{
			{"role": "system", "content": "You must use the get_weather tool to answer weather questions. Do not answer from your own knowledge."},
			{"role": "user", "content": "What's the weather in Paris right now? Use the get_weather tool."},
		},
		"tools":       []any{weatherTool},
		"tool_choice": "auto",
		"stream":      true,
	}

	resp := postJSON(t, "/v1/chat/completions", payload)
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)
	assertContentType(t, resp, "text/event-stream")

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	toolName := ""
	argsByIndex := map[int]*strings.Builder{}
	finishReason := ""
	gotDone := false

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if line == "data: [DONE]" {
			gotDone = true
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatalf("failed to parse chunk: %v", err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		for _, tcd := range ch.Delta.ToolCalls {
			if tcd.Function.Name != "" {
				toolName = tcd.Function.Name
			}
			if _, ok := argsByIndex[tcd.Index]; !ok {
				argsByIndex[tcd.Index] = &strings.Builder{}
			}
			argsByIndex[tcd.Index].WriteString(tcd.Function.Arguments)
		}
		if ch.FinishReason != nil {
			finishReason = *ch.FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("SSE scanner error: %v", err)
	}

	if !gotDone {
		t.Error("expected [DONE] marker")
	}
	if toolName != "get_weather" {
		t.Errorf("expected a get_weather tool_calls delta, got name %q", toolName)
	}
	if finishReason != "tool_calls" {
		t.Errorf("expected finish_reason=tool_calls, got %q", finishReason)
	}
	if b, ok := argsByIndex[0]; ok {
		var args map[string]any
		if err := json.Unmarshal([]byte(b.String()), &args); err != nil {
			t.Errorf("streamed arguments not valid JSON: %v (%q)", err, b.String())
		}
		t.Logf("streamed tool call: %s(%s)", toolName, b.String())
	} else {
		t.Error("expected tool_calls arguments at index 0")
	}
}
