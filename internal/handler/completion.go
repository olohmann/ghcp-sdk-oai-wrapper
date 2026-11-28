package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/metrics"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

const completionTimeout = 5 * time.Minute

type completionState struct {
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  *copilot.SessionErrorData
}

func newCompletionState() *completionState {
	return &completionState{done: make(chan struct{})}
}

func (s *completionState) finish() {
	s.once.Do(func() { close(s.done) })
}

func (s *completionState) fail(event copilot.SessionEvent, err *copilot.SessionErrorData) {
	if event.AgentID != nil && *event.AgentID != "" {
		return
	}
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.finish()
}

func (s *completionState) failed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err != nil
}

func (s *completionState) failResponse(ctx context.Context, w http.ResponseWriter, sse *oai.SSEWriter, req *oai.ChatCompletionRequest, start time.Time, logger *slog.Logger) bool {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		logger.Error("Copilot session failed", "error_type", err.ErrorType, "message", err.Message, "error_code", err.ErrorCode, "status_code", err.StatusCode)
		metrics.RecordCompletion(req.Model, req.Stream, "error", time.Since(start))
		message := "Copilot session failed (" + err.ErrorType + ")"
		if err.Message != "" {
			message = "Copilot session failed: " + err.Message
		}
		status := http.StatusInternalServerError
		if err.StatusCode != nil && *err.StatusCode >= 400 && *err.StatusCode <= 599 {
			status = int(*err.StatusCode)
		}
		detail := oai.ErrorDetail{Message: message, Type: "server_error", Code: err.ErrorCode}
		if err.ProviderErrorType != nil && *err.ProviderErrorType != "" {
			detail.Type = *err.ProviderErrorType
		}
		if detail.Code == nil {
			detail.Code = err.ProviderErrorCode
		}
		writeCompletionError(w, sse, status, detail, logger)
		return true
	}
	if ctx.Err() != nil {
		logger.Warn("chat completion context ended", "cause", ctx.Err(), "elapsed", time.Since(start).String())
		metrics.RecordCompletion(req.Model, req.Stream, "error", time.Since(start))
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeCompletionOperationError(ctx, w, sse, "", logger)
		}
		return true
	}
	return false
}

// RPC waits stay bounded without tying the parked agent loop to disconnects.
func detachedCompletionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(completionTimeout)
	}
	return context.WithDeadline(context.WithoutCancel(ctx), deadline)
}

func writeCompletionOperationError(ctx context.Context, w http.ResponseWriter, sse *oai.SSEWriter, message string, logger *slog.Logger) {
	status := http.StatusInternalServerError
	var code *string
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
		message = "chat completion timed out"
		code = oai.StringPtr("completion_timeout")
	}
	writeCompletionError(w, sse, status, oai.ErrorDetail{Message: message, Type: "server_error", Code: code}, logger)
}

func writeCompletionError(w http.ResponseWriter, sse *oai.SSEWriter, status int, detail oai.ErrorDetail, logger *slog.Logger) {
	response := oai.ErrorResponse{Error: detail}
	if sse == nil {
		oai.WriteJSON(w, status, response)
		return
	}
	if err := sse.WriteEvent(response); err != nil {
		logger.Warn("failed to write SSE error", "error", err)
	}
	if err := sse.WriteDone(); err != nil {
		logger.Warn("failed to terminate SSE error response", "error", err)
	}
}
