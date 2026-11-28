package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

type toolUpdateRPCRequest struct {
	ID     json.RawMessage            `json:"id"`
	Method string                     `json:"method"`
	Params map[string]json.RawMessage `json:"params"`
}

func toolUpdateTestSession(t *testing.T, reject string, initialTools ...copilot.Tool) (*copilot.Session, <-chan toolUpdateRPCRequest) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan toolUpdateRPCRequest, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		collisionTool := ""
		for {
			size := 0
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimSpace(line)
				if line == "" {
					break
				}
				if value, ok := strings.CutPrefix(line, "Content-Length:"); ok {
					size, err = strconv.Atoi(strings.TrimSpace(value))
					if err != nil {
						t.Error(err)
						return
					}
				}
			}
			body := make([]byte, size)
			if _, err := io.ReadFull(reader, body); err != nil {
				return
			}
			var request toolUpdateRPCRequest
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			if len(request.ID) == 0 {
				continue
			}
			result := any(map[string]any{})
			switch request.Method {
			case "connect":
				result = map[string]any{"ok": true, "protocolVersion": 3, "version": "test"}
			case "session.create":
				result = map[string]any{"sessionId": "test-session"}
				if reject == "builtin-collision" {
					requests <- request
				}
			case "session.send":
				result = map[string]any{"messageId": "test-message"}
			case "session.tools.set", "session.tools.handlePendingToolCall":
				requests <- request
			case "session.options.update":
				requests <- request
				result = map[string]any{"success": reject != "options-refused"}
			}
			if (reject == "stall-result" && request.Method == "session.tools.handlePendingToolCall") ||
				(reject == "stall-send" && request.Method == "session.send") ||
				(reject == "stall-update" && request.Method == "session.tools.set") {
				continue
			}
			response := map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}
			// Stop at result delivery; this fixture never runs a real model.
			if request.Method == reject || (request.Method == "session.tools.handlePendingToolCall" && reject != "emit-discovered" && reject != "builtin-collision" && !strings.HasPrefix(reject, "session-error") && reject != "stall") {
				delete(response, "result")
				response["error"] = map[string]any{"code": -32603, "message": "test RPC rejected"}
			}
			if reject == "builtin-collision" && (request.Method == "session.create" || request.Method == "session.tools.set") {
				var tools []copilot.Tool
				if err := json.Unmarshal(request.Params["tools"], &tools); err != nil {
					t.Error(err)
					return
				}
				for _, tool := range tools {
					if tool.Name != "bash" && tool.Name != "grep" {
						continue
					}
					if !tool.OverridesBuiltInTool {
						delete(response, "result")
						response["error"] = map[string]any{
							"code": -32603, "message": fmt.Sprintf("external tool %q conflicts with a built-in tool", tool.Name),
						}
					}
					collisionTool = tool.Name
				}
			}
			data, err := json.Marshal(response)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := fmt.Fprintf(conn, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
				return
			}
			if (request.Method == "session.tools.handlePendingToolCall" || request.Method == "session.send") &&
				(reject == "emit-discovered" || reject == "builtin-collision" || strings.HasPrefix(reject, "session-error")) {
				name := "clipboard__copy"
				if reject == "builtin-collision" {
					name = collisionTool
				}
				event := copilot.SessionEvent{
					ID: "discovered-call", Timestamp: time.Now(),
					Data: &copilot.ExternalToolRequestedData{
						SessionID: "test-session", RequestID: "pending-copy",
						ToolCallID: "sdk-copy-call", ToolName: name,
						Arguments: map[string]any{"text": "synthetic transcript"},
					},
				}
				if strings.HasPrefix(reject, "session-error") {
					failure := &copilot.SessionErrorData{ErrorType: "query", Message: "synthetic model failure"}
					if reject == "session-error-rate-limit" {
						status := int32(http.StatusTooManyRequests)
						failure.StatusCode = &status
						failure.ProviderErrorType = copilot.String("rate_limit_error")
						failure.ProviderErrorCode = copilot.String("synthetic_rate_limit")
					}
					event.Data = failure
				}
				events := []copilot.SessionEvent{event}
				if reject == "session-error-tool" {
					events = append([]copilot.SessionEvent{{
						ID: "tool-before-error", Timestamp: time.Now(),
						Data: &copilot.ExternalToolRequestedData{
							SessionID: "test-session", RequestID: "pending-failed-tool",
							ToolCallID: "failed-tool-call", ToolName: "search_tools",
						},
					}}, events...)
				}
				if reject == "session-error-idle" {
					events = append(events, copilot.SessionEvent{ID: "idle-after-error", Timestamp: time.Now(), Data: &copilot.SessionIdleData{}})
				}
				for _, event := range events {
					notification := map[string]any{
						"jsonrpc": "2.0", "method": "session.event",
						"params": map[string]any{"sessionId": "test-session", "event": event},
					}
					data, err := json.Marshal(notification)
					if err != nil {
						t.Error(err)
						return
					}
					if _, err := fmt.Fprintf(conn, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
						return
					}
				}
			}
		}
	}()
	client := copilot.NewClient(&copilot.ClientOptions{
		Connection: copilot.URIConnection{URL: listener.Addr().String()},
	})
	t.Cleanup(func() {
		client.ForceStop()
		listener.Close()
		<-done
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if len(initialTools) == 0 {
		initialTools = []copilot.Tool{{Name: "search_tools"}}
	}
	names := make([]string, len(initialTools))
	for i, tool := range initialTools {
		names[i] = tool.Name
	}
	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		SessionID:           "test-session",
		Model:               "test-model",
		Tools:               initialTools,
		AvailableTools:      names,
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session, requests
}

func TestToolResume_UpdatesDeclarationsBeforeResults(t *testing.T) {
	tests := []struct {
		name  string
		tools string
		names []string
	}{
		{name: "discovered tool", tools: `[{"type":"function","function":{"name":"search_tools"}},{"type":"function","function":{"name":"clipboard__copy","description":"Copy supplied text","parameters":{"type":"object","properties":{"text":{"type":"string"}}}}}]`, names: []string{"search_tools", "clipboard__copy"}},
		{name: "replacement removes old tool", tools: `[{"type":"function","function":{"name":"clipboard__copy"}}]`, names: []string{"clipboard__copy"}},
		{name: "explicit empty clears tools", tools: `[]`, names: []string{}},
		{name: "omitted retains tools"},
		{name: "null retains tools", tools: `null`},
	}
	for _, tt := range tests {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tt.name, streaming), func(t *testing.T) {
				session, requests := toolUpdateTestSession(t, "")
				registry := NewToolSessionRegistry(time.Minute, 10, func(*copilot.Session) {}, silentLogger())
				token := newToolSessionToken()
				callID := encodeToolCallID(token, 0)
				registry.store(token, session, map[string]string{callID: "pending-search"}, "")
				payload := map[string]any{
					"model":    "test-model",
					"messages": []map[string]string{{"role": "tool", "tool_call_id": callID, "content": "discovered clipboard tool"}},
					"stream":   streaming,
				}
				if tt.tools != "" {
					payload["tools"] = json.RawMessage(tt.tools)
				}
				rec := httptest.NewRecorder()
				ChatCompletions(&reasoningTestClient{}, registry, silentLogger()).ServeHTTP(rec, reasoningTestRequest(t, payload))
				var calls []toolUpdateRPCRequest
				for len(requests) > 0 {
					calls = append(calls, <-requests)
				}
				methods := make([]string, len(calls))
				for i, call := range calls {
					methods[i] = call.Method
				}
				want := []string{"session.tools.handlePendingToolCall"}
				if tt.names != nil {
					want = []string{"session.tools.set", "session.options.update", "session.tools.handlePendingToolCall"}
				}
				if !reflect.DeepEqual(methods, want) {
					t.Fatalf("RPC order = %v, want %v", methods, want)
				}
				if tt.names == nil {
					return
				}
				var tools []struct {
					Name        string         `json:"name"`
					Description string         `json:"description"`
					Parameters  map[string]any `json:"parameters"`
				}
				if err := json.Unmarshal(calls[0].Params["tools"], &tools); err != nil {
					t.Fatal(err)
				}
				names := make([]string, len(tools))
				for i, tool := range tools {
					names[i] = tool.Name
				}
				if !reflect.DeepEqual(names, tt.names) {
					t.Fatalf("registered names = %v, want %v", names, tt.names)
				}
				if tt.name == "discovered tool" && (tools[1].Description != "Copy supplied text" || tools[1].Parameters["type"] != "object") {
					t.Fatalf("discovered tool schema not forwarded: %+v", tools[1])
				}
				var available []string
				if err := json.Unmarshal(calls[1].Params["availableTools"], &available); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(available, tt.names) || len(calls[1].Params) != 2 {
					t.Fatalf("availability patch must change only caller tool names: %v", calls[1].Params)
				}
			})
		}
	}
}

