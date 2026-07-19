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

// handleToolResumeNonStreaming runs turn-2 of an OpenAI tool-calling exchange
// for a non-streaming request. It reconnects to the parked turn-1 session via
// the token embedded in the echoed tool_call IDs, resolves each pending tool
// call with the client-supplied result, and continues the original agent loop.
// A missing or expired parked session returns HTTP 409 (no replay fallback).
func handleToolResumeNonStreaming(ctx context.Context, w http.ResponseWriter, registry *ToolSessionRegistry, req *oai.ChatCompletionRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("chat completion tool-resume request",
		"model", req.Model,
		"stream", false,
		"messages", len(req.Messages),
	)

	parked, ok := takeParkedSession(registry, req.Messages)
	if !ok {
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusConflict, toolSessionExpiredMessage, "tool_session_expired")
		return
	}
	session := parked.session
	reparked := false
	defer func() {
		if !reparked {
			discardSession(session, logger)
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

	// Resolve the pending call(s) on a detached context so the parked session's
	// continuation is not tied to this request's lifetime.
	sessCtx := context.WithoutCancel(ctx)
	if err := resolvePending(sessCtx, session, parked.pending, resultsByToolCallID(req.Messages), logger); err != nil {
		logger.Error("failed to resolve pending tool call", "error", err)
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusInternalServerError, "failed to resolve tool result", "server_error")
		return
	}

	select {
	case <-toolCh:
		// Multi-step: the model wants more tools. Re-park and emit tool_calls.
		collectDebounce(ctx, toolCh, toolEmitDebounce)
		mu.Lock()
		evs := append([]*copilot.ExternalToolRequestedData(nil), events...)
		mu.Unlock()
		token := newToolSessionToken()
		calls, pending := mintToolCalls(token, evs)
		registry.store(token, session, pending)
		reparked = true
		logger.Info("tool calls emitted; session re-parked (multi-step)", "count", len(calls), "token", token)
		metrics.RecordCompletion(req.Model, false, "success", time.Since(start))
		oai.WriteJSON(w, http.StatusOK, toolCallsResponse(req, calls))

	case <-idleCh:
		mu.Lock()
		text := finalText
		mu.Unlock()
		metrics.RecordCompletion(req.Model, false, "success", time.Since(start))
		oai.WriteJSON(w, http.StatusOK, stopResponse(req, text))

	case <-ctx.Done():
		logger.Warn("request context cancelled during tool resume", "cause", ctx.Err(), "elapsed", time.Since(start).String())
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))

	case <-time.After(5 * time.Minute):
		logger.Warn("tool resume timeout reached", "elapsed", time.Since(start).String())
		metrics.RecordCompletion(req.Model, false, "error", time.Since(start))
		oai.WriteError(w, http.StatusGatewayTimeout, "tool resume timeout", "server_error")
	}
}

// handleToolResumeStreaming is the streaming counterpart of
// handleToolResumeNonStreaming. The parked-session lookup happens BEFORE the SSE
// response is committed so a missing/expired session can still return HTTP 409.
func handleToolResumeStreaming(ctx context.Context, w http.ResponseWriter, registry *ToolSessionRegistry, req *oai.ChatCompletionRequest, logger *slog.Logger) {
	start := time.Now()
	logger.Info("chat completion tool-resume request",
		"model", req.Model,
		"stream", true,
		"messages", len(req.Messages),
	)

	// Validate and claim the parked session first: NewSSEWriter commits a 200,
	// after which we could no longer send a 409.
	parked, ok := takeParkedSession(registry, req.Messages)
	if !ok {
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		oai.WriteError(w, http.StatusConflict, toolSessionExpiredMessage, "tool_session_expired")
		return
	}
	session := parked.session
	reparked := false
	defer func() {
		if !reparked {
			discardSession(session, logger)
		}
	}()

	sse, err := oai.NewSSEWriter(w)
	if err != nil {
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
		oai.WriteError(w, http.StatusInternalServerError, "streaming not supported", "server_error")
		return
	}

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

	sessCtx := context.WithoutCancel(ctx)
	if err := resolvePending(sessCtx, session, parked.pending, resultsByToolCallID(req.Messages), logger); err != nil {
		logger.Error("failed to resolve pending tool call", "error", err)
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
		reparked = true
		streamToolCalls(send, completionID, created, req.Model, calls)
		finish()
		logger.Info("tool calls emitted; session re-parked (multi-step)", "count", len(calls), "token", token)
		metrics.RecordCompletion(req.Model, true, "success", time.Since(start))

	case <-idleCh:
		send(oai.ChatCompletionChunk{
			ID: completionID, Object: "chat.completion.chunk", Created: created, Model: req.Model,
			Choices: []oai.Choice{{Index: 0, Delta: &oai.DeltaMessage{}, FinishReason: oai.StringPtr("stop")}},
		})
		finish()
		metrics.RecordCompletion(req.Model, true, "success", time.Since(start))

	case <-ctx.Done():
		logger.Warn("request context cancelled during tool resume", "cause", ctx.Err(), "elapsed", time.Since(start).String())
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))

	case <-time.After(5 * time.Minute):
		logger.Warn("tool resume timeout reached", "elapsed", time.Since(start).String())
		finish()
		metrics.RecordCompletion(req.Model, true, "error", time.Since(start))
	}
}

// toolSessionExpiredMessage is the 409 body when a turn-2 request cannot be
// reconnected to a live parked session.
const toolSessionExpiredMessage = "no live tool session for the supplied tool_call_id; it may have expired or this replica did not handle turn-1. Restart the tool round-trip."

// takeParkedSession decodes the parking token from a turn-2 request and removes
// the matching live session from the registry, transferring ownership to the
// caller. Returns false when no token decodes or no live session exists.
func takeParkedSession(registry *ToolSessionRegistry, messages []oai.Message) (*parkedSession, bool) {
	if registry == nil {
		return nil, false
	}
	token, ok := tokenFromMessages(messages)
	if !ok {
		return nil, false
	}
	return registry.take(token)
}

// resolvePending delivers the client-supplied result for each pending tool call
// on the parked session. Any pending call the client did not answer is resolved
// with an empty result so the agent loop does not hang.
func resolvePending(ctx context.Context, session *copilot.Session, pending, results map[string]string, logger *slog.Logger) error {
	for toolCallID, requestID := range pending {
		result, ok := results[toolCallID]
		if !ok {
			logger.Warn("no client result for pending tool call; resolving empty", "tool_call_id", toolCallID)
		}
		if err := wrapper.ResolvePendingTool(ctx, session, requestID, result); err != nil {
			return err
		}
	}
	return nil
}
