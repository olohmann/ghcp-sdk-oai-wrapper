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

## Commit Identity

- Commit with the locally configured git identity. **Never** set or override `user.name`/`user.email` (no `git config user.*`, no `git -c user.email=…`, no `--author`, no `GIT_AUTHOR_*`/`GIT_COMMITTER_*` env vars).
- Never invent emails such as `<login>@users.noreply.github.com` or `@example.com`: GitHub attributes legacy noreply addresses to whichever account owns that login. CI rejects such commits.
- `.mailmap` maps historical identities; keep it.

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
- Configuration via env vars; CLI flags (`--port`, `--mode`, `--default-model`) override env vars
- Requests without a `model` use `DEFAULT_MODEL` (default `claude-haiku-5.5`)
- Docker runtime image: distroless `gcr.io/distroless/cc-debian13` (glibc required by the Copilot CLI native binary; Alpine/musl does NOT work). The CLI's native binary is extracted from `@github/copilot-linux-<arch>` at build time; the image has no Node.js, npm, or shell. Health checks use `/server --healthcheck`.
