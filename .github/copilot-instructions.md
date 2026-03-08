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

## Architecture

This is an HTTP server that translates the OpenAI chat completions API into GitHub Copilot SDK calls. Clients speaking the OpenAI protocol hit this wrapper, which manages Copilot SDK sessions under the hood.

**Request flow:** HTTP request → auth middleware → handler → creates a one-shot Copilot SDK session → sends prompt → collects response (or streams deltas via SSE) → destroys session → returns OpenAI-formatted response.

Each chat completion request creates and destroys its own `copilot.Session` — there is no session reuse or connection pooling. The server is stateless.

### Key packages

- `internal/copilot` — Wraps the Copilot SDK client (`github.com/github/copilot-sdk/go`). Manages CLI server lifecycle. All SDK calls go through `Client` which holds a mutex for `ListModels`.
- `internal/handler` — HTTP handlers. `chat.go` contains the core translation logic: it extracts system messages into `SessionConfig.SystemMessage`, builds prompts from non-system messages, and handles both streaming (via event subscription) and non-streaming (via `SendAndWait`) paths.
- `internal/oai` — OpenAI-compatible request/response types and SSE writing utilities. All JSON responses and errors go through `WriteJSON` / `WriteError`.
- `internal/middleware` — Bearer token auth. Disabled when `API_KEY` env var is empty.
- `internal/config` — Environment-variable-only configuration via `config.Load()`.

## Conventions

- **Zero external dependencies** beyond stdlib and the Copilot SDK. No routers, no frameworks.
- **Handlers are factory functions** returning `http.HandlerFunc` (e.g., `handler.ChatCompletions(client, logger)`), not methods on a struct.
- **Structured logging** uses `log/slog` with JSON output. Pass `*slog.Logger` as a dependency, don't use global loggers.
- **Error responses** follow the OpenAI error format (`ErrorResponse` with `type` and `message`). Always use `oai.WriteError()`.
- **E2E tests are build-tag gated** with `//go:build e2e`. They spin up the real server binary and require an authenticated Copilot CLI.
- **Unit tests** use stdlib `testing` + `httptest` only. No test frameworks.
- **Configuration** is environment-variable-only (PORT, API_KEY, LOG_LEVEL, COPILOT_CLI_PATH). No config files.
