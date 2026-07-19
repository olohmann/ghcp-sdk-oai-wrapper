package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	wrapper "github.com/olohmann/ghcp-sdk-oai-wrapper/internal/copilot"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/metrics"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

// toolEmitDebounce is how long to wait after the first intercepted tool request
// before finalizing, so parallel tool calls emitted together are collected.
const toolEmitDebounce = 300 * time.Millisecond

// handleToolEmitNonStreaming runs turn-1 of an OpenAI tool-calling exchange for
// a non-streaming request. It registers the caller's tools as declaration-only
// SDK tools, sends the prompt, and intercepts the pending tool requests the
// model emits. If the model calls tools, it returns finish_reason:"tool_calls";
// if it answers directly, it returns the text with finish_reason:"stop".
func handleToolEmitNonStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, registry *ToolSessionRegistry, req *oai.ChatCompletionRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("chat completion request",
		"model", req.Model,
		"stream", false,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
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

	// The session is parked (kept alive) between turn-1 and turn-2, so it must
	// outlive this HTTP request: create and drive it with a detached context.
	sessCtx := context.WithoutCancel(ctx)
	tools := toolsToSDK(req.Tools) // declaration-only => calls stay pending
	session, err := createSession(sessCtx, client, req, false, tools)
	if err != nil {
		logger.Error("failed to create session", "error", err)
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusInternalServerError, "failed to create session", "server_error")
		return
	}
	parked := false
	defer func() {
		if !parked {
			session.Disconnect()
		}
	}()

	var mu sync.Mutex
	var events []*copilot.ExternalToolRequestedData
	var finalText string
	toolCh := make(chan struct{}, 1)
	idleCh := make(chan struct{})
	var idleOnce sync.Once

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		switch d := event.Data.(type) {
		case *copilot.ExternalToolRequestedData:
			mu.Lock()
			events = append(events, d)
			mu.Unlock()
			select {
			case toolCh <- struct{}{}:
			default:
			}
		case *copilot.AssistantMessageData:
			if d.Content != "" {
				mu.Lock()
				finalText = d.Content
				mu.Unlock()
			}
		case *copilot.SessionIdleData:
			idleOnce.Do(func() { close(idleCh) })
		}
	})
	defer unsubscribe()

	sendCtx, cancel := context.WithTimeout(sessCtx, 5*time.Minute)
	defer cancel()
	if _, err := session.Send(sendCtx, copilot.MessageOptions{Prompt: buildPrompt(req.Messages), Attachments: attachments}); err != nil {
		logger.Error("failed to send message", "error", err)
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusInternalServerError, "failed to get completion", "server_error")
		return
	}

	select {
	case <-toolCh:
		collectDebounce(ctx, toolCh, toolEmitDebounce)
		mu.Lock()
		evs := append([]*copilot.ExternalToolRequestedData(nil), events...)
		mu.Unlock()
		token := newToolSessionToken()
		calls, pending := mintToolCalls(token, evs)
		registry.store(token, session, pending)
		parked = true // ownership transferred to the registry; skip Disconnect
		logger.Info("tool calls emitted; session parked", "count", len(calls), "token", token)
		metrics.RecordCompletion(req.Model, false, "success", time.Since(start))
		oai.WriteJSON(w, http.StatusOK, toolCallsResponse(req, calls))

	case <-idleCh:
		mu.Lock()
		text := finalText
		mu.Unlock()
		metrics.RecordCompletion(req.Model, false, "success", time.Since(start))
		oai.WriteJSON(w, http.StatusOK, stopResponse(req, text))

	case <-ctx.Done():
		logger.Warn("request context cancelled during tool emit", "cause", ctx.Err(), "elapsed", time.Since(start).String())
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))

	case <-time.After(5 * time.Minute):
		logger.Warn("tool emit timeout reached", "elapsed", time.Since(start).String())
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusGatewayTimeout, "tool emission timeout", "server_error")
	}
}

