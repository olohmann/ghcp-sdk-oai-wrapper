package copilot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
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
		opts.CLIPath = cliPath
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
