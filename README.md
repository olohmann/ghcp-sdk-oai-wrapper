<div align="center">

# 🛰️ ghcp-sdk-oai-wrapper

**Use GitHub Copilot models from any OpenAI- or Ollama-compatible client.**

A small, stateless Go server that turns the [GitHub Copilot SDK](https://github.com/github/copilot-sdk)
into a drop-in `/v1/chat/completions` (or Ollama `/api/chat`) endpoint, with
streaming, tool calling, vision, reasoning effort, and file attachments.

[![CI](https://github.com/olohmann/ghcp-sdk-oai-wrapper/actions/workflows/ci.yml/badge.svg)](https://github.com/olohmann/ghcp-sdk-oai-wrapper/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/olohmann/ghcp-sdk-oai-wrapper?sort=semver)](https://github.com/olohmann/ghcp-sdk-oai-wrapper/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/olohmann/ghcp-sdk-oai-wrapper)](go.mod)
[![Go Report Card](https://goreportcard.com/badge/github.com/olohmann/ghcp-sdk-oai-wrapper)](https://goreportcard.com/report/github.com/olohmann/ghcp-sdk-oai-wrapper)
[![Image: distroless](https://img.shields.io/badge/image-distroless-4285F4?logo=docker&logoColor=white)](#-docker)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[Quick start](#-quick-start) ·
[Features](#-features) ·
[Configuration](#%EF%B8%8F-configuration) ·
[API guide](#-api-guide) ·
[Raycast](#-raycast) ·
[Deploy](#-deploy) ·
[Operations](#-operations)

</div>

---

```text
OpenAI SDK · Raycast · LangChain · Open WebUI · curl          Ollama clients
                 │                                                  │
                 ▼                                                  ▼
          /v1/chat/completions  ◄──── --mode openai | ollama ────►  /api/chat
                 └──────────────────────────┬───────────────────────┘
                                            ▼
                         Copilot SDK (JSON-RPC) → Copilot CLI → GitHub Copilot
```

## ✨ Features

| | |
|---|---|
| 🔌 **Two API surfaces** | OpenAI-compatible (`/v1/chat/completions`, `/v1/models`) or Ollama-compatible (`/api/chat`, `/api/generate`, `/api/tags`, …), selected with `--mode`. |
| ⚡ **Streaming** | SSE in OpenAI mode and NDJSON in Ollama mode, with explicit error events instead of silent hangs. |
| 🛠️ **Tool calling** | Structured OpenAI `tool_calls`, executed by **your** client. Supports multi-round flows, parallel calls, and tools discovered mid-turn. |
| 👁️ **Vision and files** | Inline `image_url` and `file` (PDF, text, …) content parts as base64 data URIs. |
| 🧠 **Reasoning effort** | Per-request `reasoning_effort`, validated against each model's SDK-reported levels. |
| 📇 **Rich model catalog** | `/v1/models` exposes `context_window`, `supports_vision`, and the reasoning capabilities reported by the SDK. |
| 🔐 **Auth** | Optional bearer-token API auth, plus headless Copilot auth through `GITHUB_TOKEN`. |
| 📈 **Observability** | Prometheus `/metrics`, structured JSON logs (`log/slog`), and `/healthz`. |
| 📦 **Hardened delivery** | Distroless multi-arch image, cosign signatures, SLSA provenance, SBOM, and a Helm chart. |
| 🪶 **Lean** | Go stdlib plus the Copilot SDK only; no web frameworks. Every request uses a fresh, isolated Copilot session. |

## 🚀 Quick start

### Docker (recommended)

```bash
docker run --rm -p 8080:8080 \
  -e GITHUB_TOKEN=ghp_your_copilot_token \
  -e API_KEY=my-secret \
  ghcr.io/olohmann/ghcp-sdk-oai-wrapper:latest
```

`GITHUB_TOKEN` must be a GitHub token with Copilot access, and the account needs an active
**GitHub Copilot subscription**.

### From source

Requires **Go 1.26.11+** and **GitHub Copilot CLI 1.0.112**. The CLI must be authenticated
and on your `PATH`, or you can set `COPILOT_CLI_PATH`.

```bash
make build                      # → bin/ghcp-sdk-oai-wrapper
make run                        # build + run on :8080
make install                    # → ~/.local/bin

PORT=9090 API_KEY=my-secret LOG_LEVEL=debug ghcp-sdk-oai-wrapper
ghcp-sdk-oai-wrapper --mode ollama --port 11434
```

> The wrapper uses Copilot SDK **v1.0.34**. The Docker image bundles the matching CLI;
> source and binary installs must provide the CLI themselves.

### Say hello

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer my-secret" \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-sonnet-5.5", "messages": [{"role": "user", "content": "Hello!"}]}'
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="my-secret")
resp = client.chat.completions.create(
    model="claude-sonnet-5.5",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(resp.choices[0].message.content)
```

Use `GET /v1/models` to list the model IDs available to your Copilot account.

## ⚙️ Configuration

| Variable | Flag | Default | Description |
|---|---|---|---|
| `PORT` | `--port` | `8080` | HTTP listen port |
| `MODE` | `--mode` | `openai` | API surface: `openai` or `ollama` |
| `DEFAULT_MODEL` | `--default-model` | `claude-haiku-5.5` | Model used when a request omits `model` |
| `API_KEY` | | *(empty)* | Bearer token required from clients; auth is disabled when empty |
| `GITHUB_TOKEN` | | *(empty)* | GitHub token with Copilot access for headless authentication |
| `COPILOT_CLI_PATH` | | *(auto)* | Path to the `copilot` CLI; defaults to `copilot` on `PATH` |
| `LOG_LEVEL` | | `info` | `debug`, `info`, `warn`, or `error` |
| `TOOL_SESSION_TTL` | | `10m` | How long a parked tool session waits for the client's tool results |
| `TOOL_SESSION_MAX_PARKED` | | `256` | Maximum number of concurrently parked tool sessions; the oldest is evicted first |

Flags override environment variables. Additional commands:

- `--version` prints the version.
- `--healthcheck` probes the local health endpoint and exits with `0` when healthy.

## 📖 API guide

### Endpoints

| OpenAI mode (default) | Ollama mode |
|---|---|
| `POST /v1/chat/completions`: chat, SSE streaming, tools | `POST /api/chat`: chat, NDJSON streaming |
| `GET /v1/models`: model catalog and capabilities | `POST /api/generate`: prompt completion |
| `GET /healthz`: health *(no auth)* | `GET /api/tags`, `POST /api/show`: models |
| `GET /metrics`: Prometheus *(no auth)* | `GET /api/version`, `GET /`: version and health *(no auth)* |

<details>
<summary><b>⚡ Streaming</b></summary>

Set `"stream": true` to receive OpenAI `chat.completion.chunk` events over SSE, terminated by
`data: [DONE]`.

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer my-secret" -H "Content-Type: application/json" \
  -d '{"model": "claude-sonnet-5.5", "stream": true,
       "messages": [{"role": "user", "content": "Tell me a joke"}]}'
```

</details>

<details>
<summary><b>🛠️ Tool calling</b></summary>

Declare functions in the OpenAI `tools` field. When the model calls one, the wrapper returns
structured `tool_calls` with `finish_reason: "tool_calls"`. Your client executes the tool and
sends the result back as a `role: "tool"` message with the matching `tool_call_id`.

- **Client-side execution.** Tools are declaration-only. They never run inside the wrapper.
  Caller tools keep their names and take precedence over same-named Copilot built-ins such as
  `bash` and `grep`. Copilot's own built-in and MCP tools stay disabled.
- **Session parking.** Between rounds, the live Copilot session is parked in memory and keyed
  by an opaque token embedded in each `tool_call_id`.
- **Continuation.** A continuation request must end with the current round's tool results.
  - Full chat history is fine: only the latest tool round selects the parked session.
  - A new user message starts a fresh session.
  - Tool declarations do not need to be repeated.
- **Multi-round, parallel, and dynamic tools.**
  - The model can call several tools at once or chain rounds.
  - A continuation that includes `tools` replaces the session's tool set before results are
    delivered. This supports clients that discover tools mid-turn.
  - Omitted or `null` tools keep the current set; `[]` clears it.
- **Failure behavior.**
  - A failed tool update returns HTTP 500 before streaming starts, without resolving pending
    calls.
  - Expired sessions, restarts, or requests routed to another replica return **HTTP 409**
    (`tool_session_expired`).
  - Tools are never replayed automatically.

> Parked sessions live on the instance that issued the call. With multiple replicas, use
> session affinity.

</details>

<details>
<summary><b>👁️ Vision and file attachments</b></summary>

Send images and documents inline as base64 data URIs using the standard OpenAI content-part
format:

```json
{"role": "user", "content": [
  {"type": "text", "text": "What is in this image? Then summarize the PDF."},
  {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo..."}},
  {"type": "file", "file": {"file_data": "data:application/pdf;base64,JVBERi0...", "filename": "report.pdf"}}
]}
```

- Use a model whose `/v1/models` entry has `supports_vision: true`.
- `image_url` requires an `image/*` MIME type. Use `file` for PDFs and other documents.
- Only `data:` URIs are supported. Remote `https://` URLs and OpenAI `file_id`
  references are not.
- MIME types pass through unchanged. Attachments are forwarded in memory; nothing is written
  to disk.

</details>

<details>
<summary><b>🧠 Reasoning effort</b></summary>

```json
{"model": "claude-sonnet-5.5", "reasoning_effort": "high",
 "messages": [{"role": "user", "content": "Solve this carefully."}]}
```

- Choose a level from the model's `supported_reasoning_efforts` in `/v1/models`. Levels
  such as `low`, `medium`, `high`, `xhigh`, and `max` are model-dependent.
- Omit the field or set it to `null` to use the SDK default.
- Unsupported values return **HTTP 400** before streaming starts.
- During a tool round-trip, omit the field or repeat the original value. Changing it returns
  HTTP 400 without consuming the parked session.

</details>

<details>
<summary><b>📇 Model catalog</b></summary>

`GET /v1/models` returns standard OpenAI model objects, extended with SDK-reported metadata:

| Field | Meaning |
|---|---|
| `context_window` | Total context limit in tokens; omitted when the SDK does not report one |
| `supports_vision` | Image input support; always present |
| `supports_reasoning_effort` | Whether `reasoning_effort` is accepted |
| `supported_reasoning_efforts` / `default_reasoning_effort` | Exact SDK levels and default; omitted when unknown |

Values are never inferred from model names or vendor defaults.

</details>

<details>
<summary><b>🧯 Completion failures and timeouts</b></summary>

- Each chat request has a **five-minute budget**. It covers session setup and tool-result RPCs.
- Copilot session errors are returned explicitly; they are never reported as empty
  "successful" answers.
- Non-streaming responses preserve upstream HTTP status, provider error type, and provider
  error code when they are available. Otherwise the status is 500. A timeout returns **504**.
- After SSE has started, failures are sent as an OpenAI `error` event followed by `[DONE]`,
  without a `finish_reason`.

</details>

## 🪄 Raycast

The wrapper works as a [Raycast custom provider](https://manual.raycast.com/ai/custom-providers).
Add it to `~/.config/raycast/ai/providers.yaml`:

```yaml
providers:
  - id: ghcp
    name: GitHub Copilot SDK
    base_url: https://copilot.example.com/v1
    api_keys:
      ghcp: GHCP_WRAPPER_KEY        # only if API_KEY is set
    models:
      - id: claude-sonnet-5.5
        name: Claude Sonnet 5.5
        context: 1000000            # ← context_window from /v1/models
        abilities:
          vision:
            supported: true         # ← supports_vision
          reasoning_effort:
            supported: true         # ← supports_reasoning_effort
          tools:
            supported: true         # structured tool calling
          system_message:
            supported: true
          temperature:
            supported: false
```

Populate each model from your deployment's `/v1/models` output, and enable abilities only when
the catalog confirms them. Raycast watches the file and reloads it automatically.

## 🚢 Deploy

### 🐳 Docker

`ghcr.io/olohmann/ghcp-sdk-oai-wrapper` is a multi-arch image (`linux/amd64`, `linux/arm64`)
built on **distroless** `gcr.io/distroless/cc-debian13`.

- It contains no shell, package manager, Node.js, or npm.
- It includes only the server and the Copilot CLI's standalone native binary.
- It runs as uid/gid `1000` with `HOME=/home/copilot`.
- Its `HEALTHCHECK` uses `/server --healthcheck`.

> Configure containers through environment variables (`PORT`, `MODE`, …), not CLI flags.
> The health check runs as a separate process and reads only the environment.

```bash
docker build -t ghcp-sdk-oai-wrapper .
VERSION=1.2.3 make docker-build
```

### ☸️ Kubernetes / Helm

```bash
helm install copilot-proxy \
  oci://ghcr.io/olohmann/ghcp-sdk-oai-wrapper/charts/ghcp-sdk-oai-wrapper \
  --version 1.0.0 \
  --set secrets.githubToken=ghp_... \
  --set secrets.apiKey=my-secret
```

Use an externally managed secret with the keys `GITHUB_TOKEN` and, optionally, `API_KEY`:

```bash
kubectl create secret generic copilot-proxy-secret \
  --from-literal=GITHUB_TOKEN=ghp_... --from-literal=API_KEY=my-secret
helm install copilot-proxy oci://ghcr.io/olohmann/ghcp-sdk-oai-wrapper/charts/ghcp-sdk-oai-wrapper \
  --version 1.0.0 --set secrets.existingSecret=copilot-proxy-secret
```

| Value | Default | Description |
|---|---|---|
| `secrets.githubToken` | `""` | **Required.** GitHub token with Copilot access |
| `secrets.apiKey` | `""` | Optional client bearer token |
| `secrets.existingSecret` | `""` | Use a pre-existing Kubernetes Secret |
| `config.port` / `config.logLevel` | `"8080"` / `"info"` | Listen port and log level |
| `config.defaultModel` | `""` | Fallback model; empty uses `claude-haiku-5.5` |
| `metrics.serviceMonitor.enabled` | `false` | Create a Prometheus Operator ServiceMonitor |
| `autoscaling.enabled` | `false` | Enable the HorizontalPodAutoscaler; use session affinity for tools |

The chart is also available in the repository at `charts/ghcp-sdk-oai-wrapper/`.

## 🔭 Operations

### Metrics

`GET /metrics` is unauthenticated and Prometheus-compatible.

| Metric | Type | Labels |
|---|---|---|
| `http_requests_total` | Counter | method, path, status |
| `http_request_duration_seconds` | Histogram | method, path |
| `http_requests_in_flight` | Gauge | — |
| `copilot_chat_completions_total` | Counter | model, stream, status |
| `copilot_chat_completion_duration_seconds` | Histogram | model, stream |
| `copilot_image_attachments_total` | Counter | — |

### How it works

Every chat request creates a fresh Copilot session, sends the prompt, and returns or streams the
response.

- Finished sessions are torn down and deleted from the CLI session store in the background.
- Only sessions awaiting tool results are kept alive between requests.
- Sessions run in an empty temporary working directory.
- Custom instructions, skills, git context, config discovery, and MCP servers are disabled.
  This lets the model call start within tens of milliseconds.
- At startup, the server warms the runtime with one throwaway session.

## 🔒 Supply-chain security

| Feature | Tool | Purpose |
|---|---|---|
| Image signing | [cosign](https://docs.sigstore.dev/cosign/) (keyless) | Proves the image was built by this repository's workflow |
| Build provenance | [GitHub Attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) (SLSA) | Records build inputs, environment, and steps |
| SBOM | BuildKit | Software Bill of Materials attached to the image |
| Image scanning | [Trivy](https://trivy.dev/) | CRITICAL/HIGH findings reported to GitHub code scanning |
| Go vulnerabilities | [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) | Checks dependencies against the Go vulnerability database |
| Minimal runtime | distroless | No shell or package manager in the image |

```bash
cosign verify \
  --certificate-identity-regexp "https://github.com/olohmann/ghcp-sdk-oai-wrapper/" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/olohmann/ghcp-sdk-oai-wrapper:1.0.0

gh attestation verify oci://ghcr.io/olohmann/ghcp-sdk-oai-wrapper:1.0.0 --owner olohmann
```

## 🧑‍💻 Development

```bash
make test        # unit tests (stdlib testing + httptest)
make lint        # go vet
make test-e2e    # live integration tests; requires an authenticated Copilot CLI
E2E_MODEL=<model-id> make test-e2e
make help        # all targets
```

```text
cmd/server/              Entrypoint, flags, health-check subcommand
internal/config/         Environment and flag configuration
internal/copilot/        Copilot SDK client, session lifecycle and tool RPCs
internal/handler/        OpenAI handlers: chat, tools and parking, models, health
internal/oai/            OpenAI types and SSE writer
internal/ollama/         Self-contained Ollama API: types, NDJSON, handlers
internal/middleware/     Bearer-token auth
internal/metrics/        Prometheus metrics and middleware
test/e2e/                Build-tagged end-to-end tests
charts/                  Helm chart
```

### CI and releases

- **CI** (`ci.yml`) runs on pushes and pull requests.
  - Go build, tests, vet, and `govulncheck`
  - Commit-identity checks
  - Helm lint
  - Docker build, smoke test, and Trivy scan
- **Release** (`release.yml`) runs when you push a `v*` tag, such as
  `git tag v1.2.3 && git push origin v1.2.3`.
  - Validate the build.
  - Build the binaries (linux/darwin × amd64/arm64), Docker image, and Helm chart.
  - Publish them as a GitHub Release with checksums.
  - All artifacts share the tag's version. Docker tags are `1.2.3`, `1.2`, `1`, and
    `sha-<commit>`.

## 📄 License

[MIT](LICENSE) © Oliver Lohmann
