# GitHub Copilot SDK → OpenAI API Wrapper

An HTTP server written in Go that exposes an **OpenAI-compatible API** and delegates
all inference to the [GitHub Copilot SDK](https://github.com/github/copilot-sdk).

Any tool, library, or application that speaks the OpenAI chat completions protocol
can use GitHub Copilot models through this wrapper without modification.

## Features

- **`POST /v1/chat/completions`** — streaming (SSE) and non-streaming modes
- **Multimodal image support** — `content` accepts the OpenAI array format with `text` and `image_url` parts (base64 data URIs)
- **`GET /v1/models`** — lists all models available through GitHub Copilot
- **`GET /healthz`** — health check
- **`GET /metrics`** — Prometheus-compatible metrics endpoint
- Optional **Bearer-token authentication**
- **Headless authentication** via `GITHUB_TOKEN` for CI/CD and Kubernetes deployments
- Structured JSON logging via `log/slog`
- Graceful shutdown on SIGINT/SIGTERM
- **Helm chart** for Kubernetes deployment

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
| `GITHUB_TOKEN`       | *(empty)*  | GitHub token with Copilot scope for headless auth    |

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

### Multimodal (Image Input)

Send images as base64 data URIs using the OpenAI multimodal content format:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer my-secret" \
  -d '{
    "model": "gpt-4.1",
    "messages": [
      {"role": "user", "content": [
        {"type": "text", "text": "What is in this image?"},
        {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo..."}}
      ]}
    ]
  }'
```

Supported image formats: PNG, JPEG, GIF, WebP, BMP, TIFF, ICO, HEIC, AVIF.

> **Note:** Only `data:` URIs (inline base64) are supported. External `https://` image URLs are not forwarded to the model.

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
  -e GITHUB_TOKEN=ghp_... \
  ghcp-oai-wrapper
```

Authenticate by passing `GITHUB_TOKEN` (a GitHub PAT with Copilot scope).

## Kubernetes / Helm

A Helm chart is provided in `charts/ghcp-sdk-oai-wrapper/` for production Kubernetes deployments.

### Install

```bash
helm install copilot-proxy ./charts/ghcp-sdk-oai-wrapper \
  --set secrets.githubToken=ghp_your_token_here \
  --set secrets.apiKey=my-secret
```

### Using an existing Secret

If you manage secrets externally (e.g., Sealed Secrets, External Secrets Operator):

```bash
kubectl create secret generic copilot-proxy-secret \
  --from-literal=GITHUB_TOKEN=ghp_... \
  --from-literal=API_KEY=my-secret

helm install copilot-proxy ./charts/ghcp-sdk-oai-wrapper \
  --set secrets.existingSecret=copilot-proxy-secret
```

### Key values

| Value                              | Default     | Description                            |
|------------------------------------|-------------|----------------------------------------|
| `secrets.githubToken`              | `""`        | **Required.** GitHub PAT with Copilot scope |
| `secrets.apiKey`                   | `""`        | Optional Bearer token for API auth     |
| `secrets.existingSecret`           | `""`        | Use pre-existing K8s Secret            |
| `config.port`                      | `"8080"`    | HTTP listen port                       |
| `config.logLevel`                  | `"info"`    | Log level                              |
| `metrics.enabled`                  | `true`      | Expose `/metrics` endpoint             |
| `metrics.serviceMonitor.enabled`   | `false`     | Create Prometheus Operator ServiceMonitor |
| `autoscaling.enabled`              | `false`     | Enable HorizontalPodAutoscaler        |

## Metrics

Prometheus metrics are exposed at `GET /metrics` (unauthenticated).

### HTTP metrics

| Metric                          | Type      | Labels                   |
|---------------------------------|-----------|--------------------------|
| `http_requests_total`           | Counter   | method, path, status     |
| `http_request_duration_seconds` | Histogram | method, path             |
| `http_requests_in_flight`       | Gauge     | —                        |

### Copilot metrics

| Metric                                     | Type      | Labels         |
|---------------------------------------------|-----------|----------------|
| `copilot_chat_completions_total`            | Counter   | model, stream, status |
| `copilot_chat_completion_duration_seconds`  | Histogram | model, stream  |
| `copilot_image_attachments_total`           | Counter   | —              |

```bash
curl http://localhost:8080/metrics
```

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
internal/metrics/metrics.go  — Prometheus metrics definitions & middleware
internal/middleware/auth.go   — Bearer-token auth middleware
internal/oai/types.go        — OpenAI request/response types
internal/oai/sse.go          — SSE streaming helpers
test/e2e/e2e_test.go         — End-to-end integration tests
charts/                      — Helm chart for Kubernetes deployment
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

## Releasing

Releases are fully automated via GitHub Actions. A single git tag produces all artifacts in sync.

### Release artifacts

| Artifact | Location |
|----------|----------|
| Go binaries (linux/darwin × amd64/arm64) | GitHub Release attachments |
| Docker image | `ghcr.io/olohmann/ghcp-sdk-oai-wrapper:<version>` |
| Helm chart (OCI) | `oci://ghcr.io/olohmann/ghcp-sdk-oai-wrapper/charts/ghcp-sdk-oai-wrapper` |

### How to release

```bash
# 1. Tag the release (triggers the workflow)
git tag v1.2.3
git push origin v1.2.3
```

The `release.yml` workflow will:
1. Build cross-platform Go binaries with the version embedded
2. Build and push a multi-arch Docker image to ghcr.io
3. Package the Helm chart with matching version and push to ghcr.io as OCI
4. Create a GitHub Release with binary tarballs, checksums, and auto-generated changelog

### Version synchronization

All artifacts share the same version derived from the git tag:

- **Binary:** `ghcp-sdk-oai-wrapper --version` → `1.2.3`
- **Docker:** `ghcr.io/olohmann/ghcp-sdk-oai-wrapper:1.2.3`
- **Helm chart:** `version: 1.2.3`, `appVersion: "1.2.3"`
- **Helm default image tag:** automatically resolves to `appVersion` (= `1.2.3`)

### Installing from released artifacts

```bash
# Docker
docker pull ghcr.io/olohmann/ghcp-sdk-oai-wrapper:1.2.3

# Helm (OCI)
helm install copilot-proxy \
  oci://ghcr.io/olohmann/ghcp-sdk-oai-wrapper/charts/ghcp-sdk-oai-wrapper \
  --version 1.2.3 \
  --set secrets.githubToken=ghp_...
```

### Local versioned builds

```bash
# Build with a specific version
VERSION=1.2.3 make build

# Docker build with version
VERSION=1.2.3 make docker-build
```

### CI

The `ci.yml` workflow runs automatically on pushes to `main` and pull requests:
build, test, lint, and Helm chart lint.

## License

MIT
