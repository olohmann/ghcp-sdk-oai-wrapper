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

FROM node:22-alpine

RUN apk update && apk upgrade --no-cache \
    && apk add --no-cache ca-certificates git \
    && npm install -g @github/copilot \
    && npm cache clean --force \
    && rm -rf /var/cache/apk/*

# Reuse the existing node user (uid/gid 1000) from the base image
COPY --from=builder --chown=node:node /server /server

USER node
EXPOSE 8080
ENTRYPOINT ["/server"]
