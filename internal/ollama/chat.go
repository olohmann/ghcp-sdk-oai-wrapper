package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	wrapper "github.com/olohmann/ghcp-sdk-oai-wrapper/internal/copilot"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/metrics"
)

const maxRequestBodySize = 50 * 1024 * 1024 // 50 MB

// Chat returns the handler for POST /api/chat.
func Chat(client *wrapper.Client, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var req ChatRequest
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		if req.Model == "" {
			WriteError(w, http.StatusBadRequest, "model is required")
			return
		}
		if len(req.Messages) == 0 {
			WriteError(w, http.StatusBadRequest, "messages is required")
			return
		}

		if req.IsStreaming() {
			handleChatStreaming(r.Context(), w, client, &req, logger)
		} else {
			handleChatNonStreaming(r.Context(), w, client, &req, logger)
		}
	}
}

func handleChatNonStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, req *ChatRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("ollama chat request",
		"model", req.Model,
		"stream", false,
		"messages", len(req.Messages),
	)

	sysMsg := extractSystemMessage(req.Messages)
	session, err := client.NewChatSession(ctx, req.Model, sysMsg, false)
	if err != nil {
		logger.Error("failed to create session", "error", err)
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		WriteError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	defer session.Disconnect()

	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	reply, err := session.SendAndWait(sendCtx, copilot.MessageOptions{
		Prompt: buildPrompt(req.Messages),
	})
	if err != nil {
		logger.Error("failed to send message", "error", err)
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		WriteError(w, http.StatusInternalServerError, "failed to get completion")
		return
	}

	content := ""
	if reply != nil && reply.Data.Content != nil {
		content = *reply.Data.Content
	}

	duration := time.Since(start)
	metrics.RecordCompletion(req.Model, false, "success", duration)

	WriteJSON(w, http.StatusOK, ChatResponse{
		Model:         req.Model,
		CreatedAt:     NowRFC3339Milli(),
		Message:       Message{Role: "assistant", Content: content},
		Done:          true,
		DoneReason:    "stop",
		TotalDuration: duration.Nanoseconds(),
	})
}

func handleChatStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, req *ChatRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("ollama chat request",
		"model", req.Model,
		"stream", true,
		"messages", len(req.Messages),
	)

	sysMsg := extractSystemMessage(req.Messages)
	session, err := client.NewChatSession(ctx, req.Model, sysMsg, true)
	if err != nil {
		logger.Error("failed to create session", "error", err)
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		WriteError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	defer session.Disconnect()

	ndjson, err := NewNDJSONWriter(w)
	if err != nil {
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		WriteError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	done := make(chan struct{})
	var once sync.Once
	var gotDelta atomic.Bool

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		switch event.Type {
		case copilot.AssistantMessageDelta:
			gotDelta.Store(true)
			if event.Data.DeltaContent != nil {
				_ = ndjson.WriteLine(ChatStreamChunk{
					Model:     req.Model,
					CreatedAt: NowRFC3339Milli(),
					Message:   Message{Role: "assistant", Content: *event.Data.DeltaContent},
					Done:      false,
				})
			}

		case copilot.AssistantMessage:
			if !gotDelta.Load() && event.Data.Content != nil {
				_ = ndjson.WriteLine(ChatStreamChunk{
					Model:     req.Model,
					CreatedAt: NowRFC3339Milli(),
					Message:   Message{Role: "assistant", Content: *event.Data.Content},
					Done:      false,
				})
			}

		case copilot.SessionIdle:
			duration := time.Since(start)
			metrics.RecordCompletion(req.Model, true, "success", duration)
			_ = ndjson.WriteLine(ChatStreamChunk{
				Model:         req.Model,
				CreatedAt:     NowRFC3339Milli(),
				Message:       Message{Role: "assistant", Content: ""},
				Done:          true,
				DoneReason:    "stop",
				TotalDuration: duration.Nanoseconds(),
			})
			once.Do(func() { close(done) })
		}
	})
	defer unsubscribe()

	_, err = session.Send(ctx, copilot.MessageOptions{
		Prompt: buildPrompt(req.Messages),
	})
	if err != nil {
		logger.Error("failed to send message", "error", err)
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		once.Do(func() { close(done) })
		return
	}

	select {
	case <-done:
	case <-ctx.Done():
		logger.Warn("request context cancelled during streaming")
	case <-time.After(5 * time.Minute):
		logger.Warn("streaming timeout reached")
	}
}

// buildPrompt concatenates non-system messages into a single prompt.
func buildPrompt(messages []Message) string {
	var nonSystem []Message
	for _, m := range messages {
		if m.Role != "system" {
			nonSystem = append(nonSystem, m)
		}
	}

	if len(nonSystem) == 0 {
		return ""
	}
	if len(nonSystem) == 1 {
		return nonSystem[0].Content
	}

	var sb strings.Builder
	for _, m := range nonSystem {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", m.Role, m.Content))
	}
	return sb.String()
}

// extractSystemMessage returns the combined system messages.
func extractSystemMessage(messages []Message) string {
	var parts []string
	for _, m := range messages {
		if m.Role == "system" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}