func TestToolResume_UpdateFailureBeforeStreaming(t *testing.T) {
	for _, reject := range []string{"session.tools.set", "session.options.update", "options-refused"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", reject, streaming), func(t *testing.T) {
				session, requests := toolUpdateTestSession(t, reject)
				registry := NewToolSessionRegistry(time.Minute, 10, func(*copilot.Session) {}, silentLogger())
				token := newToolSessionToken()
				callID := encodeToolCallID(token, 0)
				registry.store(token, session, map[string]string{callID: "pending-search"}, "")
				payload := map[string]any{
					"model":    "test-model",
					"messages": []map[string]string{{"role": "tool", "tool_call_id": callID, "content": "discovered tool"}},
					"tools":    reasoningTestTools(),
					"stream":   streaming,
				}

				rec := httptest.NewRecorder()
				ChatCompletions(&reasoningTestClient{}, registry, silentLogger()).ServeHTTP(rec, reasoningTestRequest(t, payload))
				if rec.Code != http.StatusInternalServerError || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
					t.Fatalf("update failure must precede SSE: status=%d type=%q body=%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), "failed to update session tools") {
					t.Fatalf("missing explicit update error: %s", rec.Body.String())
				}
				for len(requests) > 0 {
					if request := <-requests; request.Method == "session.tools.handlePendingToolCall" {
						t.Fatal("resolved pending result after a failed tool update")
					}
				}
			})
		}
	}
}

