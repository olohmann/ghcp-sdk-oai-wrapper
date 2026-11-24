package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/oai"
)

// parkedSession is a live turn-1 Copilot session held open between HTTP calls,
// awaiting the client's tool results. Its pending map ties each emitted OpenAI
// tool_call ID to the SDK RequestID needed to resolve that exact pending call
// on turn-2.
type parkedSession struct {
	session         *copilot.Session
	pending         map[string]string // emitted tool_call ID -> SDK RequestID
	createdAt       time.Time
	reasoningEffort string
}

// ToolSessionRegistry holds parked tool sessions keyed by an opaque token that
// is embedded in the emitted tool_call IDs. It enforces a TTL and a maximum
// number of concurrently parked sessions, evicting (and tearing down) the
// oldest session when the cap is exceeded. It is safe for concurrent use.
type ToolSessionRegistry struct {
	mu        sync.Mutex
	entries   map[string]*parkedSession
	ttl       time.Duration
	maxParked int
	release   func(*copilot.Session)
	logger    *slog.Logger
}

// NewToolSessionRegistry constructs a registry with the given TTL and parked-cap.
// release tears down a discarded session (normally wrapper.Client.ReleaseSession);
// when nil, the session is only disconnected.
func NewToolSessionRegistry(ttl time.Duration, maxParked int, release func(*copilot.Session), logger *slog.Logger) *ToolSessionRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if maxParked <= 0 {
		maxParked = 256
	}
	return &ToolSessionRegistry{
		entries:   make(map[string]*parkedSession),
		ttl:       ttl,
		maxParked: maxParked,
		release:   release,
		logger:    logger,
	}
}

// store parks a live session under the given token, taking ownership of the
// session's lifetime. When the parked-cap is exceeded the oldest entry is
// evicted and torn down.
func (r *ToolSessionRegistry) store(token string, session *copilot.Session, pending map[string]string, reasoningEffort string) {
	r.mu.Lock()
	var evict *parkedSession
	if _, exists := r.entries[token]; !exists && len(r.entries) >= r.maxParked {
		evict = r.evictOldestLocked()
	}
	r.entries[token] = &parkedSession{
		session:         session,
		pending:         pending,
		createdAt:       time.Now(),
		reasoningEffort: reasoningEffort,
	}
	r.mu.Unlock()

	if evict != nil {
		r.logger.Warn("tool session registry full; evicting oldest parked session", "cap", r.maxParked)
		r.discard(evict.session)
	}
}

// take removes and returns the parked session for a token, transferring
// ownership to the caller. An effort mismatch leaves the session available.
func (r *ToolSessionRegistry) take(token string, reasoningEffort *string) (*parkedSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[token]
	if !ok {
		return nil, errToolSessionExpired
	}
	if time.Since(entry.createdAt) > r.ttl {
		delete(r.entries, token)
		// Expired: tear it down and report a miss so turn-2 gets a 409.
		go r.discard(entry.session)
		return nil, errToolSessionExpired
	}
	if reasoningEffort != nil && (*reasoningEffort == "" || *reasoningEffort != entry.reasoningEffort) {
		return nil, fmt.Errorf("%w: cannot change during a tool round-trip; omit it or repeat the original value", errInvalidReasoningEffort)
	}
	delete(r.entries, token)
	return entry, nil
}

// len reports the number of currently parked sessions (used by tests).
func (r *ToolSessionRegistry) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// evictOldestLocked removes and returns the oldest entry. Caller must hold r.mu.
func (r *ToolSessionRegistry) evictOldestLocked() *parkedSession {
	var oldestToken string
	var oldest *parkedSession
	for token, e := range r.entries {
		if oldest == nil || e.createdAt.Before(oldest.createdAt) {
			oldest = e
			oldestToken = token
		}
	}
	if oldest != nil {
		delete(r.entries, oldestToken)
	}
	return oldest
}

// StartGC launches a background sweep that evicts and tears down parked sessions
// older than the TTL. It stops when ctx is cancelled.
func (r *ToolSessionRegistry) StartGC(ctx context.Context) {
	interval := r.ttl / 2
	if interval < time.Minute {
		interval = time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.sweep()
			}
		}
	}()
}

