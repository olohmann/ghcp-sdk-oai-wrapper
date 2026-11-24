package handler

import (
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

func TestEncodeDecodeToolCallID_RoundTrip(t *testing.T) {
	token := newToolSessionToken()
	if strings.Contains(token, "_") {
		t.Fatalf("token must be underscore-free, got %q", token)
	}
	for _, idx := range []int{0, 1, 7, 42} {
		id := encodeToolCallID(token, idx)
		if !strings.HasPrefix(id, "call_") {
			t.Errorf("id missing call_ prefix: %q", id)
		}
		got, ok := decodeToolSessionToken(id)
		if !ok {
			t.Fatalf("decode failed for %q", id)
		}
		if got != token {
			t.Errorf("round-trip mismatch: got %q want %q (id %q)", got, token, id)
		}
	}
}

func TestDecodeToolSessionToken_Negatives(t *testing.T) {
	cases := []string{
		"",           // empty
		"call_",      // no token, no index
		"call_abc",   // no index separator
		"call__0",    // empty token
		"nope_abc_0", // wrong prefix
		"abc_0",      // wrong prefix
		"call_0",     // only index, no token before final underscore
	}
	for _, c := range cases {
		if _, ok := decodeToolSessionToken(c); ok {
			t.Errorf("expected decode to fail for %q", c)
		}
	}
}

func TestRegistry_StoreTake(t *testing.T) {
	r := NewToolSessionRegistry(time.Minute, 10, nil, nil)
	pending := map[string]string{"call_x_0": "req-1"}
	// Store with a nil session; take does not touch the session on the happy path.
	r.store("tok1", nil, pending, "")
	if r.len() != 1 {
		t.Fatalf("expected 1 parked, got %d", r.len())
	}
	entry, err := r.take("tok1", nil)
	if err != nil {
		t.Fatalf("expected take to succeed: %v", err)
	}
	if entry.pending["call_x_0"] != "req-1" {
		t.Errorf("pending not preserved: %+v", entry.pending)
	}
	if r.len() != 0 {
		t.Errorf("take should remove the entry, len=%d", r.len())
	}
	// Second take is a miss.
	if _, err := r.take("tok1", nil); !errors.Is(err, errToolSessionExpired) {
		t.Error("expected second take to miss")
	}
	// Unknown token is a miss.
	if _, err := r.take("nope", nil); !errors.Is(err, errToolSessionExpired) {
		t.Error("expected unknown token to miss")
	}
}

func TestRegistry_TakeExpired(t *testing.T) {
	r := NewToolSessionRegistry(time.Millisecond, 10, nil, nil)
	r.store("tok", nil, nil, "")
	time.Sleep(5 * time.Millisecond)
	if _, err := r.take("tok", nil); !errors.Is(err, errToolSessionExpired) {
		t.Error("expected expired entry to miss")
	}
	if r.len() != 0 {
		t.Errorf("expired take should remove the entry, len=%d", r.len())
	}
}

func TestRegistry_EvictOldestOnCap(t *testing.T) {
	r := NewToolSessionRegistry(time.Minute, 2, nil, nil)
	r.store("a", nil, nil, "")
	time.Sleep(2 * time.Millisecond)
	r.store("b", nil, nil, "")
	time.Sleep(2 * time.Millisecond)
	// Exceed cap: "a" (oldest) must be evicted.
	r.store("c", nil, nil, "")
	if r.len() != 2 {
		t.Fatalf("expected cap enforced at 2, got %d", r.len())
	}
	if _, err := r.take("a", nil); !errors.Is(err, errToolSessionExpired) {
		t.Error("expected oldest entry 'a' to be evicted")
	}
	if _, err := r.take("b", nil); err != nil {
		t.Error("expected 'b' to remain")
	}
	if _, err := r.take("c", nil); err != nil {
		t.Error("expected 'c' to remain")
	}
}

func TestRegistry_ReStoreSameTokenNoEvict(t *testing.T) {
	r := NewToolSessionRegistry(time.Minute, 1, nil, nil)
	r.store("a", nil, nil, "")
	// Re-store the same token; should not evict (replaces in place).
	r.store("a", nil, nil, "")
	if r.len() != 1 {
		t.Fatalf("expected 1, got %d", r.len())
	}
}

func TestRegistry_ReasoningEffort(t *testing.T) {
	high, low, empty := "high", "low", ""
	tests := []struct {
		name      string
		original  string
		requested *string
		wantError bool
	}{
		{name: "omitted", original: high},
		{name: "unchanged", original: high, requested: &high},
		{name: "changed", original: high, requested: &low, wantError: true},
		{name: "empty", original: high, requested: &empty, wantError: true},
		{name: "override default", requested: &high, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewToolSessionRegistry(time.Minute, 10, nil, silentLogger())
			r.store("tok", nil, nil, tt.original)
			entry, err := r.take("tok", tt.requested)
			if tt.wantError {
				if !errors.Is(err, errInvalidReasoningEffort) {
					t.Fatalf("expected invalid effort, got %v", err)
				}
				if r.len() != 1 {
					t.Fatal("rejected effort consumed the parked session")
				}
				entry, err = r.take("tok", nil)
			}
			if err != nil {
				t.Fatalf("expected original session to remain usable: %v", err)
			}
			if entry.reasoningEffort != tt.original {
				t.Errorf("effort = %q, want %q", entry.reasoningEffort, tt.original)
			}
		})
	}
}

