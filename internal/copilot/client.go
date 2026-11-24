package copilot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// builtinMCPServers are the MCP servers the runtime supplies itself. MCP
// discovery does not report them, and the CLI's --disable-builtin-mcps flag
// does not apply in SDK server mode, so they are disabled per session by name.
var builtinMCPServers = []string{"github-mcp-server", "githubiq"}

// releaseTimeout bounds the background teardown of a finished session.
const releaseTimeout = 30 * time.Second

// Client wraps the Copilot SDK client with lifecycle management.
type Client struct {
	inner  *copilot.Client
	mu     sync.Mutex
	logger *slog.Logger
	// workDir is an empty, server-owned directory used as the working
	// directory of the CLI runtime and every session, so the runtime never
	// scans the server's own working directory (repo files, git state,
	// instruction files) when building a session.
	workDir  string
	releases sync.WaitGroup
	// disabledMCP lists every MCP server the runtime would otherwise start for
	// each session (user config, plugins, built-ins). Chat sessions never
	// expose MCP tools, so starting them only delays the first model call.
	disabledMCP []string
	// defaultModel is used for requests that do not name a model.
	defaultModel string
}

// NewClient creates a new Copilot SDK client wrapper.
// defaultModel is used for requests without a model and for the startup
// warm-up; it may be empty.
func NewClient(cliPath, githubToken, defaultModel string, logger *slog.Logger) *Client {
	if cliPath == "" {
		// The SDK defaults to an embedded runtime, which this server does not bundle.
		cliPath = "copilot"
	}

	workDir, err := os.MkdirTemp("", "ghcp-sdk-oai-wrapper-")
	if err != nil {
		logger.Warn("failed to create isolated working directory; using process working directory", "error", err)
		workDir = ""
	}
	if workDir != "" && strings.ContainsRune(cliPath, filepath.Separator) {
		// A relative CLI path would otherwise resolve against the new working directory.
		if abs, err := filepath.Abs(cliPath); err == nil {
			cliPath = abs
		}
	}

	opts := &copilot.ClientOptions{
		LogLevel:         "error",
		Connection:       copilot.StdioConnection{Path: cliPath},
		WorkingDirectory: workDir,
	}
	if githubToken != "" {
		opts.GitHubToken = githubToken
	}

	return &Client{
		inner:        copilot.NewClient(opts),
		logger:       logger,
		workDir:      workDir,
		disabledMCP:  append([]string(nil), builtinMCPServers...),
		defaultModel: defaultModel,
	}
}

// Start starts the underlying Copilot CLI server.
func (c *Client) Start(ctx context.Context) error {
	c.logger.Info("starting Copilot CLI server")
	if err := c.inner.Start(ctx); err != nil {
		return fmt.Errorf("copilot client start: %w", err)
	}
	c.logger.Info("Copilot CLI server started")
	c.discoverMCPServers(ctx)
	c.warmUp(ctx)
	return nil
}

// warmUp creates and releases one throwaway session for an available model so
// the runtime's one-time model initialisation (roughly a second) is not paid
// by the first client request. No prompt is sent, so it consumes no model
// usage. Failure is non-fatal.
func (c *Client) warmUp(ctx context.Context) {
	start := time.Now()
	model := c.defaultModel
	if model == "" {
		models, err := c.ListModels(ctx)
		if err != nil || len(models) == 0 {
			c.logger.Warn("session warm-up skipped: no models available", "error", err)
			return
		}
		model = models[0].ID
	}
	session, err := c.NewChatSession(ctx, model, "", false)
	if err != nil {
		c.logger.Warn("session warm-up failed", "error", err)
		return
	}
	c.ReleaseSession(session)
	c.logger.Info("session warm-up complete", "elapsed", time.Since(start).String())
}

// discoverMCPServers records the names of all MCP servers the runtime knows
// about so sessions can disable them. Failure is non-fatal: sessions then
// start with the runtime's default MCP servers.
func (c *Client) discoverMCPServers(ctx context.Context) {
	if c.inner.RPC == nil || c.inner.RPC.MCP == nil {
		return
	}
	req := &rpc.MCPDiscoverRequest{}
	if c.workDir != "" {
		req.WorkingDirectory = &c.workDir
	}
	res, err := c.inner.RPC.MCP.Discover(ctx, req)
	if err != nil {
		c.logger.Warn("failed to discover MCP servers; sessions will start them", "error", err)
		return
	}
	names := make([]string, 0, len(res.Servers))
	for _, s := range res.Servers {
		names = append(names, s.Name)
	}
	c.disabledMCP = append(c.disabledMCP, names...)
	c.logger.Info("disabling MCP servers for chat sessions", "servers", c.disabledMCP)
}