// Close tears down every parked session. Call it on shutdown so parked
// sessions are released instead of being left in the runtime's session store.
func (r *ToolSessionRegistry) Close() {
	r.mu.Lock()
	entries := r.entries
	r.entries = make(map[string]*parkedSession)
	r.mu.Unlock()
	for _, e := range entries {
		r.discard(e.session)
	}
}

// sweep tears down all entries older than the TTL.
func (r *ToolSessionRegistry) sweep() {
	var expired []*parkedSession
	r.mu.Lock()
	for token, e := range r.entries {
		if time.Since(e.createdAt) > r.ttl {
			expired = append(expired, e)
			delete(r.entries, token)
		}
	}
	r.mu.Unlock()

	for _, e := range expired {
		r.logger.Info("evicting expired parked tool session", "age", time.Since(e.createdAt).String())
		r.discard(e.session)
	}
}

// discard aborts any in-flight run (cancelling the pending tool call) and
// releases the session. Failures are non-fatal.
func (r *ToolSessionRegistry) discard(session *copilot.Session) {
	if session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.Abort(ctx); err != nil {
		r.logger.Debug("failed to abort parked session", "error", err)
	}
	if r.release != nil {
		r.release(session)
		return
	}
	if err := session.Disconnect(); err != nil {
		r.logger.Debug("failed to disconnect parked session", "error", err)
	}
}

// newToolSessionToken returns a random, underscore-free token used both as the
// registry key and as the segment embedded in emitted tool_call IDs.
func newToolSessionToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should not fail; fall back to a timestamp-derived token.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// encodeToolCallID mints an OpenAI tool_call ID that embeds the parking token so
// the "stateless" turn-2 request reconnects to its parked session. The token is
// underscore-free, so the token is everything between the "call_" prefix and the
// final "_<index>" segment.
func encodeToolCallID(token string, index int) string {
	return fmt.Sprintf("call_%s_%d", token, index)
}

// decodeToolSessionToken extracts the parking token from a tool_call ID of the
// form call_<token>_<index>. Returns false when the ID was not minted by us.
func decodeToolSessionToken(toolCallID string) (string, bool) {
	rest, ok := strings.CutPrefix(toolCallID, "call_")
	if !ok {
		return "", false
	}
	idx := strings.LastIndex(rest, "_")
	if idx <= 0 {
		return "", false
	}
	token := rest[:idx]
	if token == "" {
		return "", false
	}
	return token, true
}

// mintToolCalls converts intercepted ExternalToolRequested events into OpenAI
// tool_calls whose IDs embed the parking token, and returns the pending map
// (emitted tool_call ID -> SDK RequestID) for later resolution on turn-2.
func mintToolCalls(token string, events []*copilot.ExternalToolRequestedData) ([]oai.ToolCall, map[string]string) {
	calls := make([]oai.ToolCall, 0, len(events))
	pending := make(map[string]string, len(events))
	idx := 0
	for _, ev := range events {
		if ev == nil {
			continue
		}
		id := encodeToolCallID(token, idx)
		idx++
		pending[id] = ev.RequestID
		calls = append(calls, oai.ToolCall{
			ID:   id,
			Type: "function",
			Function: oai.FunctionCall{
				Name:      ev.ToolName,
				Arguments: argumentsToJSON(ev.Arguments),
			},
		})
	}
	return calls, pending
}

// resultsByToolCallID maps each role:"tool" message's tool_call_id to its
// content, so turn-2 can pair client-supplied results to pending SDK requests.
func resultsByToolCallID(messages []oai.Message) map[string]string {
	out := make(map[string]string)
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID != "" {
			out[m.ToolCallID] = m.Content.TextContent()
		}
	}
	return out
}

// tokenFromMessages recovers the parking token from the echoed tool_call IDs in
// a turn-2 request, preferring role:"tool" results and falling back to the
// assistant's tool_calls. Returns false when no ID decodes to a token.
func tokenFromMessages(messages []oai.Message) (string, bool) {
	for _, m := range messages {
		if m.Role == "tool" {
			if token, ok := decodeToolSessionToken(m.ToolCallID); ok {
				return token, true
			}
		}
	}
	for _, m := range messages {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if token, ok := decodeToolSessionToken(tc.ID); ok {
					return token, true
				}
			}
		}
	}
	return "", false
}
