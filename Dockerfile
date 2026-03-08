FROM golang:1.24-alpine AS builder

ARG VERSION=dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /server ./cmd/server

FROM node:22-alpine

RUN apk add --no-cache ca-certificates git \
    && npm install -g @github/copilot \
    && npm cache clean --force \
    && addgroup -g 1000 appuser \
    && adduser -D -u 1000 -G appuser appuser

COPY --from=builder --chown=appuser:appuser /server /server

USER appuser
EXPOSE 8080
ENTRYPOINT ["/server"]
