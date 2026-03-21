package ollama

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	wrapper "github.com/olohmann/ghcp-sdk-oai-wrapper/internal/copilot"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/metrics"
)

// Generate returns the handler for POST /api/generate.
func Generate(client *wrapper.Client, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var req GenerateRequest
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		if req.Model == "" {
			WriteError(w, http.StatusBadRequest, "model is required")
			return
		}
		if req.Prompt == "" {
			WriteError(w, http.StatusBadRequest, "prompt is required")
			return
		}

		if req.IsStreaming() {
			handleGenerateStreaming(r.Context(), w, client, &req, logger)
		} else {
			handleGenerateNonStreaming(r.Context(), w, client, &req, logger)
		}
	}
}

func handleGenerateNonStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, req *GenerateRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("ollama generate request",
		"model", req.Model,
		"stream", false,
	)

	session, err := client.NewChatSession(ctx, req.Model, req.System, false)
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
		Prompt: req.Prompt,
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

	WriteJSON(w, http.StatusOK, GenerateResponse{
		Model:         req.Model,
		CreatedAt:     NowRFC3339Milli(),
		Response:      content,
		Done:          true,
		DoneReason:    "stop",
		TotalDuration: duration.Nanoseconds(),
	})
}

func handleGenerateStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, req *GenerateRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("ollama generate request",
		"model", req.Model,
		"stream", true,
	)

	session, err := client.NewChatSession(ctx, req.Model, req.System, true)
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
				_ = ndjson.WriteLine(GenerateStreamChunk{
					Model:     req.Model,
					CreatedAt: NowRFC3339Milli(),
					Response:  *event.Data.DeltaContent,
					Done:      false,
				})
			}

		case copilot.AssistantMessage:
			if !gotDelta.Load() && event.Data.Content != nil {
				_ = ndjson.WriteLine(GenerateStreamChunk{
					Model:     req.Model,
					CreatedAt: NowRFC3339Milli(),
					Response:  *event.Data.Content,
					Done:      false,
				})
			}

		case copilot.SessionIdle:
			duration := time.Since(start)
			metrics.RecordCompletion(req.Model, true, "success", duration)
			_ = ndjson.WriteLine(GenerateStreamChunk{
				Model:         req.Model,
				CreatedAt:     NowRFC3339Milli(),
				Response:      "",
				Done:          true,
				DoneReason:    "stop",
				TotalDuration: duration.Nanoseconds(),
			})
			once.Do(func() { close(done) })
		}
	})
	defer unsubscribe()

	_, err = session.Send(ctx, copilot.MessageOptions{
		Prompt: req.Prompt,
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
