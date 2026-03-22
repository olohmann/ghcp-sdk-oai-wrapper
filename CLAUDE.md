# CLAUDE.md

## Build & Test

```bash
make build          # Build binary to bin/
make test           # Unit tests (go test ./...)
make lint           # Runs go vet
make run            # Build and run the server
```

## Release Process

- Releases are triggered by pushing a `v*` tag (e.g., `v0.2.1`).
- **Bug fixes after a release MUST use a new patch version.** Never retag an existing version that was already published. If v0.2.0 was released and a bug is found, the fix goes into v0.2.1. This applies even if the bug was discovered minutes after release.
- The release pipeline runs: validate (build+test+lint) -> binaries (4 platforms) -> docker (multi-arch + trivy + cosign) -> helm -> github-release.
- The Trivy scan and cosign signing steps are non-blocking (`continue-on-error: true`) because they depend on external infrastructure.

## Architecture

Dual-mode HTTP server wrapping the GitHub Copilot SDK:

- `--mode openai` (default): OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/models`)
- `--mode ollama`: Ollama-compatible endpoints (`/api/chat`, `/api/generate`, `/api/tags`, etc.)

Both modes share the same Copilot SDK backend via `internal/copilot.Client.NewChatSession()`.

### Package Isolation Rule

`internal/ollama/` must NOT import `internal/oai/` or `internal/handler/` (and vice versa). Each API surface is self-contained. Shared logic lives in `internal/copilot/`.

## Key Conventions

- Zero external frameworks (stdlib + Copilot SDK only)
- Handlers are factory functions returning `http.HandlerFunc`
- Structured logging with `log/slog` (JSON output)
- Configuration via env vars; CLI flags (`--port`, `--mode`) override env vars
- Docker base image: `node:22-slim` (Debian, glibc required by Copilot CLI native binary; Alpine/musl does NOT work)
