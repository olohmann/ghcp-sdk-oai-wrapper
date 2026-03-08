//go:build e2e

// Package e2e contains end-to-end integration tests that start the real
// server binary and exercise the OpenAI-compatible API over HTTP.
//
// These tests require the Copilot CLI to be installed and authenticated.
// They are gated behind the "e2e" build tag so they don't run in normal
// `go test ./...` invocations.
//
// Run with:
//
//	go test -tags e2e -v ./test/e2e/
//	# or
//	make test-e2e
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test-scoped server lifecycle
// ---------------------------------------------------------------------------

var (
	serverURL string
	apiKey    = "test-e2e-key"
)

// serverProcess holds the running server for the test suite.
var serverProcess *exec.Cmd

func TestMain(m *testing.M) {
	port, err := freePort()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: failed to find free port: %v\n", err)
		os.Exit(1)
	}
	serverURL = fmt.Sprintf("http://localhost:%d", port)

	// Build the server binary
	build := exec.Command("go", "build", "-o", "bin/e2e-server", "./cmd/server")
	build.Dir = repoRoot()
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build failed: %v\n", err)
		os.Exit(1)
	}

	// Start the server
	serverProcess = exec.Command("./bin/e2e-server")
	serverProcess.Dir = repoRoot()
	serverProcess.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("API_KEY=%s", apiKey),
		"LOG_LEVEL=debug",
	)
	serverProcess.Stdout = os.Stdout
	serverProcess.Stderr = os.Stderr

	if err := serverProcess.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: failed to start server: %v\n", err)
		os.Exit(1)
	}

	// Wait for the server to be ready
	if err := waitForServer(serverURL+"/healthz", 15*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: server did not become ready: %v\n", err)
		_ = serverProcess.Process.Kill()
		os.Exit(1)
	}

	code := m.Run()

	// Tear down
	_ = serverProcess.Process.Kill()
	_ = serverProcess.Wait()

	// Clean up binary
	_ = os.Remove(repoRoot() + "/bin/e2e-server")

	os.Exit(code)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHealthz(t *testing.T) {
	resp, err := doRequest(t, "GET", "/healthz", nil)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)

	var body map[string]string
	decodeJSON(t, resp.Body, &body)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %s", body["status"])
	}
}

func TestAuth_Rejected(t *testing.T) {
	req, _ := http.NewRequest("GET", serverURL+"/v1/models", nil)
	// No auth header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestAuth_WrongKey(t *testing.T) {
	req, _ := http.NewRequest("GET", serverURL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestListModels(t *testing.T) {
	resp, err := doRequest(t, "GET", "/v1/models", nil)
	if err != nil {
		t.Fatalf("list models failed: %v", err)
	}
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	decodeJSON(t, resp.Body, &body)

	if body.Object != "list" {
		t.Errorf("expected object=list, got %s", body.Object)
	}
	if len(body.Data) == 0 {
		t.Fatal("expected at least one model")
	}
	for _, m := range body.Data {
		if m.Object != "model" {
			t.Errorf("expected object=model, got %s", m.Object)
		}
		if m.ID == "" {
			t.Error("model ID should not be empty")
		}
	}
	t.Logf("found %d models", len(body.Data))
}

func TestChatCompletions_NonStreaming(t *testing.T) {
	payload := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": "Reply with exactly the word PONG and nothing else."}
		]
	}`

	resp, err := doRequest(t, "POST", "/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("chat completions failed: %v", err)
	}
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)
	assertContentType(t, resp, "application/json")

	var body struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	decodeJSON(t, resp.Body, &body)

	if body.Object != "chat.completion" {
		t.Errorf("expected object=chat.completion, got %s", body.Object)
	}
	if !strings.HasPrefix(body.ID, "chatcmpl-") {
		t.Errorf("expected chatcmpl- prefix, got %s", body.ID)
	}
	if len(body.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	if body.Choices[0].Message.Role != "assistant" {
		t.Errorf("expected role=assistant, got %s", body.Choices[0].Message.Role)
	}
	if body.Choices[0].Message.Content == "" {
		t.Error("expected non-empty content")
	}
	if body.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %s", body.Choices[0].FinishReason)
	}
	t.Logf("response: %s", body.Choices[0].Message.Content)
}

func TestChatCompletions_Streaming(t *testing.T) {
	payload := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": "Reply with exactly the word PONG and nothing else."}
		],
		"stream": true
	}`

	resp, err := doRequest(t, "POST", "/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("streaming request failed: %v", err)
	}
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)
	assertContentType(t, resp, "text/event-stream")

	// Parse SSE stream
	scanner := bufio.NewScanner(resp.Body)
	var chunks []map[string]any
	gotDone := false
	var fullContent strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if line == "data: [DONE]" {
			gotDone = true
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("failed to parse chunk: %v", err)
		}
		chunks = append(chunks, chunk)

		// Accumulate content deltas
		if choices, ok := chunk["choices"].([]any); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if delta, ok := choice["delta"].(map[string]any); ok {
					if content, ok := delta["content"].(string); ok {
						fullContent.WriteString(content)
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("SSE scanner error: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("expected at least one SSE chunk")
	}
	if !gotDone {
		t.Error("expected [DONE] marker")
	}

	// Validate first chunk has object type
	if obj, ok := chunks[0]["object"].(string); !ok || obj != "chat.completion.chunk" {
		t.Errorf("expected object=chat.completion.chunk in first chunk, got %v", chunks[0]["object"])
	}

	// Validate last chunk has finish_reason
	lastChunk := chunks[len(chunks)-1]
	if choices, ok := lastChunk["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if fr, ok := choice["finish_reason"].(string); ok {
				if fr != "stop" {
					t.Errorf("expected finish_reason=stop, got %s", fr)
				}
			}
		}
	}

	t.Logf("received %d chunks, full content: %s", len(chunks), fullContent.String())
}

