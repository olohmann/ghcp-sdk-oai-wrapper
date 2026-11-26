package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

func TestTokenFromMessages_CurrentRound(t *testing.T) {
	oldID := encodeToolCallID("old", 0)
	newID := encodeToolCallID("new", 0)
	history := []oai.Message{
		{Role: "user", Content: oai.NewTextContent("start")},
		{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: oldID}}},
		{Role: "tool", ToolCallID: oldID, Content: oai.NewTextContent("old result")},
	}
	tests := []struct {
		name  string
		tail  []oai.Message
		token string
	}{
		{
			name: "latest round",
			tail: []oai.Message{
				{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: newID}}},
				{Role: "tool", ToolCallID: newID},
			},
			token: "new",
		},
		{
			name: "latest assistant fallback",
			tail: []oai.Message{
				{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: newID}}},
			},
			token: "new",
		},
		{
			name: "parallel results in current round",
			tail: []oai.Message{
				{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: newID}, {ID: encodeToolCallID("new", 1)}}},
				{Role: "tool", ToolCallID: encodeToolCallID("new", 1)},
				{Role: "tool", ToolCallID: newID},
			},
			token: "new",
		},
		{
			name: "invalid current round must not reuse history",
			tail: []oai.Message{
				{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: "external-id"}}},
				{Role: "tool", ToolCallID: "external-id"},
			},
		},
		{
			name: "completed turn",
			tail: []oai.Message{{Role: "assistant", Content: oai.NewTextContent("finished")}},
		},
		{
			name: "new user turn",
			tail: []oai.Message{
				{Role: "assistant", Content: oai.NewTextContent("finished")},
				{Role: "user", Content: oai.NewTextContent("next question")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := append(append([]oai.Message(nil), history...), tt.tail...)
			got, ok := tokenFromMessages(messages)
			if got != tt.token || ok != (tt.token != "") {
				t.Errorf("token = %q, ok = %v; want %q", got, ok, tt.token)
			}
		})
	}
}

func TestTakeParkedSession_MultipleRounds(t *testing.T) {
	registry := NewToolSessionRegistry(time.Minute, 10, nil, silentLogger())
	req := &oai.ChatCompletionRequest{}
	for round := range 3 {
		token := fmt.Sprintf("round%d", round)
		id := encodeToolCallID(token, 0)
		pending := map[string]string{id: fmt.Sprintf("request%d", round)}
		registry.store(token, nil, pending, "high")
		req.Messages = append(req.Messages,
			oai.Message{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: id}}},
			oai.Message{Role: "tool", ToolCallID: id, Content: oai.NewTextContent("result")},
		)
		entry, err := takeParkedSession(registry, req)
		if err != nil {
			t.Fatalf("round %d selected a stale session: %v", round, err)
		}
		if entry.pending[id] != pending[id] || entry.reasoningEffort != "high" {
			t.Fatalf("round %d did not claim the current session: %+v", round, entry)
		}
	}
	// A truly consumed latest session still returns 409; no old-round fallback.
	if _, err := takeParkedSession(registry, req); !errors.Is(err, errToolSessionExpired) {
		t.Errorf("expected expired latest session, got %v", err)
	}
}

func TestChatCompletions_ToolHistoryNewUserTurn(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, withTools := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/tools=%t", streaming, withTools), func(t *testing.T) {
				client := &reasoningTestClient{}
				payload := map[string]any{
					"model":  "reasoning-model",
					"stream": streaming,
					"messages": []oai.Message{
						{Role: "user", Content: oai.NewTextContent("previous question")},
						{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: encodeToolCallID("old", 0)}}},
						{Role: "tool", ToolCallID: encodeToolCallID("old", 0), Content: oai.NewTextContent("old result")},
						{Role: "assistant", Content: oai.NewTextContent("previous answer")},
						{Role: "user", Content: oai.NewTextContent("new question")},
					},
				}
				if withTools {
					payload["tools"] = reasoningTestTools()
				}
				rec := httptest.NewRecorder()
				ChatCompletions(client, nil, silentLogger()).ServeHTTP(rec, reasoningTestRequest(t, payload))
				if client.createCalls != 1 {
					t.Fatalf("new user turn should create a new session, got %d calls: %s", client.createCalls, rec.Body.String())
				}
			})
		}
	}
}

func TestChatCompletions_ToolResumeWithoutDeclarations(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			client := &reasoningTestClient{}
			payload := map[string]any{
				"model":  "reasoning-model",
				"stream": streaming,
				"messages": []oai.Message{
					{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: encodeToolCallID("expired", 0)}}},
					{Role: "tool", ToolCallID: encodeToolCallID("expired", 0), Content: oai.NewTextContent("result")},
				},
			}
			rec := httptest.NewRecorder()
			ChatCompletions(client, nil, silentLogger()).ServeHTTP(rec, reasoningTestRequest(t, payload))
			if rec.Code != http.StatusConflict || client.createCalls != 0 {
				t.Fatalf("tool continuation must not create a fresh session: status=%d calls=%d body=%s", rec.Code, client.createCalls, rec.Body.String())
			}
		})
	}
}