// Stop waits for in-flight session releases, then stops the underlying
// Copilot CLI server and removes the isolated working directory.
func (c *Client) Stop() {
	c.logger.Info("stopping Copilot CLI server")
	c.releases.Wait()
	_ = c.inner.Stop()
	if c.workDir != "" {
		_ = os.RemoveAll(c.workDir)
	}
}

// ReleaseSession tears down a finished one-shot session in the background:
// it disconnects the session and permanently deletes its persisted state.
// Disconnect alone keeps the session on disk for later resumption, which a
// stateless server never does, so every request would otherwise leave a
// session directory behind. Running in the background keeps the teardown
// RPCs off the response path.
func (c *Client) ReleaseSession(session *copilot.Session) {
	if session == nil {
		return
	}
	c.releases.Add(1)
	go func() {
		defer c.releases.Done()
		id := session.SessionID
		if err := session.Disconnect(); err != nil {
			c.logger.Debug("failed to disconnect session", "session_id", id, "error", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
		defer cancel()
		if err := c.inner.DeleteSession(ctx, id); err != nil {
			c.logger.Debug("failed to delete session", "session_id", id, "error", err)
		}
	}()
}

// ResolveModel returns model, or the configured default model when model is
// empty. The result is empty only when neither is set.
func (c *Client) ResolveModel(model string) string {
	if model != "" || c == nil {
		return model
	}
	return c.defaultModel
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
	return c.NewChatSessionWithTools(ctx, model, systemMessage, streaming, nil, "")
}

// NewChatSessionWithTools is like NewChatSession but also exposes caller-defined
// tools to the model. When tools is non-empty, only those tools are made
// available (built-in agentic tools stay disabled); when it is empty the
// behavior is identical to NewChatSession. Empty reasoningEffort keeps the
// model's default.
func (c *Client) NewChatSessionWithTools(ctx context.Context, model, systemMessage string, streaming bool, tools []copilot.Tool, reasoningEffort string) (*copilot.Session, error) {
	return c.inner.CreateSession(ctx, c.chatSessionConfig(model, systemMessage, streaming, tools, reasoningEffort))
}

func (c *Client) chatSessionConfig(model, systemMessage string, streaming bool, tools []copilot.Tool, reasoningEffort string) *copilot.SessionConfig {
	cfg := &copilot.SessionConfig{
		Model:               model,
		ReasoningEffort:     reasoningEffort,
		Streaming:           copilot.Bool(streaming),
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		InfiniteSessions:    &copilot.InfiniteSessionConfig{Enabled: copilot.Bool(false)},
		AvailableTools:      []string{},
		WorkingDirectory:    c.workDir,

		// One-shot chat sessions need none of the CLI's agentic context. These
		// mirror the SDK's ModeEmpty defaults (which we cannot use because it
		// relocates COPILOT_HOME and thereby the CLI's stored login) and cut
		// session-creation work and prompt size.
		EnableConfigDiscovery:              copilot.Bool(false),
		SkipCustomInstructions:             copilot.Bool(true),
		EnableOnDemandInstructionDiscovery: copilot.Bool(false),
		EnableFileHooks:                    copilot.Bool(false),
		EnableHostGitOperations:            copilot.Bool(false),
		EnableSessionStore:                 copilot.Bool(false),
		EnableSkills:                       copilot.Bool(false),
		EnableExperimentalMode:             copilot.Bool(false),
		EnableSessionTelemetry:             copilot.Bool(false),
		SkipEmbeddingRetrieval:             copilot.Bool(true),
		EmbeddingCacheStorage:              copilot.String("in-memory"),
		CustomAgentsLocalOnly:              copilot.Bool(true),
		Memory:                             &copilot.MemoryConfiguration{Enabled: false},
		MCPOAuthTokenStorage:               "in-memory",
		DisabledMCPServers:                 c.disabledMCP,
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
	} else {
		// Keep the default prompt but drop the host environment section
		// (cwd, OS, git state), which is meaningless to API callers.
		cfg.SystemMessage = &copilot.SystemMessageConfig{
			Mode: "customize",
			Sections: map[string]copilot.SectionOverride{
				"environment_context": {Action: copilot.SectionActionRemove},
			},
		}
	}
	return cfg
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
