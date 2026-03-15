# GitHub Copilot SDK → OpenAI API Wrapper

[![CI](https://github.com/olohmann/ghcp-sdk-oai-wrapper/actions/workflows/ci.yml/badge.svg)](https://github.com/olohmann/ghcp-sdk-oai-wrapper/actions/workflows/ci.yml)
[![Release](https://github.com/olohmann/ghcp-sdk-oai-wrapper/actions/workflows/release.yml/badge.svg)](https://github.com/olohmann/ghcp-sdk-oai-wrapper/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/olohmann/ghcp-sdk-oai-wrapper)](https://goreportcard.com/report/github.com/olohmann/ghcp-sdk-oai-wrapper)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

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

## CI/CD

### Continuous Integration (`ci.yml`)

The CI workflow runs automatically on pushes to `main` and on pull requests:

| Job | What it does |
|-----|-------------|
| **build-and-test** | Build, unit tests, `go vet` lint, `govulncheck` vulnerability scan |
| **helm-lint** | Lint the Helm chart |
| **docker-build** | Build the Docker image (no push), scan with [Trivy](https://trivy.dev/), upload SARIF results to GitHub Security |

All checks must pass before a pull request can be merged.

### Releasing (`release.yml`)

Releases are fully automated. A single git tag produces **all artifacts with the same semantic version**.

#### How to release

```bash
git tag v1.2.3
git push origin v1.2.3
```

#### Release pipeline

The workflow runs five jobs after the tag push:

```
validate ─┬─► binaries ──┐
           ├─► docker ────┼─► github-release
           └─► helm ──────┘
```

| Job | Description |
|-----|-------------|
| **validate** | Build, test, lint, `govulncheck` — gates all downstream jobs |
| **binaries** | Cross-compile Go binaries (linux/darwin × amd64/arm64), create tarballs and SHA-256 checksums |
| **docker** | Build multi-arch image, push to ghcr.io, generate SBOM, scan with Trivy, sign with cosign, attest provenance |
| **helm** | Update `Chart.yaml` version, lint, package, push OCI chart to ghcr.io |
| **github-release** | Create GitHub Release with binaries, checksums, Helm chart, and verification instructions |

#### Release artifacts

| Artifact | Location |
|----------|----------|
| Go binaries (linux/darwin × amd64/arm64) | GitHub Release attachments |
| Docker image (multi-arch) | `ghcr.io/olohmann/ghcp-sdk-oai-wrapper:<version>` |
| Helm chart (OCI) | `oci://ghcr.io/olohmann/ghcp-sdk-oai-wrapper/charts/ghcp-sdk-oai-wrapper` |

#### Version synchronization

All artifacts share the same semantic version derived from the git tag:

- **Binary:** `ghcp-sdk-oai-wrapper --version` → `1.2.3`
- **Docker tags:** `1.2.3`, `1.2`, `1`, `sha-<commit>`
- **Helm chart:** `version: 1.2.3`, `appVersion: "1.2.3"`
- **Helm default image tag:** resolves to `appVersion` (= `1.2.3`)

### Supply chain security

Every release image is secured with multiple layers of verification:

| Feature | Tool | Purpose |
|---------|------|---------|
| **Image signing** | [cosign](https://docs.sigstore.dev/cosign/) (keyless/Sigstore OIDC) | Proves the image was built by this repository's GitHub Actions workflow |
| **Build provenance** | [GitHub Attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) (SLSA) | Cryptographic proof of build inputs, environment, and steps |
| **SBOM** | Docker BuildKit built-in | Software Bill of Materials attached to the image manifest |
| **Vulnerability scan** | [Trivy](https://trivy.dev/) | Blocks releases with CRITICAL vulnerabilities |
| **OCI labels** | [docker/metadata-action](https://github.com/docker/metadata-action) | Standardized OCI annotations (source, revision, vendor, etc.) |
| **Go vuln check** | [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) | Checks Go dependencies against the Go vulnerability database |
| **Reproducible builds** | `-trimpath` flag | Strips local paths from binaries for reproducibility |

#### Verify an image signature

```bash
cosign verify \
  --certificate-identity-regexp "https://github.com/olohmann/ghcp-sdk-oai-wrapper/" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/olohmann/ghcp-sdk-oai-wrapper:1.2.3
```

#### Verify build provenance

```bash
gh attestation verify \
  oci://ghcr.io/olohmann/ghcp-sdk-oai-wrapper:1.2.3 \
  --owner olohmann
```

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

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.
