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

RUN apt-get update && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && npm install -g @github/copilot \
    && npm cache clean --force \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Reuse the existing node user (uid/gid 1000) from the base image
COPY --from=builder --chown=node:node /server /server

USER node
EXPOSE 8080
ENTRYPOINT ["/server"]
