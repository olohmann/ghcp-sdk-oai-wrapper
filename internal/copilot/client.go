package copilot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// Client wraps the Copilot SDK client with lifecycle management.
type Client struct {
	inner  *copilot.Client
	mu     sync.Mutex
	logger *slog.Logger
}

// NewClient creates a new Copilot SDK client wrapper.
func NewClient(cliPath string, githubToken string, logger *slog.Logger) *Client {
	opts := &copilot.ClientOptions{
		LogLevel: "error",
	}
	if cliPath != "" {
		opts.Connection = copilot.StdioConnection{Path: cliPath}
	}
	if githubToken != "" {
		opts.GitHubToken = githubToken
	}

	return &Client{
		inner:  copilot.NewClient(opts),
		logger: logger,
	}
}

// Start starts the underlying Copilot CLI server.
func (c *Client) Start(ctx context.Context) error {
	c.logger.Info("starting Copilot CLI server")
	if err := c.inner.Start(ctx); err != nil {
		return fmt.Errorf("copilot client start: %w", err)
	}
	c.logger.Info("Copilot CLI server started")
	return nil
}

// Stop stops the underlying Copilot CLI server.
func (c *Client) Stop() {
	c.logger.Info("stopping Copilot CLI server")
	_ = c.inner.Stop()
}

// ListModels returns the available models from the Copilot CLI.
func (c *Client) ListModels(ctx context.Context) ([]copilot.ModelInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inner.ListModels(ctx)
}

// CreateSession creates a new Copilot session with the given configuration.
func (c *Client) CreateSession(ctx context.Context, cfg *copilot.SessionConfig) (*copilot.Session, error) {
	return c.inner.CreateSession(ctx, cfg)
}

// NewChatSession creates a new Copilot chat session with common defaults.
// This is a convenience method that both OpenAI and Ollama handlers can use
// without depending on each other's types.
func (c *Client) NewChatSession(ctx context.Context, model, systemMessage string, streaming bool) (*copilot.Session, error) {
	return c.NewChatSessionWithTools(ctx, model, systemMessage, streaming, nil)
}

// NewChatSessionWithTools is like NewChatSession but also exposes caller-defined
// tools to the model. When tools is non-empty, only those tools are made
// available (built-in agentic tools stay disabled); when it is empty the
// behavior is identical to NewChatSession.
func (c *Client) NewChatSessionWithTools(ctx context.Context, model, systemMessage string, streaming bool, tools []copilot.Tool) (*copilot.Session, error) {
	cfg := &copilot.SessionConfig{
		Model:               model,
		Streaming:           copilot.Bool(streaming),
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		InfiniteSessions:    &copilot.InfiniteSessionConfig{Enabled: copilot.Bool(false)},
		AvailableTools:      []string{},
	}
	if len(tools) > 0 {
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name)
		}
		cfg.Tools = tools
		cfg.AvailableTools = names
	}
	if systemMessage != "" {
		cfg.SystemMessage = &copilot.SystemMessageConfig{
			Mode:    "replace",
			Content: systemMessage,
		}
	}
	return c.inner.CreateSession(ctx, cfg)
}

// ResolvePendingTool delivers a result for a pending external tool call on a
// live session. A declaration-only tool (nil handler) leaves the model's call
// pending — surfaced as a *copilot.ExternalToolRequestedData carrying a
// RequestID — instead of running a handler. Calling this resolves that exact
// pending call from outside any handler, letting the original agent loop
// continue to its final answer. This is the mechanism behind faithful OpenAI
// tool round-trips ("session parking"): the turn-1 session is kept alive and its
// pending call is resolved on turn-2 with the client-supplied result.
//
// requestID comes from the ExternalToolRequestedData event captured on turn-1.
func ResolvePendingTool(ctx context.Context, session *copilot.Session, requestID, result string) error {
	if session == nil {
		return fmt.Errorf("resolve pending tool: nil session")
	}
	if session.RPC == nil || session.RPC.Tools == nil {
		return fmt.Errorf("resolve pending tool: session RPC unavailable")
	}
	_, err := session.RPC.Tools.HandlePendingToolCall(ctx, &rpc.HandlePendingToolCallRequest{
		RequestID: requestID,
		Result:    rpc.ExternalToolStringResult(result),
	})
	if err != nil {
		return fmt.Errorf("resolve pending tool %s: %w", requestID, err)
	}
	return nil
}
