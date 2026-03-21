package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/config"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/copilot"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/handler"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/metrics"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/middleware"
	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/ollama"
)

// version is set at build time via ldflags:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "dev"

func main() {
	// Handle --version before flag parsing (exits immediately).
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		os.Exit(0)
	}

	// Parse CLI flags.
	var flagPort, flagMode string
	flag.StringVar(&flagPort, "port", "", "HTTP listen port (overrides PORT env var)")
	flag.StringVar(&flagMode, "mode", "", "API mode: openai or ollama (overrides MODE env var)")
	flag.Parse()

	cfg := config.Load()
	cfg.ApplyFlags(flagPort, flagMode)

	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	logger.Info("ghcp-sdk-oai-wrapper", "version", version)

	// Initialize Copilot SDK client
	client := copilot.NewClient(cfg.CopilotCLIPath, cfg.GitHubToken, logger)
	if err := client.Start(context.Background()); err != nil {
		logger.Error("failed to start Copilot client", "error", err)
		os.Exit(1)
	}
	defer client.Stop()

	// Build router with mode-based route registration
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	var authExempt []string
	switch cfg.Mode {
	case "ollama":
		mux.HandleFunc("/", ollama.Root(version))
		mux.HandleFunc("/api/chat", ollama.Chat(client, logger))
		mux.HandleFunc("/api/generate", ollama.Generate(client, logger))
		mux.HandleFunc("/api/tags", ollama.Tags(client, logger))
		mux.HandleFunc("/api/show", ollama.Show(client, logger))
		mux.HandleFunc("/api/version", ollama.Version(version))
		authExempt = []string{"/", "/api/version", "/metrics"}
	default: // "openai"
		mux.HandleFunc("/healthz", handler.Health())
		mux.HandleFunc("/v1/chat/completions", handler.ChatCompletions(client, logger))
		mux.HandleFunc("/v1/models", handler.Models(client, logger))
		authExempt = []string{"/healthz", "/metrics"}
	}

	// Apply middleware: metrics first (all requests), then auth (skips exempt paths)
	authMiddleware := middleware.Auth(cfg.APIKey, authExempt...)
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      metrics.Middleware(authMiddleware(mux)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Minute, // Long timeout for streaming
		IdleTimeout:  120 * time.Second,
	}

	// Start server
	go func() {
		logger.Info("server starting", "port", cfg.Port, "mode", cfg.Mode, "auth_enabled", cfg.APIKey != "")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("shutting down", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	logger.Info("server stopped")
}