func TestToolResume_DiscoveredToolEmittedAndReparked(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			session, requests := toolUpdateTestSession(t, "emit-discovered")
			registry := NewToolSessionRegistry(time.Minute, 10, func(*copilot.Session) {}, silentLogger())
			t.Cleanup(registry.Close)
			token := newToolSessionToken()
			callID := encodeToolCallID(token, 0)
			registry.store(token, session, map[string]string{callID: "pending-search"}, "high")
			payload := map[string]any{
				"model":    "test-model",
				"messages": []map[string]string{{"role": "tool", "tool_call_id": callID, "content": "discovered clipboard tool"}},
				"tools":    json.RawMessage(`[{"type":"function","function":{"name":"clipboard__copy"}}]`),
				"stream":   streaming,
			}

			rec := httptest.NewRecorder()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request := reasoningTestRequest(t, payload).WithContext(ctx)
			ChatCompletions(&reasoningTestClient{}, registry, silentLogger()).ServeHTTP(rec, request)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"clipboard__copy"`) || !strings.Contains(rec.Body.String(), `"finish_reason":"tool_calls"`) {
				t.Fatalf("expected structured discovered call: status=%d body=%s", rec.Code, rec.Body.String())
			}
			for _, method := range []string{"session.tools.set", "session.options.update", "session.tools.handlePendingToolCall"} {
				select {
				case call := <-requests:
					if call.Method != method {
						t.Fatalf("RPC = %q, want %q", call.Method, method)
					}
				default:
					t.Fatalf("missing RPC %s", method)
				}
			}
			if registry.len() != 1 {
				t.Fatalf("discovered call did not repark the live session: %d", registry.len())
			}
			for newToken, entry := range registry.entries {
				if newToken == token || entry.session != session || entry.reasoningEffort != "high" {
					t.Fatalf("session identity, token, or original effort not preserved: %+v", entry)
				}
				if entry.pending[encodeToolCallID(newToken, 0)] != "pending-copy" {
					t.Fatalf("new external request was not mapped: %v", entry.pending)
				}
			}
		})
	}
}

func TestInitialTools_BuiltinNameOverrides(t *testing.T) {
	for _, name := range []string{"grep", "bash"} {
		t.Run(name, func(t *testing.T) {
			session, requests := toolUpdateTestSession(t, "builtin-collision", toolsToSDK([]oai.Tool{{
				Type: "function", Function: oai.FunctionDef{Name: name},
			}})...)
			create := <-requests
			var available []string
			if err := json.Unmarshal(create.Params["availableTools"], &available); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(available, []string{name}) {
				t.Fatalf("only caller tools should be available: %v", available)
			}
			events := make(chan *copilot.ExternalToolRequestedData, 1)
			unsubscribe := session.On(func(event copilot.SessionEvent) {
				if call, ok := event.Data.(*copilot.ExternalToolRequestedData); ok {
					events <- call
				}
			})
			defer unsubscribe()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := session.Send(ctx, copilot.MessageOptions{Prompt: "synthetic caller-tool request"}); err != nil {
				t.Fatal(err)
			}
			select {
			case call := <-events:
				if call.ToolName != name || call.RequestID == "" {
					t.Fatalf("caller tool must remain external with its original name: %+v", call)
				}
			case <-ctx.Done():
				t.Fatal("overridden tool was not emitted as an external call")
			}
		})
	}
}

func TestToolResume_BuiltinNameOverrides(t *testing.T) {
	for _, name := range []string{"grep", "bash"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", name, streaming), func(t *testing.T) {
				session, requests := toolUpdateTestSession(t, "builtin-collision")
				<-requests // Initial declaration has no built-in collision.
				registry := NewToolSessionRegistry(time.Minute, 10, func(*copilot.Session) {}, silentLogger())
				t.Cleanup(registry.Close)
				token := newToolSessionToken()
				callID := encodeToolCallID(token, 0)
				registry.store(token, session, map[string]string{callID: "pending-search"}, "")
				payload := map[string]any{
					"model": "test-model", "stream": streaming,
					"messages": []map[string]string{{"role": "tool", "tool_call_id": callID, "content": "synthetic discovery"}},
					"tools":    []oai.Tool{{Type: "function", Function: oai.FunctionDef{Name: name}}},
				}
				rec := httptest.NewRecorder()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ChatCompletions(&reasoningTestClient{}, registry, silentLogger()).ServeHTTP(rec, reasoningTestRequest(t, payload).WithContext(ctx))
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"`+name+`"`) || !strings.Contains(rec.Body.String(), `"finish_reason":"tool_calls"`) {
					t.Fatalf("updated caller tool must remain external with its original name: status=%d body=%s", rec.Code, rec.Body.String())
				}
				if registry.len() != 1 {
					t.Fatal("overridden caller tool was not parked for client execution")
				}
			})
		}
	}
}
