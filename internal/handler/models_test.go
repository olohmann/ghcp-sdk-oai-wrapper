package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

type modelListClient struct {
	models []copilot.ModelInfo
}

func TestModels_ReasoningEffort(t *testing.T) {
	tests := []struct {
		name          string
		supported     bool
		levels        []string
		defaultEffort string
	}{
		{name: "supported", supported: true, levels: []string{"low", "high", "max"}, defaultEffort: "high"},
		{name: "unsupported"},
		{name: "unknown levels", supported: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := modelListClient{models: []copilot.ModelInfo{{
				ID: "test-model",
				Capabilities: copilot.ModelCapabilities{
					Supports: copilot.ModelSupports{ReasoningEffort: tt.supported},
				},
				SupportedReasoningEfforts: tt.levels,
				DefaultReasoningEffort:    tt.defaultEffort,
			}}}
			rec := httptest.NewRecorder()
			Models(client, silentLogger()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Data) != 1 {
				t.Fatalf("expected one model, got %d", len(body.Data))
			}
			model := body.Data[0]
			var supported bool
			if err := json.Unmarshal(model["supports_reasoning_effort"], &supported); err != nil {
				t.Fatal(err)
			}
			if supported != tt.supported {
				t.Errorf("supports_reasoning_effort = %v, want %v", supported, tt.supported)
			}
			if raw, present := model["supported_reasoning_efforts"]; tt.levels == nil {
				if present {
					t.Errorf("unknown levels should be omitted, got %s", raw)
				}
			} else {
				var levels []string
				if err := json.Unmarshal(raw, &levels); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(levels, tt.levels) {
					t.Errorf("levels = %v, want %v", levels, tt.levels)
				}
			}
			if raw, present := model["default_reasoning_effort"]; tt.defaultEffort == "" {
				if present {
					t.Errorf("unknown default should be omitted, got %s", raw)
				}
			} else {
				var effort string
				if err := json.Unmarshal(raw, &effort); err != nil {
					t.Fatal(err)
				}
				if effort != tt.defaultEffort {
					t.Errorf("default = %q, want %q", effort, tt.defaultEffort)
				}
			}
		})
	}
}

func (c modelListClient) ListModels(context.Context) ([]copilot.ModelInfo, error) {
	return c.models, nil
}

func TestModels_Vision(t *testing.T) {
	for _, supported := range []bool{false, true} {
		name := "unsupported"
		if supported {
			name = "supported"
		}
		t.Run(name, func(t *testing.T) {
			client := modelListClient{models: []copilot.ModelInfo{{
				ID: "test-model",
				Capabilities: copilot.ModelCapabilities{
					Supports: copilot.ModelSupports{Vision: supported},
				},
			}}}
			rec := httptest.NewRecorder()
			Models(client, silentLogger()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Data) != 1 {
				t.Fatalf("expected one model, got %d", len(body.Data))
			}
			raw, present := body.Data[0]["supports_vision"]
			if !present {
				t.Fatal("supports_vision must be present even when false")
			}
			var got bool
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got != supported {
				t.Errorf("supports_vision = %v, want %v", got, supported)
			}
		})
	}
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
