package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

type completionEventClient struct {
	session     *copilot.Session
	stallCreate bool
}

func (c *completionEventClient) ResolveModel(model string) string { return model }
func (c *completionEventClient) ListModels(context.Context) ([]copilot.ModelInfo, error) {
	return nil, nil
}
func (c *completionEventClient) NewChatSessionWithTools(ctx context.Context, _ string, _ string, _ bool, _ []copilot.Tool, _ string) (*copilot.Session, error) {
	if c.stallCreate {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
			return nil, errors.New("test stopped unbounded session creation")
		}
	}
	return c.session, nil
}
func (c *completionEventClient) ReleaseSession(*copilot.Session) {}

func completionFailureRequest(t *testing.T, mode, phase string, streaming bool) (*httptest.ResponseRecorder, *ToolSessionRegistry) {
	t.Helper()
	done := make(chan struct{})
	// The fixture's transport cleanup must run before waiting on a stalled RPC.
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("completion handler did not exit after transport shutdown")
		}
	})
	session, _ := toolUpdateTestSession(t, mode)
	registry := NewToolSessionRegistry(time.Minute, 10, func(*copilot.Session) {}, silentLogger())
	payload := map[string]any{
		"model": "test-model", "stream": streaming,
		"messages": []map[string]string{{"role": "user", "content": "synthetic request"}},
	}
	if phase == "resume" {
		token := newToolSessionToken()
		callID := encodeToolCallID(token, 0)
		registry.store(token, session, map[string]string{callID: "pending-search"}, "")
		payload["messages"] = []map[string]string{{"role": "tool", "tool_call_id": callID, "content": "synthetic result"}}
		if mode == "stall-update" {
			payload["tools"] = reasoningTestTools()
		}
	} else if phase == "tools" {
		payload["tools"] = reasoningTestTools()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	req := reasoningTestRequest(t, payload).WithContext(ctx)
	go func() {
		defer close(done)
		ChatCompletions(&completionEventClient{session: session, stallCreate: mode == "stall-create"}, registry, silentLogger()).ServeHTTP(rec, req)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("completion RPC outlived the request deadline")
	}
	return rec, registry
}

func completionFailureBody(t *testing.T, rec *httptest.ResponseRecorder, streaming bool, status int) oai.ErrorResponse {
	t.Helper()
	body := rec.Body.String()
	if strings.Contains(body, `"finish_reason":"stop"`) || strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Fatalf("failure was reported as a successful completion: %s", body)
	}
	var failure oai.ErrorResponse
	if streaming {
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("expected SSE error: status=%d body=%s", rec.Code, body)
		}
		if strings.Count(body, "data: [DONE]") != 1 {
			t.Fatalf("stream did not terminate exactly once: %s", body)
		}
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "data: {") && strings.Contains(line, `"error"`) {
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &failure); err != nil {
					t.Fatal(err)
				}
			}
		}
	} else {
		if rec.Code != status {
			t.Fatalf("status=%d, want %d: %s", rec.Code, status, body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &failure); err != nil {
			t.Fatal(err)
		}
	}
	if failure.Error.Type == "" || failure.Error.Message == "" {
		t.Fatalf("missing explicit completion error: %s", body)
	}
	return failure
}

func TestCompletion_SessionErrorsSurface(t *testing.T) {
	for _, mode := range []string{"session-error", "session-error-idle", "session-error-tool"} {
		for _, phase := range []string{"text", "tools", "resume"} {
			for _, streaming := range []bool{false, true} {
				if phase == "text" && !streaming {
					continue // SendAndWait already handles errors for normal JSON requests.
				}
				if phase == "text" && mode == "session-error-tool" {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/stream=%t", mode, phase, streaming), func(t *testing.T) {
					rec, registry := completionFailureRequest(t, mode, phase, streaming)
					failure := completionFailureBody(t, rec, streaming, http.StatusInternalServerError)
					if !strings.Contains(failure.Error.Message, "synthetic model failure") {
						t.Fatalf("SDK error was hidden: %+v", failure)
					}
					if registry.len() != 0 {
						t.Fatal("failed session was reparked")
					}
				})
			}
		}
	}
}

func TestCompletion_DeadlineIsExplicit(t *testing.T) {
	for _, phase := range []string{"text", "tools", "resume"} {
		for _, streaming := range []bool{false, true} {
			if phase == "text" && !streaming {
				continue
			}
			t.Run(fmt.Sprintf("%s/stream=%t", phase, streaming), func(t *testing.T) {
				rec, registry := completionFailureRequest(t, "stall", phase, streaming)
				failure := completionFailureBody(t, rec, streaming, http.StatusGatewayTimeout)
				if !strings.Contains(failure.Error.Message, "timed out") {
					t.Fatalf("deadline not identified: %+v", failure)
				}
				if registry.len() != 0 {
					t.Fatal("timed-out session was reparked")
				}
			})
		}
	}
}

func TestCompletion_ProviderErrorMetadataPreserved(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			rec, _ := completionFailureRequest(t, "session-error-rate-limit", "resume", streaming)
			failure := completionFailureBody(t, rec, streaming, http.StatusTooManyRequests)
			if failure.Error.Type != "rate_limit_error" || failure.Error.Code == nil || *failure.Error.Code != "synthetic_rate_limit" {
				t.Fatalf("provider classification was lost: %+v", failure)
			}
		})
	}
}

func TestCompletion_StalledRPCIsBounded(t *testing.T) {
	for _, mode := range []string{"stall-create", "stall-send", "stall-result", "stall-update"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", mode, streaming), func(t *testing.T) {
				phase := "resume"
				if mode == "stall-send" || mode == "stall-create" {
					phase = "tools"
				}
				rec, _ := completionFailureRequest(t, mode, phase, streaming)
				// Tool updates are validated before SSE is opened.
				failure := completionFailureBody(t, rec, streaming && mode != "stall-update", http.StatusGatewayTimeout)
				if !strings.Contains(failure.Error.Message, "timed out") {
					t.Fatalf("stalled RPC did not report its deadline: %+v", failure)
				}
			})
		}
	}
}
