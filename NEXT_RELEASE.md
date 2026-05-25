### ✨ Features

**Phase 68 complete (2026-05-25)** — Extended Monitoring: Prometheus `/metrics` endpoint (Bearer-authenticated, 8 custom metrics: requests_total, request_duration_seconds, artifacts_total, bytes_stored_bytes, downloads_total, artifacts_deleted_total, goroutines, memory_alloc_bytes); `deploy/monitoring/` stack (prom/prometheus:v2.51.0 + grafana/grafana:10.4.0, `--profile monitoring`); standalone compose at `deploy/monitoring/docker-compose.yml`; ring-buffer history API (`GET /api/v1/metrics/history`, 360 data points at 10s intervals); repo metrics API (`GET /api/v1/metrics/repos`); MonitoringPage extended to 3 tabs (Overview / Charts / Repositories) using recharts. 474 Go tests pass.

### 🐛 Bug Fixes

_No bug fixes in this release._
