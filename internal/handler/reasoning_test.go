package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

type reasoningTestClient struct {
	models      []copilot.ModelInfo
	listErr     error
	listCalls   int
	createCalls int
	model       string
	effort      string
	streaming   bool
	tools       []copilot.Tool
}

func (c *reasoningTestClient) ResolveModel(model string) string {
	if model == "" {
		return "reasoning-model"
	}
	return model
}

func (c *reasoningTestClient) ListModels(context.Context) ([]copilot.ModelInfo, error) {
	c.listCalls++
	return c.models, c.listErr
}

func (c *reasoningTestClient) NewChatSessionWithTools(_ context.Context, model, _ string, streaming bool, tools []copilot.Tool, effort string) (*copilot.Session, error) {
	c.createCalls++
	c.model, c.effort, c.streaming, c.tools = model, effort, streaming, tools
	// Stop before a live SDK session is needed.
	return nil, errors.New("test session creation stopped")
}

func (c *reasoningTestClient) ReleaseSession(*copilot.Session) {}

func reasoningTestModel() copilot.ModelInfo {
	return copilot.ModelInfo{
		ID: "reasoning-model",
		Capabilities: copilot.ModelCapabilities{
			Supports: copilot.ModelSupports{ReasoningEffort: true},
		},
		SupportedReasoningEfforts: []string{"low", "high", "max"},
		DefaultReasoningEffort:    "high",
	}
}

func reasoningTestRequest(t *testing.T, payload map[string]any) *http.Request {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(data))
}

func reasoningTestTools() []map[string]any {
	return []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":       "lookup",
			"parameters": map[string]any{"type": "object"},
		},
	}}
}

func TestChatCompletions_ReasoningEffortForwarding(t *testing.T) {
	tests := []struct {
		name         string
		effort       string
		null         bool
		streaming    bool
		tools        bool
		defaultModel bool
	}{
		{name: "omitted"},
		{name: "null", null: true},
		{name: "explicit", effort: "high"},
		{name: "streaming", effort: "max", streaming: true},
		{name: "tools", effort: "high", tools: true},
		{name: "streaming tools", effort: "high", streaming: true, tools: true},
		{name: "resolved default model", effort: "high", defaultModel: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &reasoningTestClient{models: []copilot.ModelInfo{reasoningTestModel()}}
			if tt.effort == "" {
				client.listErr = errors.New("metadata must not be queried for defaults")
			}
			payload := map[string]any{
				"model":    "reasoning-model",
				"messages": []map[string]string{{"role": "user", "content": "hello"}},
				"stream":   tt.streaming,
			}
			if tt.defaultModel {
				delete(payload, "model")
			}
			if tt.effort != "" {
				payload["reasoning_effort"] = tt.effort
			} else if tt.null {
				payload["reasoning_effort"] = nil
			}
			if tt.tools {
				payload["tools"] = reasoningTestTools()
			}
			rec := httptest.NewRecorder()
			ChatCompletions(client, nil, silentLogger()).ServeHTTP(rec, reasoningTestRequest(t, payload))
			if client.createCalls != 1 {
				t.Fatalf("session creation calls = %d: %s", client.createCalls, rec.Body.String())
			}
			if client.effort != tt.effort || client.model != "reasoning-model" {
				t.Errorf("session received model %q / effort %q", client.model, client.effort)
			}
			if client.streaming != tt.streaming || (len(client.tools) > 0) != tt.tools {
				t.Errorf("streaming/tools not preserved: streaming=%v tools=%v", client.streaming, client.tools)
			}
			wantLookups := 0
			if tt.effort != "" {
				wantLookups = 1
			}
			if client.listCalls != wantLookups {
				t.Errorf("metadata lookups = %d, want %d", client.listCalls, wantLookups)
			}
		})
	}
}

func TestChatCompletions_ReasoningEffortErrors(t *testing.T) {
	unsupported := reasoningTestModel()
	unsupported.Capabilities.Supports.ReasoningEffort = false
	noLevels := reasoningTestModel()
	noLevels.SupportedReasoningEfforts = nil
	tests := []struct {
		name       string
		effort     any
		model      copilot.ModelInfo
		listErr    error
		wantStatus int
	}{
		{name: "empty", effort: "", wantStatus: http.StatusBadRequest},
		{name: "wrong type", effort: 3, wantStatus: http.StatusBadRequest},
		{name: "unadvertised level", effort: "xhigh", model: reasoningTestModel(), wantStatus: http.StatusBadRequest},
		{name: "unsupported model", effort: "high", model: unsupported, wantStatus: http.StatusBadRequest},
		{name: "unknown levels", effort: "high", model: noLevels, wantStatus: http.StatusBadRequest},
		{name: "unknown model", effort: "high", model: copilot.ModelInfo{ID: "different"}, wantStatus: http.StatusBadRequest},
		{name: "metadata unavailable", effort: "high", model: reasoningTestModel(), listErr: errors.New("backend unavailable"), wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tt.name, streaming), func(t *testing.T) {
				client := &reasoningTestClient{models: []copilot.ModelInfo{tt.model}, listErr: tt.listErr}
				payload := map[string]any{
					"model":            "reasoning-model",
					"messages":         []map[string]string{{"role": "user", "content": "hello"}},
					"reasoning_effort": tt.effort,
					"stream":           streaming,
				}
				rec := httptest.NewRecorder()
				ChatCompletions(client, nil, silentLogger()).ServeHTTP(rec, reasoningTestRequest(t, payload))
				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
				}
				if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
					t.Errorf("expected JSON error before SSE, got %q", rec.Header().Get("Content-Type"))
				}
				if client.createCalls != 0 {
					t.Error("invalid effort must not create a session")
				}
				var body struct {
					Error struct {
						Type string `json:"type"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				wantType := "invalid_request_error"
				if tt.wantStatus == http.StatusInternalServerError {
					wantType = "server_error"
				}
				if body.Error.Type != wantType {
					t.Errorf("error type = %q, want %q", body.Error.Type, wantType)
				}
			})
		}
	}
}

func TestChatCompletions_ReasoningEffortResumeMismatch(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			client := &reasoningTestClient{}
			registry := NewToolSessionRegistry(time.Minute, 10, nil, silentLogger())
			token := newToolSessionToken()
			callID := encodeToolCallID(token, 0)
			registry.store(token, nil, map[string]string{callID: "request-1"}, "high")
			payload := map[string]any{
				"model":            "reasoning-model",
				"messages":         []map[string]string{{"role": "tool", "content": "result", "tool_call_id": callID}},
				"tools":            reasoningTestTools(),
				"reasoning_effort": "low",
				"stream":           streaming,
			}
			rec := httptest.NewRecorder()
			ChatCompletions(client, registry, silentLogger()).ServeHTTP(rec, reasoningTestRequest(t, payload))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
				t.Error("resume mismatch committed SSE instead of returning JSON")
			}
			if client.listCalls != 0 || client.createCalls != 0 {
				t.Error("resume must use the original session configuration")
			}
			entry, err := registry.take(token, nil)
			if err != nil {
				t.Fatalf("mismatch consumed the retriable session: %v", err)
			}
			if entry.reasoningEffort != "high" {
				t.Errorf("original effort changed to %q", entry.reasoningEffort)
			}
		})
	}
}
