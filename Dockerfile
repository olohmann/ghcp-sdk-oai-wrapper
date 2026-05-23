FROM golang:1.25-alpine AS builder

ARG VERSION=dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -trimpath \
    -o /server ./cmd/server

FROM node:22-slim

# Pin @github/copilot to a known-compatible version. The Go SDK speaks a
# protocol version that must match the CLI; rebuilding the image later
# against a newer CLI could break compatibility silently. Bump intentionally
# alongside the github.com/github/copilot-sdk/go module version.
ARG COPILOT_CLI_VERSION=1.0.51

RUN apt-get update && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates git wget \
    && npm install -g @github/copilot@${COPILOT_CLI_VERSION} \
    && npm cache clean --force \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Reuse the existing node user (uid/gid 1000) from the base image
COPY --from=builder --chown=node:node /server /server

USER node
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/server"]
