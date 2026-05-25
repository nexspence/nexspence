# Extended Monitoring Design

**Date:** 2026-05-25  
**Status:** Approved

## Overview

Three-layer monitoring extension for Nexspence:
1. **Prometheus endpoint** — `/metrics` with Bearer auth, standard Prometheus format
2. **Docker Compose + Grafana** — auto-provisioned stack, also installable standalone
3. **UI Dashboard** — existing MonitoringPage extended with tabs, line charts, per-repo stats

## Layer 1 — Prometheus Endpoint

### New Prometheus metrics

Registered in `internal/metrics/metrics.go` alongside existing atomics:

| Metric | Type | Labels |
|--------|------|--------|
| `nexspence_requests_total` | Counter | `method`, `status_class` (2xx/4xx/5xx) |
| `nexspence_request_duration_seconds` | Histogram | `method` |
| `nexspence_artifacts_total` | Gauge | — (DB-seeded on startup, incremented live) |
| `nexspence_bytes_stored_bytes` | Gauge | — |
| `nexspence_downloads_total` | Counter | — |
| `nexspence_artifacts_deleted_total` | Counter | — |
| `nexspence_goroutines` | Gauge | — (refreshed every 15s by background goroutine) |
| `nexspence_memory_alloc_bytes` | Gauge | — |

### Endpoint

`GET /metrics` — served by `promhttp.Handler()` wrapped in Bearer auth middleware.

Auth: same pattern as existing `AuthMiddleware` — accepts `Authorization: Bearer <jwt>` or `Authorization: Bearer nxs_*` API token. Returns 401 if missing/invalid.

`GET /api/v1/metrics` (existing JSON snapshot) is unchanged.

### MetricsMiddleware update

`MetricsMiddleware()` in `handlers/metrics.go` — additionally calls `prometheus.Counter.Inc()` and `prometheus.Histogram.Observe(latency)` for each request.

### Dependencies

Add to `go.mod`:
- `github.com/prometheus/client_golang`

## Layer 2 — Docker Compose + Grafana

### File layout

```
deploy/monitoring/
├── docker-compose.yml              ← standalone compose
├── prometheus.yml                  ← scrape config
└── grafana/
    ├── provisioning/
    │   ├── datasources/nexspence.yml
    │   └── dashboards/nexspence.yml
    └── dashboards/
        └── nexspence.json          ← pre-built dashboard (8 panels)
```

### Main docker-compose.yml additions (profile: monitoring)

```yaml
prometheus:
  profiles: [monitoring]
  image: prom/prometheus:v2.51.0
  volumes:
    - ./deploy/monitoring/prometheus.yml:/etc/prometheus/prometheus.yml:ro
    - ./deploy/monitoring/prometheus-token:/etc/prometheus/token:ro
  ports: ["9090:9090"]
  depends_on: [nexspence]

grafana:
  profiles: [monitoring]
  image: grafana/grafana:10.4.0
  environment:
    GF_SECURITY_ADMIN_PASSWORD: admin
    GF_AUTH_ANONYMOUS_ENABLED: "true"
    GF_AUTH_ANONYMOUS_ORG_ROLE: Viewer
  volumes:
    - ./deploy/monitoring/grafana/provisioning:/etc/grafana/provisioning:ro
    - ./deploy/monitoring/grafana/dashboards:/var/lib/grafana/dashboards:ro
    - grafana_data:/var/lib/grafana
  ports: ["3000:3000"]
  depends_on: [prometheus]
```

### Standalone install

`deploy/monitoring/docker-compose.yml` uses `NEXSPENCE_URL` env var (default `http://host.docker.internal:8081`) so it can target any running Nexspence instance.

### Prometheus scrape config

Bearer token auth via `bearer_token_file: /etc/prometheus/token`. Token is a `nxs_*` API token created by admin and written to `deploy/monitoring/prometheus-token` (gitignored).

`deploy/monitoring/prometheus-token.example` contains instructions.

### Grafana dashboard panels (8)

1. Requests/sec (rate)
2. Error rate %
3. Request latency p95
4. Artifacts total (gauge)
5. Storage used (gauge, formatted bytes)
6. Downloads/min (rate)
7. Goroutines
8. Memory alloc bytes

### Startup

```bash
# Together with Nexspence
docker compose --profile monitoring up

# Standalone
cd deploy/monitoring && NEXSPENCE_URL=http://my-nexspence:8081 docker compose up
```

## Layer 3 — UI Dashboard

### Backend additions

**`internal/metrics/ring_buffer.go`** — thread-safe circular buffer:
- Stores last 360 `DataPoint` entries (1 hour at 10s intervals)
- `DataPoint`: `Timestamp int64`, `RequestsTotal`, `RequestErrors`, `ArtifactsStored`, `BytesStored`, `DownloadsTotal int64`, `Goroutines int`
- `Add(DataPoint)` / `Snapshot() []DataPoint` methods with `sync.RWMutex`
- Background goroutine started from `main.go` samples every 10s

**New endpoints:**
- `GET /api/v1/metrics/history` — returns ring buffer as JSON array (auth required, any user)
- `GET /api/v1/metrics/repos` — top 10 repos by downloads and by storage size (DB query, auth required)

### Frontend changes

Install: `recharts`

**`MonitoringPage.tsx`** refactored to 3 tabs:

**Overview tab** — existing stat cards unchanged (requests, artifacts, downloads, errors, goroutines, memory, storage activity)

**Charts tab** — 3 `recharts` `<LineChart>` components using `GET /api/v1/metrics/history`:
- Requests/sec (delta between consecutive points ÷ 10)
- Error rate % (errors delta / requests delta × 100)
- Storage growth (bytes_stored over time, `<AreaChart>`)
- X-axis: relative time labels ("10m ago", "5m ago", "now")
- Auto-refresh every 30s

**Repositories tab** — table using `GET /api/v1/metrics/repos`:
- Columns: Repository, Format, Type, Downloads, Storage Used
- Sorted by downloads descending by default, toggle to storage
- No pagination (top 10 only)

### Routing

No new route — MonitoringPage already has its own route. Tab state is local React state (not URL param).

## Error Handling

- `/metrics` with invalid token → 401 JSON `{"error":"unauthorized"}`
- `GET /api/v1/metrics/history` when buffer is empty (fresh start) → empty array `[]`
- `GET /api/v1/metrics/repos` DB error → 500
- Recharts with empty data → renders empty axes (no crash)

## Testing

- Unit test: ring buffer — fill past capacity, verify oldest entries evicted
- Unit test: `PrometheusHandler` — valid token returns 200 with `text/plain`, missing token returns 401
- Existing 465 Go tests must continue to pass
