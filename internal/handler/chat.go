package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	wrapper "github.com/olohmann/ghcp-sdk-oai-wrapper/internal/copilot"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

// ChatCompletions returns the handler for POST /v1/chat/completions.
func ChatCompletions(client *wrapper.Client, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			oai.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}

		var req oai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			oai.WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error")
			return
		}

		if req.Model == "" {
			oai.WriteError(w, http.StatusBadRequest, "model is required", "invalid_request_error")
			return
		}
		if len(req.Messages) == 0 {
			oai.WriteError(w, http.StatusBadRequest, "messages is required", "invalid_request_error")
			return
		}

		if req.Stream {
			handleStreaming(r.Context(), w, client, &req, logger)
		} else {
			handleNonStreaming(r.Context(), w, client, &req, logger)
		}
	}
}

// truncate returns the first n runes of s, appending "…" if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// buildPrompt concatenates user/assistant messages into a single prompt for the SDK.
// The last user message becomes the prompt; prior messages provide conversation context.
func buildPrompt(messages []oai.Message) string {
	if len(messages) == 0 {
		return ""
	}

	// If there's only one non-system message, use it directly.
	var nonSystem []oai.Message
	for _, m := range messages {
		if m.Role != "system" {
			nonSystem = append(nonSystem, m)
		}
	}

	if len(nonSystem) == 1 {
		return nonSystem[0].Content
	}

	// For multi-turn, format as a conversation prompt.
	var sb strings.Builder
	for _, m := range nonSystem {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", m.Role, m.Content))
	}
	return sb.String()
}

// extractSystemMessage returns the combined system messages.
func extractSystemMessage(messages []oai.Message) string {
	var parts []string
	for _, m := range messages {
		if m.Role == "system" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func handleNonStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, req *oai.ChatCompletionRequest, logger *slog.Logger) {
	logger.Info("chat completion request",
		"model", req.Model,
		"stream", false,
		"messages", len(req.Messages),
		"preview", truncate(req.Messages[len(req.Messages)-1].Content, 80),
	)
	session, err := createSession(ctx, client, req, false)
	if err != nil {
		logger.Error("failed to create session", "error", err)
		oai.WriteError(w, http.StatusInternalServerError, "failed to create session", "server_error")
		return
	}
	defer session.Destroy()

	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	reply, err := session.SendAndWait(sendCtx, copilot.MessageOptions{
		Prompt: buildPrompt(req.Messages),
	})
	if err != nil {
		logger.Error("failed to send message", "error", err)
		oai.WriteError(w, http.StatusInternalServerError, "failed to get completion", "server_error")
		return
	}

	content := ""
	if reply != nil && reply.Data.Content != nil {
		content = *reply.Data.Content
	}

	completionID := oai.NewCompletionID()
	resp := oai.ChatCompletionResponse{
		ID:      completionID,
		Object:  "chat.completion",
		Created: oai.NowUnix(),
		Model:   req.Model,
		Choices: []oai.Choice{
			{
				Index: 0,
				Message: &oai.Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: oai.StringPtr("stop"),
			},
		},
	}

	oai.WriteJSON(w, http.StatusOK, resp)
}

func handleStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, req *oai.ChatCompletionRequest, logger *slog.Logger) {
	logger.Info("chat completion request",
		"model", req.Model,
		"stream", true,
		"messages", len(req.Messages),
		"preview", truncate(req.Messages[len(req.Messages)-1].Content, 80),
	)
	sse, err := oai.NewSSEWriter(w)
	if err != nil {
		oai.WriteError(w, http.StatusInternalServerError, "streaming not supported", "server_error")
		return
	}

	session, err := createSession(ctx, client, req, true)
	if err != nil {
		logger.Error("failed to create session", "error", err)
		// Headers already sent for SSE, write error as event
		_ = sse.WriteEvent(oai.ErrorResponse{
			Error: oai.ErrorDetail{Message: "failed to create session", Type: "server_error"},
		})
		_ = sse.WriteDone()
		return
	}
	defer session.Destroy()

	completionID := oai.NewCompletionID()
	created := oai.NowUnix()

	done := make(chan struct{})
	var once sync.Once
	var gotDelta bool

	// Send the initial chunk with role
	_ = sse.WriteEvent(oai.ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   req.Model,
		Choices: []oai.Choice{
			{
				Index: 0,
				Delta: &oai.Message{Role: "assistant"},
			},
		},
	})

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		switch event.Type {
		case copilot.AssistantMessageDelta:
			gotDelta = true
			if event.Data.DeltaContent != nil {
				_ = sse.WriteEvent(oai.ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   req.Model,
					Choices: []oai.Choice{
						{
							Index: 0,
							Delta: &oai.Message{Content: *event.Data.DeltaContent},
						},
					},
				})
			}

		case copilot.AssistantMessage:
			// Only send the full message if we never received deltas (fallback)
			if !gotDelta && event.Data.Content != nil {
				_ = sse.WriteEvent(oai.ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   req.Model,
					Choices: []oai.Choice{
						{
							Index: 0,
							Delta: &oai.Message{Content: *event.Data.Content},
						},
					},
				})
			}

		case copilot.SessionIdle:
			// Send the final chunk with finish_reason
			_ = sse.WriteEvent(oai.ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   req.Model,
				Choices: []oai.Choice{
					{
						Index:        0,
						Delta:        &oai.Message{},
						FinishReason: oai.StringPtr("stop"),
					},
				},
			})
			_ = sse.WriteDone()
			once.Do(func() { close(done) })
		}
	})
	defer unsubscribe()

	_, err = session.Send(ctx, copilot.MessageOptions{
		Prompt: buildPrompt(req.Messages),
	})
	if err != nil {
		logger.Error("failed to send message", "error", err)
		_ = sse.WriteDone()
		once.Do(func() { close(done) })
		return
	}

	// Wait for completion or context cancellation
	select {
	case <-done:
	case <-ctx.Done():
		logger.Warn("request context cancelled during streaming")
	case <-time.After(5 * time.Minute):
		logger.Warn("streaming timeout reached")
	}
}

func createSession(ctx context.Context, client *wrapper.Client, req *oai.ChatCompletionRequest, streaming bool) (*copilot.Session, error) {
	cfg := &copilot.SessionConfig{
		Model:               req.Model,
		Streaming:           streaming,
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		InfiniteSessions:    &copilot.InfiniteSessionConfig{Enabled: copilot.Bool(false)},
		AvailableTools:      []string{},
	}

	sysMsg := extractSystemMessage(req.Messages)
	if sysMsg != "" {
		cfg.SystemMessage = &copilot.SystemMessageConfig{
			Mode:    "replace",
			Content: sysMsg,
		}
	}

	return client.CreateSession(ctx, cfg)
}
