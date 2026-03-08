package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
	logger.Info("chat completion request",
		"model", req.Model,
		"stream", false,
		"messages", len(req.Messages),
		"preview", truncate(req.Messages[len(req.Messages)-1].Content.TextContent(), 80),
	)

	attachments, cleanup, err := extractImageAttachments(req.Messages, logger)
	if err != nil {
		logger.Error("failed to extract image attachments", "error", err)
		oai.WriteError(w, http.StatusBadRequest, "failed to process image attachments: "+err.Error(), "invalid_request_error")
		return
	}
	defer cleanup()

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
		Prompt:      buildPrompt(req.Messages),
		Attachments: attachments,
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
					Content: oai.NewTextContent(content),
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
		"preview", truncate(req.Messages[len(req.Messages)-1].Content.TextContent(), 80),
	)

	attachments, cleanup, err := extractImageAttachments(req.Messages, logger)
	if err != nil {
		logger.Error("failed to extract image attachments", "error", err)
		oai.WriteError(w, http.StatusBadRequest, "failed to process image attachments: "+err.Error(), "invalid_request_error")
		return
	}
	defer cleanup()

	sse, err := oai.NewSSEWriter(w)
	if err != nil {
		oai.WriteError(w, http.StatusInternalServerError, "streaming not supported", "server_error")
		return
	}

	session, err := createSession(ctx, client, req, true)
	if err != nil {
		logger.Error("failed to create session", "error", err)
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
							Delta: &oai.Message{Content: oai.NewTextContent(*event.Data.DeltaContent)},
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
							Delta: &oai.Message{Content: oai.NewTextContent(*event.Data.Content)},
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
		Prompt:      buildPrompt(req.Messages),
		Attachments: attachments,
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

// mimeToExt maps common image MIME types to file extensions.
var mimeToExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"image/bmp":  ".bmp",
	"image/tiff": ".tiff",
	"image/x-icon": ".ico",
	"image/heic": ".heic",
	"image/avif": ".avif",
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

// extractImageAttachments scans all messages for image_url content parts with
// data URIs, writes them to temp files, and returns Copilot SDK file attachments.
// The returned cleanup function removes all temp files and must be deferred.
func extractImageAttachments(messages []oai.Message, logger *slog.Logger) ([]copilot.Attachment, func(), error) {
	var attachments []copilot.Attachment
	var tmpFiles []string

	cleanup := func() {
		for _, f := range tmpFiles {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
				logger.Warn("failed to remove temp image file", "path", f, "error", err)
			}
		}
	}

	for _, msg := range messages {
		for _, img := range msg.Content.ImageParts() {
			url := img.ImageURL.URL

			if !strings.HasPrefix(url, "data:") {
				// Non-data URIs (e.g., https://) are not supported by the SDK file workaround.
				logger.Warn("skipping non-data image URL (not supported)", "url", truncate(url, 60))
				continue
			}

			mimeType, data, err := parseDataURI(url)
			if err != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("invalid image data URI: %w", err)
			}

			ext := mimeToExt[mimeType]
			if ext == "" {
				ext = ".bin"
			}

			tmpFile, err := os.CreateTemp("", "copilot-img-*"+ext)
			if err != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("failed to create temp file: %w", err)
			}

			if _, err := tmpFile.Write(data); err != nil {
				tmpFile.Close()
				cleanup()
				return nil, func() {}, fmt.Errorf("failed to write temp file: %w", err)
			}
			tmpFile.Close()

			absPath, _ := filepath.Abs(tmpFile.Name())
			tmpFiles = append(tmpFiles, absPath)

			attachments = append(attachments, copilot.Attachment{
				Type: copilot.File,
				Path: &absPath,
			})

			logger.Info("extracted image attachment",
				"mime", mimeType,
				"size", len(data),
				"path", absPath,
			)
		}
	}

	return attachments, cleanup, nil
}
