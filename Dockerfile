# ── Build stage ───────────────────────────────────────────────
# $BUILDPLATFORM = native runner arch (amd64); cross-compile for $TARGETPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

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
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-builder

WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# ── Final image ───────────────────────────────────────────────
FROM alpine:3.21

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
COPY --from=frontend-builder /frontend/dist /app/frontend/dist

EXPOSE 8081 5000

ENTRYPOINT ["/app/nexspence"]
CMD ["serve", "--config", "/app/config.yaml"]
