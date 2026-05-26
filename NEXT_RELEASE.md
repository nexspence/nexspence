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
- **Monitoring page crash ("t is not a function") fixed** — the production build crashed on `/admin?tab=monitoring`. Root cause: `es-toolkit`'s `./compat/*` package export has no `import` condition (CJS only), so recharts' `es-toolkit/compat/*` imports resolved to CommonJS, which Vite 8/rolldown's interop miscompiled into a self-shadowing `var require_X = require_X()` that threw at chunk init. Fixed with a `vite.config.ts` plugin that redirects `es-toolkit/compat/<name>` to its ESM `.mjs` build (default-export interop shim), so the subtree bundles as pure ESM
- **Docker Compose default config mount fixed** — `docker-compose.yml` and `docker-compose.ha.yml` now default to `./config.yaml` instead of `./config.yaml.example`; the previous default caused a `read /app/config.yaml: is a directory` crash when `config.yaml.example` was extracted as an empty directory from the release zip
- **Docker bind-mount on Mac fixed** — `config.yaml.example` is now bundled in the image as `/app/config.yaml`; without a file at that path Docker Desktop (Mac) created a directory and failed with `not a directory` on bind-mount
- **README logo fixed** — logo path updated to `docs/assets/logo.png` which is synced to the public repo; the previous `frontend/src/assets/logo.png` path was broken on github.com/skensell201/nexspence
- **Role privileges not saved on create/update fixed** — `RoleHandler.Create` and `Update` accepted `privileges` in the JSON body but never wrote to `role_privileges`; `SetPrivileges` was never called, so the CS → Privilege → Role chain (e.g. from `seed-rbac.sh`) was always broken
- **Monitoring profile missing from HA Compose fixed** — `--profile monitoring` was silently ignored when using `docker-compose.ha.yml` because Prometheus/Grafana were only defined in the single-node `docker-compose.yml`; added both services to `docker-compose.ha.yml` with a dedicated `deploy/monitoring/prometheus-ha.yml` that scrapes both `nexspence_1:8081` and `nexspence_2:8081`
