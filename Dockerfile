# ── Build stage ───────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary with version injection
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /nexspence \
    ./cmd/server

# ── Frontend build stage ──────────────────────────────────────
FROM node:22-alpine AS frontend-builder

WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# ── Final image ───────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app

COPY --from=builder /nexspence /app/nexspence
COPY --from=frontend-builder /frontend/dist /app/frontend/dist

# Default config (overridden by volume mount or env vars)
COPY config.yaml /app/config.yaml

EXPOSE 8081 5000

ENTRYPOINT ["/app/nexspence"]
CMD ["serve", "--config", "/app/config.yaml"]
