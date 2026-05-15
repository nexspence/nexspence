# Nexspence — Deployment Guide

## Quick Start

```bash
git clone https://github.com/nexspence-oss/nexspence
cd nexspence
docker compose up -d
```

Open http://localhost:8081 — login `admin` / `admin123`.

---

## Docker Compose — Standard

```bash
# 1. Clone
git clone https://github.com/nexspence-oss/nexspence
cd nexspence

# 2. Edit config.yaml — change at minimum:
#      auth.jwt_secret        (min 32 characters)
#      bootstrap.admin_password

# 3. Start PostgreSQL + Nexspence (auto-migrates schema on first run)
docker compose up -d

# 4. Verify
docker compose ps
docker compose logs -f nexspence
```

| Service | URL | Default credentials |
|---------|-----|---------------------|
| Web UI & REST API | http://localhost:8081 | `admin` / `admin123` |
| Docker registry | localhost:5000 | same credentials |
| PostgreSQL | localhost:5437 | `nexspence` / `nexspence` |

> Change the admin password immediately after first login via **Admin → Security → Users**.

---

## Docker Compose — With MinIO (S3)

MinIO is included in `docker-compose.yml` as an optional profile. Enable it
by setting the storage type env var before starting:

```bash
# Start with MinIO as the default blob store
NEXSPENCE_STORAGE_DEFAULT_TYPE=s3 \
  docker compose up -d

# MinIO S3 API:    http://localhost:9000
# MinIO console:   http://localhost:9001  (minioadmin / minioadmin)
# Nexspence UI:    http://localhost:8081
```

---

## Docker Compose — HA Cluster

`docker-compose.ha.yml` runs 2 Nexspence nodes, nginx (`least_conn` load
balancer), Redis, MinIO, and PostgreSQL. All nodes are stateless — shared
state lives in PostgreSQL, Redis, and S3.

```bash
# Start 2-node HA cluster
docker compose \
  -f docker-compose.ha.yml \
  up -d

# Load balancer:  http://localhost:8080
# 2 x Nexspence + nginx LB + Redis + MinIO + PostgreSQL
```

Enable Redis in `config.yaml` for each node:

```yaml
redis:
  enabled: true
  addr: "redis:6379"
  password: ""
  db: 0
```

See [docs/ha-setup.md](ha-setup.md) for the full HA guide including
Kubernetes probe examples.

---

## Docker Compose — With Keycloak SSO

Starts a pre-configured Keycloak dev instance with the `nexspence` realm
imported. "Sign in with Keycloak" appears on the login page automatically.

```bash
# Start with Keycloak OIDC provider
OIDC_ENABLED=true \
  docker compose \
  --profile keycloak \
  up -d

# Nexspence UI:    http://localhost:8081  (admin / admin123)
# Keycloak admin:  http://localhost:8180  (admin / admin)
# Test SSO user:   testuser / testpass (mapped to nx-admin role)
```

See [docs/oidc-setup.md](oidc-setup.md) for manual OIDC config and all
supported providers (Keycloak, Google, Entra ID, Okta).

---

## From Source

**Requirements:** Go 1.22+, Node.js 22+, PostgreSQL 16+

```bash
# 1. Clone
git clone https://github.com/nexspence-oss/nexspence
cd nexspence

# 2. Start PostgreSQL only (skip if you have a local instance)
docker compose up -d db

# 3. Run backend — applies DB migrations automatically
go run ./cmd/server serve

# 4. In a separate terminal — frontend dev server with HMR
cd frontend && npm ci
npm run dev       # http://localhost:5173

# — or — production build
npm run build     # output → frontend/dist/
```

To produce a self-contained binary:

```bash
go build -o nexspence ./cmd/server
./nexspence serve
```

The binary serves both the REST API and the built frontend SPA from
`./frontend/dist`.

---

## Configuration Reference

`config.yaml` is the primary configuration file. Every key can be
overridden via an environment variable using the pattern
`NEXSPENCE_<SECTION>_<KEY>` (uppercase, underscore-separated).

| Key | Default | Description |
|-----|---------|-------------|
| `http.addr` | `:8081` | Listen address |
| `http.base_url` | `http://localhost:8081` | Public URL used in download links |
| `database.dsn` | postgres://nexspence:nexspence@localhost:5437/nexspence | PostgreSQL connection string |
| `storage.default_type` | `local` | `local` or `s3` |
| `storage.local.base_path` | `./data/blobs` | Filesystem path for local blob store |
| `storage.s3.bucket` | — | S3 bucket name (required when type=s3) |
| `storage.s3.endpoint` | — | S3 endpoint URL (e.g. `http://minio:9000`) |
| `storage.s3.force_path_style` | `true` | Required for MinIO / non-AWS S3 |
| `auth.jwt_secret` | — | JWT signing key — **change before production** |
| `auth.jwt_expiry_hours` | `24` | JWT token lifetime |
| `auth.anonymous_enabled` | `true` | Allow unauthenticated read on public repos |
| `auth.token_max_days` | `180` | Maximum lifetime for user API tokens (`nxs_*`) |
| `bootstrap.admin_password` | `admin123` | Auto-created admin password — **change this** |
| `cleanup.default_schedule` | `0 2 * * *` | Default cron for cleanup policies |
| `audit.retention_days` | `90` | Audit log partition retention |
| `redis.enabled` | `false` | Enable Redis (required for HA) |
| `redis.addr` | `localhost:6379` | Redis address |
