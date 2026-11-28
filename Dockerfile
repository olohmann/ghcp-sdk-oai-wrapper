FROM golang:1.26-alpine AS builder

ARG VERSION=dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -trimpath \
    -o /server ./cmd/server

# The Copilot CLI ships as a standalone native binary in a per-platform npm
# package; the `@github/copilot` package is only a Node launcher for it. Fetch
# the binary for the target platform (npm verifies the registry integrity
# hash) so the runtime image needs neither Node nor npm.
FROM --platform=$BUILDPLATFORM node:22-slim AS cli

# Pin @github/copilot to a known-compatible version. The Go SDK speaks a
# protocol version that must match the CLI; rebuilding the image later
# against a newer CLI could break compatibility silently. Bump intentionally
# alongside the github.com/github/copilot-sdk/go module version.
ARG COPILOT_CLI_VERSION=1.0.112
ARG TARGETARCH

WORKDIR /cli
RUN case "${TARGETARCH}" in \
        amd64) arch=x64 ;; \
        arm64) arch=arm64 ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && npm pack --silent "@github/copilot-linux-${arch}@${COPILOT_CLI_VERSION}" \
    && tar -xzf github-copilot-linux-*.tgz package/copilot \
    && install -m 0755 package/copilot /cli/copilot \
    && mkdir -p /cli/home/copilot \
    && printf '%s\n' \
        'root:x:0:0:root:/root:/sbin/nologin' \
        'nobody:x:65534:65534:nobody:/nonexistent:/sbin/nologin' \
        'nonroot:x:65532:65532:nonroot:/home/nonroot:/sbin/nologin' \
        'copilot:x:1000:1000:copilot:/home/copilot:/sbin/nologin' > /cli/passwd \
    && printf '%s\n' \
        'root:x:0:' 'nobody:x:65534:' 'nonroot:x:65532:' 'copilot:x:1000:' > /cli/group

# Distroless glibc runtime (libc, libstdc++, libgcc, CA certificates) with no
# shell or package manager. The Copilot CLI binary requires glibc; musl-based
# images such as Alpine do not work.
FROM gcr.io/distroless/cc-debian13@sha256:e8343d4a7da8bb210df0c3ea29de38bbadbba6c773bed0cd8bde506fca3a5c44

COPY --from=cli /cli/passwd /etc/passwd
COPY --from=cli /cli/group /etc/group
COPY --from=cli --chown=1000:1000 /cli/home/copilot /home/copilot
COPY --from=cli /cli/copilot /usr/local/bin/copilot
COPY --from=builder /server /server

# uid/gid 1000 matches the Helm chart's securityContext.runAsUser.
USER 1000:1000
ENV HOME=/home/copilot \
    COPILOT_CLI_PATH=/usr/local/bin/copilot
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/server", "--healthcheck"]

ENTRYPOINT ["/server"]
