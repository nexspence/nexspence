### ✨ Features

**Phase 68 complete (2026-05-25)** — Extended Monitoring

- **Prometheus `/metrics` endpoint** — Bearer-authenticated (JWT or `nxs_*` token); 8 custom metrics: `nexspence_requests_total`, `nexspence_request_duration_seconds`, `nexspence_artifacts_total`, `nexspence_bytes_stored_bytes`, `nexspence_downloads_total`, `nexspence_artifacts_deleted_total`, `nexspence_goroutines`, `nexspence_memory_alloc_bytes`
- **Docker Compose monitoring profile** — `docker compose --profile monitoring up` starts Prometheus v2.51.0 + Grafana 10.4.0; pre-built dashboard with 8 panels (req/s, error rate, latency p95, artifacts, storage, downloads, goroutines, memory); can also run standalone via `deploy/monitoring/docker-compose.yml` pointed at any Nexspence instance
- **Combine with other profiles**: `OIDC_ENABLED=true docker compose --profile keycloak --profile monitoring up`
- **History API** — `GET /api/v1/metrics/history` returns last 360 data points (1 hour at 10s intervals) from an in-memory ring buffer
- **Repo metrics API** — `GET /api/v1/metrics/repos` returns top 10 repositories by downloads and storage size
- **UI — MonitoringPage 3 tabs**: Overview (existing stat cards), Charts (req/s, error rate, storage growth line/area charts via recharts, auto-refresh 30s), Repositories (top-10 table with sort toggle)

### 🐛 Bug Fixes

- **User creation now persists roles** — `UserService.Create` was silently ignoring the `roles` field; users created via the REST API or seed scripts now get their role assignments saved
- **System Admin blank screen fixed** — `recharts` was missing from `node_modules`; running `npm install` in `frontend/` after pulling is required; added React `ErrorBoundary` so future render errors show a recoverable fallback instead of wiping the entire page
