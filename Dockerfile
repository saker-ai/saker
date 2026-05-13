# syntax=docker/dockerfile:1

# Build the full Saker CLI with embedded frontend
# Usage: docker build --build-arg GOOS=linux --build-arg GOARCH=amd64 -t saker .

ARG GO_VERSION=1.26
ARG NODE_VERSION=22
ARG GOOS=linux
ARG GOARCH=amd64

# --- Stage 1: Frontend build ---
FROM node:${NODE_VERSION}-alpine AS frontend
RUN corepack enable && corepack prepare pnpm@10 --activate
WORKDIR /src

# Copy workspace config and shared packages first for dependency resolution
COPY pnpm-workspace.yaml package.json pnpm-lock.yaml ./
COPY packages/editor-protocol/package.json ./packages/editor-protocol/
COPY web/package.json ./web/
COPY web-editor-next/package.json ./web-editor-next/

RUN pnpm install --frozen-lockfile

# Copy source and build
COPY packages/editor-protocol/ ./packages/editor-protocol/
COPY web/ ./web/
RUN pnpm --filter saker-web run build

COPY web-editor-next/ ./web-editor-next/
RUN pnpm --filter saker-web-editor run build

# --- Stage 2: Go binary with embedded frontend ---
FROM golang:${GO_VERSION}-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src

ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Copy frontend build outputs from stage 1
COPY --from=frontend /src/web/out ./cmd/saker/frontend/dist/
COPY --from=frontend /src/web-editor-next/out ./cmd/saker/editor/dist/

RUN CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/saker ./cmd/saker/

# --- Stage 3: Runtime ---
FROM alpine:3.20
RUN addgroup -S agent && adduser -S agent -G agent \
    && apk add --no-cache ca-certificates wget \
    && mkdir -p /var/saker && chown -R agent:agent /var/saker
WORKDIR /app
ENV TMPDIR=/var/saker \
    ANTHROPIC_API_KEY="" \
    SAKER_HTTP_ADDR=":10112" \
    SAKER_MODEL="claude-sonnet-4-5-20250929"
COPY --from=builder /out/saker /usr/local/bin/saker
EXPOSE 10112
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["sh","-c","ADDR=${SAKER_HTTP_ADDR:-:10112}; PORT=${ADDR##*:}; [ -z \"$PORT\" ] && PORT=10112; wget -qO- http://127.0.0.1:${PORT}/health || exit 1"]
USER agent
ENTRYPOINT ["/usr/local/bin/saker"]