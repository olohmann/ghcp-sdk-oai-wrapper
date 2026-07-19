package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	wrapper "github.com/olohmann/ghcp-sdk-oai-wrapper/internal/copilot"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/metrics"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

const maxRequestBodySize = 50 * 1024 * 1024 // 50 MB

// ChatCompletions returns the handler for POST /v1/chat/completions. The
// registry holds parked tool sessions for faithful OpenAI tool round-trips; it
// may be nil when tool-calling is not needed (e.g. in tests exercising the
// method guard).
func ChatCompletions(client *wrapper.Client, registry *ToolSessionRegistry, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			oai.WriteError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}

		var req oai.ChatCompletionRequest
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
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

		// Enrich logger with client identity for debugging disconnects
		reqLogger := logger.With(
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)

		// Turn-2 tool resume: the request declares tools AND already carries tool
		// results, so it echoes back the tool_call IDs we minted on turn-1. We
		// decode the parking token, resolve the pending call(s) on the same live
		// session, and continue the original agent loop to its answer. A missing
		// or expired parked session yields HTTP 409 (there is no replay fallback).
		if requestHasTools(&req) && hasToolResults(req.Messages) {
			if req.Stream {
				handleToolResumeStreaming(r.Context(), w, registry, &req, reqLogger)
			} else {
				handleToolResumeNonStreaming(r.Context(), w, registry, &req, reqLogger)
			}
			return
		}

		// Turn-1 tool emission: the request declares tools but carries no tool
		// results yet, so the model may respond with tool_calls. This path
		// registers declaration-only tools, intercepts the SDK's pending tool
		// requests, and parks the live session for the follow-up turn.
		if requestHasTools(&req) && !hasToolResults(req.Messages) {
			if req.Stream {
				handleToolEmitStreaming(r.Context(), w, client, registry, &req, reqLogger)
			} else {
				handleToolEmitNonStreaming(r.Context(), w, client, registry, &req, reqLogger)
			}
			return
		}

		if req.Stream {
			handleStreaming(r.Context(), w, client, &req, reqLogger)
		} else {
			handleNonStreaming(r.Context(), w, client, &req, reqLogger)
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
		return nonSystem[0].Content.TextContent()
	}

	// For multi-turn, format as a conversation prompt.
	var sb strings.Builder
	for _, m := range nonSystem {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", m.Role, m.Content.TextContent()))
	}
	return sb.String()
}

// extractSystemMessage returns the combined system messages.
func extractSystemMessage(messages []oai.Message) string {
	var parts []string
	for _, m := range messages {
		if m.Role == "system" {
			parts = append(parts, m.Content.TextContent())
		}
	}
	return strings.Join(parts, "\n")
}

func handleNonStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, req *oai.ChatCompletionRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("chat completion request",
		"model", req.Model,
		"stream", false,
		"messages", len(req.Messages),
		"preview", truncate(req.Messages[len(req.Messages)-1].Content.TextContent(), 80),
	)

	attachments, err := extractAttachments(req.Messages, logger)
	if err != nil {
		logger.Error("failed to extract attachments", "error", err)
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusBadRequest, "failed to process attachments: "+err.Error(), "invalid_request_error")
		return
	}
	metrics.RecordImageAttachments(len(attachments))

	prompt := buildPrompt(req.Messages)
	session, err := createSession(ctx, client, req, false, nil)
	if err != nil {
		logger.Error("failed to create session", "error", err)
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusInternalServerError, "failed to create session", "server_error")
		return
	}
	defer session.Disconnect()

	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	reply, err := session.SendAndWait(sendCtx, copilot.MessageOptions{
		Prompt:      prompt,
		Attachments: attachments,
	})
	if err != nil {
		logger.Error("failed to send message", "error", err)
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusInternalServerError, "failed to get completion", "server_error")
		return
	}

	content := ""
	if reply != nil {
		if d, ok := reply.Data.(*copilot.AssistantMessageData); ok {
			content = d.Content
		}
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
					Content: oai.NewTextContent(content),
				},
				FinishReason: oai.StringPtr("stop"),
			},
		},
	}

	metrics.RecordCompletion(req.Model, false, "success", time.Since(start))
	oai.WriteJSON(w, http.StatusOK, resp)
}

func handleStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, req *oai.ChatCompletionRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("chat completion request",
		"model", req.Model,
		"stream", true,
		"messages", len(req.Messages),
		"preview", truncate(req.Messages[len(req.Messages)-1].Content.TextContent(), 80),
	)

	attachments, err := extractAttachments(req.Messages, logger)
	if err != nil {
		logger.Error("failed to extract attachments", "error", err)
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		oai.WriteError(w, http.StatusBadRequest, "failed to process attachments: "+err.Error(), "invalid_request_error")
		return
	}
	metrics.RecordImageAttachments(len(attachments))

	sse, err := oai.NewSSEWriter(w)
	if err != nil {
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		oai.WriteError(w, http.StatusInternalServerError, "streaming not supported", "server_error")
		return
	}

	prompt := buildPrompt(req.Messages)
	session, err := createSession(ctx, client, req, true, nil)
	if err != nil {
		logger.Error("failed to create session", "error", err)
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		_ = sse.WriteEvent(oai.ErrorResponse{
			Error: oai.ErrorDetail{Message: "failed to create session", Type: "server_error"},
		})
		_ = sse.WriteDone()
		return
	}
	defer session.Disconnect()

	completionID := oai.NewCompletionID()
	created := oai.NowUnix()

	done := make(chan struct{})
	var once sync.Once
	var gotDelta atomic.Bool
	var writeErrors atomic.Int64

	// Send the initial chunk with role
	if err := sse.WriteEvent(oai.ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   req.Model,
		Choices: []oai.Choice{
			{
				Index: 0,
				Delta: &oai.DeltaMessage{Role: "assistant"},
			},
		},
	}); err != nil {
		writeErrors.Add(1)
		logger.Debug("SSE write error (initial chunk)", "error", err)
	}

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		switch d := event.Data.(type) {
		case *copilot.AssistantMessageDeltaData:
			gotDelta.Store(true)
			if d.DeltaContent != "" {
				delta := d.DeltaContent
				if err := sse.WriteEvent(oai.ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   req.Model,
					Choices: []oai.Choice{
						{
							Index: 0,
							Delta: &oai.DeltaMessage{Content: &delta},
						},
					},
				}); err != nil {
					writeErrors.Add(1)
					logger.Debug("SSE write error (delta)", "error", err)
				}
			}

		case *copilot.AssistantMessageData:
			// Only send the full message if we never received deltas (fallback)
			if !gotDelta.Load() && d.Content != "" {
				content := d.Content
				if err := sse.WriteEvent(oai.ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   req.Model,
					Choices: []oai.Choice{
						{
							Index: 0,
							Delta: &oai.DeltaMessage{Content: &content},
						},
					},
				}); err != nil {
					writeErrors.Add(1)
					logger.Debug("SSE write error (message)", "error", err)
				}
			}

		case *copilot.SessionIdleData:
			metrics.RecordCompletion(req.Model, true, "success", time.Since(start))
			// Send the final chunk with finish_reason
			_ = sse.WriteEvent(oai.ChatCompletionChunk{
				ID:      completionID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   req.Model,
				Choices: []oai.Choice{
					{
						Index:        0,
						Delta:        &oai.DeltaMessage{},
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
		Prompt:      prompt,
		Attachments: attachments,
	})
	if err != nil {
		logger.Error("failed to send message", "error", err)
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		_ = sse.WriteDone()
		once.Do(func() { close(done) })
		return
	}

	// Wait for completion or context cancellation
	select {
	case <-done:
	case <-ctx.Done():
		logger.Warn("request context cancelled during streaming",
			"cause", ctx.Err(),
			"elapsed", time.Since(start).String(),
			"got_delta", gotDelta.Load(),
			"write_errors", writeErrors.Load(),
		)
	case <-time.After(5 * time.Minute):
		logger.Warn("streaming timeout reached",
			"elapsed", time.Since(start).String(),
			"got_delta", gotDelta.Load(),
			"write_errors", writeErrors.Load(),
		)
	}
}

func createSession(ctx context.Context, client *wrapper.Client, req *oai.ChatCompletionRequest, streaming bool, tools []copilot.Tool) (*copilot.Session, error) {
	sysMsg := extractSystemMessage(req.Messages)
	return client.NewChatSessionWithTools(ctx, req.Model, sysMsg, streaming, tools)
}

// extractAttachments scans all messages for `image_url` and `file` content
// parts with inline data URIs and returns Copilot SDK blob attachments —
// the SDK accepts base64 bytes plus a MIME type directly via
// UserMessageAttachmentBlob, with no need to materialize files on disk.
//
// `image_url` parts only accept image/* MIME types — PDFs and other documents
// must be sent as `file` parts (matching the official OpenAI API). `file`
// parts accept any MIME the model can ingest. `file.file_id` references are
// not proxied and return an error.
func extractAttachments(messages []oai.Message, logger *slog.Logger) ([]copilot.Attachment, error) {
	var attachments []copilot.Attachment

	for _, msg := range messages {
		// image_url parts: images only.
		for _, img := range msg.Content.ImageParts() {
			url := img.ImageURL.URL

			if !strings.HasPrefix(url, "data:") {
				logger.Warn("skipping non-data image URL (not supported)", "url", truncate(url, 60))
				continue
			}

			mimeType, data, err := parseDataURI(url)
			if err != nil {
				return nil, fmt.Errorf("invalid image_url data URI: %w", err)
			}
			if !strings.HasPrefix(mimeType, "image/") {
				return nil, fmt.Errorf("image_url parts must use an image/* MIME type (got %q); use a `file` content part for documents like PDFs", mimeType)
			}

			attachments = append(attachments, &copilot.AttachmentBlob{
				Data:     copilot.String(base64.StdEncoding.EncodeToString(data)),
				MIMEType: mimeType,
			})
			logger.Info("extracted attachment",
				"kind", "image",
				"mime", mimeType,
				"size", len(data),
			)
		}

		// file parts: any MIME, caller-supplied filename preferred.
		for _, fp := range msg.Content.FileParts() {
			file := fp.File
			if file.FileID != "" {
				return nil, fmt.Errorf("file.file_id is not supported by this server; embed the file inline via file.file_data as a `data:<mime>;base64,...` URI")
			}
			if file.FileData == "" {
				return nil, fmt.Errorf("file part requires file.file_data (a `data:<mime>;base64,...` URI)")
			}
			if !strings.HasPrefix(file.FileData, "data:") {
				return nil, fmt.Errorf("file.file_data must be a `data:<mime>;base64,...` URI")
			}

			mimeType, data, err := parseDataURI(file.FileData)
			if err != nil {
				return nil, fmt.Errorf("invalid file.file_data URI: %w", err)
			}
			if mimeType == "" {
				return nil, fmt.Errorf("file.file_data data URI is missing its MIME type")
			}

			blob := &copilot.AttachmentBlob{
				Data:     copilot.String(base64.StdEncoding.EncodeToString(data)),
				MIMEType: mimeType,
			}
			if file.Filename != "" {
				dn := filepath.Base(file.Filename)
				blob.DisplayName = &dn
			}
			attachments = append(attachments, blob)
			logger.Info("extracted attachment",
				"kind", "file",
				"mime", mimeType,
				"size", len(data),
				"filename", file.Filename,
			)
		}
	}

	return attachments, nil
}

// parseDataURI parses a data URI (data:<mediatype>;base64,<data>) and returns
// the MIME type and decoded bytes. Returns an error for non-base64 or invalid URIs.
func parseDataURI(uri string) (mimeType string, data []byte, err error) {
	if !strings.HasPrefix(uri, "data:") {
		return "", nil, fmt.Errorf("not a data URI")
	}
	// data:<mediatype>;base64,<data>
	rest := uri[len("data:"):]
	semicolon := strings.Index(rest, ";")
	if semicolon < 0 {
		return "", nil, fmt.Errorf("invalid data URI: missing semicolon")
	}
	mimeType = rest[:semicolon]

	after := rest[semicolon+1:]
	if !strings.HasPrefix(after, "base64,") {
		return "", nil, fmt.Errorf("unsupported data URI encoding (expected base64)")
	}

	b64 := after[len("base64,"):]
	data, err = base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// Retry with RawStdEncoding for unpadded base64.
		data, err = base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			return "", nil, fmt.Errorf("failed to decode base64: %w", err)
		}
	}
	return mimeType, data, nil
}