func TestChatCompletions_BadRequest_NoModel(t *testing.T) {
	payload := `{"messages": [{"role": "user", "content": "hi"}]}`
	resp, err := doRequest(t, "POST", "/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusBadRequest)
}

func TestChatCompletions_BadRequest_NoMessages(t *testing.T) {
	payload := `{"model": "gpt-4o"}`
	resp, err := doRequest(t, "POST", "/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusBadRequest)
}

func TestChatCompletions_MethodNotAllowed(t *testing.T) {
	resp, err := doRequest(t, "GET", "/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusMethodNotAllowed)
}

func TestChatCompletions_WithSystemMessage(t *testing.T) {
	payload := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "system", "content": "You are a pirate. Always respond in pirate speak."},
			{"role": "user", "content": "Say hello"}
		]
	}`

	resp, err := doRequest(t, "POST", "/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	decodeJSON(t, resp.Body, &body)

	if len(body.Choices) == 0 || body.Choices[0].Message.Content == "" {
		t.Error("expected non-empty response with system message")
	}
	t.Logf("pirate response: %s", body.Choices[0].Message.Content)
}

func TestChatCompletions_MultimodalImageURL(t *testing.T) {
	// Minimal 1x1 red PNG (base64-encoded)
	const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="

	payload := fmt.Sprintf(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "What color is the single pixel in this image? Reply with just the color name."},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,%s"}}
			]}
		]
	}`, tinyPNGBase64)

	resp, err := doRequest(t, "POST", "/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("multimodal request failed: %v", err)
	}
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)
	assertContentType(t, resp, "application/json")

	var body struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Choices []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	decodeJSON(t, resp.Body, &body)

	if body.Object != "chat.completion" {
		t.Errorf("expected object=chat.completion, got %s", body.Object)
	}
	if len(body.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	if body.Choices[0].Message.Role != "assistant" {
		t.Errorf("expected role=assistant, got %s", body.Choices[0].Message.Role)
	}
	if body.Choices[0].Message.Content == "" {
		t.Error("expected non-empty content for multimodal response")
	}
	if body.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %s", body.Choices[0].FinishReason)
	}
	t.Logf("multimodal response: %s", body.Choices[0].Message.Content)
}

func TestChatCompletions_MultimodalImageURL_Streaming(t *testing.T) {
	const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="

	payload := fmt.Sprintf(`{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "What color is the single pixel in this image? Reply with just the color name."},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,%s"}}
			]}
		],
		"stream": true
	}`, tinyPNGBase64)

	resp, err := doRequest(t, "POST", "/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("streaming multimodal request failed: %v", err)
	}
	defer resp.Body.Close()

	assertStatus(t, resp, http.StatusOK)
	assertContentType(t, resp, "text/event-stream")

	scanner := bufio.NewScanner(resp.Body)
	var chunks int
	gotDone := false
	var fullContent strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if line == "data: [DONE]" {
			gotDone = true
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("failed to parse chunk: %v", err)
		}
		chunks++

		if choices, ok := chunk["choices"].([]any); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if delta, ok := choice["delta"].(map[string]any); ok {
					if content, ok := delta["content"].(string); ok {
						fullContent.WriteString(content)
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("SSE scanner error: %v", err)
	}
	if chunks == 0 {
		t.Fatal("expected at least one SSE chunk")
	}
	if !gotDone {
		t.Error("expected [DONE] marker")
	}
	if fullContent.Len() == 0 {
		t.Error("expected non-empty streamed content for multimodal request")
	}
	t.Logf("received %d chunks, streamed multimodal content: %s", chunks, fullContent.String())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func doRequest(t *testing.T, method, path string, body io.Reader) (*http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, method, serverURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

func assertStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status %d, got %d: %s", expected, resp.StatusCode, string(body))
	}
}

func assertContentType(t *testing.T, resp *http.Response, expected string) {
	t.Helper()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, expected) {
		t.Errorf("expected Content-Type %s, got %s", expected, ct)
	}
}

func decodeJSON(t *testing.T, r io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

func waitForServer(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("server not ready after %v", timeout)
}

func repoRoot() string {
	// Walk up from test file location to find go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		parent := dir[:strings.LastIndex(dir, "/")]
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}