// handleToolEmitStreaming is the streaming counterpart of
// handleToolEmitNonStreaming. Text answers stream as content deltas; tool calls
// stream as tool_calls deltas followed by a final chunk with
// finish_reason:"tool_calls".
func handleToolEmitStreaming(ctx context.Context, w http.ResponseWriter, client *wrapper.Client, registry *ToolSessionRegistry, req *oai.ChatCompletionRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("chat completion request",
		"model", req.Model,
		"stream", true,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
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

	// The session is parked (kept alive) between turn-1 and turn-2, so it must
	// outlive this HTTP request: create and drive it with a detached context.
	sessCtx := context.WithoutCancel(ctx)
	tools := toolsToSDK(req.Tools) // declaration-only => calls stay pending
	session, err := createSession(sessCtx, client, req, true, tools)
	if err != nil {
		logger.Error("failed to create session", "error", err)
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		_ = sse.WriteEvent(oai.ErrorResponse{Error: oai.ErrorDetail{Message: "failed to create session", Type: "server_error"}})
		_ = sse.WriteDone()
		return
	}
	parked := false
	defer func() {
		if !parked {
			session.Disconnect()
		}
	}()

	completionID := oai.NewCompletionID()
	created := oai.NowUnix()

	var wmu sync.Mutex
	send := func(chunk oai.ChatCompletionChunk) {
		wmu.Lock()
		defer wmu.Unlock()
		if err := sse.WriteEvent(chunk); err != nil {
			logger.Debug("SSE write error", "error", err)
		}
	}
	finish := func() {
		wmu.Lock()
		defer wmu.Unlock()
		_ = sse.WriteDone()
	}

	send(oai.ChatCompletionChunk{
		ID: completionID, Object: "chat.completion.chunk", Created: created, Model: req.Model,
		Choices: []oai.Choice{{Index: 0, Delta: &oai.DeltaMessage{Role: "assistant"}}},
	})

	var mu sync.Mutex
	var events []*copilot.ExternalToolRequestedData
	var gotTool atomic.Bool
	var gotDelta atomic.Bool
	toolCh := make(chan struct{}, 1)
	idleCh := make(chan struct{})
	var idleOnce sync.Once

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		switch d := event.Data.(type) {
		case *copilot.ExternalToolRequestedData:
			gotTool.Store(true)
			mu.Lock()
			events = append(events, d)
			mu.Unlock()
			select {
			case toolCh <- struct{}{}:
			default:
			}
		case *copilot.AssistantMessageDeltaData:
			if !gotTool.Load() && d.DeltaContent != "" {
				gotDelta.Store(true)
				delta := d.DeltaContent
				send(oai.ChatCompletionChunk{
					ID: completionID, Object: "chat.completion.chunk", Created: created, Model: req.Model,
					Choices: []oai.Choice{{Index: 0, Delta: &oai.DeltaMessage{Content: &delta}}},
				})
			}
		case *copilot.AssistantMessageData:
			if !gotTool.Load() && !gotDelta.Load() && d.Content != "" {
				content := d.Content
				send(oai.ChatCompletionChunk{
					ID: completionID, Object: "chat.completion.chunk", Created: created, Model: req.Model,
					Choices: []oai.Choice{{Index: 0, Delta: &oai.DeltaMessage{Content: &content}}},
				})
			}
		case *copilot.SessionIdleData:
			idleOnce.Do(func() { close(idleCh) })
		}
	})
	defer unsubscribe()

	if _, err := session.Send(sessCtx, copilot.MessageOptions{Prompt: buildPrompt(req.Messages), Attachments: attachments}); err != nil {
		logger.Error("failed to send message", "error", err)
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		finish()
		return
	}

	select {
	case <-toolCh:
		collectDebounce(ctx, toolCh, toolEmitDebounce)
		mu.Lock()
		evs := append([]*copilot.ExternalToolRequestedData(nil), events...)
		mu.Unlock()
		token := newToolSessionToken()
		calls, pending := mintToolCalls(token, evs)
		registry.store(token, session, pending)
		parked = true // ownership transferred to the registry; skip Disconnect
		streamToolCalls(send, completionID, created, req.Model, calls)
		finish()
		logger.Info("tool calls emitted; session parked", "count", len(calls), "token", token)
		metrics.RecordCompletion(req.Model, true, "success", time.Since(start))

	case <-idleCh:
		send(oai.ChatCompletionChunk{
			ID: completionID, Object: "chat.completion.chunk", Created: created, Model: req.Model,
			Choices: []oai.Choice{{Index: 0, Delta: &oai.DeltaMessage{}, FinishReason: oai.StringPtr("stop")}},
		})
		finish()
		metrics.RecordCompletion(req.Model, true, "success", time.Since(start))

	case <-ctx.Done():
		logger.Warn("request context cancelled during tool emit", "cause", ctx.Err(), "elapsed", time.Since(start).String())
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))

	case <-time.After(5 * time.Minute):
		logger.Warn("tool emit timeout reached", "elapsed", time.Since(start).String())
		finish()
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
	}
}

// streamToolCalls emits one tool_calls delta chunk per call followed by a final
// chunk carrying finish_reason:"tool_calls".
func streamToolCalls(send func(oai.ChatCompletionChunk), completionID string, created int64, model string, calls []oai.ToolCall) {
	for i, c := range calls {
		send(oai.ChatCompletionChunk{
			ID: completionID, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []oai.Choice{{
				Index: 0,
				Delta: &oai.DeltaMessage{ToolCalls: []oai.ToolCallDelta{{
					Index:    i,
					ID:       c.ID,
					Type:     "function",
					Function: &oai.FunctionCallDelta{Name: c.Function.Name, Arguments: c.Function.Arguments},
				}}},
			}},
		})
	}
	send(oai.ChatCompletionChunk{
		ID: completionID, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []oai.Choice{{Index: 0, Delta: &oai.DeltaMessage{}, FinishReason: oai.StringPtr("tool_calls")}},
	})
}

// collectDebounce drains additional signals from ch, extending a debounce timer
// each time, until the timer fires or the context is cancelled.
func collectDebounce(ctx context.Context, ch <-chan struct{}, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case <-ch:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
			t.Reset(d)
		case <-t.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// toolCallsResponse builds a non-streaming response carrying tool_calls.
func toolCallsResponse(req *oai.ChatCompletionRequest, calls []oai.ToolCall) oai.ChatCompletionResponse {
	return oai.ChatCompletionResponse{
		ID:      oai.NewCompletionID(),
		Object:  "chat.completion",
		Created: oai.NowUnix(),
		Model:   req.Model,
		Choices: []oai.Choice{{
			Index:        0,
			Message:      &oai.Message{Role: "assistant", Content: oai.NewTextContent(""), ToolCalls: calls},
			FinishReason: oai.StringPtr("tool_calls"),
		}},
	}
}

// stopResponse builds a normal non-streaming text response.
func stopResponse(req *oai.ChatCompletionRequest, text string) oai.ChatCompletionResponse {
	return oai.ChatCompletionResponse{
		ID:      oai.NewCompletionID(),
		Object:  "chat.completion",
		Created: oai.NowUnix(),
		Model:   req.Model,
		Choices: []oai.Choice{{
			Index:        0,
			Message:      &oai.Message{Role: "assistant", Content: oai.NewTextContent(text)},
			FinishReason: oai.StringPtr("stop"),
		}},
	}
}