func TestMintToolCalls(t *testing.T) {
	token := newToolSessionToken()
	events := []*copilot.ExternalToolRequestedData{
		{RequestID: "req-a", ToolName: "get_weather", Arguments: map[string]any{"city": "Paris"}},
		nil, // skipped
		{RequestID: "req-b", ToolName: "get_time", Arguments: nil},
	}
	calls, pending := mintToolCalls(token, events)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}
	// IDs embed the token and map back to the right RequestID.
	if pending[calls[0].ID] != "req-a" {
		t.Errorf("call[0] pending: %+v", pending)
	}
	if pending[calls[1].ID] != "req-b" {
		t.Errorf("call[1] pending: %+v", pending)
	}
	tok, ok := decodeToolSessionToken(calls[0].ID)
	if !ok || tok != token {
		t.Errorf("call[0] id does not embed token: %q", calls[0].ID)
	}
	if calls[0].Function.Name != "get_weather" || calls[0].Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("unexpected call[0]: %+v", calls[0].Function)
	}
	if calls[1].Function.Arguments != "{}" {
		t.Errorf("nil args should be {}, got %q", calls[1].Function.Arguments)
	}
	// Indices are distinct.
	if calls[0].ID == calls[1].ID {
		t.Error("expected distinct IDs per call")
	}
}

func TestResultsByToolCallID(t *testing.T) {
	msgs := []oai.Message{
		{Role: "user", Content: oai.NewTextContent("q")},
		{Role: "assistant", Content: oai.NewTextContent(""), ToolCalls: []oai.ToolCall{
			{ID: "call_t_0", Function: oai.FunctionCall{Name: "get_weather"}},
		}},
		{Role: "tool", ToolCallID: "call_t_0", Content: oai.NewTextContent("18C")},
		{Role: "tool", ToolCallID: "", Content: oai.NewTextContent("orphan")}, // dropped
	}
	got := resultsByToolCallID(msgs)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d (%v)", len(got), got)
	}
	if got["call_t_0"] != "18C" {
		t.Errorf("unexpected results: %v", got)
	}
}

func TestTokenFromMessages(t *testing.T) {
	token := newToolSessionToken()
	id := encodeToolCallID(token, 0)

	// Prefer role:"tool".
	msgs := []oai.Message{
		{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: id}}},
		{Role: "tool", ToolCallID: id, Content: oai.NewTextContent("r")},
	}
	got, ok := tokenFromMessages(msgs)
	if !ok || got != token {
		t.Errorf("expected token %q from tool message, got %q ok=%v", token, got, ok)
	}

	// Fall back to assistant tool_calls when no tool message decodes.
	msgs = []oai.Message{
		{Role: "assistant", ToolCalls: []oai.ToolCall{{ID: id}}},
	}
	got, ok = tokenFromMessages(msgs)
	if !ok || got != token {
		t.Errorf("expected token %q from assistant, got %q ok=%v", token, got, ok)
	}

	// No decodable IDs -> miss.
	msgs = []oai.Message{
		{Role: "tool", ToolCallID: "external-id", Content: oai.NewTextContent("r")},
	}
	if _, ok := tokenFromMessages(msgs); ok {
		t.Error("expected miss for non-minted IDs")
	}
}
