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

# Re-declared: ARGs do not cross stage boundaries, and the OCI version label
# below would otherwise be empty.
ARG VERSION=dev

WORKDIR /app

COPY --from=builder /nexspence /app/nexspence
COPY --from=builder /src/config.yaml.example /app/config.yaml
COPY --from=builder /src/deploy/docker-entrypoint.sh /app/entrypoint.sh
COPY --from=frontend-builder /frontend/dist /app/frontend/dist

# AGPL-3.0 §4 requires a copy of the license to travel with the program, and an
# image is a way the program is conveyed. Shipping it only in the git repository
# does not cover someone who only ever pulls the image.
COPY --from=builder /src/LICENSE /app/LICENSE
COPY --from=builder /src/NOTICE /app/NOTICE

# Standard OCI annotations: registries, `docker scout`, SBOM tooling and
# corporate policy scanners read the licence from here, not from the repository.
LABEL org.opencontainers.image.title="Nexspence" \
      org.opencontainers.image.description="Universal artifact repository manager" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.source="https://github.com/nexspence/nexspence" \
      org.opencontainers.image.url="https://nexspence.com" \
      org.opencontainers.image.documentation="https://github.com/nexspence/nexspence#readme" \
      org.opencontainers.image.version="${VERSION}"

# Run as a non-root user (uid/gid 1000). Pre-create the dirs the app writes to
# (default blob path /app/data/blobs) and hand /app to the unprivileged user.
# Creating /app/data/blobs in the image is what lets a FRESH named volume
# mounted there inherit uid-1000 ownership (Docker only copies the image dir's
# ownership into an empty named volume when the mountpoint dir exists in the
# image).
#
# HOME and TRIVY_CACHE_DIR stay even though this image ships no Trivy: an
# operator who supplies one (see docs/scanning.md) needs its cache on a
# writable path, and pinning it here means they do not have to know that.
RUN addgroup -g 1000 nexspence && adduser -D -u 1000 -G nexspence nexspence \
    && mkdir -p /app/data/blobs /app/.cache /app/secrets \
    && chmod +x /app/entrypoint.sh \
    && chown -R nexspence:nexspence /app
ENV HOME=/app
ENV TRIVY_CACHE_DIR=/app/.cache/trivy
USER 1000

EXPOSE 8081 5000

ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["serve", "--config", "/app/config.yaml"]
