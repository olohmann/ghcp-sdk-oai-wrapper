package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

type modelListClient struct {
	models []copilot.ModelInfo
}

func (c modelListClient) ListModels(context.Context) ([]copilot.ModelInfo, error) {
	return c.models, nil
}

func TestModels_ContextWindow(t *testing.T) {
	contextWindow := 262144
	promptLimit := 240000
	outputLimit := 32768
	tests := []struct {
		name          string
		contextWindow *int
	}{
		{name: "reported", contextWindow: &contextWindow},
		{name: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := modelListClient{models: []copilot.ModelInfo{{
				ID: "test-model",
				Capabilities: copilot.ModelCapabilities{
					Limits: copilot.ModelLimits{
						MaxContextWindowTokens: tt.contextWindow,
						MaxPromptTokens:        &promptLimit,
						MaxOutputTokens:        &outputLimit,
					},
				},
			}}}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			Models(client, silentLogger()).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("failed to decode model list: %v", err)
			}
			if len(body.Data) != 1 {
				t.Fatalf("expected one model, got %d", len(body.Data))
			}
			raw, present := body.Data[0]["context_window"]
			if tt.contextWindow == nil {
				if present {
					t.Errorf("unknown context_window should be omitted, got %s", raw)
				}
				return
			}
			if !present {
				t.Fatal("SDK-reported context_window is missing")
			}
			var got int
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("failed to decode context_window: %v", err)
			}
			if got != *tt.contextWindow {
				t.Errorf("expected SDK context window %d, got %d", *tt.contextWindow, got)
			}
		})
	}
}
