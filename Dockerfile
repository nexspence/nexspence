# ── Build stage ───────────────────────────────────────────────
# $BUILDPLATFORM = native runner arch (amd64); cross-compile for $TARGETPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary with version injection — cross-compile natively, no QEMU needed
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /nexspence \
    ./cmd/server

# ── Frontend build stage ──────────────────────────────────────
# Static assets are arch-independent — always build on native amd64, skip QEMU
FROM --platform=$BUILDPLATFORM node:26-alpine AS frontend-builder

WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# ── Final image ───────────────────────────────────────────────
FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata wget

# Trivy (optional CVE scans from Security UI / ScanService)
ARG TRIVY_VERSION=0.70.0
ARG TARGETARCH=amd64
RUN set -eu; \
  case "$TARGETARCH" in \
    amd64) TRIVY_ARCH=64bit ;; \
    arm64) TRIVY_ARCH=ARM64 ;; \
    *) echo "unsupported TARGETARCH=$TARGETARCH" >&2; exit 1 ;; \
  esac; \
  wget -qO- "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-${TRIVY_ARCH}.tar.gz" \
    | tar -xzf - -C /usr/local/bin trivy; \
  chmod +x /usr/local/bin/trivy

WORKDIR /app

COPY --from=builder /nexspence /app/nexspence
COPY --from=builder /src/config.yaml.example /app/config.yaml
COPY --from=frontend-builder /frontend/dist /app/frontend/dist

# Run as a non-root user (uid/gid 1000). Pre-create the dirs the app and the
# bundled trivy write to (default blob path /app/data/blobs, trivy cache) and
# hand /app to the unprivileged user. Creating /app/data/blobs in the image is
# what lets a FRESH named volume mounted there inherit uid-1000 ownership
# (Docker only copies the image dir's ownership into an empty named volume when
# the mountpoint dir exists in the image). HOME=/app keeps trivy's fallback cache
# under a writable path; TRIVY_CACHE_DIR pins it explicitly (correct env var
# for modern trivy ≥0.x).
RUN addgroup -g 1000 nexspence && adduser -D -u 1000 -G nexspence nexspence \
    && mkdir -p /app/data/blobs /app/.cache \
    && chown -R nexspence:nexspence /app
ENV HOME=/app
ENV TRIVY_CACHE_DIR=/app/.cache/trivy
USER 1000

EXPOSE 8081 5000

ENTRYPOINT ["/app/nexspence"]
CMD ["serve", "--config", "/app/config.yaml"]
