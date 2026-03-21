# Copilot Instructions

## Build & Test

```bash
make build          # Build binary to bin/
make test           # Unit tests (go test ./...)
make test-e2e       # E2E tests (requires Copilot CLI installed & authenticated)
make lint           # Runs go vet
make run            # Build and run the server

# Run a single test
go test -run TestAuth_ValidKey ./internal/middleware/

# Run a single e2e test
go test -tags e2e -v -run TestChatCompletions_Streaming -timeout 10m ./test/e2e/
```

## CLI Flags

```bash
./server --mode openai --port 8080   # OpenAI-compatible mode (default)
./server --mode ollama --port 11434  # Ollama-compatible mode
./server --version                   # Print version and exit
```

CLI flags override their corresponding environment variables (`MODE`, `PORT`).

## Architecture

This is an HTTP server that translates either the OpenAI or Ollama chat API into GitHub Copilot SDK calls. A `--mode` switch selects the API surface. Both modes share the same Copilot SDK backend — only the HTTP protocol layer differs.

**Request flow:** HTTP request → auth middleware → handler → creates a one-shot Copilot SDK session → sends prompt → collects response (or streams deltas) → destroys session → returns mode-specific response.

- **OpenAI mode** (default): Endpoints at `/v1/chat/completions`, `/v1/models`, `/healthz`. Streaming via SSE.
- **Ollama mode**: Endpoints at `/api/chat`, `/api/generate`, `/api/tags`, `/api/show`, `/api/version`, `/`. Streaming via NDJSON.

Each chat completion request creates and destroys its own `copilot.Session` — there is no session reuse or connection pooling. The server is stateless.

### Key packages

- `internal/copilot` — Wraps the Copilot SDK client (`github.com/github/copilot-sdk/go`). Manages CLI server lifecycle. Provides `NewChatSession()` used by both OpenAI and Ollama handlers.
- `internal/handler` — OpenAI HTTP handlers. `chat.go` handles `/v1/chat/completions` with streaming (SSE) and non-streaming paths.
- `internal/oai` — OpenAI-compatible request/response types and SSE writing utilities.
- `internal/ollama` — **Self-contained** Ollama-compatible module: types, NDJSON streaming writer, and all handlers. Has zero imports from `internal/oai` or `internal/handler`.
- `internal/middleware` — Bearer token auth with configurable exempt paths. API-format-agnostic (no dependency on `oai` or `ollama`).
- `internal/config` — Configuration via environment variables, with CLI flag overrides via `ApplyFlags()`.
- `internal/metrics` — Prometheus HTTP and Copilot metrics. Normalizes paths for both OpenAI and Ollama endpoints.

## Conventions

- **Zero external dependencies** beyond stdlib and the Copilot SDK. No routers, no frameworks.
- **Handlers are factory functions** returning `http.HandlerFunc` (e.g., `handler.ChatCompletions(client, logger)`), not methods on a struct.
- **Structured logging** uses `log/slog` with JSON output. Pass `*slog.Logger` as a dependency, don't use global loggers.
- **Error responses** use the mode-appropriate format: OpenAI handlers use `oai.WriteError()`, Ollama handlers use `ollama.WriteError()`.
- **Package isolation**: `internal/ollama` must not import `internal/oai` or `internal/handler` and vice versa. Shared logic lives in `internal/copilot` (e.g., `NewChatSession`).
- **E2E tests are build-tag gated** with `//go:build e2e`. They spin up the real server binary and require an authenticated Copilot CLI.
- **Unit tests** use stdlib `testing` + `httptest` only. No test frameworks.
- **Configuration** is environment-variable-based (PORT, API_KEY, LOG_LEVEL, COPILOT_CLI_PATH, MODE). CLI flags (`--port`, `--mode`) override env vars.
