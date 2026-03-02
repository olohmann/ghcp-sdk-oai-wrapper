# GitHub Copilot SDK → OpenAI API Wrapper

An HTTP server written in Go that exposes an **OpenAI-compatible API** and delegates
all inference to the [GitHub Copilot SDK](https://github.com/github/copilot-sdk).

Any tool, library, or application that speaks the OpenAI chat completions protocol
can use GitHub Copilot models through this wrapper without modification.

## Features

- **`POST /v1/chat/completions`** — streaming (SSE) and non-streaming modes
- **`GET /v1/models`** — lists all models available through GitHub Copilot
- **`GET /healthz`** — health check
- Optional **Bearer-token authentication**
- Structured JSON logging via `log/slog`
- Graceful shutdown on SIGINT/SIGTERM
- Zero external dependencies beyond stdlib + Copilot SDK

## Prerequisites

- **Go 1.24+**
- **GitHub Copilot CLI** installed and authenticated
  (`copilot` must be on your `PATH`, or set `COPILOT_CLI_PATH`)
- An active **GitHub Copilot subscription**

## Quick Start

```bash
# Build
make build

# Run
make run

# Install to ~/.local/bin
make install

# Or with configuration
PORT=9090 API_KEY=my-secret LOG_LEVEL=debug ghcp-sdk-oai-wrapper
```

Run `make help` for all available targets.

## Configuration

| Environment Variable | Default    | Description                                          |
|----------------------|------------|------------------------------------------------------|
| `PORT`               | `8080`     | HTTP server listen port                              |
| `API_KEY`            | *(empty)*  | Bearer token for API auth (disabled if empty)        |
| `LOG_LEVEL`          | `info`     | Log level: `debug`, `info`, `warn`, `error`          |
| `COPILOT_CLI_PATH`   | *(empty)*  | Path to `copilot` CLI binary (auto-detected if empty)|

## Usage Examples

### Non-streaming

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer my-secret" \
  -d '{
    "model": "gpt-4.1",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

### Streaming

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer my-secret" \
  -d '{
    "model": "gpt-4.1",
    "messages": [
      {"role": "user", "content": "Tell me a joke"}
    ],
    "stream": true
  }'
```

### List Models

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer my-secret"
```

### Use with OpenAI Python Client

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="my-secret",
)

response = client.chat.completions.create(
    model="gpt-4.1",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)
```

## Docker

The Docker image includes the Copilot CLI pre-installed via npm.

```bash
docker build -t ghcp-oai-wrapper .
docker run -p 8080:8080 \
  -e API_KEY=my-secret \
  -e COPILOT_GITHUB_TOKEN=ghp_... \
  ghcp-oai-wrapper
```

Authenticate by passing `COPILOT_GITHUB_TOKEN` (a GitHub PAT with Copilot scope)
or by running the container interactively and using `/login`.

## Architecture

```
Client (OpenAI-compatible)
       │
       ▼
  HTTP Server (net/http)
       │
       ▼
  OpenAI ↔ Copilot SDK translation layer
       │
       ▼
  Copilot SDK (JSON-RPC)
       │
       ▼
  Copilot CLI (server mode)
       │
       ▼
  GitHub Copilot API
```

Each chat completion request creates a fresh Copilot session, sends the prompt,
collects the response (or streams deltas), and tears down the session. This
provides stateless, OpenAI-compatible semantics.

## Project Structure

```
cmd/server/main.go           — Entrypoint, config, lifecycle
internal/config/config.go    — Environment-based configuration
internal/copilot/client.go   — Copilot SDK client wrapper
internal/handler/chat.go     — POST /v1/chat/completions
internal/handler/models.go   — GET /v1/models
internal/handler/health.go   — GET /healthz
internal/middleware/auth.go   — Bearer-token auth middleware
internal/oai/types.go        — OpenAI request/response types
internal/oai/sse.go          — SSE streaming helpers
test/e2e/e2e_test.go         — End-to-end integration tests
Makefile                     — Build, install, test, run targets
Dockerfile                   — Multi-stage container build
```

## Testing

```bash
# Unit tests
make test

# End-to-end integration tests (requires Copilot CLI installed & authenticated)
make test-e2e
```

## License

MIT
